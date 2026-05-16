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
	released  bool
	discarded bool
	done      chan struct{}
}

type limitedReadCloser struct {
	reader io.Reader
	closer io.Closer
	once   sync.Once
	err    error
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

// Finalized reports whether Release or Discard has completed successfully.
func (l *FileLease) Finalized() bool {
	return l.wasFinalized()
}

// Released reports whether Release has completed successfully.
func (l *FileLease) Released() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.released
}

// Discarded reports whether Discard has completed successfully.
func (l *FileLease) Discarded() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.discarded
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
	l.released = !discard
	l.discarded = discard
	if l.done == nil {
		l.done = make(chan struct{})
	}
	close(l.done)
	return nil
}

func (l *FileLease) waitFinalized(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	if l.finalized {
		l.mu.Unlock()
		return nil
	}
	if l.done == nil {
		l.done = make(chan struct{})
	}
	done := l.done
	l.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// FileSelectionController lets callers change the file set used by Files while
// it is running. A nil selection means all files; an empty non-nil selection
// means no files.
type FileSelectionController struct {
	mu      sync.Mutex
	indexes []int
	changed chan struct{}
	closed  bool
}

func NewFileSelectionController(indexes []int) *FileSelectionController {
	return &FileSelectionController{
		indexes: cloneFileIndexes(indexes),
		changed: make(chan struct{}),
	}
}

func (c *FileSelectionController) SetSelectedFileIndexes(indexes []int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("file selection controller is closed")
	}
	c.indexes = cloneFileIndexes(indexes)
	c.notifyLocked()
	return nil
}

func (c *FileSelectionController) SelectedFileIndexes() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneFileIndexes(c.indexes)
}

func (c *FileSelectionController) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.notifyLocked()
}

func (c *FileSelectionController) snapshot() ([]int, bool, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.changed == nil {
		c.changed = make(chan struct{})
	}
	return cloneFileIndexes(c.indexes), c.closed, c.changed
}

func (c *FileSelectionController) notifyLocked() {
	if c.changed == nil {
		c.changed = make(chan struct{})
	}
	close(c.changed)
	if !c.closed {
		c.changed = make(chan struct{})
	}
}

func cloneFileIndexes(indexes []int) []int {
	if indexes == nil {
		return nil
	}
	return append([]int(nil), indexes...)
}

type FilesOptions struct {
	// FileIndexes selects zero-based torrent file indexes. Nil means all
	// files; an empty non-nil slice means none.
	FileIndexes []int
	// Selection allows FileIndexes to be changed while Files is running. When
	// set, FileIndexes is ignored and Files runs until the context is cancelled,
	// the controller is closed, or an error occurs.
	Selection *FileSelectionController
	// MaxActive limits how many selected files are requested at once. Values
	// <=0 request all selected files at once.
	MaxActive int
	// Readahead overrides the Reader default when non-zero.
	Readahead int64
	// RequireExplicitRelease makes Files wait for the handler to call Release
	// or Discard when the handler returns nil without finalizing the lease.
	// While waiting, the lease still counts against MaxActive.
	RequireExplicitRelease bool
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
	if opts.Selection != nil {
		return filesWithSelection(ctx, t, opts, h)
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
	slots := make(chan struct{}, maxActive)
	results := make(chan error, len(files))
	var wg sync.WaitGroup
	var resultErr error
queue:
	for _, f := range files {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			resultErr = errors.Join(resultErr, ctx.Err())
			break queue
		}
		f.Download()
		wg.Add(1)
		go func(f *torrent.File) {
			defer wg.Done()
			defer func() {
				f.SetPriority(torrent.PiecePriorityNone)
				<-slots
			}()
			if err := f.WaitVerifiedComplete(waitCtx); err != nil {
				results <- err
				return
			}
			lease := newLease(ctx, f, opts.Readahead)
			handlerErr := h(ctx, lease)
			var finalizeErr error
			if !lease.wasFinalized() {
				if handlerErr != nil || !opts.RequireExplicitRelease {
					finalizeErr = lease.Release(context.Background())
				} else {
					finalizeErr = lease.waitFinalized(ctx)
				}
			}
			results <- errors.Join(handlerErr, finalizeErr)
		}(f)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}
	return resultErr
}

type selectedFileRun struct {
	cancel context.CancelFunc
}

