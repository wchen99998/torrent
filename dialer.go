package torrent

import (
	"github.com/wchen99998/torrent/dialer"
)

type (
	Dialer        = dialer.T
	NetworkDialer = dialer.WithNetwork
)

var DefaultNetDialer = &dialer.Default
