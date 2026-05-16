package torrent

import (
	"context"
	"iter"

	"github.com/RoaringBitmap/roaring/v2"
	g "github.com/anacrolix/generics"

	"github.com/wchen99998/torrent/metainfo"
	"github.com/wchen99998/torrent/storage"
	infohash_v2 "github.com/wchen99998/torrent/types/infohash-v2"
)

// Provides access to regions of torrent data that correspond to its files.
type File struct {
	t           *Torrent
	index       int
	path        string
	offset      int64
	length      int64
	fi          metainfo.FileInfo
	displayPath string
	prio        PiecePriority
	piecesRoot  g.Option[infohash_v2.T]
}

func (f *File) String() string {
	return f.Path()
}

func (f *File) Torrent() *Torrent {
	return f.t
}

// Index returns the file's zero-based index in Torrent.Files.
func (f *File) Index() int {
	return f.index
}

// Completed returns the number of bytes completed for this file.
func (f *File) Completed() int64 {
	return f.BytesCompleted()
}

// WaitComplete waits until the file is complete, the context is cancelled, or
// the torrent is closed. It does not change file or piece priorities.
func (f *File) WaitComplete(ctx context.Context) error {
	return f.waitComplete(ctx, f.completeLocked)
}

// VerifiedComplete reports whether every piece containing data for the file is
// known complete. It is stricter than Completed/WaitComplete, which can include
// dirty chunks that have not been verified yet.
func (f *File) VerifiedComplete() bool {
	f.t.cl.rLock()
	defer f.t.cl.rUnlock()
	return f.verifiedCompleteLocked()
}

// WaitVerifiedComplete waits until every piece containing data for the file is
// known complete, the context is cancelled, or the torrent is closed. It does
// not change file or piece priorities.
func (f *File) WaitVerifiedComplete(ctx context.Context) error {
	return f.waitComplete(ctx, f.verifiedCompleteLocked)
}

func (f *File) waitComplete(ctx context.Context, complete func() bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			f.t.cl.lock()
			f.t.cl.event.Broadcast()
			f.t.cl.unlock()
		case <-done:
		}
	}()
	defer close(done)
	f.t.cl.lock()
	defer f.t.cl.unlock()
	for {
		if complete() {
			return nil
		}
		if f.t.closed.IsSet() {
			return errTorrentClosed
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		f.t.cl.event.Wait()
	}
}

func (f *File) completeLocked() bool {
	return f.bytesCompletedLocked() >= f.length
}

func (f *File) verifiedCompleteLocked() bool {
	if f.length == 0 {
		return true
	}
	for i := f.BeginPieceIndex(); i < f.EndPieceIndex(); i++ {
		state := f.t.pieceState(i)
		if !state.Ok || !state.Complete {
			return false
		}
	}
	return true
}

// ReleaseStorage releases storage for a file that has been completed and handed
// off to the caller.
func (f *File) ReleaseStorage() error {
	f.t.storageLock.RLock()
	storage := f.t.storage
	f.t.storageLock.RUnlock()
	if storage == nil {
		return errTorrentClosed
	}
	if err := storage.MarkFileReleased(f.index); err != nil {
		return err
	}
	for p := range f.Pieces() {
		p.UpdateCompletion()
	}
	return nil
}

// DiscardStorage releases storage for a file without preserving completion.
// Affected pieces are marked incomplete so future reads can request them again.
func (f *File) DiscardStorage() error {
	f.t.storageLock.RLock()
	storage := f.t.storage
	f.t.storageLock.RUnlock()
	if storage == nil {
		return errTorrentClosed
	}
	if err := storage.MarkFileDiscarded(f.index); err != nil {
		return err
	}
	f.t.cl.lock()
	for i := range f.PieceIndices() {
		f.t.pendAllChunkSpecs(i)
		f.t.updatePieceCompletion(i)
	}
	f.t.cl.unlock()
	return nil
}

// StorageState returns lifecycle state reported by the torrent storage backend.
func (f *File) StorageState() (storage.FileState, error) {
	f.t.storageLock.RLock()
	st := f.t.storage
	f.t.storageLock.RUnlock()
	if st == nil {
		return storage.FileState{}, errTorrentClosed
	}
	return st.FileState(f.index)
}

// Released reports whether storage for this file has been finalized and
// intentionally removed while preserving completion.
func (f *File) Released() bool {
	state, err := f.StorageState()
	return err == nil && state.Released
}

// Data for this file begins this many bytes into the Torrent.
func (f *File) Offset() int64 {
	return f.offset
}

// The FileInfo from the metainfo.Info to which this file corresponds.
func (f *File) FileInfo() metainfo.FileInfo {
	return f.fi
}

// The file's path components including the directory name joined by '/'.
func (f *File) Path() string {
	return f.path
}

// The file's length in bytes.
func (f *File) Length() int64 {
	return f.length
}

// Number of bytes of the entire file we have completed. This is the sum of
// completed pieces, and dirtied chunks of incomplete pieces.
func (f *File) BytesCompleted() (n int64) {
	f.t.cl.rLock()
	n = f.bytesCompletedLocked()
	f.t.cl.rUnlock()
	return
}

func (f *File) bytesCompletedLocked() int64 {
	return f.length - f.bytesLeft()
}

