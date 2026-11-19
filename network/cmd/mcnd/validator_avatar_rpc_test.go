package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtbytes "github.com/cometbft/cometbft/libs/bytes"
	"github.com/cometbft/cometbft/p2p"
	rpcclient "github.com/cometbft/cometbft/rpc/client"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/gogoproto/proto"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

// Only these three read methods exist on the test double. The embedded nil
// interface makes any accidental transaction/network operation fail locally.
type avatarSnapshotRPC struct {
	client.CometRPC
	network, blockNetwork      string
	latest                     int64
	blockDelta, responseDelta  int64
	wrongOperator              bool
	queryError                 error
	rows                       map[string]validatortypes.Validator
	avatars                    map[string][]byte
	queryHeights, blockHeights []int64
	storeKeys                  [][]byte
}

func (r *avatarSnapshotRPC) Status(context.Context) (*coretypes.ResultStatus, error) {
	return &coretypes.ResultStatus{NodeInfo: p2p.DefaultNodeInfo{Network: r.network}, SyncInfo: coretypes.SyncInfo{LatestBlockHeight: r.latest}}, nil
}

func (r *avatarSnapshotRPC) Block(_ context.Context, height *int64) (*coretypes.ResultBlock, error) {
	if height == nil {
		return nil, errors.New("snapshot block height must be explicit")
	}
	r.blockHeights = append(r.blockHeights, *height)
	return &coretypes.ResultBlock{BlockID: cmttypes.BlockID{Hash: bytes.Repeat([]byte{0xab}, 32)},
		Block: &cmttypes.Block{Header: cmttypes.Header{ChainID: r.blockNetwork, Height: *height + r.blockDelta}}}, nil
}

func (r *avatarSnapshotRPC) ABCIQueryWithOptions(_ context.Context, path string, data cmtbytes.HexBytes, options rpcclient.ABCIQueryOptions) (*coretypes.ResultABCIQuery, error) {
	r.queryHeights = append(r.queryHeights, options.Height)
	if r.queryError != nil {
		return nil, r.queryError
	}
	var value []byte
	height := options.Height
	switch path {
	case "/decimal.validator.v1.Query/Validator":
		var request validatortypes.QueryValidatorRequest
		if err := proto.Unmarshal(data, &request); err != nil {
			return nil, err
		}
		row, exists := r.rows[request.Validator]
		if !exists {
			return nil, errors.New("unexpected queried operator")
		}
		if r.wrongOperator {
			row.OperatorAddress = "unexpected-operator"
		}
		var err error
		value, err = proto.Marshal(&validatortypes.QueryValidatorResponse{Validator: row})
		if err != nil {
			return nil, err
		}
	case "/store/" + validatortypes.StoreKey + "/key":
		if len(data) != 21 || data[0] != 0x82 {
			return nil, errors.New("unexpected avatar store key")
		}
		r.storeKeys = append(r.storeKeys, bytes.Clone(data))
		value = r.avatars[string(data[1:])]
		height += r.responseDelta
	default:
		return nil, errors.New("export attempted an unexpected RPC route")
	}
	return &coretypes.ResultABCIQuery{Response: abci.ResponseQuery{Height: height, Value: value}}, nil
}

func avatarSnapshotFixture(t *testing.T) ([]byte, *avatarSnapshotRPC) {
	t.Helper()
	raw, _ := avatarTransferFixture(t)
	doc, err := readAvatarGenesis(raw, 990001)
	require.NoError(t, err)
	rpc := &avatarSnapshotRPC{network: doc.chainID, blockNetwork: doc.chainID, latest: 120,
		rows: make(map[string]validatortypes.Validator), avatars: make(map[string][]byte)}
	codec := address.NewBech32Codec(identity.Bech32PrefixValAddr)
	for i, row := range doc.module.Validators {
		rpc.rows[row.OperatorAddress] = row
		key, err := codec.StringToBytes(row.OperatorAddress)
		require.NoError(t, err)
		uri := []string{"ipfs://bafyfixture/first.png", "https://cdn.example.org/second.png"}[i]
		rpc.avatars[string(key)] = []byte(uri)
	}
	return raw, rpc
}

