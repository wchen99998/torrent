package httpTracker

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent/bencode"
)

// TODO: Use netip.Addr and Option[[20]byte].
type Peer struct {
	IP   net.IP `bencode:"ip"`
	Port int    `bencode:"port"`
	ID   []byte `bencode:"peer id"`
}

var _ bencode.Marshaler = Peer{}

func (p Peer) MarshalBencode() ([]byte, error) {
	d := map[string]interface{}{
		"ip":   p.IP.String(),
		"port": p.Port,
	}
	if len(p.ID) != 0 {
		d["peer id"] = string(p.ID)
	}
	return bencode.Marshal(d)
}

func (p Peer) ToNetipAddrPort() (addrPort netip.AddrPort, ok bool) {
	addr, ok := netip.AddrFromSlice(p.IP)
	addrPort = netip.AddrPortFrom(addr, uint16(p.Port))
	return
}

func (p Peer) String() string {
	loc := net.JoinHostPort(p.IP.String(), fmt.Sprintf("%d", p.Port))
	if len(p.ID) != 0 {
		return fmt.Sprintf("%x at %s", p.ID, loc)
	} else {
		return loc
	}
}

// Set from the non-compact form in BEP 3.
func (p *Peer) FromDictInterface(d map[string]interface{}) {
	if err := p.fromDictInterface(d); err != nil {
		panic(err)
	}
}

func (p *Peer) fromDictInterface(d map[string]interface{}) error {
	ipString, ok := d["ip"].(string)
	if !ok {
		return errors.New("missing or invalid peer ip")
	}
	p.IP = net.ParseIP(ipString)
	if p.IP == nil {
		return fmt.Errorf("invalid peer ip %q", ipString)
	}
	if _, ok := d["peer id"]; ok {
		id, ok := d["peer id"].(string)
		if !ok {
			return errors.New("invalid peer id")
		}
		p.ID = []byte(id)
	}
	port, ok := d["port"].(int64)
	if !ok {
		return errors.New("missing or invalid peer port")
	}
	if port < 0 || port > 0xffff {
		return fmt.Errorf("peer port out of range: %d", port)
	}
	p.Port = int(port)
	return nil
}

func (p Peer) FromNodeAddr(na krpc.NodeAddr) Peer {
	p.IP = na.IP
	p.Port = na.Port
	return p
}
