package typedRoaring

import "testing"

func FuzzBitmapOperations(f *testing.F) {
	for _, seed := range [][]byte{
		{},
		{0, 0, 1, 0, 2, 1},
		{7, 0, 7, 1, 8, 2, 9, 3},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, ops []byte) {
		var bitmap Bitmap[uint32]
		expected := make(map[uint32]bool)
		for i := 0; i+1 < len(ops); i += 2 {
			value := uint32(ops[i])
			switch ops[i+1] % 4 {
			case 0:
				bitmap.Add(value)
				expected[value] = true
			case 1:
				bitmap.Remove(value)
				delete(expected, value)
			case 2:
				got := bitmap.CheckedAdd(value)
				want := !expected[value]
				if got != want {
					t.Fatalf("CheckedAdd(%d) = %v, want %v", value, got, want)
				}
				expected[value] = true
			case 3:
				got := bitmap.CheckedRemove(value)
				want := expected[value]
				if got != want {
					t.Fatalf("CheckedRemove(%d) = %v, want %v", value, got, want)
				}
				delete(expected, value)
			}
			assertBitmapMatches(t, &bitmap, expected)
		}
	})
}

func assertBitmapMatches(t *testing.T, bitmap *Bitmap[uint32], expected map[uint32]bool) {
	t.Helper()
	if got, want := bitmap.GetCardinality(), uint64(len(expected)); got != want {
		t.Fatalf("cardinality = %d, want %d", got, want)
	}
	if got, want := bitmap.IsEmpty(), len(expected) == 0; got != want {
		t.Fatalf("IsEmpty = %v, want %v", got, want)
	}
	seen := make(map[uint32]bool)
	bitmap.Iterate(func(value uint32) bool {
		if !expected[value] {
			t.Fatalf("unexpected iterated value %d", value)
		}
		if seen[value] {
			t.Fatalf("duplicate iterated value %d", value)
		}
		seen[value] = true
		return true
	})
	if len(seen) != len(expected) {
		t.Fatalf("iterated %d values, want %d", len(seen), len(expected))
	}
	for value := uint32(0); value < 32; value++ {
		if got, want := bitmap.Contains(value), expected[value]; got != want {
			t.Fatalf("Contains(%d) = %v, want %v", value, got, want)
		}
	}
	for start := uint32(0); start < 32; start += 7 {
		end := start + 11
		var want uint64
		for value := range expected {
			if start <= value && value < end {
				want++
			}
		}
		if got := bitmap.RangeCardinality(start, end); got != want {
			t.Fatalf("RangeCardinality(%d, %d) = %d, want %d", start, end, got, want)
		}
	}
	clone := bitmap.Clone()
	if got, want := clone.GetCardinality(), bitmap.GetCardinality(); got != want {
		t.Fatalf("clone cardinality = %d, want %d", got, want)
	}
}
