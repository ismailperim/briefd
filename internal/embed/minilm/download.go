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

// Files that make up the model, with pinned digests of the upstream
// revision so a tampered or truncated download is rejected.
var files = []struct {
	name, sha256 string
}{
	{"model.safetensors", "53aa51172d142c89d9012cce15ae4d6cc0ca6895895114379cacb4fab128d9db"},
	{"vocab.txt", "07eced375cec144d27c900241f3e339478dec958f92fddbc551f295c992038a3"},
}

// DefaultBaseURL is where the weights are fetched from.
const DefaultBaseURL = "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/main/"

// Present reports whether dir already holds every model file.
func Present(dir string) bool {
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(dir, f.name)); err != nil {
			return false
		}
	}
	return true
}

// Pull downloads any missing model file into dir, verifying digests. It is
// idempotent: files already present are left alone (their digest is not
// re-checked so an offline install with locally provided files works).
func Pull(ctx context.Context, dir, baseURL string, logger *slog.Logger) error {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating model dir %s: %w", dir, err)
	}
	client := &http.Client{Timeout: 15 * time.Minute}
	for _, f := range files {
		dst := filepath.Join(dir, f.name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		logger.Info("downloading embedding model file", "file", f.name, "from", baseURL)
		if err := download(ctx, client, baseURL+f.name, dst, f.sha256); err != nil {
			return fmt.Errorf("downloading %s: %w", f.name, err)
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

// LoadOrPull loads the model from dir, downloading it first when allowed.
func LoadOrPull(ctx context.Context, dir string, autoDownload bool, baseURL string, logger *slog.Logger) (*Model, error) {
	if !Present(dir) {
		if !autoDownload {
			return nil, fmt.Errorf("%w in %s (run `briefd model pull` or enable embeddings.auto_download)", ErrNotPresent, dir)
		}
		if err := Pull(ctx, dir, baseURL, logger); err != nil {
			return nil, err
		}
	}
	return Load(dir)
}
