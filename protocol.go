package torrent

import (
	pp "github.com/wchen99998/torrent/peer_protocol"
)

func makeCancelMessage(r Request) pp.Message {
	return pp.MakeCancelMessage(r.Index, r.Begin, r.Length)
}
