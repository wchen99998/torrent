package torrent

import (
	"bytes"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-quicktest/qt"

	"github.com/wchen99998/torrent/internal/testutil"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Tests that the client can download a multi-file torrent from two webseeds simultaneously when
// each webseed only has one of the two files (non-overlapping data). When a webseed receives a 404
// for a file it doesn't have, the pieces for that file are removed from its bitmap and the
// scheduler reassigns them to the other webseed.
func TestDownloadFromTwoNonOverlappingWebseeds(t *testing.T) {
	const pieceLen = 2 * defaultChunkSize // 32 KiB; two 16 KiB chunks per piece

	// Two files, each spanning exactly 2 pieces.
	fileLen := 2 * pieceLen
	dataA := make([]byte, fileLen)
	dataB := make([]byte, fileLen)
	rand.Read(dataA)
	rand.Read(dataB)

	tu := testutil.Torrent{
		Name: "testdata",
		Files: []testutil.File{
			{Name: "a.bin", Data: string(dataA)},
			{Name: "b.bin", Data: string(dataB)},
		},
	}
	mi, _ := tu.Generate(int64(pieceLen))

	// Server 1: serves only a.bin; b.bin returns 404 naturally.
	dir1 := t.TempDir()
	qt.Assert(t, qt.IsNil(os.MkdirAll(filepath.Join(dir1, "testdata"), 0o755)))
	qt.Assert(t, qt.IsNil(os.WriteFile(filepath.Join(dir1, "testdata", "a.bin"), dataA, 0o644)))
	srv1 := httptest.NewServer(http.FileServer(http.Dir(dir1)))
	defer srv1.Close()

	// Server 2: serves only b.bin; a.bin returns 404 naturally.
	dir2 := t.TempDir()
	qt.Assert(t, qt.IsNil(os.MkdirAll(filepath.Join(dir2, "testdata"), 0o755)))
	qt.Assert(t, qt.IsNil(os.WriteFile(filepath.Join(dir2, "testdata", "b.bin"), dataB, 0o644)))
	srv2 := httptest.NewServer(http.FileServer(http.Dir(dir2)))
	defer srv2.Close()

	cfg := TestingConfig(t)
	cl, err := NewClient(cfg)
	qt.Assert(t, qt.IsNil(err))
	defer cl.Close()

	// BEP 19 multi-file webseeds use a trailing slash; the file path is appended automatically.
	tt, _, err := cl.AddTorrentSpec(&TorrentSpec{
		AddTorrentOpts: AddTorrentOpts{
			InfoHash:  mi.HashInfoBytes(),
			InfoBytes: mi.InfoBytes,
		},
		Webseeds: []string{srv1.URL + "/", srv2.URL + "/"},
	})
	qt.Assert(t, qt.IsNil(err))

	tt.DownloadAll()
	qt.Assert(t, qt.IsTrue(cl.WaitAll()))

	r := tt.NewReader()
	defer r.Close()
	got, err := io.ReadAll(r)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(got, append(dataA, dataB...)))
}

func TestWebseedHTTPCustomizationFromClientConfig(t *testing.T) {
	data := []byte("webseed http customization from client config")
	srv, headers := newRecordingWebseedServer(data)
	defer srv.Close()

	cfg := TestingConfig(t)
	cfg.HTTPUserAgent = "torrent-webseed-config-test"
	cfg.WebseedRequestHeader = http.Header{
		"X-Webseed-Header": {"client-config"},
	}
	cfg.HttpRequestDirector = func(req *http.Request) error {
		req.Header.Set("X-Webseed-Director", "client-config")
		return nil
	}
	cfg.WebseedHttpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			req.Header.Set("X-Webseed-Http-Client", "client-config")
			return http.DefaultTransport.RoundTrip(req)
		}),
	}

	cl, err := NewClient(cfg)
	qt.Assert(t, qt.IsNil(err))
	defer cl.Close()

	tt, _, err := cl.AddTorrentSpec(webseedHTTPTestSpec(data, srv.URL))
	qt.Assert(t, qt.IsNil(err))
	tt.DownloadAll()
	waitAllOrFatal(t, cl)
	assertTorrentData(t, tt, data)

	header := recordedWebseedHeader(t, headers)
	qt.Assert(t, qt.Equals(header.Get("User-Agent"), "torrent-webseed-config-test"))
	qt.Assert(t, qt.Equals(header.Get("X-Webseed-Header"), "client-config"))
	qt.Assert(t, qt.Equals(header.Get("X-Webseed-Director"), "client-config"))
	qt.Assert(t, qt.Equals(header.Get("X-Webseed-Http-Client"), "client-config"))
}

