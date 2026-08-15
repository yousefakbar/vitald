package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type File struct {
	Path     string
	Checksum string
	Size     int64
}

type Filesystem struct{ Root string }

func (f Filesystem) Save(ctx context.Context, metric, runID string, page int, data []byte) (File, error) {
	if err := ctx.Err(); err != nil {
		return File{}, err
	}
	dir := filepath.Join(f.Root, "googlehealth", metric, runID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return File{}, fmt.Errorf("create raw archive directory: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("page-%04d.json", page))
	if _, err := os.Stat(path); err == nil {
		return File{}, fmt.Errorf("raw archive already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return File{}, fmt.Errorf("inspect raw archive path: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".page-*")
	if err != nil {
		return File{}, fmt.Errorf("create raw archive file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		return File{}, err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return File{}, fmt.Errorf("write raw archive: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return File{}, fmt.Errorf("sync raw archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return File{}, fmt.Errorf("close raw archive: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return File{}, fmt.Errorf("commit raw archive: %w", err)
	}
	digest := sha256.Sum256(data)
	return File{Path: path, Checksum: hex.EncodeToString(digest[:]), Size: int64(len(data))}, nil
}

func RunID(from, to time.Time) string {
	return fmt.Sprintf("%s_%s_%s", from.Format(time.DateOnly), to.Format(time.DateOnly), time.Now().UTC().Format("20060102T150405.000000000Z"))
}
