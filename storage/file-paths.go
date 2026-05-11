package storage

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/wchen99998/torrent/metainfo"
)

// Determines the filepath to be used for each file in a torrent.
type FilePathMaker func(opts FilePathMakerOpts) (string, error)

// Determines the directory for a given torrent within a storage client.
type TorrentDirFilePathMaker func(baseDir string, info *metainfo.Info, infoHash metainfo.Hash) string

// Info passed to a FilePathMaker.
type FilePathMakerOpts struct {
	Info        *metainfo.Info
	InfoHash    metainfo.Hash
	File        *metainfo.FileInfo
	FileIndex   int
	DefaultPath string
}

// defaultPathMaker just returns the storage client's base directory.
func defaultPathMaker(baseDir string, info *metainfo.Info, infoHash metainfo.Hash) string {
	return baseDir
}

func infoHashPathMaker(baseDir string, info *metainfo.Info, infoHash metainfo.Hash) string {
	return filepath.Join(baseDir, infoHash.HexString())
}

func defaultFilePath(info *metainfo.Info, file *metainfo.FileInfo) string {
	var parts []string
	if info.BestName() != metainfo.NoName {
		parts = append(parts, info.BestName())
	}
	return filepath.Join(append(parts, file.BestPath()...)...)
}

func isSubFilepath(base, sub string) bool {
	rel, err := filepath.Rel(base, sub)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
