package httpTracker

import (
	"testing"

	"github.com/wchen99998/torrent/bencode"
)

func FuzzHttpResponseUnmarshal(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte("de"),
		[]byte("d5:peers0:e"),
		[]byte("d5:peersli1eee"),
		[]byte("d5:peersld2:ip7:1.2.3.44:porti6881eeee"),
		[]byte("d5:peersld2:ip7:1.2.3.47:peer id20:thisisthe20bytepeeri4:porti9999eeee"),
		[]byte("d8:intervali1800e5:peers6:abcdefe"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		var hr HttpResponse
		if err := bencode.Unmarshal(b, &hr); err != nil {
			t.Skip(err)
		}
		out, err := bencode.Marshal(hr)
		if err != nil {
			t.Fatalf("marshal parsed http tracker response: %v", err)
		}
		var hr2 HttpResponse
		if err := bencode.Unmarshal(out, &hr2); err != nil {
			t.Fatalf("reparse marshaled http tracker response: %v", err)
		}
	})
}
