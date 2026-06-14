package torrent

import (
	"context"
	"testing"
	"time"

	g "github.com/anacrolix/generics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wchen99998/torrent/metainfo"
	"github.com/wchen99998/torrent/storage"
)

func newConsumerAPITestTorrent(t *testing.T, info *metainfo.Info) (*Client, *Torrent) {
	dir := t.TempDir()
	cfg := TestingConfig(t)
	cfg.DataDir = dir
	cfg.DefaultStorage = storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir:      dir,
		UsePartFiles:       g.Some(false),
		ForceClassicFileIO: true,
	})
	cl, err := NewClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	tor, new := cl.AddTorrentOpt(AddTorrentOpts{
		InfoHash:                 testingTorrentInfoHash,
		DisableInitialPieceCheck: true,
	})
	require.True(t, new)
	require.NoError(t, tor.setInfoUnlocked(info))
	return cl, tor
}

type cappedConsumerAPIStorage struct{}

func (cappedConsumerAPIStorage) OpenTorrent(
	_ context.Context,
	_ *metainfo.Info,
	_ metainfo.Hash,
) (storage.TorrentImpl, error) {
	capacity := func() (int64, bool) { return 1, true }
	return storage.TorrentImpl{
		Piece: func(metainfo.Piece) storage.PieceImpl {
			return storagePiece{complete: false}
		},
		Capacity: &capacity,
	}, nil
}

func newCappedConsumerAPITestTorrent(t *testing.T, info *metainfo.Info) (*Client, *Torrent) {
	cfg := TestingConfig(t)
	cfg.DefaultStorage = cappedConsumerAPIStorage{}
	cl, err := NewClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	tor, new := cl.AddTorrentOpt(AddTorrentOpts{
		InfoHash:                 testingTorrentInfoHash,
		DisableInitialPieceCheck: true,
	})
	require.True(t, new)
	require.NoError(t, tor.setInfoUnlocked(info))
	return cl, tor
}

func requirePendingPiecesMatchRequestOrder(t *testing.T, tor *Torrent) {
	t.Helper()
	exclPro, exclPending, ok := tor.pendingPiecesMatchRequestOrder()
	require.Truef(
		t,
		ok,
		"piece request order has %v and pending pieces has %v",
		exclPro.String(),
		exclPending.String(),
	)
}

func completePiece(t *testing.T, tor *Torrent, pieceIndex int, data []byte) {
	p := tor.piece(pieceIndex).Storage()
	_, err := p.WriteAt(data, 0)
	require.NoError(t, err)
	require.NoError(t, p.MarkComplete())
	tor.piece(pieceIndex).UpdateCompletion()
}

func TestFileIndexCompletedAndSnapshots(t *testing.T) {
	_, tor := newConsumerAPITestTorrent(t, &metainfo.Info{
		Name:        "payload",
		PieceLength: 2,
		Pieces:      make([]byte, metainfo.HashSize*2),
		Files: []metainfo.FileInfo{
			{Path: []string{"a.bin"}, Length: 2},
			{Path: []string{"b.bin"}, Length: 2},
		},
	})
	completePiece(t, tor, 0, []byte("aa"))

	files := tor.Files()
	require.Len(t, files, 2)
	assert.Equal(t, 0, files[0].Index())
	assert.Equal(t, 1, files[1].Index())
	assert.EqualValues(t, 2, files[0].Completed())
	assert.EqualValues(t, 0, files[1].Completed())

	snapshots := tor.FileSnapshots()
	require.Len(t, snapshots, 2)
	assert.Equal(t, 0, snapshots[0].Index)
	assert.Equal(t, "payload/a.bin", snapshots[0].Path)
	assert.Equal(t, "a.bin", snapshots[0].DisplayPath)
	assert.EqualValues(t, 2, snapshots[0].Length)
	assert.EqualValues(t, 2, snapshots[0].Completed)
	assert.Equal(t, 1, snapshots[1].Index)
	assert.EqualValues(t, 0, snapshots[1].Completed)

	snapshots[0].FileInfo.Path[0] = "mutated"
	assert.Equal(t, "a.bin", tor.FileSnapshots()[0].FileInfo.Path[0])
}

func TestWaitInfo(t *testing.T) {
	cl := newTestingClient(t)
	tor, new := cl.AddTorrentOpt(testingAddTorrentOpts)
	require.True(t, new)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tor.WaitInfo(ctx)
	assert.ErrorIs(t, err, context.Canceled)

	info := &metainfo.Info{
		Name:        "payload",
		Length:      1,
		PieceLength: 1,
		Pieces:      make([]byte, metainfo.HashSize),
	}
	require.NoError(t, tor.setInfoUnlocked(info))
	got, err := tor.WaitInfo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, info, got)
}

