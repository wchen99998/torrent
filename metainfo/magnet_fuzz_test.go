package metainfo

import "testing"

func FuzzParseMagnetUriRoundTrip(f *testing.F) {
	for _, seed := range []string{
		exampleMagnetURI,
		exampleMagnet.String(),
		"magnet:?xt=urn:btih:51340689c960f0778a4387aef9b4b52fd08390cd&dn=one&dn=two",
		"magnet:?xt=urn:btih:ZOCMZQIPFFW7OLLMIC5HUB6BPCSDEOQU&xt=urn:sha1:YNCKHTQCWBTRNJIV4WNAE52SJUQCZO5C&tr=udp%3A%2F%2Ftracker.example%3A6969",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, uri string) {
		m, err := ParseMagnetUri(uri)
		if err != nil {
			t.Skip(err)
		}
		canonical := m.String()
		m2, err := ParseMagnetUri(canonical)
		if err != nil {
			t.Fatalf("canonical magnet failed to parse: %q: %v", canonical, err)
		}
		if got := m2.String(); got != canonical {
			t.Fatalf("magnet string not stable after parse: got %q, want %q", got, canonical)
		}
	})
}

func FuzzParseMagnetV2UriRoundTrip(f *testing.F) {
	for _, seed := range []string{
		"magnet:",
		"magnet:?dn=one&dn=two",
		"magnet:?xt=urn:btmh:1220caf1e1c30e81cb361b9ee167c4aa64228a7fa4fa9f6105232b28ad099f3a302e&dn=bittorrent-v2-test",
		"magnet:?xt=urn:btih:631a31dd0a46257d5078c0dee4e66e26f73e42ac&xt=urn:btmh:1220d8dd32ac93357c368556af3ac1d95c9d76bd0dff6fa9833ecdac3d53134efabb&dn=hybrid&dn=backup",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, uri string) {
		m, err := ParseMagnetV2Uri(uri)
		if err != nil {
			t.Skip(err)
		}
		canonical := m.String()
		m2, err := ParseMagnetV2Uri(canonical)
		if err != nil {
			t.Fatalf("canonical magnet failed to parse: %q: %v", canonical, err)
		}
		if got := m2.String(); got != canonical {
			t.Fatalf("magnet string not stable after parse: got %q, want %q", got, canonical)
		}
	})
}

func FuzzNodeUnmarshalBencode(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte("9:127.0.0.1"),
		[]byte("l9:localhosti6881ee"),
		[]byte("li1eee"),
		[]byte("d1:a1:be"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		var n Node
		_ = n.UnmarshalBencode(b)
	})
}
