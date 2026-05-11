package stream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/wchen99998/torrent"
)

// FileLease is a completed torrent file handed to a consumer.
type FileLease struct {
	File        *torrent.File
	Index       int
	Path        string
	DisplayPath string
	Length      int64
	Reader      io.ReadCloser

	mu        sync.Mutex
	finalized bool
}

// Release finalizes a completed file that has been handed off to the caller.
func (l *FileLease) Release(ctx context.Context) error {
	return l.finalize(ctx, false)
}

// Discard finalizes a completed file without preserving completion.
func (l *FileLease) Discard(ctx context.Context) error {
	return l.finalize(ctx, true)
}

func (l *FileLease) wasFinalized() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.finalized
}

func (l *FileLease) finalize(ctx context.Context, discard bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	if l.finalized {
		l.mu.Unlock()
		return nil
	}
	l.finalized = true
	l.mu.Unlock()

	var closeErr error
	if l.Reader != nil {
		closeErr = l.Reader.Close()
	}
	var storageErr error
	if discard {
		storageErr = l.File.DiscardStorage()
	} else {
		storageErr = l.File.ReleaseStorage()
	}
	return errors.Join(ctx.Err(), closeErr, storageErr)
}

type FilesOptions struct {
	// FileIndexes selects zero-based torrent file indexes. Nil means all
	// files; an empty non-nil slice means none.
	FileIndexes []int
	// MaxActive limits how many selected files are requested at once. Values
	// <=0 request all selected files at once.
	MaxActive int
	// Readahead overrides the Reader default when non-zero.
	Readahead int64
}

// Files downloads selected files, waits for each to complete, hands it to h,
// and releases storage if h does not explicitly Release or Discard the lease.
func Files(
	ctx context.Context,
	t *torrent.Torrent,
	opts FilesOptions,
	h func(context.Context, *FileLease) error,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if h == nil {
		return errors.New("nil file handler")
	}
	if _, err := t.WaitInfo(ctx); err != nil {
		return err
	}
	files, err := selectFiles(t.Files(), opts.FileIndexes)
	if err != nil || len(files) == 0 {
		return err
	}
	maxActive := opts.MaxActive
	if maxActive <= 0 || maxActive > len(files) {
		maxActive = len(files)
	}
	for begin := 0; begin < len(files); begin += maxActive {
		end := begin + maxActive
		if end > len(files) {
			end = len(files)
		}
		batch := files[begin:end]
		for _, f := range batch {
			f.Download()
		}
		for _, f := range batch {
			if err := f.WaitComplete(ctx); err != nil {
				cancelFiles(batch)
				return err
			}
			lease := newLease(ctx, f, opts.Readahead)
			handlerErr := h(ctx, lease)
			var finalizeErr error
			if !lease.wasFinalized() {
				finalizeErr = lease.Release(context.Background())
			}
			f.SetPriority(torrent.PiecePriorityNone)
			if err := errors.Join(handlerErr, finalizeErr); err != nil {
				cancelFiles(batch)
				return err
			}
		}
	}
	return nil
}

func newLease(ctx context.Context, f *torrent.File, readahead int64) *FileLease {
	r := f.NewReader()
	r.SetContext(ctx)
	if readahead != 0 {
		r.SetReadahead(readahead)
	}
	return &FileLease{
		File:        f,
		Index:       f.Index(),
		Path:        f.Path(),
		DisplayPath: f.DisplayPath(),
		Length:      f.Length(),
		Reader:      r,
	}
}

func selectFiles(files []*torrent.File, indexes []int) ([]*torrent.File, error) {
	if indexes == nil {
		return append([]*torrent.File(nil), files...), nil
	}
	if len(indexes) == 0 {
		return nil, nil
	}
	want := make(map[int]struct{}, len(indexes))
	for _, index := range indexes {
		want[index] = struct{}{}
	}
	ret := make([]*torrent.File, 0, len(want))
	for _, f := range files {
		if _, ok := want[f.Index()]; ok {
			ret = append(ret, f)
			delete(want, f.Index())
		}
	}
	if len(want) != 0 {
		for index := range want {
			return nil, fmt.Errorf("file index %d not found", index)
		}
	}
	return ret, nil
}

func cancelFiles(files []*torrent.File) {
	for _, f := range files {
		f.SetPriority(torrent.PiecePriorityNone)
	}
}