func TestWaitInfoTorrentClosed(t *testing.T) {
	cl := newTestingClient(t)
	tor, new := cl.AddTorrentOpt(testingAddTorrentOpts)
	require.True(t, new)
	tor.Drop()
	_, err := tor.WaitInfo(context.Background())
	assert.ErrorIs(t, err, errTorrentClosed)
}

func TestFileWaitComplete(t *testing.T) {
	_, tor := newConsumerAPITestTorrent(t, &metainfo.Info{
		Name:        "payload",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, metainfo.HashSize),
	})
	file := tor.Files()[0]

	done := make(chan error, 1)
	go func() {
		done <- file.WaitComplete(context.Background())
	}()
	completePiece(t, tor, 0, []byte("data"))
	require.NoError(t, <-done)
}

func TestFileVerifiedComplete(t *testing.T) {
	_, tor := newConsumerAPITestTorrent(t, &metainfo.Info{
		Name:        "payload",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, metainfo.HashSize),
	})
	file := tor.Files()[0]
	piece := tor.piece(0)
	storage := piece.Storage()
	_, err := storage.WriteAt([]byte("data"), 0)
	require.NoError(t, err)

	assert.False(t, file.VerifiedComplete())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, file.WaitVerifiedComplete(ctx), context.Canceled)

	require.NoError(t, storage.MarkComplete())
	piece.UpdateCompletion()
	assert.True(t, file.VerifiedComplete())
	require.NoError(t, file.WaitVerifiedComplete(context.Background()))
}

func TestFileWaitCompleteContextCancelAndClose(t *testing.T) {
	_, tor := newConsumerAPITestTorrent(t, &metainfo.Info{
		Name:        "payload",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, metainfo.HashSize),
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, tor.Files()[0].WaitComplete(ctx), context.Canceled)

	tor.Drop()
	assert.ErrorIs(t, tor.Files()[0].WaitComplete(context.Background()), errTorrentClosed)
}

