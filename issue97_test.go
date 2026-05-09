package torrent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wchen99998/torrent/internal/testutil"
	"github.com/wchen99998/torrent/metainfo"
	"github.com/wchen99998/torrent/storage"
)

func TestHashPieceAfterStorageClosed(t *testing.T) {
	cl := newTestingClient(t)
	td := t.TempDir()
	cs := storage.NewFile(td)
	defer cs.Close()
	tt := cl.newTorrent(metainfo.Hash{1}, cs)
	mi := testutil.GreetingMetaInfo()
	require.NoError(t, tt.SetInfoBytes(mi.InfoBytes))
	go tt.piece(0).VerifyDataContext(t.Context())
	require.NoError(t, tt.storage.Close())
}