type selectedFileResult struct {
	index     int
	err       error
	completed bool
}

func filesWithSelection(
	ctx context.Context,
	t *torrent.Torrent,
	opts FilesOptions,
	h func(context.Context, *FileLease) error,
) error {
	if _, err := t.WaitInfo(ctx); err != nil {
		return err
	}
	files := t.Files()
	maxActive := opts.MaxActive
	if maxActive <= 0 || maxActive > len(files) {
		maxActive = len(files)
	}
	active := make(map[int]selectedFileRun)
	finished := make(map[int]struct{})
	results := make(chan selectedFileResult, len(files))
	var resultErr error
	var stopErr error
	for {
		indexes, closed, changed := opts.Selection.snapshot()
		selected, err := selectedFileSet(files, indexes)
		if err != nil && stopErr == nil {
			stopErr = err
		}
		if stopErr == nil {
			for index, run := range active {
				if _, ok := selected[index]; !ok {
					run.cancel()
				}
			}
			for _, f := range files {
				if len(active) >= maxActive {
					break
				}
				index := f.Index()
				if _, ok := selected[index]; !ok {
					continue
				}
				if _, ok := finished[index]; ok {
					continue
				}
				if _, ok := active[index]; ok {
					continue
				}
				waitCtx, cancel := context.WithCancel(ctx)
				active[index] = selectedFileRun{cancel: cancel}
				go runSelectedFile(waitCtx, ctx, f, opts, h, results)
			}
		} else {
			for _, run := range active {
				run.cancel()
			}
		}
		if stopErr != nil && len(active) == 0 {
			return errors.Join(resultErr, stopErr)
		}
		if closed && len(active) == 0 {
			return resultErr
		}
		if closed {
			changed = nil
		}
		select {
		case <-ctx.Done():
			if stopErr == nil {
				stopErr = ctx.Err()
			}
			for _, run := range active {
				run.cancel()
			}
		case <-changed:
		case result := <-results:
			delete(active, result.index)
			if result.completed {
				finished[result.index] = struct{}{}
			}
			if result.err != nil {
				resultErr = errors.Join(resultErr, result.err)
				if stopErr == nil {
					stopErr = result.err
				}
			}
		}
	}
}

func runSelectedFile(
	waitCtx context.Context,
	handlerCtx context.Context,
	f *torrent.File,
	opts FilesOptions,
	h func(context.Context, *FileLease) error,
	results chan<- selectedFileResult,
) {
	f.Download()
	defer f.SetPriority(torrent.PiecePriorityNone)
	if err := f.WaitVerifiedComplete(waitCtx); err != nil {
		if errors.Is(err, context.Canceled) && handlerCtx.Err() == nil {
			results <- selectedFileResult{index: f.Index()}
		} else {
			results <- selectedFileResult{index: f.Index(), err: err}
		}
		return
	}
	lease := newLease(handlerCtx, f, opts.Readahead)
	handlerErr := h(handlerCtx, lease)
	var finalizeErr error
	if !lease.wasFinalized() {
		if handlerErr != nil || !opts.RequireExplicitRelease {
			finalizeErr = lease.Release(context.Background())
		} else {
			finalizeErr = lease.waitFinalized(handlerCtx)
		}
	}
	err := errors.Join(handlerErr, finalizeErr)
	results <- selectedFileResult{
		index:     f.Index(),
		err:       err,
		completed: err == nil,
	}
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
		Reader:      newLimitedReadCloser(r, f.Length()),
		done:        make(chan struct{}),
	}
}

func newLimitedReadCloser(r io.ReadCloser, n int64) *limitedReadCloser {
	return &limitedReadCloser{
		reader: io.LimitReader(r, n),
		closer: r,
	}
}

func (r *limitedReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *limitedReadCloser) Close() error {
	r.once.Do(func() {
		r.err = r.closer.Close()
	})
	return r.err
}

func selectedFileSet(files []*torrent.File, indexes []int) (map[int]*torrent.File, error) {
	selectedFiles, err := selectFiles(files, indexes)
	if err != nil {
		return nil, err
	}
	ret := make(map[int]*torrent.File, len(selectedFiles))
	for _, f := range selectedFiles {
		ret[f.Index()] = f
	}
	return ret, nil
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