func TestAddWebSeedsHTTPCustomizationOverridesClientConfig(t *testing.T) {
	data := []byte("webseed http customization from add option")
	srv, headers := newRecordingWebseedServer(data)
	defer srv.Close()

	cfg := TestingConfig(t)
	cfg.HTTPUserAgent = "torrent-webseed-config-test"
	cfg.WebseedRequestHeader = http.Header{
		"X-Webseed-Header": {"client-config"},
	}
	cfg.HttpRequestDirector = func(req *http.Request) error {
		req.Header.Set("X-Webseed-Director", "client-config")
		return nil
	}
	cfg.WebseedHttpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			req.Header.Set("X-Webseed-Http-Client", "client-config")
			return http.DefaultTransport.RoundTrip(req)
		}),
	}

	cl, err := NewClient(cfg)
	qt.Assert(t, qt.IsNil(err))
	defer cl.Close()

	tt, _, err := cl.AddTorrentSpec(webseedHTTPTestSpec(data))
	qt.Assert(t, qt.IsNil(err))
	tt.AddWebSeeds(
		[]string{srv.URL},
		WebSeedUserAgent("torrent-webseed-option-test"),
		WebSeedRequestHeader(http.Header{
			"X-Webseed-Header": {"add-option"},
		}),
		WebSeedHttpRequestDirector(func(req *http.Request) error {
			req.Header.Set("X-Webseed-Director", "add-option")
			return nil
		}),
		WebSeedHttpClient(&http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				req.Header.Set("X-Webseed-Http-Client", "add-option")
				return http.DefaultTransport.RoundTrip(req)
			}),
		}),
	)
	tt.DownloadAll()
	waitAllOrFatal(t, cl)
	assertTorrentData(t, tt, data)

	header := recordedWebseedHeader(t, headers)
	qt.Assert(t, qt.Equals(header.Get("User-Agent"), "torrent-webseed-option-test"))
	qt.Assert(t, qt.Equals(header.Get("X-Webseed-Header"), "add-option"))
	qt.Assert(t, qt.Equals(header.Get("X-Webseed-Director"), "add-option"))
	qt.Assert(t, qt.Equals(header.Get("X-Webseed-Http-Client"), "add-option"))
}

func newRecordingWebseedServer(data []byte) (*httptest.Server, <-chan http.Header) {
	headers := make(chan http.Header, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case headers <- r.Header.Clone():
		default:
		}
		http.ServeContent(w, r, "webseed-http-test", time.Now(), bytes.NewReader(data))
	}))
	return srv, headers
}

func webseedHTTPTestSpec(data []byte, webseeds ...string) *TorrentSpec {
	tu := testutil.Torrent{
		Name: "webseed-http-test",
		Files: []testutil.File{{
			Data: string(data),
		}},
	}
	mi, _ := tu.Generate(int64(len(data)))
	return &TorrentSpec{
		AddTorrentOpts: AddTorrentOpts{
			InfoHash:  mi.HashInfoBytes(),
			InfoBytes: mi.InfoBytes,
		},
		Webseeds: webseeds,
	}
}

func waitAllOrFatal(t *testing.T, cl *Client) {
	t.Helper()
	done := make(chan bool, 1)
	go func() {
		done <- cl.WaitAll()
	}()
	select {
	case ok := <-done:
		qt.Assert(t, qt.IsTrue(ok))
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for torrent download")
	}
}

func assertTorrentData(t *testing.T, tt *Torrent, want []byte) {
	t.Helper()
	r := tt.NewReader()
	defer r.Close()
	got, err := io.ReadAll(r)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(got, want))
}

func recordedWebseedHeader(t *testing.T, headers <-chan http.Header) http.Header {
	t.Helper()
	select {
	case header := <-headers:
		return header
	default:
		t.Fatal("no webseed request was recorded")
		return nil
	}
}