func TestFileWaitCompleteZeroLength(t *testing.T) {
	_, tor := newConsumerAPITestTorrent(t, &metainfo.Info{
		Name:        "payload",
		PieceLength: 4,
		Files:       []metainfo.FileInfo{{Path: []string{"empty"}, Length: 0}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	require.NoError(t, tor.Files()[0].WaitComplete(ctx))
}

func TestDisallowDataDownloadUpdatesPendingPieces(t *testing.T) {
	cl, tor := newConsumerAPITestTorrent(t, &metainfo.Info{
		Name:        "payload",
		Length:      8,
		PieceLength: 4,
		Pieces:      make([]byte, 2*metainfo.HashSize),
	})
	require.NoError(t, tor.piece(0).Storage().MarkNotComplete())
	require.NoError(t, tor.piece(1).Storage().MarkNotComplete())
	tor.piece(0).UpdateCompletion()
	tor.piece(1).UpdateCompletion()
	tor.Files()[0].Download()

	cl.lock()
	defer cl.unlock()
	require.False(t, tor._pendingPieces.IsEmpty())
	requirePendingPiecesMatchRequestOrder(t, tor)
	tor.disallowDataDownloadLocked()
	assert.True(t, tor._pendingPieces.IsEmpty())
	requirePendingPiecesMatchRequestOrder(t, tor)
	short := *tor.canonicalShortInfohash()
	for item := range tor.getPieceRequestOrder().Iter {
		if item.Key.InfoHash.Value() == short && item.State.Priority > PiecePriorityNone && !tor.ignorePieceForRequests(item.Key.Index) {
			t.Fatalf("piece %d remained requestable after data download was disallowed", item.Key.Index)
		}
	}
}

func TestAllowAndDisallowDataDownloadKeepCappedRequestOrderConsistent(t *testing.T) {
	cl, tor := newCappedConsumerAPITestTorrent(t, &metainfo.Info{
		Name:        "payload",
		Length:      8,
		PieceLength: 4,
		Pieces:      make([]byte, 2*metainfo.HashSize),
	})
	tor.Files()[0].Download()

	cl.lock()
	require.False(t, tor._pendingPieces.IsEmpty())
	requirePendingPiecesMatchRequestOrder(t, tor)
	tor.disallowDataDownloadLocked()
	assert.True(t, tor._pendingPieces.IsEmpty())
	requirePendingPiecesMatchRequestOrder(t, tor)
	cl.unlock()

	tor.AllowDataDownload()

	cl.lock()
	defer cl.unlock()
	require.False(t, tor._pendingPieces.IsEmpty())
	requirePendingPiecesMatchRequestOrder(t, tor)
}

func TestPrivateTorrentDisablesPeerExchangeAndDhtAnnounce(t *testing.T) {
	private := true
	_, tor := newConsumerAPITestTorrent(t, &metainfo.Info{
		Name:        "payload",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, metainfo.HashSize),
		Private:     &private,
	})

	assert.True(t, tor.Private())
	assert.False(t, tor.peerExchangeEnabled())
	_, _, err := tor.AnnounceToDht(nil)
	assert.ErrorContains(t, err, "private")
}

func TestAddPeerAddrs(t *testing.T) {
	cfg := TestingConfig(t)
	cfg.DialForPeerConns = false
	cl, err := NewClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	tor, new := cl.AddTorrentOpt(testingAddTorrentOpts)
	require.True(t, new)
	assert.Equal(t, 2, tor.AddPeerAddrs([]string{"127.0.0.1:1", "127.0.0.1:2"}))
	assert.Equal(t, 0, tor.AddPeerAddrs([]string{"127.0.0.1:1"}))
}

func TestPeerSnapshots(t *testing.T) {
	cl := newTestingClient(t)
	tor := cl.newTorrentForTesting()
	require.NoError(t, tor.setInfoUnlocked(&metainfo.Info{
		Name:        "payload",
		Length:      10,
		PieceLength: 1,
		Pieces:      make([]byte, metainfo.HashSize*10),
	}))
	pc := cl.newConnection(nil, newConnectionOpts{
		outgoing:   true,
		remoteAddr: StringAddr("127.0.0.1:6881"),
		network:    "tcp",
		connString: "127.0.0.1:6881",
	})
	pc.setTorrent(tor)
	pc.PeerID[0] = 1
	pc.Discovery = PeerSourceDirect
	pc.requestState.Interested = true
	pc.peerInterested = true
	pc.peerChoking = false
	pc.choking = true
	tor.conns[pc] = struct{}{}
	require.NoError(t, pc.peerSentBitfield([]bool{
		true, false, true, false, false, false, false, true,
		false, false, false, false, false, false, false, false,
	}))

	snapshots := tor.PeerSnapshots()
	require.Len(t, snapshots, 1)
	s := snapshots[0]
	assert.Equal(t, pc.PeerID, s.PeerID)
	assert.Equal(t, "127.0.0.1:6881", s.RemoteAddr)
	assert.Equal(t, PeerSource(PeerSourceDirect), s.Source)
	assert.True(t, s.LocalChoking)
	assert.True(t, s.LocalInterested)
	assert.False(t, s.RemoteChoking)
	assert.True(t, s.RemoteInterested)
	assert.False(t, s.Seeder)
	assert.Equal(t, []byte{0xa1, 0x00}, s.RemoteBitfield)

	s.RemoteBitfield[0] = 0
	assert.Equal(t, []byte{0xa1, 0x00}, tor.PeerSnapshots()[0].RemoteBitfield)
}

func TestPeerSnapshotsHaveAllAndUnknownInfo(t *testing.T) {
	cl := newTestingClient(t)
	tor := cl.newTorrentForTesting()
	pc := &PeerConn{Peer: Peer{cl: cl, t: tor, callbacks: &cl.config.Callbacks}}
	pc.initRequestState()
	pc.legacyPeerImpl = pc
	pc.peerImpl = pc
	tor.conns[pc] = struct{}{}
	require.NoError(t, pc.onPeerSentHaveAll())
	require.Nil(t, tor.PeerSnapshots()[0].RemoteBitfield)

	require.NoError(t, tor.setInfoUnlocked(&metainfo.Info{
		Name:        "payload",
		Length:      3,
		PieceLength: 1,
		Pieces:      make([]byte, metainfo.HashSize*3),
	}))
	s := tor.PeerSnapshots()[0]
	assert.True(t, s.Seeder)
	assert.Equal(t, []byte{0xe0}, s.RemoteBitfield)
}

func TestTorrentSpecConstructors(t *testing.T) {
	mi := metainfo.MetaInfo{
		InfoBytes: []byte("de"),
	}
	data := []byte("d4:infodee")
	parsedMi, info, spec, err := ParseMetaInfoBytes(data)
	require.NoError(t, err)
	require.NotNil(t, parsedMi)
	require.NotNil(t, info)
	require.NotNil(t, spec)
	require.Equal(t, mi.HashInfoBytes(), spec.InfoHash)

	spec, err = SpecFromBytes(data)
	require.NoError(t, err)
	require.Equal(t, mi.HashInfoBytes(), spec.InfoHash)

	_, _, _, err = ParseMetaInfoBytes([]byte("not bencode"))
	require.Error(t, err)
	_, _, _, err = ParseMetaInfoBytes([]byte("d4:info3:bad4:spam4:eggse"))
	require.Error(t, err)

	assert.Panics(t, func() {
		MustTorrentSpecFromMetaInfo(&metainfo.MetaInfo{InfoBytes: []byte("bad")})
	})
	_, err = TorrentSpecFromMetaInfo(&metainfo.MetaInfo{InfoBytes: []byte("bad")})
	require.Error(t, err)
}
