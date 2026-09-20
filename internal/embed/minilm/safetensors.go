package minilm

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// safetensors is a minimal reader for the Hugging Face safetensors format:
// an 8-byte little-endian header length, a JSON header mapping tensor names
// to {dtype, shape, data_offsets}, then the raw tensor bytes.
type safetensors struct {
	header map[string]tensorInfo
	data   []byte
}

type tensorInfo struct {
	DType       string `json:"dtype"`
	Shape       []int  `json:"shape"`
	DataOffsets [2]int `json:"data_offsets"`
}

func openSafetensors(path string) (*safetensors, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(raw) < 8 {
		return nil, fmt.Errorf("reading %s: file too short", path)
	}
	n := binary.LittleEndian.Uint64(raw[:8])
	if n > uint64(len(raw)-8) {
		return nil, fmt.Errorf("reading %s: corrupt header length", path)
	}
	var header map[string]json.RawMessage
	if err := json.Unmarshal(raw[8:8+n], &header); err != nil {
		return nil, fmt.Errorf("reading %s header: %w", path, err)
	}
	st := &safetensors{header: map[string]tensorInfo{}, data: raw[8+n:]}
	for name, msg := range header {
		if name == "__metadata__" {
			continue
		}
		var ti tensorInfo
		if err := json.Unmarshal(msg, &ti); err != nil {
			return nil, fmt.Errorf("reading %s tensor %s: %w", path, name, err)
		}
		if ti.DataOffsets[0] < 0 || ti.DataOffsets[1] > len(st.data) || ti.DataOffsets[0] > ti.DataOffsets[1] {
			return nil, fmt.Errorf("reading %s tensor %s: offsets out of range", path, name)
		}
		st.header[name] = ti
	}
	return st, nil
}

// f32 returns a float32 tensor by name, validating its shape.
func (st *safetensors) f32(name string, shape ...int) ([]float32, error) {
	ti, ok := st.header[name]
	if !ok {
		return nil, fmt.Errorf("tensor %s: not found", name)
	}
	if ti.DType != "F32" {
		return nil, fmt.Errorf("tensor %s: dtype %s, want F32", name, ti.DType)
	}
	if len(ti.Shape) != len(shape) {
		return nil, fmt.Errorf("tensor %s: shape %v, want %v", name, ti.Shape, shape)
	}
	want := 1
	for i, d := range shape {
		if ti.Shape[i] != d {
			return nil, fmt.Errorf("tensor %s: shape %v, want %v", name, ti.Shape, shape)
		}
		want *= d
	}
	b := st.data[ti.DataOffsets[0]:ti.DataOffsets[1]]
	if len(b) != want*4 {
		return nil, fmt.Errorf("tensor %s: %d bytes, want %d", name, len(b), want*4)
	}
	out := make([]float32, want)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out, nil
}
