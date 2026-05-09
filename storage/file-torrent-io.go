package storage

import (
	"errors"
	"io"
	"io/fs"
	"os"

	"github.com/anacrolix/missinggo/v2/panicif"

	"github.com/wchen99998/torrent/metainfo"
	"github.com/wchen99998/torrent/segments"
)

// Exposes file-based storage of a torrent, as one big ReadWriterAt.
type fileTorrentImplIO struct {
	fts *fileTorrentImpl
}

// Returns EOF on short or missing file.
func (fst fileTorrentImplIO) readFileAt(
	fileIndex int,
	file file,
	b []byte,
	off int64,
	torrentOff int64,
) (n int, err error) {
	fst.fts.logger().Debug("readFileAt", "file.safeOsPath", file.safeOsPath)
	file.mu.RLock()
	released := file.released
	file.mu.RUnlock()
	if released {
		return fst.readReleasedFileAt(fileIndex, file, b, off, torrentOff)
	}
	f, err := fst.fts.openSharedFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		// File missing is treated the same as a short file. Should we propagate this through the
		// interface now that fs.ErrNotExist is a thing?
		err = io.EOF
		return
	}
	if err != nil {
		return
	}
	defer f.Close()
	// Limit the read to within the expected bounds of this file.
	if int64(len(b)) > file.length()-off {
		b = b[:file.length()-off]
	}
	for off < file.length() && len(b) != 0 {
		n1, err1 := f.ReadAt(b, off)
		b = b[n1:]
		n += n1
		off += int64(n1)
		if n1 == 0 {
			err = err1
			break
		}
	}
	return
}

func (fst fileTorrentImplIO) readReleasedFileAt(
	fileIndex int,
	file file,
	b []byte,
	off int64,
	torrentOff int64,
) (n int, err error) {
	pieceIndex := metainfo.PieceIndex(torrentOff / fst.fts.info.PieceLength)
	fileExtent, ok := fst.fts.fileExtentInPiece(fileIndex, int(pieceIndex))
	if !ok {
		err = ErrFileReleased
		return
	}
	releasedOffset := off - fileExtent.Start
	if releasedOffset < 0 || releasedOffset+int64(len(b)) > fileExtent.Length {
		err = ErrFileReleased
		return
	}
	f, err := os.Open(file.releasedPiecePath(int(pieceIndex)))
	if errors.Is(err, fs.ErrNotExist) {
		err = ErrFileReleased
		return
	}
	if err != nil {
		return
	}
	defer f.Close()
	return f.ReadAt(b, releasedOffset)
}

// Only returns EOF at the end of the torrent. Premature EOF is ErrUnexpectedEOF.
func (fst fileTorrentImplIO) ReadAt(b []byte, off int64) (n int, err error) {
	for i, e := range fst.fts.segmentLocater.LocateIter(
		segments.Extent{off, int64(len(b))},
	) {
		n1, err1 := fst.readFileAt(i, fst.fts.file(i), b[:e.Length], e.Start, off+int64(n))
		n += n1
		b = b[n1:]
		if segments.Int(n1) == e.Length {
			switch err1 {
			// ReaderAt.ReadAt contract.
			case nil, io.EOF:
			default:
				err = err1
				return
			}
		} else {
			panicif.Nil(err1)
			err = err1
			return
		}
	}
	if len(b) != 0 {
		// We're at the end of the torrent.
		err = io.EOF
	}
	return
}

func (fst fileTorrentImplIO) WriteAt(p []byte, off int64) (n int, err error) {
	for i, e := range fst.fts.segmentLocater.LocateIter(
		segments.Extent{off, int64(len(p))},
	) {
		var n1 int
		n1, err = fst.writeFileAt(i, fst.fts.file(i), p[:e.Length], e, off+int64(n))
		n += n1
		p = p[n1:]
		if err == nil && int64(n1) != e.Length {
			err = io.ErrShortWrite
		}
		if err != nil {
			return
		}
	}
	return
}

func (fst fileTorrentImplIO) writeFileAt(
	fileIndex int,
	file file,
	p []byte,
	extent segments.Extent,
	torrentOff int64,
) (n int, err error) {
	file.mu.RLock()
	released := file.released
	file.mu.RUnlock()
	if released {
		return fst.writeReleasedFileAt(fileIndex, file, p, extent, torrentOff)
	}
	var f fileWriter
	f, err = fst.fts.openForWrite(file)
	if err != nil {
		return
	}
	n, err = f.WriteAt(p, extent.Start)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	return
}

func (fst fileTorrentImplIO) writeReleasedFileAt(
	fileIndex int,
	file file,
	p []byte,
	extent segments.Extent,
	torrentOff int64,
) (n int, err error) {
	pieceIndex := metainfo.PieceIndex(torrentOff / fst.fts.info.PieceLength)
	fileExtent, ok := fst.fts.fileExtentInPiece(fileIndex, int(pieceIndex))
	if !ok {
		err = ErrFileReleased
		return
	}
	offset := extent.Start - fileExtent.Start
	if offset < 0 || offset+int64(len(p)) > fileExtent.Length {
		err = ErrFileReleased
		return
	}
	f, err := os.OpenFile(file.releasedPiecePath(int(pieceIndex)), os.O_WRONLY, filePerm)
	if errors.Is(err, fs.ErrNotExist) {
		err = ErrFileReleased
		return
	}
	if err != nil {
		return
	}
	n, err = f.WriteAt(p, offset)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	return
}
