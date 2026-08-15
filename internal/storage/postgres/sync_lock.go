package postgres

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

const syncAdvisoryLockName = "vitald-sync"
const staleSyncError = "synchronization was abandoned after the previous process stopped"

// SyncLease holds the PostgreSQL session advisory lock that serializes sync
// executions. The underlying pooled connection is reserved until Release.
type SyncLease struct {
	conn *pgxpool.Conn
	once sync.Once
	err  error
}

type StaleSyncRecovery struct {
	Runs    int64
	Metrics int64
}

// TryAcquireSyncLease reserves a database connection and attempts to acquire
// the sync advisory lock without waiting.
func (s *Store) TryAcquireSyncLease(ctx context.Context) (*SyncLease, bool, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("reserve PostgreSQL connection for synchronization lock: %w", err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, syncAdvisoryLockName,
	).Scan(&acquired); err != nil {
		discardPooledConn(conn)
		return nil, false, fmt.Errorf("acquire synchronization lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return nil, false, nil
	}
	return &SyncLease{conn: conn}, true, nil
}

// RecoverStaleSyncRuns finalizes history that could only have been left by a
// previous lock holder. It must be called while this lease is held and before
// the new run is created.
func (l *SyncLease) RecoverStaleSyncRuns(ctx context.Context) (StaleSyncRecovery, error) {
	tx, err := l.conn.Begin(ctx)
	if err != nil {
		return StaleSyncRecovery{}, fmt.Errorf("begin stale synchronization recovery: %w", err)
	}
	defer tx.Rollback(ctx)

	metricResult, err := tx.Exec(ctx, `
UPDATE sync_run_metrics SET
    completed_at = now(),
    status = 'failed',
    error_message = $1
WHERE status = 'running'`, staleSyncError)
	if err != nil {
		return StaleSyncRecovery{}, fmt.Errorf("recover stale synchronization metrics: %w", err)
	}
	runResult, err := tx.Exec(ctx, `
UPDATE sync_runs SET
    completed_at = now(),
    status = 'failed',
    error_message = $1
WHERE status = 'running'`, staleSyncError)
	if err != nil {
		return StaleSyncRecovery{}, fmt.Errorf("recover stale synchronization runs: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return StaleSyncRecovery{}, fmt.Errorf("commit stale synchronization recovery: %w", err)
	}
	return StaleSyncRecovery{Runs: runResult.RowsAffected(), Metrics: metricResult.RowsAffected()}, nil
}

// Release explicitly unlocks and returns the reserved connection. If the
// unlock query fails, the connection is destroyed so a session-level lock can
// never leak back into the pool.
func (l *SyncLease) Release(ctx context.Context) error {
	l.once.Do(func() {
		var unlocked bool
		if err := l.conn.QueryRow(ctx,
			`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, syncAdvisoryLockName,
		).Scan(&unlocked); err != nil {
			discardPooledConn(l.conn)
			l.err = fmt.Errorf("release synchronization lock: %w", err)
			return
		}
		l.conn.Release()
		if !unlocked {
			l.err = fmt.Errorf("release synchronization lock: lock was not held")
		}
	})
	return l.err
}

func discardPooledConn(conn *pgxpool.Conn) {
	underlying := conn.Hijack()
	_ = underlying.Close(context.Background())
}
