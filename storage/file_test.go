package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/missinggo/v2"
	"github.com/go-quicktest/qt"

	"github.com/anacrolix/torrent/metainfo"
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
	qt.Assert(t, qt.IsNil(os.Remove(filepath.Join(td, "a"))))
	qt.Assert(t, qt.IsNil(ts.MarkFileReleased(0)))
	c := ts.Piece(p).Completion()
	qt.Check(t, qt.IsTrue(c.Ok))
	qt.Check(t, qt.IsTrue(c.Complete))
	var buf bytes.Buffer
	n, err := (Piece{ts.Piece(p), p}).WriteTo(&buf)
	qt.Check(t, qt.Equals(n, int64(4)))
	qt.Check(t, qt.IsNil(err))
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
	qt.Assert(t, qt.IsNil(os.Remove(filepath.Join(td, "payload", "a.bin"))))

	var buf bytes.Buffer
	n, err := (Piece{ts.Piece(p0), p0}).WriteTo(&buf)
	qt.Check(t, qt.Equals(n, int64(3)))
	qt.Check(t, qt.IsNil(err))
	qt.Check(t, qt.Equals(buf.String(), "aab"))
	_, err = os.Stat(filepath.Join(td, "payload", "a.bin.released.0"))
	qt.Check(t, qt.IsNil(err))
}
