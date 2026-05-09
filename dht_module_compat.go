package torrent

import (
	"net"

	dhtIpList "github.com/anacrolix/torrent/iplist"
	dhtMetainfo "github.com/anacrolix/torrent/metainfo"
	"github.com/wchen99998/torrent/iplist"
	"github.com/wchen99998/torrent/metainfo"
)

type dhtIPBlocklist struct {
	iplist.Ranger
}

func (me dhtIPBlocklist) Lookup(ip net.IP) (dhtIpList.Range, bool) {
	r, ok := me.Ranger.Lookup(ip)
	return dhtIpList.Range(r), ok
}

func dhtIPBlocklistFrom(r iplist.Ranger) dhtIpList.Ranger {
	if r == nil {
		return nil
	}
	return dhtIPBlocklist{r}
}

func dhtAnnouncePeerCallback(
	f func(metainfo.Hash, net.IP, int, bool),
) func(dhtMetainfo.Hash, net.IP, int, bool) {
	if f == nil {
		return nil
	}
	return func(ih dhtMetainfo.Hash, ip net.IP, port int, portOk bool) {
		f(metainfo.Hash(ih), ip, port, portOk)
	}
}
