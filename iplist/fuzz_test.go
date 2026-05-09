//go:build !wasm

package iplist

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
)

func FuzzParseBlocklistP2PLine(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(""),
		[]byte("# comment"),
		[]byte("local:127.0.0.1-127.0.0.255"),
		[]byte("ipv6:2001:db8::1-2001:db8::ffff"),
		[]byte("bad:127.0.0.1-2001:db8::1"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line []byte) {
		r, ok, err := ParseBlocklistP2PLine(line)
		if err != nil || !ok {
			return
		}
		if r.First == nil || r.Last == nil {
			t.Fatalf("parsed range has nil endpoint: %#v", r)
		}
		if len(r.First) != len(r.Last) {
			t.Fatalf("parsed range endpoint lengths differ: %q", r)
		}
		if bytes.Compare(r.First, r.Last) <= 0 {
			_, found := New([]Range{r}).Lookup(r.First)
			if !found {
				t.Fatalf("single-range list did not find first endpoint: %q", r)
			}
		}
	})
}

func FuzzPackedIPListLookup(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte{0, 0},
		[]byte{1, 4, 2, 8, 3, 16},
		[]byte{255, 0, 0, 1},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		ranges := packedFuzzRanges(b)
		if len(ranges) == 0 {
			return
		}
		ipl := New(ranges)
		var packed bytes.Buffer
		if err := ipl.WritePacked(&packed); err != nil {
			t.Fatalf("write packed ip list: %v", err)
		}
		pil := NewFromPacked(packed.Bytes())
		if got, want := pil.NumRanges(), ipl.NumRanges(); got != want {
			t.Fatalf("packed range count = %d, want %d", got, want)
		}
		for _, r := range ranges {
			for _, ip := range []net.IP{r.First, r.Last} {
				want, wantOK := ipl.Lookup(ip)
				got, gotOK := pil.Lookup(ip)
				if gotOK != wantOK {
					t.Fatalf("packed lookup ok = %v, want %v for %v", gotOK, wantOK, ip)
				}
				if gotOK && got.Description != want.Description {
					t.Fatalf("packed lookup description = %q, want %q", got.Description, want.Description)
				}
			}
		}
	})
}

func packedFuzzRanges(b []byte) (ranges []Range) {
	const maxIPv4 = uint64(1<<32 - 1)
	var next uint64
	for i := 0; i+1 < len(b) && len(ranges) < 32; i += 2 {
		next += uint64(b[i])
		if next > maxIPv4 {
			return
		}
		end := next + uint64(b[i+1])
		if end > maxIPv4 {
			end = maxIPv4
		}
		ranges = append(ranges, Range{
			First:       fuzzIPv4(uint32(next)),
			Last:        fuzzIPv4(uint32(end)),
			Description: fmt.Sprintf("range-%d", len(ranges)),
		})
		if end == maxIPv4 {
			return
		}
		next = end + 1
	}
	return
}

func fuzzIPv4(v uint32) net.IP {
	ret := make(net.IP, 4)
	binary.BigEndian.PutUint32(ret, v)
	return ret
}
