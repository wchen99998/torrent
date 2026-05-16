package storage

import (
	"context"
	"fmt"
	"log/slog"

	g "github.com/anacrolix/generics"

	"github.com/wchen99998/torrent/metainfo"
)

// File-based storage for torrents, that isn't yet bound to a particular torrent.
type fileClientImpl struct {
	opts NewFileClientOpts
}

// All Torrent data stored in this baseDir. The info names of each torrent are used as directories.
func NewFile(baseDir string) ClientImplCloser {
	return NewFileWithCompletion(baseDir, pieceCompletionForDir(baseDir))
}

type NewFileClientOpts struct {
	// The base directory for all downloads.
	ClientBaseDir   string
	FilePathMaker   FilePathMaker
	TorrentDirMaker TorrentDirFilePathMaker
	// If part files are enabled, this will default to inferring completion from file names at
	// startup, and keep the rest in memory.
	PieceCompletion PieceCompletion
	UsePartFiles    g.Option[bool]
	// ForceClassicFileIO disables mmap-backed file IO for callers that move or
	// remove completed files while a torrent remains active.
	ForceClassicFileIO bool
	Logger             *slog.Logger
}

// The specific part-files option or the default.
func (me NewFileClientOpts) partFiles() bool {
	return me.UsePartFiles.UnwrapOr(true)
}

// NewFileOpts creates a new ClientImplCloser that stores files using the OS native filesystem.
func NewFileOpts(opts NewFileClientOpts) ClientImplCloser {
	opts = withFilePathDefaults(opts)
	if opts.PieceCompletion == nil {
		if opts.partFiles() {
			opts.PieceCompletion = NewMapPieceCompletion()
		} else {
			opts.PieceCompletion = pieceCompletionForDir(opts.ClientBaseDir)
		}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &fileClientImpl{opts}
}

func (me *fileClientImpl) Close() error {
	return me.opts.PieceCompletion.Close()
}

func (fs *fileClientImpl) OpenTorrent(
	ctx context.Context,
	info *metainfo.Info,
	infoHash metainfo.Hash,
) (_ TorrentImpl, err error) {
	plan, err := PlanFiles(fs.opts, info, infoHash)
	if err != nil {
		return TorrentImpl{}, err
	}
	fs.opts.Logger.DebugContext(ctx, "opened file torrent storage", slog.String("dir", plan.TorrentDir))
	files := make([]fileExtra, len(plan.Files))
	metainfoFileInfos := make([]metainfo.FileInfo, len(plan.Files))
	for i, filePlan := range plan.Files {
		files[i].safeOsPath = filePlan.StoragePath
		metainfoFileInfos[i] = filePlan.FileInfo
		if filePlan.Length == 0 {
			err = CreateNativeZeroLengthFile(filePlan.StoragePath)
			if err != nil {
				err = fmt.Errorf("creating zero length file: %w", err)
				return
			}
		}
	}
	t := &fileTorrentImpl{
		info,
		files,
		metainfoFileInfos,
		info.FileSegmentsIndex(),
		infoHash,
		fs.newFileIo(),
		fs,
	}
	t.loadReleasedFileMarkers()
	if t.partFiles() {
		err = t.setCompletionFromPartFiles()
		if err != nil {
			err = fmt.Errorf("setting completion from part files: %w", err)
			return
		}
	}
	return TorrentImpl{
		Piece:             t.Piece,
		Close:             t.Close,
		MarkFileReleased:  t.markFileReleased,
		MarkFileDiscarded: t.markFileDiscarded,
		FileState:         t.fileState,
	}, nil
}

func (fs *fileClientImpl) newFileIo() fileIo {
	if fs.opts.ForceClassicFileIO {
		return classicFileIo{}
	}
	return defaultFileIo()
}
