package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/missinggo/v2"
	"github.com/go-quicktest/qt"

	"github.com/wchen99998/torrent/metainfo"
)

func TestShortFile(t *testing.T) {
	td := t.TempDir()
	s := NewFile(td)
	defer s.Close()
	info := &metainfo.Info{
		Name:        "a",
		Length:      2,
		PieceLength: missinggo.MiB,
		Pieces:      make([]byte, 20),
	}
	ts, err := s.OpenTorrent(context.Background(), info, metainfo.Hash{})
	qt.Assert(t, qt.IsNil(err))
	defer ts.Close()
	f, err := os.Create(filepath.Join(td, "a"))
	qt.Assert(t, qt.IsNil(err))
	err = f.Truncate(1)
	qt.Assert(t, qt.IsNil(err))
	f.Close()
	var buf bytes.Buffer
	p := info.Piece(0)
	n, err := io.Copy(&buf, io.NewSectionReader(ts.Piece(p), 0, p.Length()))
	qt.Check(t, qt.Equals(n, int64(1)))
	switch err {
	case nil, io.EOF:
	default:
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClassicFileReaderStopsAtExtentWithoutShortWrite(t *testing.T) {
	td := t.TempDir()
	s := NewFileOpts(NewFileClientOpts{
		ClientBaseDir:      td,
		UsePartFiles:       g.Some(false),
		ForceClassicFileIO: true,
	})
	defer s.Close()
	info := &metainfo.Info{
		Name:        "a",
		Length:      8,
		PieceLength: 4,
		Pieces:      make([]byte, 40),
	}
	ts, err := s.OpenTorrent(context.Background(), info, metainfo.Hash{})
	qt.Assert(t, qt.IsNil(err))
	defer ts.Close()
	qt.Assert(t, qt.IsNil(os.WriteFile(filepath.Join(td, "a"), []byte("abcdefgh"), 0o666)))

	var buf bytes.Buffer
	p := info.Piece(0)
	n, err := (Piece{ts.Piece(p), p}).WriteTo(&buf)
	qt.Check(t, qt.Equals(n, int64(4)))
	qt.Check(t, qt.IsNil(err))
	qt.Check(t, qt.DeepEquals(buf.Bytes(), []byte("abcd")))
}

func TestFilePathMakerOptsAndErrors(t *testing.T) {
	td := t.TempDir()
	infoHash := metainfo.Hash{1, 2, 3}
	info := &metainfo.Info{
		Name:        "payload",
		PieceLength: 4,
		Pieces:      make([]byte, 20),
		Files: []metainfo.FileInfo{
			{Path: []string{"a.bin"}, Length: 2},
			{Path: []string{"dir", "b.bin"}, Length: 2},
		},
	}
	var seen []FilePathMakerOpts
	s := NewFileOpts(NewFileClientOpts{
		ClientBaseDir: td,
		FilePathMaker: func(opts FilePathMakerOpts) (string, error) {
			seen = append(seen, opts)
			if opts.FileIndex == 1 {
				return "", errors.New("custom path error")
			}
			return filepath.Join("custom", opts.File.BestPath()[0]), nil
		},
	})
	defer s.Close()
	_, err := s.OpenTorrent(context.Background(), info, infoHash)
	qt.Check(t, qt.ErrorMatches(err, `file 1: making path: custom path error`))
	qt.Assert(t, qt.HasLen(seen, 2))
	qt.Check(t, qt.Equals(seen[0].Info, info))
	qt.Check(t, qt.Equals(seen[0].InfoHash, infoHash))
	qt.Check(t, qt.Equals(seen[0].FileIndex, 0))
	qt.Check(t, qt.Equals(seen[0].DefaultPath, filepath.Join("payload", "a.bin")))
	qt.Check(t, qt.DeepEquals(seen[0].File.BestPath(), []string{"a.bin"}))
	qt.Check(t, qt.Equals(seen[1].FileIndex, 1))
	qt.Check(t, qt.Equals(seen[1].DefaultPath, filepath.Join("payload", "dir", "b.bin")))
}

func TestPlanFilesUsesStoragePathOptions(t *testing.T) {
	td := t.TempDir()
	infoHash := metainfo.Hash{1, 2, 3}
	info := &metainfo.Info{
		Name:        "payload",
		PieceLength: 4,
		Pieces:      make([]byte, 40),
		Files: []metainfo.FileInfo{
			{Path: []string{"a.bin"}, Length: 2},
			{Path: []string{"dir", "b.bin"}, Length: 6},
		},
	}
	plan, err := PlanFiles(NewFileClientOpts{
		ClientBaseDir:   td,
		TorrentDirMaker: InfoHashPathMaker,
		FilePathMaker: func(opts FilePathMakerOpts) (string, error) {
			return filepath.Join("custom", opts.File.BestPath()[0]), nil
		},
	}, info, infoHash)
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.Equals(plan.TorrentDir, filepath.Join(td, infoHash.HexString())))
	qt.Assert(t, qt.HasLen(plan.Files, 2))
	qt.Check(t, qt.Equals(plan.Files[0].Index, 0))
	qt.Check(t, qt.Equals(plan.Files[0].Path, "payload/a.bin"))
	qt.Check(t, qt.Equals(plan.Files[0].DisplayPath, "a.bin"))
	qt.Check(t, qt.Equals(plan.Files[0].DefaultPath, filepath.Join("payload", "a.bin")))
	qt.Check(t, qt.Equals(plan.Files[0].StorageRelativePath, filepath.Join("custom", "a.bin")))
	qt.Check(t, qt.Equals(plan.Files[0].StoragePath, filepath.Join(td, infoHash.HexString(), "custom", "a.bin")))
	qt.Check(t, qt.Equals(plan.Files[0].Length, int64(2)))
	qt.Check(t, qt.Equals(plan.Files[0].TorrentOffset, int64(0)))
	qt.Check(t, qt.Equals(plan.Files[0].BeginPieceIndex, 0))
	qt.Check(t, qt.Equals(plan.Files[0].EndPieceIndex, 1))
	qt.Check(t, qt.DeepEquals(plan.Files[0].FileInfo.BestPath(), []string{"a.bin"}))
	qt.Check(t, qt.Equals(plan.Files[1].Index, 1))
	qt.Check(t, qt.Equals(plan.Files[1].TorrentOffset, int64(2)))
	qt.Check(t, qt.Equals(plan.Files[1].EndPieceIndex, 2))
	plan.Files[0].FileInfo.Path[0] = "mutated"
	qt.Check(t, qt.DeepEquals(info.Files[0].Path, []string{"a.bin"}))
}

func TestPlanFilesRejectsEscapesAfterCustomMaker(t *testing.T) {
	td := t.TempDir()
	info := &metainfo.Info{
		Name:        "payload",
		Length:      1,
		PieceLength: 1,
		Pieces:      make([]byte, 20),
	}
	_, err := PlanFiles(NewFileClientOpts{
		ClientBaseDir: td,
		FilePathMaker: func(opts FilePathMakerOpts) (string, error) {
			return filepath.Join("..", "escape"), nil
		},
	}, info, metainfo.Hash{})
	qt.Check(t, qt.ErrorMatches(err, `file 0: path ".*escape" is not sub path of ".*"`))
}

func TestPlanFilesDoesNotOpenStorage(t *testing.T) {
	td := t.TempDir()
	info := &metainfo.Info{
		Name:        "empty",
		Length:      0,
		PieceLength: 1,
	}
	plan, err := PlanFiles(NewFileClientOpts{ClientBaseDir: td}, info, metainfo.Hash{})
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.HasLen(plan.Files, 1))
	_, err = os.Stat(plan.Files[0].StoragePath)
	qt.Check(t, qt.IsTrue(errors.Is(err, os.ErrNotExist)))

	s := NewFileOpts(NewFileClientOpts{ClientBaseDir: td})
	defer s.Close()
	ts, err := s.OpenTorrent(context.Background(), info, metainfo.Hash{})
	qt.Assert(t, qt.IsNil(err))
	defer ts.Close()
	_, err = os.Stat(plan.Files[0].StoragePath)
	qt.Check(t, qt.IsNil(err))
}

