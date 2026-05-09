package peer_protocol

import (
	"bytes"
	"testing"

	"github.com/wchen99998/torrent/bencode"
)

func FuzzPexMsgRoundTrip(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte("de"),
		[]byte("d5:added0:e"),
		[]byte("d5:added6:abcdef7:added.f1:\x01e"),
		bencode.MustMarshal(PexMsg{}),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		msg, err := LoadPexMsg(b)
		if err != nil {
			t.Skip(err)
		}
		out, err := bencode.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal parsed pex message: %v", err)
		}
		msg2, err := LoadPexMsg(out)
		if err != nil {
			t.Fatalf("reparse marshaled pex message: %v", err)
		}
		out2, err := bencode.Marshal(msg2)
		if err != nil {
			t.Fatalf("marshal reparsed pex message: %v", err)
		}
		if !bytes.Equal(out2, out) {
			t.Fatalf("pex message marshal not stable: got %q, want %q", out2, out)
		}
	})
}
