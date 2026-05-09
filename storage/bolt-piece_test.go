package storage_test

import (
	"testing"

	"github.com/wchen99998/torrent/storage"
	"github.com/wchen99998/torrent/test"
)

func TestBoltLeecherStorage(t *testing.T) {
	test.TestLeecherStorage(t, test.LeecherStorageTestCase{Name: "Boltdb", Factory: storage.NewBoltDB, GoMaxProcs: 0})
}