func TestFilePathMakerRejectsEscapesAfterCustomMaker(t *testing.T) {
	td := t.TempDir()
	info := &metainfo.Info{
		Name:        "payload",
		Length:      1,
		PieceLength: 1,
		Pieces:      make([]byte, 20),
	}
	s := NewFileOpts(NewFileClientOpts{
		ClientBaseDir: td,
		FilePathMaker: func(opts FilePathMakerOpts) (string, error) {
			return filepath.Join("..", "escape"), nil
		},
	})
	defer s.Close()
	_, err := s.OpenTorrent(context.Background(), info, metainfo.Hash{})
	qt.Check(t, qt.ErrorMatches(err, `file 0: path ".*escape" is not sub path of ".*"`))
}

func TestFileStateTracksReleaseAndDiscard(t *testing.T) {
	td := t.TempDir()
	info := &metainfo.Info{
		Name:        "payload",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, 20),
	}
	s := NewFileOpts(NewFileClientOpts{
		ClientBaseDir: td,
		UsePartFiles:  g.Some(false),
	})
	defer s.Close()
	ts, err := s.OpenTorrent(context.Background(), info, metainfo.Hash{})
	qt.Assert(t, qt.IsNil(err))
	defer ts.Close()

	state, err := ts.FileState(0)
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.IsFalse(state.Released))
	qt.Assert(t, qt.IsNil(os.WriteFile(filepath.Join(td, "payload"), []byte("data"), 0o666)))
	qt.Assert(t, qt.IsNil(ts.MarkFileReleased(0)))
	state, err = ts.FileState(0)
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.IsTrue(state.Released))
	qt.Assert(t, qt.IsNil(ts.MarkFileDiscarded(0)))
	state, err = ts.FileState(0)
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.IsFalse(state.Released))
}

