package googlehealth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var Scopes = []string{
	"https://www.googleapis.com/auth/googlehealth.activity_and_fitness.readonly",
	"https://www.googleapis.com/auth/googlehealth.health_metrics_and_measurements.readonly",
	"https://www.googleapis.com/auth/googlehealth.sleep.readonly",
	"https://www.googleapis.com/auth/googlehealth.nutrition.readonly",
}

type TokenStore struct{ Path string }

func OAuthConfig(clientID, clientSecret, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     google.Endpoint,
		Scopes:       Scopes,
	}
}

func (s TokenStore) Load() (*oauth2.Token, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("OAuth token not found; run 'vitald auth'")
		}
		return nil, fmt.Errorf("read OAuth token: %w", err)
	}
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("decode OAuth token: %w", err)
	}
	return &token, nil
}

func (s TokenStore) Save(token *oauth2.Token) error {
	if token == nil {
		return errors.New("cannot save a nil OAuth token")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("encode OAuth token: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".token-*")
	if err != nil {
		return fmt.Errorf("create temporary token: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("set token permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write token: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close token: %w", err)
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		return fmt.Errorf("replace token: %w", err)
	}
	return nil
}

func Authorize(ctx context.Context, cfg *oauth2.Config, store TokenStore, openBrowser bool, out func(string, ...any)) (*oauth2.Token, error) {
	state, err := randomState()
	if err != nil {
		return nil, err
	}
	redirect, err := url.Parse(cfg.RedirectURL)
	if err != nil {
		return nil, fmt.Errorf("parse redirect URL: %w", err)
	}
	if redirect.Scheme != "http" || (redirect.Hostname() != "127.0.0.1" && redirect.Hostname() != "localhost") {
		return nil, errors.New("GOOGLE_REDIRECT_URL must use an http localhost loopback address")
	}

	if redirect.Port() == "" {
		return nil, errors.New("GOOGLE_REDIRECT_URL must include a loopback port")
	}
	listenAddress := ":" + redirect.Port()
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return nil, fmt.Errorf("listen for OAuth callback on %s: %w", listenAddress, err)
	}
	defer listener.Close()

	result := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(redirect.Path, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "invalid OAuth state", http.StatusBadRequest)
			result <- callbackResult{err: errors.New("OAuth callback state mismatch")}
			return
		}
		if message := r.URL.Query().Get("error"); message != "" {
			http.Error(w, "authorization denied", http.StatusBadRequest)
			result <- callbackResult{err: fmt.Errorf("OAuth authorization failed: %s", message)}
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "authorization code missing", http.StatusBadRequest)
			result <- callbackResult{err: errors.New("OAuth callback did not contain a code")}
			return
		}
		_, _ = fmt.Fprintln(w, "Authorization complete. You can close this window.")
		result <- callbackResult{code: code}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case result <- callbackResult{err: err}:
			default:
			}
		}
	}()
	defer server.Shutdown(context.Background())

	authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))
	out("Open this URL to authorize vitald:\n\n%s\n", authURL)
	if openBrowser {
		_ = launchBrowser(authURL)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case callback := <-result:
		if callback.err != nil {
			return nil, callback.err
		}
		token, err := cfg.Exchange(ctx, callback.code)
		if err != nil {
			return nil, fmt.Errorf("exchange OAuth code: %w", err)
		}
		if err := store.Save(token); err != nil {
			return nil, err
		}
		return token, nil
	}
}

type callbackResult struct {
	code string
	err  error
}

func randomState() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate OAuth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func launchBrowser(target string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "open", []string{target}
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", target}
	default:
		command, args = "xdg-open", []string{target}
	}
	return exec.Command(command, args...).Start()
}
