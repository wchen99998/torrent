package requestStrategy

import (
	"reflect"
	"testing"
	"unique"

	"github.com/bradfitz/iter"
	"github.com/wchen99998/torrent/metainfo"
)

type testInput struct {
	hash       unique.Handle[metainfo.Hash]
	maxBytes   int64
	pieceLen   int64
	request    bool
	unverified bool
}

func (ti testInput) Torrent(metainfo.Hash) Torrent {
	return testTorrent{input: ti}
}

func (ti testInput) Capacity() (int64, bool) {
	return 0, false
}

func (ti testInput) MaxUnverifiedBytes() int64 {
	return ti.maxBytes
}

type testTorrent struct {
	input testInput
}

func (tt testTorrent) Piece(int) Piece {
	return testPiece{request: tt.input.request, unverified: tt.input.unverified}
}

func (tt testTorrent) PieceLength() int64 {
	return tt.input.pieceLen
}

type testPiece struct {
	request    bool
	unverified bool
}

func (tp testPiece) Request() bool {
	return tp.request
}

func (tp testPiece) CountUnverified() bool {
	return tp.unverified
}

func TestGetRequestablePiecesTreatsZeroAvailabilityAsLast(t *testing.T) {
	hash := unique.Make(metainfo.Hash{})
	pro := NewPieceOrder(NewAjwernerBtree(), 3)
	for _, piece := range []struct {
		index        int
		availability int
	}{
		{0, 0},
		{1, 0},
		{2, 1},
	} {
		pro.Add(PieceRequestOrderKey{InfoHash: hash, Index: piece.index}, PieceRequestOrderState{
			Availability: piece.availability,
			Priority:     1,
		})
	}
	input := testInput{
		hash:     hash,
		maxBytes: 1,
		pieceLen: 1,
		request:  true,
	}
	var got []int
	GetRequestablePieces(input, pro, func(_ metainfo.Hash, pieceIndex int, _ PieceRequestOrderState) bool {
		got = append(got, pieceIndex)
		return true
	})
	if !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("got requestable pieces %v", got)
	}
}

func benchmarkPieceRequestOrder[B Btree](
	b *testing.B,
	// Initialize the next run, and return a Btree
	newBtree func() B,
	// Set any path hinting for the specified piece
	hintForPiece func(index int),
	numPieces int,
) {
	b.ReportAllocs()
	zeroHashHandle := unique.Make(metainfo.Hash{})
	for b.Loop() {
		pro := NewPieceOrder(newBtree(), numPieces)
		state := PieceRequestOrderState{}
		doPieces := func(m func(PieceRequestOrderKey) bool) {
			for i := range iter.N(numPieces) {
				key := PieceRequestOrderKey{
					Index:    i,
					InfoHash: zeroHashHandle,
				}
				hintForPiece(i)
				m(key)
			}
		}
		doPieces(func(key PieceRequestOrderKey) bool {
			return !pro.Add(key, state).Ok
		})
		state.Availability++
		doPieces(func(key PieceRequestOrderKey) bool {
			pro.Update(key, state)
			return true
		})
		pro.tree.Scan(func(item PieceRequestOrderItem) bool {
			return true
		})
		doPieces(func(key PieceRequestOrderKey) bool {
			state.Priority = piecePriority(key.Index / 4)
			pro.Update(key, state)
			return true
		})
		pro.tree.Scan(func(item PieceRequestOrderItem) bool {
			return item.Key.Index < 1000
		})
		state.Priority = 0
		state.Availability++
		doPieces(func(key PieceRequestOrderKey) bool {
			pro.Update(key, state)
			return true
		})
		pro.tree.Scan(func(item PieceRequestOrderItem) bool {
			return item.Key.Index < 1000
		})
		state.Availability--
		doPieces(func(key PieceRequestOrderKey) bool {
			pro.Update(key, state)
			return true
		})
		doPieces(pro.Delete)
		if pro.Len() != 0 {
			b.FailNow()
		}
	}
}

func zero[T any](t *T) {
	var zt T
	*t = zt
}

func BenchmarkPieceRequestOrder(b *testing.B) {
	const numPieces = 2000
	b.Run("TidwallBtree", func(b *testing.B) {
		b.Run("NoPathHints", func(b *testing.B) {
			benchmarkPieceRequestOrder(b, NewTidwallBtree, func(int) {}, numPieces)
		})
		b.Run("SharedPathHint", func(b *testing.B) {
			var pathHint PieceRequestOrderPathHint
			var btree *tidwallBtree
			benchmarkPieceRequestOrder(
				b, func() *tidwallBtree {
					zero(&pathHint)
					btree = NewTidwallBtree()
					btree.PathHint = &pathHint
					return btree
				}, func(int) {}, numPieces,
			)
		})
		b.Run("PathHintPerPiece", func(b *testing.B) {
			pathHints := make([]PieceRequestOrderPathHint, numPieces)
			var btree *tidwallBtree
			benchmarkPieceRequestOrder(
				b, func() *tidwallBtree {
					btree = NewTidwallBtree()
					return btree
				}, func(index int) {
					btree.PathHint = &pathHints[index]
				}, numPieces,
			)
		})
	})
	b.Run("AjwernerBtree", func(b *testing.B) {
		benchmarkPieceRequestOrder(b, NewAjwernerBtree, func(index int) {}, numPieces)
	})
}
