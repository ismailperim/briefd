package minilm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Present reports whether dir already holds every file of spec.
func Present(spec Spec, dir string) bool {
	for _, f := range spec.Files {
		if _, err := os.Stat(filepath.Join(dir, f.Name)); err != nil {
			return false
		}
	}
	return true
}

// Pull downloads any missing file of spec into dir, verifying digests. It
// is idempotent: files already present are left alone (their digest is not
// re-checked so an offline install with locally provided files works).
func Pull(ctx context.Context, spec Spec, dir, baseURL string, logger *slog.Logger) error {
	if baseURL == "" {
		baseURL = spec.BaseURL
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating model dir %s: %w", dir, err)
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	for _, f := range spec.Files {
		dst := filepath.Join(dir, f.Name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		logger.Info("downloading embedding model file", "model", spec.Name, "file", f.Name, "from", baseURL)
		if err := download(ctx, client, baseURL+f.Name, dst, f.SHA256); err != nil {
			return fmt.Errorf("downloading %s: %w", f.Name, err)
		}
	}
	return nil
}

func download(ctx context.Context, client *http.Client, url, dst, wantSHA string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".briefd-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, wantSHA)
	}
	return os.Rename(tmp.Name(), dst)
}

// ErrNotPresent is returned by LoadOrPull when the model is missing and
// downloading is disabled.
var ErrNotPresent = errors.New("model files not present")

// LoadOrPull loads spec from dir, downloading it first when allowed.
func LoadOrPull(ctx context.Context, spec Spec, dir string, autoDownload bool, baseURL string, logger *slog.Logger) (*Model, error) {
	if !Present(spec, dir) {
		if !autoDownload {
			return nil, fmt.Errorf("%w in %s (run `briefd model pull` or enable embeddings.auto_download)", ErrNotPresent, dir)
		}
		if err := Pull(ctx, spec, dir, baseURL, logger); err != nil {
			return nil, err
		}
	}
	return Load(spec, dir)
}
