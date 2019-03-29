// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package audit_test

import (
	"crypto/rand"
	"math"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/golang/protobuf/ptypes/timestamp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"storj.io/storj/internal/testcontext"
	"storj.io/storj/internal/testplanet"
	"storj.io/storj/internal/teststorj"
	"storj.io/storj/pkg/audit"
	"storj.io/storj/pkg/pb"
	"storj.io/storj/pkg/pointerdb"
	"storj.io/storj/pkg/storage/meta"
	"storj.io/storj/pkg/storj"
)

func TestAuditSegment(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 4, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		type pathCount struct {
			path  storj.Path
			count int
		}

		// note: to simulate better,
		// change limit in library to 5 in
		// list api call, default is  0 == 1000 listing
		//populate pointerdb with 10 non-expired pointers of test data
		tests, cursor, pointerdb := populateTestData(t, planet, &timestamp.Timestamp{Seconds: time.Now().Unix() + 3000})

		t.Run("NextStripe", func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.bm, func(t *testing.T) {
					assert1 := assert.New(t)
					stripe, err := cursor.NextStripe(ctx)
					if err != nil {
						assert1.Error(err)
						assert1.Nil(stripe)
					}
					if stripe != nil {
						assert1.Nil(err)
					}
				})
			}
		})

		// test to see how random paths are
		t.Run("probabilisticTest", func(t *testing.T) {
			list, _, err := pointerdb.List("", "", "", true, 10, meta.None)
			require.NoError(t, err)
			require.Len(t, list, 10)

			// get count of items picked at random
			uniquePathCounted := []pathCount{}
			pathCounter := []pathCount{}

			// get a list of 100 paths generated from random
			for i := 0; i < 100; i++ {
				randomNum, err := rand.Int(rand.Reader, big.NewInt(int64(len(list))))
				if err != nil {
					t.Error("num error: failed to get num")
				}
				pointerItem := list[randomNum.Int64()]
				path := pointerItem.Path
				val := pathCount{path: path, count: 1}
				pathCounter = append(pathCounter, val)
			}

			// get a count for paths in list
			for _, pc := range pathCounter {
				skip := false
				for i, up := range uniquePathCounted {
					if reflect.DeepEqual(pc.path, up.path) {
						up.count = up.count + 1
						uniquePathCounted[i] = up
						skip = true
						break
					}
				}
				if !skip {
					uniquePathCounted = append(uniquePathCounted, pc)
				}
			}

			// Section: binomial test for randomness
			n := float64(100) // events
			p := float64(.10) // theoretical probability of getting  1/10 paths
			m := n * p
			s := math.Sqrt(m * (1 - p)) // binomial distribution

			// if values fall outside of the critical values of test statistics (ie Z value)
			// in a 2-tail test
			// we can assume, 95% confidence, it's not sampling according to a 10% probability
			for _, v := range uniquePathCounted {
				z := (float64(v.count) - m) / s
				if z <= -1.96 || z >= 1.96 {
					t.Log(false)
				} else {
					t.Log(true)
				}
			}
		})
	})
}

func makePutRequest(path storj.Path, expiration *timestamp.Timestamp) pb.PutRequest {
	var rps []*pb.RemotePiece
	rps = append(rps, &pb.RemotePiece{
		PieceNum: 1,
		NodeId:   teststorj.NodeIDFromString("testId"),
	})
	pr := pb.PutRequest{
		Path: path,
		Pointer: &pb.Pointer{
			ExpirationDate: expiration,
			Type:           pb.Pointer_REMOTE,
			Remote: &pb.RemoteSegment{
				Redundancy: &pb.RedundancyScheme{
					Type:             pb.RedundancyScheme_RS,
					MinReq:           1,
					Total:            3,
					RepairThreshold:  2,
					SuccessThreshold: 3,
					ErasureShareSize: 2,
				},
				RootPieceId:  teststorj.PieceIDFromString("testId"),
				RemotePieces: rps,
			},
			SegmentSize: int64(10),
		},
	}
	return pr
}

func TestDeleteExpired(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 4, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		//populate pointerdb with 10 expired pointers of test data
		tests, cursor, pointerdb := populateTestData(t, planet, &timestamp.Timestamp{})
		//make sure it they're in there
		list, _, err := pointerdb.List("", "", "", true, 10, meta.None)
		require.NoError(t, err)
		require.Len(t, list, 10)
		// make sure its all null and no errors
		t.Run("NextStripe", func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.bm, func(t *testing.T) {
					stripe, err := cursor.NextStripe(ctx)
					require.NoError(t, err)
					require.Nil(t, stripe)
				})
			}
		})
		//make sure it they're not in there anymore
		list, _, err = pointerdb.List("", "", "", true, 10, meta.None)
		require.NoError(t, err)
		require.Len(t, list, 0)
	})
}

type testData struct {
	bm   string
	path storj.Path
}

func populateTestData(t *testing.T, planet *testplanet.Planet, expiration *timestamp.Timestamp) ([]testData, *audit.Cursor, *pointerdb.Service) {
	tests := []testData{
		{bm: "success-1", path: "folder1/file1"},
		{bm: "success-2", path: "foodFolder1/file1/file2"},
		{bm: "success-3", path: "foodFolder1/file1/file2/foodFolder2/file3"},
		{bm: "success-4", path: "projectFolder/project1.txt/"},
		{bm: "success-5", path: "newProjectFolder/project2.txt"},
		{bm: "success-6", path: "Pictures/image1.png"},
		{bm: "success-7", path: "Pictures/Nature/mountains.png"},
		{bm: "success-8", path: "Pictures/City/streets.png"},
		{bm: "success-9", path: "Pictures/Animals/Dogs/dogs.png"},
		{bm: "success-10", path: "Nada/ビデオ/😶"},
	}
	pointerdb := planet.Satellites[0].Metainfo.Service
	cursor := audit.NewCursor(pointerdb)

	// put 10 pointers in db with expirations
	t.Run("putToDB", func(t *testing.T) {
		for _, tt := range tests {
			t.Run(tt.bm, func(t *testing.T) {
				putRequest := makePutRequest(tt.path, expiration)
				require.NoError(t, pointerdb.Put(tt.path, putRequest.Pointer))
			})
		}
	})
	return tests, cursor, pointerdb
}
