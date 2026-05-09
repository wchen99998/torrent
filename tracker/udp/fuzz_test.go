package udp

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func FuzzBinaryProtocolRoundTrip(f *testing.F) {
	f.Add(make([]byte, 16))
	f.Add([]byte{
		0, 0, 0, 0, 0, 0, 4, 0,
		0, 0, 0, 1, 0, 0, 0, 2,
		0, 0, 0, 3,
	})
	f.Fuzz(func(t *testing.T, b []byte) {
		fuzzFixedBinaryRoundTrip[ConnectionRequest](t, b)
		fuzzFixedBinaryRoundTrip[RequestHeader](t, b)
		fuzzFixedBinaryRoundTrip[ResponseHeader](t, b)
		fuzzFixedBinaryRoundTrip[ConnectionResponse](t, b)
		fuzzFixedBinaryRoundTrip[AnnounceResponseHeader](t, b)
	})
}

func fuzzFixedBinaryRoundTrip[T any](t *testing.T, b []byte) {
	var v T
	size := binary.Size(v)
	if size < 0 {
		t.Fatalf("non-fixed size protocol value %T", v)
	}
	input := make([]byte, size)
	copy(input, b)
	if err := Read(bytes.NewReader(input), &v); err != nil {
		t.Fatalf("read %T: %v", v, err)
	}
	var out bytes.Buffer
	if err := Write(&out, v); err != nil {
		t.Fatalf("write %T: %v", v, err)
	}
	if !bytes.Equal(out.Bytes(), input) {
		t.Fatalf("binary round trip for %T changed bytes: got %x, want %x", v, out.Bytes(), input)
	}
}
