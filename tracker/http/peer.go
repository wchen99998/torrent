package httpTracker

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/wchen99998/dht/v2/krpc"
	"github.com/wchen99998/torrent/bencode"
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

// Package-level errors for malformed peer dictionaries. We use these instead of
// fmt.Errorf to avoid allocating a new error string on every malformed entry,
// since a hostile tracker could send many of them in a single response.
var (
	errPeerDictMissingIP    = errors.New("peer dict: missing or invalid \"ip\"")
	errPeerDictInvalidID    = errors.New("peer dict: invalid \"peer id\"")
	errPeerDictMissingPort  = errors.New("peer dict: missing or invalid \"port\"")
	errPeerDictPortOutRange = errors.New("peer dict: \"port\" out of range")
)

// Set from the non-compact form in BEP 3. This keeps the fork's established
// public signature; tracker response parsing uses the error-returning helper
// below so malformed network input is never allowed to panic.
func (p *Peer) FromDictInterface(d map[string]interface{}) {
	if err := p.fromDictInterface(d); err != nil {
		panic(err)
	}
}

func (p *Peer) fromDictInterface(d map[string]interface{}) error {
	ipStr, ok := d["ip"].(string)
	if !ok {
		return errPeerDictMissingIP
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		// Don't let garbage like "not-an-ip" through — a nil IP would cause
		// problems when we actually try to connect to this peer later.
		return errPeerDictMissingIP
	}
	var id []byte
	if rawID, present := d["peer id"]; present {
		peerID, ok := rawID.(string)
		if !ok {
			return errPeerDictInvalidID
		}
		id = []byte(peerID)
	}
	portVal, ok := d["port"].(int64)
	if !ok {
		return errPeerDictMissingPort
	}
	if portVal < 0 || portVal > 0xffff {
		return errPeerDictPortOutRange
	}
	p.IP = ip
	p.ID = id
	p.Port = int(portVal)
	return nil
}

func (p Peer) FromNodeAddr(na krpc.NodeAddr) Peer {
	p.IP = na.IP
	p.Port = na.Port
	return p
}