func TestReleasedFileRemainsCompleteAfterRemoval(t *testing.T) {
	td := t.TempDir()
	info := &metainfo.Info{
		Name:        "a",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, 20),
	}
	newClient := func() ClientImplCloser {
		return NewFileOpts(NewFileClientOpts{
			ClientBaseDir: td,
			UsePartFiles:  g.Some(false),
		})
	}
	s := newClient()
	defer s.Close()
	ts, err := s.OpenTorrent(context.Background(), info, metainfo.Hash{})
	qt.Assert(t, qt.IsNil(err))
	p := info.Piece(0)
	_, err = ts.Piece(p).WriteAt([]byte("data"), 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsNil(ts.Piece(p).MarkComplete()))
	qt.Assert(t, qt.IsNil(ts.MarkFileReleased(0)))
	_, err = os.Stat(filepath.Join(td, "a"))
	qt.Check(t, qt.IsTrue(errors.Is(err, os.ErrNotExist)))
	c := ts.Piece(p).Completion()
	qt.Check(t, qt.IsTrue(c.Ok))
	qt.Check(t, qt.IsTrue(c.Complete))
	var buf bytes.Buffer
	n, err := (Piece{ts.Piece(p), p}).WriteTo(&buf)
	qt.Check(t, qt.Equals(n, int64(0)))
	qt.Check(t, qt.IsTrue(errors.Is(err, ErrFileReleased)))
	qt.Assert(t, qt.IsNil(ts.Close()))
	qt.Assert(t, qt.IsNil(s.Close()))

	s = newClient()
	defer s.Close()
	ts, err = s.OpenTorrent(context.Background(), info, metainfo.Hash{})
	qt.Assert(t, qt.IsNil(err))
	defer ts.Close()
	c = ts.Piece(p).Completion()
	qt.Check(t, qt.IsTrue(c.Ok))
	qt.Check(t, qt.IsTrue(c.Complete))
	_, err = os.Stat(filepath.Join(td, "a.released"))
	qt.Check(t, qt.IsNil(err))
	_, err = os.Stat(filepath.Join(td, "a"))
	qt.Check(t, qt.IsTrue(errors.Is(err, os.ErrNotExist)))
}

func TestReleasedFileKeepsBoundaryPieceData(t *testing.T) {
	td := t.TempDir()
	info := &metainfo.Info{
		Name:        "payload",
		PieceLength: 3,
		Pieces:      make([]byte, 40),
		Files: []metainfo.FileInfo{
			{Path: []string{"a.bin"}, Length: 2},
			{Path: []string{"b.bin"}, Length: 2},
		},
	}
	s := NewFileOpts(NewFileClientOpts{
		ClientBaseDir: td,
		UsePartFiles:  g.Some(false),
	})
	defer s.Close()
	ts, err := s.OpenTorrent(context.Background(), info, metainfo.Hash{})
	qt.Assert(t, qt.IsNil(err))
	defer ts.Close()

	p0 := info.Piece(0)
	_, err = ts.Piece(p0).WriteAt([]byte("aab"), 0)
	qt.Assert(t, qt.IsNil(err))
	p1 := info.Piece(1)
	_, err = ts.Piece(p1).WriteAt([]byte("b"), 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsNil(ts.Piece(p0).MarkComplete()))
	qt.Assert(t, qt.IsNil(ts.Piece(p1).MarkComplete()))
	qt.Assert(t, qt.IsNil(ts.MarkFileReleased(0)))
	_, err = os.Stat(filepath.Join(td, "payload", "a.bin"))
	qt.Check(t, qt.IsTrue(errors.Is(err, os.ErrNotExist)))

	var buf bytes.Buffer
	n, err := (Piece{ts.Piece(p0), p0}).WriteTo(&buf)
	qt.Check(t, qt.Equals(n, int64(3)))
	qt.Check(t, qt.IsNil(err))
	qt.Check(t, qt.Equals(buf.String(), "aab"))
	readAtBuf := make([]byte, 3)
	nRead, err := ts.Piece(p0).ReadAt(readAtBuf, 0)
	qt.Check(t, qt.Equals(nRead, 3))
	qt.Check(t, qt.IsNil(err))
	qt.Check(t, qt.Equals(string(readAtBuf), "aab"))
	_, err = os.Stat(filepath.Join(td, "payload", "a.bin.released.0"))
	qt.Check(t, qt.IsNil(err))
}

