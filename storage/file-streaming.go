package storage

// NewFileByInfoHashForStreaming stores data by infohash using classic file IO.
// It is intended for consumers that hand off completed files while the torrent
// remains active. Calling torrent.File.ReleaseStorage removes the handed-off
// file data while preserving completion markers; DiscardStorage removes the
// file data and marks affected pieces incomplete.
func NewFileByInfoHashForStreaming(baseDir string) ClientImplCloser {
	return NewFileOpts(NewFileClientOpts{
		ClientBaseDir:      baseDir,
		TorrentDirMaker:    infoHashPathMaker,
		PieceCompletion:    pieceCompletionForDir(baseDir),
		ForceClassicFileIO: true,
	})
}
