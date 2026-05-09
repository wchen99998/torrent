package main

import (
	"fmt"

	"github.com/davecgh/go-spew/spew"

	"github.com/wchen99998/torrent"
	"github.com/wchen99998/torrent/tracker"
	"github.com/wchen99998/torrent/tracker/udp"
	"github.com/wchen99998/torrent/types/infohash"
)

type AnnounceCmd struct {
	Event    udp.AnnounceEvent
	Port     *uint16
	Tracker  string     `arg:"positional"`
	InfoHash infohash.T `arg:"positional"`
}

func announceErr(flags AnnounceCmd) error {
	req := tracker.AnnounceRequest{
		InfoHash: flags.InfoHash,
		Port:     uint16(torrent.NewDefaultClientConfig().ListenPort),
		NumWant:  -1,
		Event:    flags.Event,
		Left:     -1,
	}
	if flags.Port != nil {
		req.Port = *flags.Port
	}
	response, err := tracker.Announce{
		TrackerUrl: flags.Tracker,
		Request:    req,
	}.Do()
	if err != nil {
		return fmt.Errorf("doing announce: %w", err)
	}
	spew.Dump(response)
	return nil
}
