package torrent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	g "github.com/anacrolix/generics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wchen99998/torrent/metainfo"
	"github.com/wchen99998/torrent/storage"
)

func TestFileExclusivePieces(t *testing.T) {
	for _, _case := range []struct {
		off, size, pieceSize int64
		begin, end           int
	}{
		{0, 2, 2, 0, 1},
		{1, 2, 2, 1, 1},
		{1, 4, 2, 1, 2},
	} {
		begin, end := byteRegionExclusivePieces(_case.off, _case.size, _case.pieceSize)
		assert.EqualValues(t, _case.begin, begin)
		assert.EqualValues(t, _case.end, end)
	}
}

func TestFileReleaseStorageRemovesFile(t *testing.T) {
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
	defer cl.Close()
	tor, new := cl.AddTorrentOpt(AddTorrentOpts{
		InfoHash:                 testingTorrentInfoHash,
		DisableInitialPieceCheck: true,
	})
	require.True(t, new)
	require.NoError(t, tor.setInfoUnlocked(&metainfo.Info{
		Name:        "payload",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, metainfo.HashSize),
	}))
	path := filepath.Join(dir, "payload")
	require.NoError(t, os.WriteFile(path, []byte("data"), 0o666))
	assert.False(t, tor.Files()[0].Released())
	assert.False(t, tor.FileSnapshots()[0].Released)

	require.NoError(t, tor.Files()[0].ReleaseStorage())

	_, err = os.Stat(path)
	assert.True(t, errors.Is(err, os.ErrNotExist), "expected released file to be removed, got %v", err)
	assert.True(t, tor.Files()[0].Released())
	assert.True(t, tor.FileSnapshots()[0].Released)
}

func TestFileReleasedSnapshotRestoredAfterReopen(t *testing.T) {
	dir := t.TempDir()
	info := &metainfo.Info{
		Name:        "payload",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, metainfo.HashSize),
	}
	newTorrent := func(t *testing.T) (*Client, *Torrent) {
		cfg := TestingConfig(t)
		cfg.DataDir = dir
		cfg.DefaultStorage = storage.NewFileOpts(storage.NewFileClientOpts{
			ClientBaseDir:      dir,
			UsePartFiles:       g.Some(false),
			ForceClassicFileIO: true,
		})
		cl, err := NewClient(cfg)
		require.NoError(t, err)
		tor, new := cl.AddTorrentOpt(AddTorrentOpts{
			InfoHash:                 testingTorrentInfoHash,
			DisableInitialPieceCheck: true,
		})
		require.True(t, new)
		require.NoError(t, tor.setInfoUnlocked(info))
		return cl, tor
	}

	cl, tor := newTorrent(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "payload"), []byte("data"), 0o666))
	require.NoError(t, tor.Files()[0].ReleaseStorage())
	assert.True(t, tor.FileSnapshots()[0].Released)
	require.Empty(t, cl.Close())

	cl, tor = newTorrent(t)
	defer cl.Close()
	assert.True(t, tor.Files()[0].Released())
	assert.True(t, tor.FileSnapshots()[0].Released)
}

func TestFileDiscardStorageRemovesFileAndMarksIncomplete(t *testing.T) {
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
	defer cl.Close()
	tor, new := cl.AddTorrentOpt(AddTorrentOpts{
		InfoHash:                 testingTorrentInfoHash,
		DisableInitialPieceCheck: true,
	})
	require.True(t, new)
	require.NoError(t, tor.setInfoUnlocked(&metainfo.Info{
		Name:        "payload",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, metainfo.HashSize),
	}))
	path := filepath.Join(dir, "payload")
	p := tor.piece(0).Storage()
	_, err = p.WriteAt([]byte("data"), 0)
	require.NoError(t, err)
	require.NoError(t, p.MarkComplete())
	tor.piece(0).UpdateCompletion()
	require.True(t, tor.pieceComplete(0))

	require.NoError(t, tor.Files()[0].DiscardStorage())

	_, err = os.Stat(path)
	assert.True(t, errors.Is(err, os.ErrNotExist), "expected discarded file to be removed, got %v", err)
	assert.False(t, tor.pieceComplete(0))
}