func TestReleaseStorageFailsWhenBoundarySourceMissing(t *testing.T) {
	td := t.TempDir()
	info := &metainfo.Info{
		Name:        "payload",
		PieceLength: 3,
		Pieces:      make([]byte, 40),
		Files: []metainfo.FileInfo{
			{Path: []string{"a.bin"}, Length: 2},
			{Path: []string{"b.bin"}, Length: 2},
		},
	}
	s := NewFileOpts(NewFileClientOpts{
		ClientBaseDir: td,
		UsePartFiles:  g.Some(false),
	})
	defer s.Close()
	ts, err := s.OpenTorrent(context.Background(), info, metainfo.Hash{})
	qt.Assert(t, qt.IsNil(err))
	defer ts.Close()

	p0 := info.Piece(0)
	_, err = ts.Piece(p0).WriteAt([]byte("aab"), 0)
	qt.Assert(t, qt.IsNil(err))
	p1 := info.Piece(1)
	_, err = ts.Piece(p1).WriteAt([]byte("b"), 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsNil(ts.Piece(p0).MarkComplete()))
	qt.Assert(t, qt.IsNil(ts.Piece(p1).MarkComplete()))
	aPath := filepath.Join(td, "payload", "a.bin")
	qt.Assert(t, qt.IsNil(os.Remove(aPath)))

	err = ts.MarkFileReleased(0)
	qt.Check(t, qt.ErrorMatches(err, `released file boundary source .* is missing: .*`))
	_, err = os.Stat(aPath + ".released")
	qt.Check(t, qt.IsTrue(errors.Is(err, os.ErrNotExist)))
	_, err = os.Stat(aPath + ".released.0")
	qt.Check(t, qt.IsTrue(errors.Is(err, os.ErrNotExist)))
	_, err = ts.Piece(p0).WriteAt([]byte("aab"), 0)
	qt.Check(t, qt.IsNil(err))
}

func TestInvalidReleasedMarkerIgnoredWhenBoundaryMissing(t *testing.T) {
	td := t.TempDir()
	info := &metainfo.Info{
		Name:        "payload",
		PieceLength: 3,
		Pieces:      make([]byte, 40),
		Files: []metainfo.FileInfo{
			{Path: []string{"a.bin"}, Length: 2},
			{Path: []string{"b.bin"}, Length: 2},
		},
	}
	aPath := filepath.Join(td, "payload", "a.bin")
	qt.Assert(t, qt.IsNil(os.MkdirAll(filepath.Dir(aPath), 0o755)))
	qt.Assert(t, qt.IsNil(os.WriteFile(aPath+".released", nil, 0o666)))

	s := NewFileOpts(NewFileClientOpts{
		ClientBaseDir: td,
		UsePartFiles:  g.Some(false),
	})
	defer s.Close()
	ts, err := s.OpenTorrent(context.Background(), info, metainfo.Hash{})
	qt.Assert(t, qt.IsNil(err))
	defer ts.Close()

	p0 := info.Piece(0)
	_, err = ts.Piece(p0).WriteAt([]byte("aab"), 0)
	qt.Assert(t, qt.IsNil(err))
	_, err = os.Stat(aPath)
	qt.Check(t, qt.IsNil(err))
	_, err = os.Stat(aPath + ".released")
	qt.Check(t, qt.IsTrue(errors.Is(err, os.ErrNotExist)))
}

