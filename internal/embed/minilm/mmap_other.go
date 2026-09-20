//go:build !unix

package minilm

import "os"

// mapFile falls back to reading the whole file where mmap is unavailable.
func mapFile(path string) ([]byte, func() error, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return data, func() error { return nil }, nil
}