func executeAvatarSnapshotExport(t *testing.T, raw []byte, rpc *avatarSnapshotRPC, height int64) ([]byte, error) {
	t.Helper()
	dir := t.TempDir()
	genesis, output := filepath.Join(dir, "genesis.json"), filepath.Join(dir, "avatars.json")
	require.NoError(t, os.WriteFile(genesis, raw, 0600))
	command := newValidatorControlCmd()
	command.SetContext(context.Background())
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	ctx := client.Context{}.WithClient(rpc).WithHomeDir(dir)
	require.NoError(t, client.SetCmdClientContext(command, ctx))
	args := []string{"export-avatars", "--genesis-file", genesis, "--evm-chain-id", "990001", "--output-file", output}
	if height != 0 {
		args = append(args, "--height", strconv.FormatInt(height, 10))
	}
	command.SetArgs(args)
	err := command.Execute()
	unchanged, readErr := os.ReadFile(genesis)
	require.NoError(t, readErr)
	require.Equal(t, raw, unchanged, "RPC export must never modify the input genesis")
	if err != nil {
		_, statErr := os.Stat(output)
		require.True(t, os.IsNotExist(statErr), "a failed snapshot must not publish a partial artifact")
		return nil, err
	}
	payload, readErr := os.ReadFile(output)
	require.NoError(t, readErr)
	return payload, nil
}

func TestValidatorAvatarRPCExportPinsOneHeightAndDistinctOperators(t *testing.T) {
	for _, selected := range []int64{37, 0} {
		t.Run(strconv.FormatInt(selected, 10), func(t *testing.T) {
			raw, rpc := avatarSnapshotFixture(t)
			payload, err := executeAvatarSnapshotExport(t, raw, rpc, selected)
			require.NoError(t, err)
			var transfer validatorAvatarTransfer
			require.NoError(t, json.Unmarshal(payload, &transfer))
			require.NoError(t, validateAvatarTransfer(transfer))
			wantHeight := selected
			if wantHeight == 0 {
				wantHeight = rpc.latest
			}
			require.Equal(t, wantHeight, transfer.Height)
			require.Equal(t, []int64{wantHeight}, rpc.blockHeights)
			require.Equal(t, []int64{wantHeight, wantHeight, wantHeight, wantHeight}, rpc.queryHeights)
			require.Len(t, rpc.storeKeys, 2)
			require.NotEqual(t, rpc.storeKeys[0], rpc.storeKeys[1])
			require.Len(t, transfer.Operators, 2)
			require.Len(t, transfer.Avatars, 2)
			require.NotEqual(t, transfer.Avatars[0].AvatarURI, transfer.Avatars[1].AvatarURI)
			for _, avatar := range transfer.Avatars {
				key, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).StringToBytes(avatar.OperatorAddress)
				require.NoError(t, err)
				require.Equal(t, string(rpc.avatars[string(key)]), avatar.AvatarURI)
			}
		})
	}
}

func TestValidatorAvatarRPCExportPreservesExplicitAbsentAvatar(t *testing.T) {
	raw, rpc := avatarSnapshotFixture(t)
	for key := range rpc.avatars {
		delete(rpc.avatars, key)
		break
	}
	payload, err := executeAvatarSnapshotExport(t, raw, rpc, 37)
	require.NoError(t, err)
	var transfer validatorAvatarTransfer
	require.NoError(t, json.Unmarshal(payload, &transfer))
	require.Len(t, transfer.Operators, 2, "source absence is still an explicitly included operator")
	require.Len(t, transfer.Avatars, 1)
	require.NoError(t, validateAvatarTransfer(transfer))
}

func TestValidatorAvatarRPCExportFailsClosedBeforePublishing(t *testing.T) {
	for name, modify := range map[string]func(*avatarSnapshotRPC){
		"wrong status chain": func(r *avatarSnapshotRPC) { r.network = "mintcandy_990002-1" },
		"wrong block chain":  func(r *avatarSnapshotRPC) { r.blockNetwork = "mintcandy_990002-1" },
		"wrong block height": func(r *avatarSnapshotRPC) { r.blockDelta = 1 },
		"wrong store height": func(r *avatarSnapshotRPC) { r.responseDelta = 1 },
		"wrong validator":    func(r *avatarSnapshotRPC) { r.wrongOperator = true },
		"RPC error":          func(r *avatarSnapshotRPC) { r.queryError = errors.New("snapshot unavailable") },
		"malformed URI": func(r *avatarSnapshotRPC) {
			for key := range r.avatars {
				r.avatars[key] = []byte("javascript:bad")
			}
		},
		"invalid UTF8": func(r *avatarSnapshotRPC) {
			for key := range r.avatars {
				r.avatars[key] = []byte{0xff}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, rpc := avatarSnapshotFixture(t)
			modify(rpc)
			_, err := executeAvatarSnapshotExport(t, raw, rpc, 37)
			require.Error(t, err)
		})
	}
	for _, height := range []int64{-1, 121} {
		raw, rpc := avatarSnapshotFixture(t)
		_, err := executeAvatarSnapshotExport(t, raw, rpc, height)
		require.Error(t, err)
	}
}
