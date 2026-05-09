package torrent

import (
	"net"
	"testing"
)

func FuzzBep40PrioritySymmetric(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4}, []byte{5, 6, 7, 8}, uint16(6881), uint16(51413))
	f.Add([]byte{0x20, 0x01, 0x0d, 0xb8}, []byte{0x20, 0x01, 0x0d, 0xb8, 1}, uint16(1), uint16(2))
	f.Fuzz(func(t *testing.T, aBytes, bBytes []byte, aPort, bPort uint16) {
		a := IpPort{IP: fuzzBep40IP(aBytes), Port: aPort}
		b := IpPort{IP: fuzzBep40IP(bBytes), Port: bPort}
		ab, abErr := bep40Priority(a, b)
		ba, baErr := bep40Priority(b, a)
		if (abErr != nil) != (baErr != nil) {
			t.Fatalf("priority error was not symmetric: %v vs %v", abErr, baErr)
		}
		if abErr == nil && ab != ba {
			t.Fatalf("priority was not symmetric: %08x vs %08x for %v and %v", ab, ba, a, b)
		}
	})
}

func fuzzBep40IP(b []byte) net.IP {
	if len(b)%2 == 0 {
		ip := make(net.IP, 4)
		copy(ip, b)
		return ip
	}
	ip := make(net.IP, 16)
	copy(ip, b)
	return ip
}
