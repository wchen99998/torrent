package segments

import (
	"encoding/binary"
	"testing"
)

func FuzzIndexLocateIter(f *testing.F) {
	for _, seed := range [][]byte{
		{0, 2, 1, 0, 2, 0, 3},
		{0, 1, 0, 2, 0, 2, 0},
		{1, 3, 2, 0, 2, 0},
		{255, 255, 0, 0},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		lengths, total := fuzzSegmentLengths(b)
		index := NewIndex(LengthIterFromSlice(lengths))
		needle := fuzzNeedle(b, total)

		var covered Int
		lastIndex := -1
		index.LocateIter(needle)(func(i int, e Extent) bool {
			if i <= lastIndex {
				t.Fatalf("indices not strictly increasing: %d after %d", i, lastIndex)
			}
			lastIndex = i
			if i < 0 || i >= len(lengths) {
				t.Fatalf("segment index %d out of range for %d lengths", i, len(lengths))
			}
			if e.Start < 0 || e.Length < 0 || e.End() > lengths[i] {
				t.Fatalf("located invalid extent %v in segment %d with length %d", e, i, lengths[i])
			}
			covered += e.Length
			return true
		})
		if want := overlapLength(needle, total); covered != want {
			t.Fatalf("covered length = %d, want %d for needle %v over total %d", covered, want, needle, total)
		}

		for _, off := range []Int{0, total / 2, total - 1, needle.Start} {
			if off < 0 || off >= total {
				continue
			}
			got := index.LocateOffset(off)
			want, wantOK := linearLocateOffset(lengths, off)
			if got.Ok != wantOK {
				t.Fatalf("LocateOffset(%d) ok = %v, want %v", off, got.Ok, wantOK)
			}
			if got.Ok && got.Value != want {
				t.Fatalf("LocateOffset(%d) = %#v, want %#v", off, got.Value, want)
			}
		}
	})
}

func fuzzSegmentLengths(b []byte) (lengths []Length, total Int) {
	for _, value := range b {
		if len(lengths) == 64 {
			break
		}
		length := Length(value % 9)
		lengths = append(lengths, length)
		total += length
	}
	return
}

func fuzzNeedle(b []byte, total Int) Extent {
	modulus := uint64(total + 2)
	if modulus == 0 {
		modulus = 1
	}
	return Extent{
		Start:  Int(fuzzUint16At(b, 0) % modulus),
		Length: Int(fuzzUint16At(b, 2) % modulus),
	}
}

func fuzzUint16At(b []byte, off int) uint64 {
	if len(b) < off+2 {
		return 0
	}
	return uint64(binary.BigEndian.Uint16(b[off:]))
}

func overlapLength(needle Extent, total Int) Int {
	if needle.Length <= 0 || needle.Start >= total {
		return 0
	}
	return max(min(needle.End(), total)-needle.Start, 0)
}

func linearLocateOffset(lengths []Length, off Int) (ret IndexAndOffset, ok bool) {
	var start Int
	for i, length := range lengths {
		if length > 0 && start <= off && off < start+length {
			return IndexAndOffset{
				Index:  i,
				Offset: off - start,
			}, true
		}
		start += length
	}
	return
}
