package torrent

import (
	"github.com/RoaringBitmap/roaring/v2"

	"github.com/wchen99998/torrent/metainfo"
)

// FileSnapshot is an immutable view of a torrent file at a point in time.
type FileSnapshot struct {
	Index       int
	Path        string
	DisplayPath string
	Length      int64
	Completed   int64
	// VerifiedCompleted excludes dirty chunks that have not passed piece
	// verification. Completed intentionally retains its historical semantics.
	VerifiedCompleted int64
	Priority          PiecePriority
	Released          bool
	FileInfo          metainfo.FileInfo
}

// PeerSnapshot is an immutable view of a peer at a point in time.
type PeerSnapshot struct {
	PeerID           PeerID
	RemoteAddr       string
	Source           PeerSource
	Stats            PeerStats
	LocalChoking     bool
	LocalInterested  bool
	RemoteChoking    bool
	RemoteInterested bool
	Seeder           bool
	// RemoteBitfield is encoded in BitTorrent wire bit order. It is nil until
	// torrent info is known.
	RemoteBitfield []byte
}

// FileSnapshots returns per-file state for consumers that need progress and
// stable file indexes without holding File handles.
func (t *Torrent) FileSnapshots() []FileSnapshot {
	t.cl.rLock()
	defer t.cl.rUnlock()
	if !t.haveInfo() {
		return nil
	}
	ret := make([]FileSnapshot, 0, len(*t.files))
	for _, f := range *t.files {
		fi := f.fi
		fi.Path = append([]string(nil), fi.Path...)
		fi.PathUtf8 = append([]string(nil), fi.PathUtf8...)
		state, _ := f.StorageState()
		ret = append(ret, FileSnapshot{
			Index:             f.index,
			Path:              f.path,
			DisplayPath:       f.displayPath,
			Length:            f.length,
			Completed:         f.bytesCompletedLocked(),
			VerifiedCompleted: f.verifiedBytesCompletedLocked(),
			Priority:          f.prio,
			Released:          state.Released,
			FileInfo:          fi,
		})
	}
	return ret
}

// PeerSnapshots returns per-peer state for consumers without exposing mutable
// peer internals.
func (t *Torrent) PeerSnapshots() []PeerSnapshot {
	t.cl.rLock()
	defer t.cl.rUnlock()
	ret := make([]PeerSnapshot, 0, len(t.conns)+len(t.webSeeds))
	t.iterPeers(func(p *Peer) {
		var peerID PeerID
		var localInterested bool
		if pc, ok := p.legacyPeerImpl.(*PeerConn); ok {
			peerID = pc.PeerID
			localInterested = pc.requestState.Interested
		}
		all, known := p.peerHasAllPieces()
		_, haveAll := t.connsWithAllPieces[p]
		snap := PeerSnapshot{
			PeerID:           peerID,
			Source:           p.Discovery,
			Stats:            p.statsLocked(),
			LocalChoking:     p.choking,
			LocalInterested:  localInterested,
			RemoteChoking:    p.peerChoking,
			RemoteInterested: p.peerInterested,
			Seeder:           t.haveInfo() && all && known,
		}
		if p.RemoteAddr != nil {
			snap.RemoteAddr = p.RemoteAddr.String()
		}
		if t.haveInfo() {
			pieces := p.peerPieces().Clone()
			if haveAll {
				pieces.AddRange(0, uint64(t.numPieces()))
			}
			snap.RemoteBitfield = packPieceBitfield(t.numPieces(), pieces)
		}
		ret = append(ret, snap)
	})
	return ret
}

func packPieceBitfield(numPieces pieceIndex, pieces *roaring.Bitmap) []byte {
	if numPieces < 0 {
		return nil
	}
	ret := make([]byte, (numPieces+7)/8)
	pieces.Iterate(func(piece uint32) bool {
		if pieceIndex(piece) >= numPieces {
			return false
		}
		ret[piece/8] |= 1 << uint(7-piece%8)
		return true
	})
	return ret
}