func TestReleaseStorageRollbackWhenRemovalFails(t *testing.T) {
	td := t.TempDir()
	info := &metainfo.Info{
		Name:        "a",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, 20),
	}
	s := NewFileOpts(NewFileClientOpts{
		ClientBaseDir: td,
		UsePartFiles:  g.Some(false),
	})
	defer s.Close()
	ts, err := s.OpenTorrent(context.Background(), info, metainfo.Hash{})
	qt.Assert(t, qt.IsNil(err))
	defer ts.Close()
	p := info.Piece(0)
	_, err = ts.Piece(p).WriteAt([]byte("data"), 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsNil(ts.Piece(p).MarkComplete()))
	path := filepath.Join(td, "a")
	qt.Assert(t, qt.IsNil(os.Remove(path)))
	qt.Assert(t, qt.IsNil(os.Mkdir(path, 0o755)))
	qt.Assert(t, qt.IsNil(os.WriteFile(filepath.Join(path, "child"), []byte("still here"), 0o666)))

	err = ts.MarkFileReleased(0)
	qt.Check(t, qt.IsNotNil(err))
	_, err = os.Stat(filepath.Join(td, "a.released"))
	qt.Check(t, qt.IsTrue(errors.Is(err, os.ErrNotExist)))
	qt.Assert(t, qt.IsNil(os.Remove(filepath.Join(path, "child"))))
	qt.Assert(t, qt.IsNil(os.Remove(path)))
	_, err = ts.Piece(p).WriteAt([]byte("data"), 0)
	qt.Check(t, qt.IsNil(err))
}

func TestDiscardedFileRemovesStorageAndMarksIncomplete(t *testing.T) {
	td := t.TempDir()
	info := &metainfo.Info{
		Name:        "a",
		Length:      4,
		PieceLength: 4,
		Pieces:      make([]byte, 20),
	}
	s := NewFileOpts(NewFileClientOpts{
		ClientBaseDir: td,
		UsePartFiles:  g.Some(false),
	})
	defer s.Close()
	ts, err := s.OpenTorrent(context.Background(), info, metainfo.Hash{})
	qt.Assert(t, qt.IsNil(err))
	defer ts.Close()
	p := info.Piece(0)
	_, err = ts.Piece(p).WriteAt([]byte("data"), 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsNil(ts.Piece(p).MarkComplete()))

	qt.Assert(t, qt.IsNil(ts.MarkFileDiscarded(0)))

	_, err = os.Stat(filepath.Join(td, "a"))
	qt.Check(t, qt.IsTrue(errors.Is(err, os.ErrNotExist)))
	c := ts.Piece(p).Completion()
	qt.Check(t, qt.IsTrue(c.Ok))
	qt.Check(t, qt.IsFalse(c.Complete))
}

func TestDiscardedFileCanRedownloadBoundaryPieceWithReleasedNeighbor(t *testing.T) {
	td := t.TempDir()
	info := &metainfo.Info{
		Name:        "payload",
		PieceLength: 3,
		Pieces:      make([]byte, 40),
		Files: []metainfo.FileInfo{
			{Path: []string{"a.bin"}, Length: 2},
			{Path: []string{"b.bin"}, Length: 2},
		},
	}
	s := NewFileOpts(NewFileClientOpts{
		ClientBaseDir: td,
		UsePartFiles:  g.Some(false),
	})
	defer s.Close()
	ts, err := s.OpenTorrent(context.Background(), info, metainfo.Hash{})
	qt.Assert(t, qt.IsNil(err))
	defer ts.Close()

	p0 := info.Piece(0)
	_, err = ts.Piece(p0).WriteAt([]byte("aab"), 0)
	qt.Assert(t, qt.IsNil(err))
	p1 := info.Piece(1)
	_, err = ts.Piece(p1).WriteAt([]byte("b"), 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsNil(ts.Piece(p0).MarkComplete()))
	qt.Assert(t, qt.IsNil(ts.Piece(p1).MarkComplete()))
	qt.Assert(t, qt.IsNil(ts.MarkFileReleased(0)))
	qt.Assert(t, qt.IsNil(ts.MarkFileDiscarded(1)))
	c := ts.Piece(p0).Completion()
	qt.Check(t, qt.IsTrue(c.Ok))
	qt.Check(t, qt.IsFalse(c.Complete))

	_, err = ts.Piece(p0).WriteAt([]byte("aab"), 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsNil(ts.Piece(p0).MarkComplete()))
	var buf bytes.Buffer
	n, err := (Piece{ts.Piece(p0), p0}).WriteTo(&buf)
	qt.Check(t, qt.Equals(n, int64(3)))
	qt.Check(t, qt.IsNil(err))
	qt.Check(t, qt.Equals(buf.String(), "aab"))
}