func fileBytesLeft(
	torrentUsualPieceSize int64,
	fileFirstPieceIndex int,
	fileEndPieceIndex int,
	fileTorrentOffset int64,
	fileLength int64,
	torrentCompletedPieces *roaring.Bitmap,
	pieceSizeCompletedFn func(pieceIndex int) int64,
) (left int64) {
	if fileLength == 0 {
		return
	}

	noCompletedMiddlePieces := roaring.New()
	noCompletedMiddlePieces.AddRange(uint64(fileFirstPieceIndex), uint64(fileEndPieceIndex))
	noCompletedMiddlePieces.AndNot(torrentCompletedPieces)
	noCompletedMiddlePieces.Iterate(func(pieceIndex uint32) bool {
		i := int(pieceIndex)
		pieceSizeCompleted := pieceSizeCompletedFn(i)
		switch i {
		case fileFirstPieceIndex:
			beginOffset := fileTorrentOffset % torrentUsualPieceSize
			beginSize := torrentUsualPieceSize - beginOffset
			beginDownLoaded := pieceSizeCompleted - beginOffset
			if beginDownLoaded < 0 {
				beginDownLoaded = 0
			}
			left += beginSize - beginDownLoaded
		case fileEndPieceIndex - 1:
			endSize := (fileTorrentOffset + fileLength) % torrentUsualPieceSize
			if endSize == 0 {
				endSize = torrentUsualPieceSize
			}
			endDownloaded := pieceSizeCompleted
			if endDownloaded > endSize {
				endDownloaded = endSize
			}
			left += endSize - endDownloaded
		default:
			left += torrentUsualPieceSize - pieceSizeCompleted
		}
		return true
	})

	if left > fileLength {
		left = fileLength
	}
	//
	//numPiecesSpanned := f.EndPieceIndex() - f.BeginPieceIndex()
	//completedMiddlePieces := f.t._completedPieces.Clone()
	//completedMiddlePieces.RemoveRange(0, uint64(f.BeginPieceIndex()+1))
	//completedMiddlePieces.RemoveRange(uint64(f.EndPieceIndex()-1), roaring.MaxRange)
	//left += int64(numPiecesSpanned-2-pieceIndex(completedMiddlePieces.GetCardinality())) * torrentUsualPieceSize
	return
}

func (f *File) bytesLeft() (left int64) {
	return fileBytesLeft(
		int64(f.t.usualPieceSize()),
		f.BeginPieceIndex(),
		f.EndPieceIndex(),
		f.offset,
		f.length,
		&f.t._completedPieces.Bitmap,
		func(pieceIndex int) int64 {
			return int64(f.t.piece(pieceIndex).numDirtyBytes())
		},
	)
}

// The relative file path for a multi-file torrent, and the torrent name for a
// single-file torrent. Dir separators are '/'.
func (f *File) DisplayPath() string {
	return f.displayPath
}

// The download status of a piece that comprises part of a File.
type FilePieceState struct {
	Bytes int64 // Bytes within the piece that are part of this File.
	PieceState
}

// Returns the state of pieces in this file.
func (f *File) State() (ret []FilePieceState) {
	f.t.cl.rLock()
	defer f.t.cl.rUnlock()
	pieceSize := int64(f.t.usualPieceSize())
	off := f.offset % pieceSize
	remaining := f.length
	for i := pieceIndex(f.offset / pieceSize); ; i++ {
		if remaining == 0 {
			break
		}
		len1 := pieceSize - off
		if len1 > remaining {
			len1 = remaining
		}
		ps := f.t.pieceState(i)
		ret = append(ret, FilePieceState{len1, ps})
		off = 0
		remaining -= len1
	}
	return
}

// Requests that all pieces containing data in the file be downloaded.
func (f *File) Download() {
	f.SetPriority(PiecePriorityNormal)
}

func byteRegionExclusivePieces(off, size, pieceSize int64) (begin, end int) {
	begin = int((off + pieceSize - 1) / pieceSize)
	end = int((off + size) / pieceSize)
	return
}

// Deprecated: Use File.SetPriority.
func (f *File) Cancel() {
	f.SetPriority(PiecePriorityNone)
}

func (f *File) NewReader() Reader {
	return f.t.newReader(f.Offset(), f.Length())
}

// Sets the minimum priority for pieces in the File.
func (f *File) SetPriority(prio PiecePriority) {
	f.t.cl.lock()
	if prio != f.prio {
		f.prio = prio
		f.t.updatePiecePriorities(f.BeginPieceIndex(), f.EndPieceIndex(), "File.SetPriority")
	}
	f.t.cl.unlock()
}

// Returns the priority per File.SetPriority.
func (f *File) Priority() (prio PiecePriority) {
	f.t.cl.rLock()
	prio = f.prio
	f.t.cl.rUnlock()
	return
}

// Returns the index of the first piece containing data for the file.
func (f *File) BeginPieceIndex() int {
	return f.fi.BeginPieceIndex(int64(f.t.usualPieceSize()))
}

// Returns the index of the piece after the last one containing data for the file.
func (f *File) EndPieceIndex() int {
	return f.fi.EndPieceIndex(int64(f.t.usualPieceSize()))
}

func (f *File) numPieces() int {
	return f.EndPieceIndex() - f.BeginPieceIndex()
}

func (f *File) PieceIndices() iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := f.BeginPieceIndex(); i < f.EndPieceIndex(); i++ {
			if !yield(i) {
				break
			}
		}
	}
}

func (f *File) Pieces() iter.Seq[*Piece] {
	return func(yield func(*Piece) bool) {
		for i := range f.PieceIndices() {
			p := f.t.piece(i)
			if !yield(p) {
				break
			}
		}
	}
}
