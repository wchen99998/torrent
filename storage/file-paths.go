package storage

import (
	"errors"
	"fmt"
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

// FilePlan is the storage-backed file layout for one torrent file.
type FilePlan struct {
	Index               int
	Path                string
	DisplayPath         string
	Length              int64
	TorrentOffset       int64
	BeginPieceIndex     int
	EndPieceIndex       int
	FileInfo            metainfo.FileInfo
	DefaultPath         string
	StorageRelativePath string
	StoragePath         string
}

// FileLayoutPlan is the storage-backed file layout for a torrent.
type FileLayoutPlan struct {
	TorrentDir string
	Files      []FilePlan
}

// defaultPathMaker just returns the storage client's base directory.
func defaultPathMaker(baseDir string, info *metainfo.Info, infoHash metainfo.Hash) string {
	return baseDir
}

func defaultFilePathMaker(opts FilePathMakerOpts) (string, error) {
	return opts.DefaultPath, nil
}

func withFilePathDefaults(opts NewFileClientOpts) NewFileClientOpts {
	if opts.TorrentDirMaker == nil {
		opts.TorrentDirMaker = defaultPathMaker
	}
	if opts.FilePathMaker == nil {
		opts.FilePathMaker = defaultFilePathMaker
	}
	return opts
}

func infoHashPathMaker(baseDir string, info *metainfo.Info, infoHash metainfo.Hash) string {
	return filepath.Join(baseDir, infoHash.HexString())
}

// InfoHashPathMaker stores each torrent under a directory named by infohash.
func InfoHashPathMaker(baseDir string, info *metainfo.Info, infoHash metainfo.Hash) string {
	return infoHashPathMaker(baseDir, info, infoHash)
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

// PlanFiles applies file storage path configuration to torrent file metadata
// without opening or creating any storage files.
func PlanFiles(opts NewFileClientOpts, info *metainfo.Info, infoHash metainfo.Hash) (FileLayoutPlan, error) {
	if info == nil {
		return FileLayoutPlan{}, errors.New("nil info")
	}
	opts = withFilePathDefaults(opts)
	dir := opts.TorrentDirMaker(opts.ClientBaseDir, info, infoHash)
	metainfoFileInfos := info.UpvertedFiles()
	files := make([]FilePlan, 0, len(metainfoFileInfos))
	for i, fileInfo := range metainfoFileInfos {
		defaultPath := defaultFilePath(info, &fileInfo)
		madePath, err := opts.FilePathMaker(FilePathMakerOpts{
			Info:        info,
			InfoHash:    infoHash,
			File:        &fileInfo,
			FileIndex:   i,
			DefaultPath: defaultPath,
		})
		if err != nil {
			return FileLayoutPlan{}, fmt.Errorf("file %v: making path: %w", i, err)
		}
		filePath := filepath.Join(dir, madePath)
		if !isSubFilepath(dir, filePath) {
			return FileLayoutPlan{}, fmt.Errorf("file %v: path %q is not sub path of %q", i, filePath, dir)
		}
		fi := fileInfo
		fi.Path = append([]string(nil), fi.Path...)
		fi.PathUtf8 = append([]string(nil), fi.PathUtf8...)
		files = append(files, FilePlan{
			Index:               i,
			Path:                strings.Join(append([]string{info.BestName()}, fileInfo.BestPath()...), "/"),
			DisplayPath:         fileInfo.DisplayPath(info),
			Length:              fileInfo.Length,
			TorrentOffset:       fileInfo.TorrentOffset,
			BeginPieceIndex:     fileInfo.BeginPieceIndex(info.PieceLength),
			EndPieceIndex:       fileInfo.EndPieceIndex(info.PieceLength),
			FileInfo:            fi,
			DefaultPath:         defaultPath,
			StorageRelativePath: madePath,
			StoragePath:         filePath,
		})
	}
	return FileLayoutPlan{
		TorrentDir: dir,
		Files:      files,
	}, nil
}
