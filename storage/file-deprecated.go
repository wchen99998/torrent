package storage

import (
	"github.com/wchen99998/torrent/metainfo"
)

func NewFileWithCompletion(baseDir string, completion PieceCompletion) ClientImplCloser {
	return NewFileWithCustomPathMakerAndCompletion(baseDir, nil, completion)
}

// File storage with data partitioned by infohash.
func NewFileByInfoHash(baseDir string) ClientImplCloser {
	return NewFileWithCustomPathMaker(baseDir, infoHashPathMaker)
}

// NewFileByInfoHashForStreaming stores data by infohash using classic file IO
// and supports ReleaseStorage on torrent files. It is intended for consumers
// that hand off completed files while the torrent remains active.
func NewFileByInfoHashForStreaming(baseDir string) ClientImplCloser {
	return NewFileOpts(NewFileClientOpts{
		ClientBaseDir:      baseDir,
		TorrentDirMaker:    infoHashPathMaker,
		PieceCompletion:    pieceCompletionForDir(baseDir),
		ForceClassicFileIO: true,
	})
}

// Deprecated: Allows passing a function to determine the path for storing torrent data. The
// function is responsible for sanitizing the info if it uses some part of it (for example
// sanitizing info.Name).
func NewFileWithCustomPathMaker(baseDir string, pathMaker func(baseDir string, info *metainfo.Info, infoHash metainfo.Hash) string) ClientImplCloser {
	return NewFileWithCustomPathMakerAndCompletion(baseDir, pathMaker, pieceCompletionForDir(baseDir))
}

// Deprecated: Allows passing custom PieceCompletion
func NewFileWithCustomPathMakerAndCompletion(
	baseDir string,
	pathMaker TorrentDirFilePathMaker,
	completion PieceCompletion,
) ClientImplCloser {
	return NewFileOpts(NewFileClientOpts{
		ClientBaseDir:   baseDir,
		TorrentDirMaker: pathMaker,
		PieceCompletion: completion,
	})
}