func TestFileDiscardStorageClearsDirtyChunks(t *testing.T) {
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
	defer cl.Close()
	tor, new := cl.AddTorrentOpt(AddTorrentOpts{
		InfoHash:                 testingTorrentInfoHash,
		DisableInitialPieceCheck: true,
	})
	require.True(t, new)
	tor.setChunkSize(2)
	require.NoError(t, tor.setInfoUnlocked(&metainfo.Info{
		Name:        "payload",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, metainfo.HashSize),
	}))
	p := tor.piece(0).Storage()
	_, err = p.WriteAt([]byte("da"), 0)
	require.NoError(t, err)
	tor.cl.lock()
	tor.dirtyChunks.Add(tor.pieceRequestIndexBegin(0))
	require.True(t, tor.piece(0).hasDirtyChunks())
	tor.cl.unlock()

	require.NoError(t, tor.Files()[0].DiscardStorage())

	tor.cl.lock()
	assert.False(t, tor.piece(0).hasDirtyChunks())
	tor.cl.unlock()
}

type testFileBytesLeft struct {
	usualPieceSize  int64
	firstPieceIndex int
	endPieceIndex   int
	fileOffset      int64
	fileLength      int64
	completedPieces roaring.Bitmap
	expected        int64
	name            string
}

func (me testFileBytesLeft) Run(t *testing.T) {
	t.Run(me.name, func(t *testing.T) {
		assert.EqualValues(t, me.expected, fileBytesLeft(me.usualPieceSize, me.firstPieceIndex, me.endPieceIndex, me.fileOffset, me.fileLength, &me.completedPieces, func(pieceIndex int) int64 {
			return 0
		}))
	})
}

func TestFileBytesLeft(t *testing.T) {
	testFileBytesLeft{
		usualPieceSize:  3,
		firstPieceIndex: 1,
		endPieceIndex:   1,
		fileOffset:      1,
		fileLength:      0,
		expected:        0,
		name:            "ZeroLengthFile",
	}.Run(t)

	testFileBytesLeft{
		usualPieceSize:  2,
		firstPieceIndex: 1,
		endPieceIndex:   2,
		fileOffset:      1,
		fileLength:      1,
		expected:        1,
		name:            "EndOfSecondPiece",
	}.Run(t)

	testFileBytesLeft{
		usualPieceSize:  3,
		firstPieceIndex: 0,
		endPieceIndex:   1,
		fileOffset:      1,
		fileLength:      1,
		expected:        1,
		name:            "FileInFirstPiece",
	}.Run(t)

	testFileBytesLeft{
		usualPieceSize:  3,
		firstPieceIndex: 0,
		endPieceIndex:   1,
		fileOffset:      1,
		fileLength:      1,
		expected:        1,
		name:            "LandLocked",
	}.Run(t)

	testFileBytesLeft{
		usualPieceSize:  3,
		firstPieceIndex: 1,
		endPieceIndex:   3,
		fileOffset:      4,
		fileLength:      4,
		expected:        4,
		name:            "TwoPieces",
	}.Run(t)

	testFileBytesLeft{
		usualPieceSize:  3,
		firstPieceIndex: 1,
		endPieceIndex:   4,
		fileOffset:      5,
		fileLength:      7,
		expected:        7,
		name:            "ThreePieces",
	}.Run(t)

	testFileBytesLeft{
		usualPieceSize:  3,
		firstPieceIndex: 1,
		endPieceIndex:   4,
		fileOffset:      5,
		fileLength:      7,
		expected:        0,
		completedPieces: func() (ret roaring.Bitmap) {
			ret.AddRange(0, 5)
			return
		}(),
		name: "ThreePiecesCompletedAll",
	}.Run(t)
}
