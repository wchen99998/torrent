package stream

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

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
		active := 0
		for _, snapshot := range snapshots {
			switch snapshot.Index {
			case 0, 2:
				if snapshot.Priority != torrent.PiecePriorityNone {
					active++
				}
			default:
				assert.Equal(t, torrent.PiecePriorityNone, snapshot.Priority)
			}
		}
		assert.LessOrEqual(t, active, 1)
		_, err := io.Copy(io.Discard, lease.Reader)
		if err != nil {
			return err
		}
		return lease.Release(ctx)
	})
	require.NoError(t, err)
	assert.Equal(t, []int{0, 2}, got)
}

func TestFilesRequireExplicitReleaseKeepsActiveSlot(t *testing.T) {
	_, tor, _ := newCompletedTorrent(t)
	leases := make(chan *FileLease, 2)
	done := make(chan error, 1)
	go func() {
		done <- Files(context.Background(), tor, FilesOptions{
			FileIndexes:            []int{0, 1},
			MaxActive:              1,
			RequireExplicitRelease: true,
		}, func(ctx context.Context, lease *FileLease) error {
			leases <- lease
			return nil
		})
	}()

	first := receiveLease(t, leases, "first")
	select {
	case second := <-leases:
		t.Fatalf("second lease %d started before first was released", second.Index)
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, first.Release(context.Background()))

	second := receiveLease(t, leases, "second")
	require.NoError(t, second.Release(context.Background()))
	require.NoError(t, <-done)
}

func TestFilesHandlesCompletedFilesOutOfOrder(t *testing.T) {
	baseDir := t.TempDir()
	spec := testutil.Torrent{
		Name: "payload",
		Files: []testutil.File{
			{Name: "a.bin", Data: "aa"},
			{Name: "b.bin", Data: "bb"},
		},
	}
	mi, _ := spec.Generate(2)
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "payload"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "payload", "b.bin"), []byte("bb"), 0o666))
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
	require.EqualValues(t, 0, tor.Files()[0].Completed())
	require.EqualValues(t, 2, tor.Files()[1].Completed())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got []int
	err = Files(ctx, tor, FilesOptions{
		FileIndexes: []int{0, 1},
		MaxActive:   2,
	}, func(ctx context.Context, lease *FileLease) error {
		got = append(got, lease.Index)
		require.Equal(t, 1, lease.Index)
		require.NoError(t, lease.Release(context.Background()))
		cancel()
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, []int{1}, got)
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

type testReadCloser struct {
	closeErr error
}

func (r testReadCloser) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (r testReadCloser) Close() error {
	return r.closeErr
}

func TestFileLeaseReleaseRetryAfterCloseError(t *testing.T) {
	_, tor, _ := newCompletedTorrent(t)
	lease := newLease(context.Background(), tor.Files()[0], 0)
	closeErr := errors.New("close error")
	lease.Reader = testReadCloser{closeErr: closeErr}

	err := lease.Release(context.Background())
	require.ErrorIs(t, err, closeErr)
	assert.False(t, lease.wasFinalized())

	lease.Reader = testReadCloser{}
	require.NoError(t, lease.Release(context.Background()))
	assert.True(t, lease.wasFinalized())
}

func TestFileLeaseReleaseRetryAfterCanceledContext(t *testing.T) {
	_, tor, _ := newCompletedTorrent(t)
	lease := newLease(context.Background(), tor.Files()[0], 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := lease.Release(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, lease.wasFinalized())

	require.NoError(t, lease.Release(context.Background()))
	assert.True(t, lease.wasFinalized())
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

func receiveLease(t *testing.T, leases <-chan *FileLease, label string) *FileLease {
	t.Helper()
	select {
	case lease := <-leases:
		return lease
	case <-time.After(3 * time.Second):
		t.Fatalf("%s lease did not start", label)
		return nil
	}
}
