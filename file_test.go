package torrent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	g "github.com/anacrolix/generics"
	qt "github.com/go-quicktest/qt"

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
		qt.Check(t, qt.Equals(begin, _case.begin))
		qt.Check(t, qt.Equals(end, _case.end))
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
	qt.Assert(t, qt.IsNil(err))
	defer cl.Close()
	tor, new := cl.AddTorrentOpt(AddTorrentOpts{
		InfoHash:                 testingTorrentInfoHash,
		DisableInitialPieceCheck: true,
	})
	qt.Assert(t, qt.IsTrue(new))
	qt.Assert(t, qt.IsNil(tor.setInfoUnlocked(&metainfo.Info{
		Name:        "payload",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, metainfo.HashSize),
	})))
	path := filepath.Join(dir, "payload")
	qt.Assert(t, qt.IsNil(os.WriteFile(path, []byte("data"), 0o666)))
	qt.Check(t, qt.IsFalse(tor.Files()[0].Released()))
	qt.Check(t, qt.IsFalse(tor.FileSnapshots()[0].Released))

	qt.Assert(t, qt.IsNil(tor.Files()[0].ReleaseStorage()))

	_, err = os.Stat(path)
	qt.Check(t, qt.IsTrue(errors.Is(err, os.ErrNotExist)), qt.Commentf("expected released file to be removed, got %v", err))
	qt.Check(t, qt.IsTrue(tor.Files()[0].Released()))
	qt.Check(t, qt.IsTrue(tor.FileSnapshots()[0].Released))
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
		qt.Assert(t, qt.IsNil(err))
		tor, new := cl.AddTorrentOpt(AddTorrentOpts{
			InfoHash:                 testingTorrentInfoHash,
			DisableInitialPieceCheck: true,
		})
		qt.Assert(t, qt.IsTrue(new))
		qt.Assert(t, qt.IsNil(tor.setInfoUnlocked(info)))
		return cl, tor
	}

	cl, tor := newTorrent(t)
	qt.Assert(t, qt.IsNil(os.WriteFile(filepath.Join(dir, "payload"), []byte("data"), 0o666)))
	qt.Assert(t, qt.IsNil(tor.Files()[0].ReleaseStorage()))
	qt.Check(t, qt.IsTrue(tor.FileSnapshots()[0].Released))
	qt.Assert(t, qt.HasLen(cl.Close(), 0))

	cl, tor = newTorrent(t)
	defer cl.Close()
	qt.Check(t, qt.IsTrue(tor.Files()[0].Released()))
	qt.Check(t, qt.IsTrue(tor.FileSnapshots()[0].Released))
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
	qt.Assert(t, qt.IsNil(err))
	defer cl.Close()
	tor, new := cl.AddTorrentOpt(AddTorrentOpts{
		InfoHash:                 testingTorrentInfoHash,
		DisableInitialPieceCheck: true,
	})
	qt.Assert(t, qt.IsTrue(new))
	qt.Assert(t, qt.IsNil(tor.setInfoUnlocked(&metainfo.Info{
		Name:        "payload",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, metainfo.HashSize),
	})))
	path := filepath.Join(dir, "payload")
	p := tor.piece(0).Storage()
	_, err = p.WriteAt([]byte("data"), 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsNil(p.MarkComplete()))
	tor.piece(0).UpdateCompletion()
	qt.Assert(t, qt.IsTrue(tor.pieceComplete(0)))

	qt.Assert(t, qt.IsNil(tor.Files()[0].DiscardStorage()))

	_, err = os.Stat(path)
	qt.Check(t, qt.IsTrue(errors.Is(err, os.ErrNotExist)), qt.Commentf("expected discarded file to be removed, got %v", err))
	qt.Check(t, qt.IsFalse(tor.pieceComplete(0)))
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
	qt.Assert(t, qt.IsNil(err))
	defer cl.Close()
	tor, new := cl.AddTorrentOpt(AddTorrentOpts{
		InfoHash:                 testingTorrentInfoHash,
		DisableInitialPieceCheck: true,
	})
	qt.Assert(t, qt.IsTrue(new))
	tor.setChunkSize(2)
	qt.Assert(t, qt.IsNil(tor.setInfoUnlocked(&metainfo.Info{
		Name:        "payload",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, metainfo.HashSize),
	})))
	p := tor.piece(0).Storage()
	_, err = p.WriteAt([]byte("da"), 0)
	qt.Assert(t, qt.IsNil(err))
	tor.cl.lock()
	tor.dirtyChunks.Add(tor.pieceRequestIndexBegin(0))
	qt.Assert(t, qt.IsTrue(tor.piece(0).hasDirtyChunks()))
	tor.cl.unlock()

	qt.Assert(t, qt.IsNil(tor.Files()[0].DiscardStorage()))

	tor.cl.lock()
	qt.Check(t, qt.IsFalse(tor.piece(0).hasDirtyChunks()))
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
		qt.Check(t, qt.Equals(fileBytesLeft(me.usualPieceSize, me.firstPieceIndex, me.endPieceIndex, me.fileOffset, me.fileLength, &me.completedPieces, func(pieceIndex int) int64 {
			return 0
		}), me.expected))
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
