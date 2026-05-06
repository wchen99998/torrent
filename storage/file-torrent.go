package storage

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/anacrolix/missinggo/v2"
	"github.com/anacrolix/missinggo/v2/panicif"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/segments"
)

type fileTorrentImpl struct {
	info              *metainfo.Info
	files             []fileExtra
	metainfoFileInfos []metainfo.FileInfo
	segmentLocater    segments.Index
	infoHash          metainfo.Hash
	io                fileIo
	// Save memory by pointing to the other data.
	client *fileClientImpl
}

func (me *fileTorrentImpl) logger() *slog.Logger {
	return me.client.opts.Logger
}

func (me *fileTorrentImpl) pieceCompletion() PieceCompletion {
	return me.client.opts.PieceCompletion
}

func (me *fileTorrentImpl) pieceCompletionKey(p int) metainfo.PieceKey {
	return metainfo.PieceKey{
		InfoHash: me.infoHash,
		Index:    p,
	}
}

func (me *fileTorrentImpl) setPieceCompletion(p int, complete bool) error {
	return me.pieceCompletion().Set(me.pieceCompletionKey(p), complete)
}

func (me *fileTorrentImpl) markFileReleased(fileIndex int) error {
	if fileIndex < 0 || fileIndex >= len(me.files) {
		return fmt.Errorf("file index %d out of range", fileIndex)
	}
	f := me.file(fileIndex)
	if err := me.writeReleasedBoundaryPieces(fileIndex, f); err != nil {
		return err
	}
	f.mu.Lock()
	f.released = true
	if err := os.MkdirAll(filepath.Dir(f.releasedFilePath()), dirPerm); err != nil {
		f.mu.Unlock()
		return err
	}
	marker, err := os.OpenFile(f.releasedFilePath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, filePerm)
	if err != nil {
		f.mu.Unlock()
		return err
	}
	err = marker.Close()
	f.mu.Unlock()
	if err != nil {
		return err
	}
	for pieceIndex := f.beginPieceIndex(); pieceIndex < f.endPieceIndex(); pieceIndex++ {
		if err := me.setPieceCompletion(pieceIndex, true); err != nil {
			return fmt.Errorf("setting released file piece %d complete: %w", pieceIndex, err)
		}
	}
	return nil
}

func (me *fileTorrentImpl) writeReleasedBoundaryPieces(fileIndex int, f file) error {
	for pieceIndex := f.beginPieceIndex(); pieceIndex < f.endPieceIndex(); pieceIndex++ {
		piece := me.info.Piece(pieceIndex)
		pieceExtent := segments.Extent{Start: piece.Offset(), Length: piece.Length()}
		var fileExtent segments.Extent
		fileSegments := 0
		foundFile := false
		for i, extent := range me.segmentLocater.LocateIter(pieceExtent) {
			fileSegments++
			if i == fileIndex {
				fileExtent = extent
				foundFile = true
			}
		}
		if !foundFile || fileSegments <= 1 {
			continue
		}
		if err := me.writeReleasedBoundaryPiece(f, pieceIndex, fileExtent); err != nil {
			return err
		}
	}
	return nil
}

func (me *fileTorrentImpl) writeReleasedBoundaryPiece(f file, pieceIndex int, extent segments.Extent) error {
	sourcePath := f.safeOsPath
	if me.partFiles() {
		if _, err := os.Stat(sourcePath); errors.Is(err, fs.ErrNotExist) {
			sourcePath = f.partFilePath()
		}
	}
	in, err := os.Open(sourcePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("opening released file boundary source %q: %w", sourcePath, err)
	}
	defer in.Close()
	if _, err := in.Seek(extent.Start, io.SeekStart); err != nil {
		return fmt.Errorf("seeking released file boundary %q: %w", sourcePath, err)
	}
	outPath := f.releasedPiecePath(pieceIndex)
	if err := os.MkdirAll(filepath.Dir(outPath), dirPerm); err != nil {
		return err
	}
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("creating released file boundary %q: %w", outPath, err)
	}
	written, copyErr := io.CopyN(out, in, extent.Length)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("copying released file boundary %q: wrote %d/%d: %w", sourcePath, written, extent.Length, copyErr)
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}

