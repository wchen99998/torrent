package torrent_test

import (
	"context"
	"io"
	"log"

	"github.com/wchen99998/torrent"
	"github.com/wchen99998/torrent/stream"
)

func Example() {
	c, _ := torrent.NewClient(nil)
	defer c.Close()
	t, _ := c.AddMagnet("magnet:?xt=urn:btih:ZOCMZQIPFFW7OLLMIC5HUB6BPCSDEOQU")
	<-t.GotInfo()
	t.DownloadAll()
	c.WaitAll()
	log.Print("ermahgerd, torrent downloaded")
}

func Example_fileReader() {
	var f torrent.File
	// Accesses the parts of the torrent pertaining to f. Data will be
	// downloaded as required, per the configuration of the torrent.Reader.
	r := f.NewReader()
	defer r.Close()
}

func Example_streamFiles() {
	ctx := context.Background()
	c, _ := torrent.NewClient(nil)
	defer c.Close()

	t, _ := c.AddMagnet("magnet:?xt=urn:btih:ZOCMZQIPFFW7OLLMIC5HUB6BPCSDEOQU")
	info, err := t.WaitInfo(ctx)
	if err != nil {
		log.Print(err)
		return
	}
	_ = info

	var indexes []int
	for _, file := range t.FileSnapshots() {
		if file.Length != 0 {
			indexes = append(indexes, file.Index)
		}
	}

	err = stream.Files(ctx, t, stream.FilesOptions{
		FileIndexes: indexes,
		MaxActive:   2,
		Readahead:   1 << 20,
	}, func(ctx context.Context, lease *stream.FileLease) error {
		_, err := io.Copy(io.Discard, lease.Reader)
		if err != nil {
			_ = lease.Discard(ctx)
			return err
		}
		return lease.Release(ctx)
	})
	if err != nil {
		log.Print(err)
	}
	for _, file := range t.FileSnapshots() {
		log.Printf("%s: %d/%d", file.DisplayPath, file.Completed, file.Length)
	}
	for _, peer := range t.PeerSnapshots() {
		log.Printf("%s: %d pieces", peer.RemoteAddr, peer.Stats.RemotePieceCount)
	}
}
