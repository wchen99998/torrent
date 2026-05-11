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
	defer l.mu.Unlock()
	if l.finalized {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if l.Reader != nil {
		if err := l.Reader.Close(); err != nil {
			return err
		}
	}
	var err error
	if discard {
		err = l.File.DiscardStorage()
	} else {
		err = l.File.ReleaseStorage()
	}
	if err != nil {
		return err
	}
	l.finalized = true
	return nil
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
	waitCtx, cancelWaits := context.WithCancel(ctx)
	defer cancelWaits()
	results := make(chan fileResult, maxActive)
	var wg sync.WaitGroup
	active := make(map[*torrent.File]struct{}, maxActive)
	next := 0
	startNext := func() {
		f := files[next]
		next++
		active[f] = struct{}{}
		f.Download()
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- fileResult{
				file: f,
				err:  f.WaitComplete(waitCtx),
			}
		}()
	}
	startUntilFull := func() {
		for len(active) < maxActive && next < len(files) {
			startNext()
		}
	}
	cancelActive := func() {
		cancelWaits()
		for f := range active {
			f.SetPriority(torrent.PiecePriorityNone)
		}
		wg.Wait()
	}
	startUntilFull()
	for len(active) != 0 {
		result := <-results
		delete(active, result.file)
		result.file.SetPriority(torrent.PiecePriorityNone)
		if result.err != nil {
			cancelActive()
			return result.err
		}
		startUntilFull()
		lease := newLease(ctx, result.file, opts.Readahead)
		handlerErr := h(ctx, lease)
		var finalizeErr error
		if !lease.wasFinalized() {
			finalizeErr = lease.Release(context.Background())
		}
		if err := errors.Join(handlerErr, finalizeErr); err != nil {
			cancelActive()
			return err
		}
	}
	wg.Wait()
	return nil
}

type fileResult struct {
	file *torrent.File
	err  error
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
