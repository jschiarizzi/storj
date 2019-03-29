// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package audit

import (
	"context"
	"crypto/rand"
	"math/big"
	"sync"
	"time"

	"github.com/golang/protobuf/ptypes"

	"storj.io/storj/pkg/eestream"
	"storj.io/storj/pkg/pb"
	"storj.io/storj/pkg/pointerdb"
	"storj.io/storj/pkg/storage/meta"
	"storj.io/storj/pkg/storj"
)

const (
	maxListAttempts = 4
	maxGetAttempts  = 4
)

// Stripe keeps track of a stripe's index and its parent segment
type Stripe struct {
	Index       int64
	Segment     *pb.Pointer
	SegmentPath storj.Path
}

// Cursor keeps track of audit location in pointer db
type Cursor struct {
	pointerdb *pointerdb.Service
	lastPath  storj.Path
	mutex     sync.Mutex
}

// NewCursor creates a Cursor which iterates over pointer db
func NewCursor(pointerdb *pointerdb.Service) *Cursor {
	return &Cursor{
		pointerdb: pointerdb,
	}
}

// NextStripe returns a random stripe to be audited
func (cursor *Cursor) NextStripe(ctx context.Context) (stripe *Stripe, err error) {
	cursor.mutex.Lock()
	defer cursor.mutex.Unlock()

	var pointerItems []*pb.ListResponse_Item
	var path storj.Path
	var more bool

	var attempts = 0
	for attempts < maxListAttempts {
		pointerItems, more, err = cursor.pointerdb.List("", cursor.lastPath, "", true, 0, meta.None)
		if err != nil {
			return nil, err
		}
		if len(pointerItems) == 0 {
			attempts++
		} else {
			break
		}
	}

	if len(pointerItems) == 0 {
		return nil, Error.New("unable to find pointers after %d attempts", maxListAttempts)
	}

	pointer, err := cursor.getRandomValidPointer(pointerItems, more, maxGetAttempts)
	if err != nil {
		return nil, err
	}

	index, err := getRandomStripe(pointer)
	if err != nil {
		return nil, err
	}

	return &Stripe{
		Index:       index,
		Segment:     pointer,
		SegmentPath: path,
	}, nil
}

func (cursor *Cursor) getRandomValidPointer(pointerItems []*pb.ListResponse_Item, more bool, retryAttempts int) (pointer *pb.Pointer, err error) {
	for i := 0; i < retryAttempts; i++ {
		pointerItem, err := getRandomPointer(pointerItems)
		if err != nil {
			return nil, err
		}

		// keep track of last path listed
		if !more {
			cursor.lastPath = ""
		} else {
			cursor.lastPath = pointerItems[len(pointerItems)-1].Path
		}

		pointer, err = cursor.pointerdb.Get(pointerItem.Path)
		if err != nil {
			return nil, err
		}

		//delete expired items rather than auditing them
		if expiration := pointer.GetExpirationDate(); expiration != nil {
			t, err := ptypes.Timestamp(expiration)
			if err != nil {
				return nil, err
			}
			if t.Before(time.Now()) {
				return nil, cursor.pointerdb.Delete(pointerItem.Path)
			}
		}

		if pointer.GetType() != pb.Pointer_REMOTE || pointer.GetSegmentSize() == 0 {
			continue
		}

		return pointer, nil
	}

	return nil, Error.New("could not find valid pointer after %d attempts", retryAttempts)
}

func getRandomStripe(pointer *pb.Pointer) (index int64, err error) {
	redundancy, err := eestream.NewRedundancyStrategyFromProto(pointer.GetRemote().GetRedundancy())
	if err != nil {
		return 0, err
	}

	// the last segment could be smaller than stripe size
	if pointer.GetSegmentSize() < int64(redundancy.StripeSize()) {
		return 0, nil
	}

	randomStripeIndex, err := rand.Int(rand.Reader, big.NewInt(pointer.GetSegmentSize()/int64(redundancy.StripeSize())))
	if err != nil {
		return -1, err
	}

	return randomStripeIndex.Int64(), nil
}

func getRandomPointer(pointerItems []*pb.ListResponse_Item) (pointer *pb.ListResponse_Item, err error) {
	randomNum, err := rand.Int(rand.Reader, big.NewInt(int64(len(pointerItems))))
	if err != nil {
		return &pb.ListResponse_Item{}, err
	}

	return pointerItems[randomNum.Int64()], nil
}
