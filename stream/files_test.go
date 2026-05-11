package stream

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	g "github.com/anacrolix/generics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wchen99998/torrent"
	"github.com/wchen99998/torrent/internal/testutil"
	"github.com/wchen99998/torrent/storage"
)

func newCompletedTorrent(t *testing.T) (*torrent.Client, *torrent.Torrent, string) {
	baseDir := t.TempDir()
	spec := testutil.Torrent{
		Name: "payload",
		Files: []testutil.File{
			{Name: "a.bin", Data: "aa"},
			{Name: "b.bin", Data: "bb"},
			{Name: "c.bin", Data: "cc"},
		},
	}
	mi, _ := spec.Generate(2)
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "payload"), 0o755))
	for _, file := range spec.Files {
		require.NoError(t, os.WriteFile(filepath.Join(baseDir, "payload", file.Name), []byte(file.Data), 0o666))
	}
	cfg := torrent.TestingConfig(t)
	cfg.DataDir = baseDir
	cfg.DefaultStorage = storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir:      baseDir,
		UsePartFiles:       g.Some(false),
		ForceClassicFileIO: true,
	})
	cl, err := torrent.NewClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	tor, err := cl.AddTorrent(&mi)
	require.NoError(t, err)
	require.NoError(t, tor.VerifyData())
	return cl, tor, baseDir
}

func TestFilesSelectedIndexesAndMaxActive(t *testing.T) {
	_, tor, _ := newCompletedTorrent(t)
	var got []int
	err := Files(context.Background(), tor, FilesOptions{
		FileIndexes: []int{0, 2},
		MaxActive:   1,
	}, func(ctx context.Context, lease *FileLease) error {
		got = append(got, lease.Index)
		snapshots := tor.FileSnapshots()
		for _, snapshot := range snapshots {
			if snapshot.Index == lease.Index {
				assert.NotEqual(t, torrent.PiecePriorityNone, snapshot.Priority)
			} else {
				assert.Equal(t, torrent.PiecePriorityNone, snapshot.Priority)
			}
		}
		_, err := io.Copy(io.Discard, lease.Reader)
		if err != nil {
			return err
		}
		return lease.Release(ctx)
	})
	require.NoError(t, err)
	assert.Equal(t, []int{0, 2}, got)
}

func TestFilesNoSelectedFiles(t *testing.T) {
	_, tor, _ := newCompletedTorrent(t)
	called := false
	err := Files(context.Background(), tor, FilesOptions{FileIndexes: []int{}}, func(context.Context, *FileLease) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.False(t, called)
}

func TestFilesAutoRelease(t *testing.T) {
	_, tor, baseDir := newCompletedTorrent(t)
	err := Files(context.Background(), tor, FilesOptions{FileIndexes: []int{0}}, func(ctx context.Context, lease *FileLease) error {
		_, err := io.Copy(io.Discard, lease.Reader)
		return err
	})
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(baseDir, "payload", "a.bin"))
	assert.True(t, errors.Is(err, os.ErrNotExist), "expected auto-released file to be removed")
}

func TestFilesExplicitDiscard(t *testing.T) {
	_, tor, baseDir := newCompletedTorrent(t)
	err := Files(context.Background(), tor, FilesOptions{FileIndexes: []int{1}}, func(ctx context.Context, lease *FileLease) error {
		return lease.Discard(ctx)
	})
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(baseDir, "payload", "b.bin"))
	assert.True(t, errors.Is(err, os.ErrNotExist), "expected discarded file to be removed")
	assert.EqualValues(t, 0, tor.Files()[1].Completed())
}

func TestFilesHandlerError(t *testing.T) {
	_, tor, _ := newCompletedTorrent(t)
	handlerErr := errors.New("handler error")
	err := Files(context.Background(), tor, FilesOptions{FileIndexes: []int{0}}, func(ctx context.Context, lease *FileLease) error {
		return handlerErr
	})
	assert.ErrorIs(t, err, handlerErr)
}

func TestFilesContextCancellation(t *testing.T) {
	baseDir := t.TempDir()
	spec := testutil.Torrent{
		Name:  "payload",
		Files: []testutil.File{{Name: "a.bin", Data: "aa"}},
	}
	mi, _ := spec.Generate(2)
	cfg := torrent.TestingConfig(t)
	cfg.DataDir = baseDir
	cfg.DefaultStorage = storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir:      baseDir,
		UsePartFiles:       g.Some(false),
		ForceClassicFileIO: true,
	})
	cl, err := torrent.NewClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	tor, err := cl.AddTorrent(&mi)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = Files(ctx, tor, FilesOptions{FileIndexes: []int{0}}, func(context.Context, *FileLease) error {
		t.Fatal("handler should not be called")
		return nil
	})
	assert.ErrorIs(t, err, context.Canceled)
}
