//go:build unix

package minilm

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// mapFile maps path read-only into memory so large weight matrices (the
// 384 MB multilingual vocabulary, for example) are paged in on demand
// instead of being copied into the Go heap.
func mapFile(path string) ([]byte, func() error, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if st.Size() == 0 {
		return nil, nil, fmt.Errorf("%s is empty", path)
	}
	data, err := unix.Mmap(int(f.Fd()), 0, int(st.Size()), unix.PROT_READ, unix.MAP_PRIVATE)
	if err != nil {
		return nil, nil, fmt.Errorf("mmap %s: %w", path, err)
	}
	return data, func() error { return unix.Munmap(data) }, nil
}
