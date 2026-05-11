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

	"github.com/wchen99998/torrent/metainfo"
	"github.com/wchen99998/torrent/segments"
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

func (me *fileTorrentImpl) validateFileIndex(fileIndex int) error {
	if fileIndex < 0 || fileIndex >= len(me.files) {
		return fmt.Errorf("file index %d out of range", fileIndex)
	}
	return nil
}

func (me *fileTorrentImpl) markFileReleased(fileIndex int) error {
	if err := me.validateFileIndex(fileIndex); err != nil {
		return err
	}
	f := me.file(fileIndex)
	f.mu.RLock()
	alreadyReleased := f.released
	f.mu.RUnlock()
	if alreadyReleased {
		return me.validateReleasedBoundaryPieces(fileIndex, f)
	}
	if err := me.writeReleasedBoundaryPieces(fileIndex, f); err != nil {
		return err
	}
	f.mu.Lock()
	if err := me.writeReleasedFileMarker(f); err != nil {
		f.mu.Unlock()
		return err
	}
	if err := me.removeFileStorage(f); err != nil {
		cleanupErr := me.removeReleasedFileStorage(f)
		f.mu.Unlock()
		return errors.Join(err, cleanupErr)
	}
	f.released = true
	f.mu.Unlock()
	for pieceIndex := f.beginPieceIndex(); pieceIndex < f.endPieceIndex(); pieceIndex++ {
		if err := me.setPieceCompletion(pieceIndex, true); err != nil {
			return fmt.Errorf("setting released file piece %d complete: %w", pieceIndex, err)
		}
	}
	return nil
}

func (me *fileTorrentImpl) markFileDiscarded(fileIndex int) error {
	if err := me.validateFileIndex(fileIndex); err != nil {
		return err
	}
	f := me.file(fileIndex)
	f.mu.Lock()
	f.released = false
	err := errors.Join(
		me.removeFileStorage(f),
		me.removeReleasedFileStorage(f),
	)
	f.mu.Unlock()
	if err != nil {
		return fmt.Errorf("discarding file storage: %w", err)
	}
	for pieceIndex := f.beginPieceIndex(); pieceIndex < f.endPieceIndex(); pieceIndex++ {
		if err := me.setPieceCompletion(pieceIndex, false); err != nil {
			return fmt.Errorf("setting discarded file piece %d incomplete: %w", pieceIndex, err)
		}
	}
	return nil
}

func (me *fileTorrentImpl) writeReleasedFileMarker(f file) error {
	if err := os.MkdirAll(filepath.Dir(f.releasedFilePath()), dirPerm); err != nil {
		return err
	}
	marker, err := os.OpenFile(f.releasedFilePath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, filePerm)
	if err != nil {
		return err
	}
	return marker.Close()
}

func (me *fileTorrentImpl) removeReleasedFileStorage(f file) error {
	var err error
	err = errors.Join(err, me.removeFilePath(f.releasedFilePath()))
	for pieceIndex := f.beginPieceIndex(); pieceIndex < f.endPieceIndex(); pieceIndex++ {
		err = errors.Join(err, me.removeFilePath(f.releasedPiecePath(pieceIndex)))
	}
	return err
}

func (me *fileTorrentImpl) removeFileStorage(f file) (err error) {
	err = errors.Join(
		me.removeFilePath(f.safeOsPath),
		me.removeFilePath(f.partFilePath()),
	)
	if err != nil {
		return fmt.Errorf("removing released file storage: %w", err)
	}
	return nil
}

func (me *fileTorrentImpl) removeFilePath(path string) error {
	err := me.io.remove(path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if errors.Is(err, fs.ErrPermission) {
		if chmodErr := os.Chmod(path, filePerm); chmodErr != nil {
			return fmt.Errorf("preparing %q for removal: %w", path, chmodErr)
		}
		err = me.io.remove(path)
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return nil
		}
	}
	return fmt.Errorf("removing %q: %w", path, err)
}

func (me *fileTorrentImpl) writeReleasedBoundaryPieces(fileIndex int, f file) error {
	for pieceIndex := f.beginPieceIndex(); pieceIndex < f.endPieceIndex(); pieceIndex++ {
		fileExtent, ok := me.fileExtentInBoundaryPiece(fileIndex, pieceIndex)
		if !ok {
			continue
		}
		if err := me.writeReleasedBoundaryPiece(f, pieceIndex, fileExtent); err != nil {
			return err
		}
	}
	return nil
}

func (me *fileTorrentImpl) validateReleasedBoundaryPieces(fileIndex int, f file) error {
	for pieceIndex := f.beginPieceIndex(); pieceIndex < f.endPieceIndex(); pieceIndex++ {
		fileExtent, ok := me.fileExtentInBoundaryPiece(fileIndex, pieceIndex)
		if !ok {
			continue
		}
		fi, err := os.Stat(f.releasedPiecePath(pieceIndex))
		if err != nil {
			return fmt.Errorf("released file boundary piece %d for %q: %w", pieceIndex, f.safeOsPath, err)
		}
		if fi.Size() != fileExtent.Length {
			return fmt.Errorf(
				"released file boundary piece %d for %q has size %d, expected %d",
				pieceIndex,
				f.safeOsPath,
				fi.Size(),
				fileExtent.Length,
			)
		}
	}
	return nil
}

func (me *fileTorrentImpl) fileExtentInBoundaryPiece(fileIndex, pieceIndex int) (segments.Extent, bool) {
	fileExtent, ok, fileSegments := me.fileExtentInPieceWithSegmentCount(fileIndex, pieceIndex)
	return fileExtent, ok && fileSegments > 1
}

func (me *fileTorrentImpl) fileExtentInPiece(fileIndex, pieceIndex int) (segments.Extent, bool) {
	fileExtent, ok, _ := me.fileExtentInPieceWithSegmentCount(fileIndex, pieceIndex)
	return fileExtent, ok
}

func (me *fileTorrentImpl) fileExtentInPieceWithSegmentCount(
	fileIndex int,
	pieceIndex int,
) (fileExtent segments.Extent, foundFile bool, fileSegments int) {
	piece := me.info.Piece(pieceIndex)
	pieceExtent := segments.Extent{Start: piece.Offset(), Length: piece.Length()}
	for i, extent := range me.segmentLocater.LocateIter(pieceExtent) {
		fileSegments++
		if i == fileIndex {
			fileExtent = extent
			foundFile = true
		}
	}
	return
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
			return fmt.Errorf("released file boundary source %q is missing: %w", sourcePath, err)
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
			if err := me.validateReleasedBoundaryPieces(i, f); err != nil {
				me.logger().Warn("ignoring invalid released file marker", "file", f.safeOsPath, "err", err)
				if err := me.removeReleasedFileStorage(f); err != nil {
					me.logger().Warn("error removing invalid released file marker", "file", f.safeOsPath, "err", err)
				}
				continue
			}
			f.mu.Lock()
			f.released = true
			f.mu.Unlock()
		}
	}
}

// Open file for reading.
func (me *fileTorrentImpl) openSharedFile(file file) (f sharableReader, err error) {
	file.mu.RLock()
	if file.released {
		file.mu.RUnlock()
		err = ErrFileReleased
		return
	}
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
	if file.released {
		file.mu.RUnlock()
		err = ErrFileReleased
		return
	}
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
	file.mu.RLock()
	if file.released {
		file.mu.RUnlock()
		return nil, ErrFileReleased
	}
	path := me.pathForWrite(&file)
	file.mu.RUnlock()
	return me.io.openForWrite(path, file.FileInfo.Length)
}
