package merkle

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func FuzzHashChunking(f *testing.F) {
	f.Add([]byte(""), []byte{1})
	f.Add(bytes.Repeat([]byte("a"), BlockSize+1), []byte{1, 7, 255})
	f.Fuzz(func(t *testing.T, data, cuts []byte) {
		oneShot := NewHash()
		if _, err := oneShot.Write(data); err != nil {
			t.Fatalf("one-shot write: %v", err)
		}

		chunked := NewHash()
		writeChunked(t, chunked, data, cuts)
		if got, want := chunked.Sum(nil), oneShot.Sum(nil); !bytes.Equal(got, want) {
			t.Fatalf("chunked hash sum = %x, want %x", got, want)
		}

		minLength := len(data)
		if len(cuts) != 0 {
			minLength += int(cuts[0])
		}
		if got := chunked.SumMinLength(nil, minLength); len(got) != sha256.Size {
			t.Fatalf("SumMinLength returned %d bytes, want %d", len(got), sha256.Size)
		}
	})
}

func FuzzCompactLayerToSliceHashes(f *testing.F) {
	f.Add("")
	f.Add(string(bytes.Repeat([]byte{1}, sha256.Size)))
	f.Add(string(bytes.Repeat([]byte{2}, sha256.Size+1)))
	f.Fuzz(func(t *testing.T, compact string) {
		hashes, err := CompactLayerToSliceHashes(compact)
		if len(compact)%sha256.Size != 0 {
			if err == nil {
				t.Fatalf("accepted compact layer with trailing bytes: len=%d", len(compact))
			}
			return
		}
		if err != nil {
			t.Fatalf("rejected complete compact layer len=%d: %v", len(compact), err)
		}
		if got, want := len(hashes)*sha256.Size, len(compact); got != want {
			t.Fatalf("decoded byte count = %d, want %d", got, want)
		}
	})
}

func writeChunked(t *testing.T, h *Hash, data, cuts []byte) {
	t.Helper()
	for len(data) != 0 {
		n := len(data)
		if len(cuts) != 0 {
			n = int(cuts[0])%len(data) + 1
			cuts = cuts[1:]
		}
		if _, err := h.Write(data[:n]); err != nil {
			t.Fatalf("chunked write: %v", err)
		}
		data = data[n:]
	}
}