// Set piece completions based on whether all files in each piece are not .part files.
func (me *fileTorrentImpl) setCompletionFromPartFiles() error {
	notComplete := make([]bool, me.info.NumPieces())
	for fileIndex := range me.files {
		f := me.file(fileIndex)
		f.mu.RLock()
		released := f.released
		f.mu.RUnlock()
		if released {
			continue
		}
		fi, err := os.Stat(f.safeOsPath)
		if err == nil {
			if fi.Size() == f.length() {
				continue
			}
			me.logger().Warn("file has unexpected size", "file", f.safeOsPath, "size", fi.Size(), "expected", f.length())
		} else if !errors.Is(err, fs.ErrNotExist) {
			me.logger().Warn("error checking file size", "err", err)
		}
		// Ensure all pieces associated with a file are not marked as complete (at most unknown).
		for pieceIndex := f.beginPieceIndex(); pieceIndex < f.endPieceIndex(); pieceIndex++ {
			notComplete[pieceIndex] = true
		}
	}
	for i, nc := range notComplete {
		if nc {
			c := me.getCompletion(i)
			if c.Complete {
				// TODO: We need to set unknown so that verification of the data we do have could
				// occur naturally but that'll be a big change.
				panicif.Err(me.setPieceCompletion(i, false))
			}
		} else {
			err := me.setPieceCompletion(i, true)
			if err != nil {
				return fmt.Errorf("setting piece %v completion: %w", i, err)
			}
		}
	}
	return nil
}

func (me *fileTorrentImpl) partFiles() bool {
	return me.client.opts.partFiles()
}

func (me *fileTorrentImpl) pathForWrite(f *file) string {
	if me.partFiles() {
		return f.partFilePath()
	}
	return f.safeOsPath
}

func (me *fileTorrentImpl) getCompletion(piece int) Completion {
	cmpl, err := me.pieceCompletion().Get(metainfo.PieceKey{me.infoHash, piece})
	cmpl.Err = errors.Join(cmpl.Err, err)
	return cmpl
}

func (me *fileTorrentImpl) Piece(p metainfo.Piece) PieceImpl {
	// Create a view onto the file-based torrent storage.
	_io := fileTorrentImplIO{me}
	// Return the appropriate segments of this.
	return &filePieceImpl{
		me,
		p,
		missinggo.NewSectionWriter(_io, p.Offset(), p.Length()),
		io.NewSectionReader(_io, p.Offset(), p.Length()),
	}
}

func (me *fileTorrentImpl) Close() error {
	return me.io.Close()
}

func (me *fileTorrentImpl) file(index int) file {
	return file{
		Info:      me.info,
		FileInfo:  &me.metainfoFileInfos[index],
		fileExtra: &me.files[index],
	}
}

func (me *fileTorrentImpl) loadReleasedFileMarkers() {
	for i := range me.files {
		f := me.file(i)
		if _, err := os.Stat(f.releasedFilePath()); err == nil {
			f.mu.Lock()
			f.released = true
			f.mu.Unlock()
		}
	}
}

// Open file for reading.
func (me *fileTorrentImpl) openSharedFile(file file) (f sharableReader, err error) {
	file.mu.RLock()
	// Fine to open once under each name on a unix system. We could make the shared file keys more
	// constrained, but it shouldn't matter. TODO: Ensure at most one of the names exist.
	if me.partFiles() {
		f, err = me.io.openForSharedRead(file.partFilePath())
	}
	if err == nil && f == nil || errors.Is(err, fs.ErrNotExist) {
		f, err = me.io.openForSharedRead(file.safeOsPath)
	}
	file.mu.RUnlock()
	return
}

// Open file for reading. Not a shared handle if that matters.
func (me *fileTorrentImpl) openFile(file file) (f fileReader, err error) {
	file.mu.RLock()
	// Fine to open once under each name on a unix system. We could make the shared file keys more
	// constrained, but it shouldn't matter. TODO: Ensure at most one of the names exist.
	if me.partFiles() {
		f, err = me.io.openForRead(file.partFilePath())
	}
	if err == nil && f == nil || errors.Is(err, fs.ErrNotExist) {
		f, err = me.io.openForRead(file.safeOsPath)
	}
	file.mu.RUnlock()
	return
}

func (me *fileTorrentImpl) openForWrite(file file) (_ fileWriter, err error) {
	// It might be possible to have a writable handle shared files cache if we need it.
	me.logger().Debug("openForWrite", "file.safeOsPath", file.safeOsPath)
	return me.io.openForWrite(me.pathForWrite(&file), file.FileInfo.Length)
}
