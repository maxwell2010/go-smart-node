// Package receipts prepares native validator projection for the shared execution
// coordinator. It installs no financial routes or EVM hooks in the application.
package receipts

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/big"
	"reflect"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	nftreceipts "github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

const maxCaptureMoves = 1024
const maxCaptureReceiptBytes = 1024 * 1024

// StakeSourceProfile must describe the currently independently approved runtime,
// registry roles and domain. Fingerprint binds that immutable role/code scope;
// NFT ownership, lineage and monetary backing are separate attestations.
type StakeSourceProfile struct {
	Domain      types.EVMSourceDomain
	Gateway     common.Address
	Fingerprint common.Hash
}
type StakeSourceAuthenticator interface {
	AuthenticateStakeSource(sdk.Context, nftreceipts.Execution) (StakeSourceProfile, error)
}
type BindingState interface {
	GetEVMSourceDomain(sdk.Context) (types.EVMSourceDomain, error)
	LookupCustodyNFTBinding(sdk.Context, []byte, []byte) (nativekeeper.BindingAssignment, error)
	CaptureBindingState(sdk.Context, []nativekeeper.BindingReference) ([]byte, error)
	ValidateBindingCapture(sdk.Context, []byte) error
}

func nilCaptureDependency(value interface{}) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Func, reflect.Slice, reflect.Chan:
		return v.IsNil()
	}
	return false
}

// StakeCaptureSession is created once per execution/plan. There is no global
// hash-to-session map or permanent processed-log ledger. Capture is one-shot;
// validation is read-only and repeatable before native stages begin writing.
type StakeCaptureSession struct {
	bindings BindingState
	auth     StakeSourceAuthenticator
	state    *stakeCaptureState
}
type stakeCaptureState struct {
	execution   nftreceipts.Execution
	noop        bool
	profile     StakeSourceProfile
	token       []byte
	fingerprint common.Hash
	selections  map[nftreceipts.MoveRef][]uint32
}

var _ nftreceipts.StakeCapture = (*StakeCaptureSession)(nil)

func NewStakeCaptureSession(bindings BindingState, auth StakeSourceAuthenticator) (*StakeCaptureSession, error) {
	if nilCaptureDependency(bindings) || nilCaptureDependency(auth) {
		return nil, errors.New("stake capture requires native bindings and an approved source authenticator")
	}
	return &StakeCaptureSession{bindings: bindings, auth: auth}, nil
}
func sourceProfileValid(profile StakeSourceProfile, domain types.EVMSourceDomain) bool {
	return profile.Gateway != (common.Address{}) && profile.Fingerprint != (common.Hash{}) && profile.Domain.Validate() == nil && types.SameEVMSourceDomain(&profile.Domain, &domain)
}
func captureFrame(out, value []byte) []byte {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(value)))
	out = append(out, n[:]...)
	return append(out, value...)
}
func captureRefKey(ref nativekeeper.BindingReference) (string, error) {
	if ref.Frozen {
		key, err := types.EVMFrozenBindingKey(ref.Gateway, ref.ID)
		return string(append([]byte{'f'}, key...)), err
	}
	key, err := types.EVMStakeBindingKey(ref.Kind, ref.Gateway, ref.ID)
	return string(append([]byte{'s'}, key...)), err
}
func copyCaptureSelections(source map[nftreceipts.MoveRef][]uint32) map[nftreceipts.MoveRef][]uint32 {
	result := make(map[nftreceipts.MoveRef][]uint32, len(source))
	for key, ids := range source {
		result[key] = append([]uint32(nil), ids...)
	}
	return result
}
func captureFingerprint(token []byte, profile StakeSourceProfile, receiptDigest common.Hash, selections map[nftreceipts.MoveRef][]uint32) common.Hash {
	out := captureFrame([]byte("native-stake-selection-v1"), token)
	out = append(out, profile.Gateway.Bytes()...)
	out = append(out, profile.Fingerprint.Bytes()...)
	out = append(out, receiptDigest.Bytes()...)
	keys := make([]nftreceipts.MoveRef, 0, len(selections))
	for key := range selections {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].LogPosition == keys[j].LogPosition {
			return keys[i].ItemPosition < keys[j].ItemPosition
		}
		return keys[i].LogPosition < keys[j].LogPosition
	})
	var word [4]byte
	for _, key := range keys {
		binary.BigEndian.PutUint32(word[:], key.LogPosition)
		out = append(out, word[:]...)
		binary.BigEndian.PutUint32(word[:], key.ItemPosition)
		out = append(out, word[:]...)
		binary.BigEndian.PutUint32(word[:], uint32(len(selections[key])))
		out = append(out, word[:]...)
		for _, id := range selections[key] {
			binary.BigEndian.PutUint32(word[:], id)
			out = append(out, word[:]...)
		}
	}
	sum := sha256.Sum256(out)
	return common.Hash(sum)
}
func copyCaptureExecution(input nftreceipts.Execution) (nftreceipts.Execution, error) {
	if err := input.EntryPoint.Validate(); err != nil {
		return nftreceipts.Execution{}, err
	}
	if input.Receipt == nil || input.Receipt.Status != 1 || len(input.Receipt.Logs) > maxCaptureMoves {
		return nftreceipts.Execution{}, errors.New("stake capture requires a bounded successful receipt")
	}
	size := 0
	copy := input
	receipt := *input.Receipt
	copy.Receipt = &receipt
	if len(receipt.PostState) > 32 {
		return nftreceipts.Execution{}, errors.New("stake capture poststate exceeds bound")
	}
	receipt.PostState = bytes.Clone(receipt.PostState)
	if receipt.EffectiveGasPrice != nil {
		if receipt.EffectiveGasPrice.Sign() < 0 || receipt.EffectiveGasPrice.BitLen() > 256 {
			return nftreceipts.Execution{}, errors.New("invalid stake capture effective gas price")
		}
		receipt.EffectiveGasPrice = new(big.Int).Set(receipt.EffectiveGasPrice)
	}
	if receipt.BlobGasPrice != nil {
		if receipt.BlobGasPrice.Sign() < 0 || receipt.BlobGasPrice.BitLen() > 256 {
			return nftreceipts.Execution{}, errors.New("invalid stake capture blob gas price")
		}
		receipt.BlobGasPrice = new(big.Int).Set(receipt.BlobGasPrice)
	}
	receipt.Logs = make([]*ethtypes.Log, len(input.Receipt.Logs))
	for i, log := range input.Receipt.Logs {
		if log == nil || log.Removed || len(log.Topics) > 4 {
			return nftreceipts.Execution{}, errors.New("invalid stake capture log")
		}
		if len(log.Data) > maxCaptureReceiptBytes-size {
			return nftreceipts.Execution{}, errors.New("stake capture receipt exceeds byte limit")
		}
		size += len(log.Data)
		if len(log.Topics)*32 > maxCaptureReceiptBytes-size {
			return nftreceipts.Execution{}, errors.New("stake capture topics exceed byte limit")
		}
		size += len(log.Topics) * 32
		detached := *log
		detached.Topics = append([]common.Hash(nil), log.Topics...)
		detached.Data = bytes.Clone(log.Data)
		receipt.Logs[i] = &detached
	}
	if receipt.BlockNumber != nil {
		if receipt.BlockNumber.Sign() < 0 || receipt.BlockNumber.BitLen() > 256 {
			return nftreceipts.Execution{}, errors.New("invalid stake capture receipt height")
		}
		receipt.BlockNumber = new(big.Int).Set(receipt.BlockNumber)
	}
	return copy, nil
}

// Capture consumes transfers already authenticated and decoded by the NFT
// planner. It selects only persisted old escrow custody IDs. New origin incoming
// allocation, the typed deposit-to-burn relation and newly minted receipt lineage
// are owned exclusively by the NFT financial planner, never recreated here.
func (s *StakeCaptureSession) Capture(ctx sdk.Context, execution nftreceipts.Execution, moves []nftreceipts.Transfer) (nftreceipts.CapturedStakes, error) {
	if s == nil || nilCaptureDependency(s.bindings) || nilCaptureDependency(s.auth) || s.state != nil {
		return nftreceipts.CapturedStakes{}, errors.New("stake capture session is missing or already captured")
	}
	if len(moves) > maxCaptureMoves {
		return nftreceipts.CapturedStakes{}, errors.New("stake capture transfer count exceeds bound")
	}
	detached, err := copyCaptureExecution(execution)
	if err != nil {
		return nftreceipts.CapturedStakes{}, err
	}
	// No decoded NFT movement needs no old custody selection. Other native
	// components still authenticate any coin/validator events independently.
	if len(moves) == 0 {
		s.state = &stakeCaptureState{execution: detached, noop: true}
		return nftreceipts.CapturedStakes{Selections: map[nftreceipts.MoveRef][]uint32{}}, nil
	}
	encodedReceipt, err := detached.Receipt.MarshalBinary()
	if err != nil {
		return nftreceipts.CapturedStakes{}, err
	}
	receiptSum := sha256.Sum256(encodedReceipt)
	profile, err := s.auth.AuthenticateStakeSource(ctx, detached)
	if err != nil {
		return nftreceipts.CapturedStakes{}, err
	}
	domain, err := s.bindings.GetEVMSourceDomain(ctx)
	if err != nil {
		return nftreceipts.CapturedStakes{}, err
	}
	if !sourceProfileValid(profile, domain) {
		return nftreceipts.CapturedStakes{}, errors.New("stake capture source profile/domain is not approved")
	}
	selections := map[nftreceipts.MoveRef][]uint32{}
	references := map[string]nativekeeper.BindingReference{}
	seen := map[nftreceipts.MoveRef]bool{}
	for _, move := range moves {
		if seen[move.Ref] || int(move.Ref.LogPosition) >= len(detached.Receipt.Logs) {
			return nftreceipts.CapturedStakes{}, errors.New("duplicate or out-of-receipt native NFT movement")
		}
		seen[move.Ref] = true
		if detached.Receipt.Logs[move.Ref.LogPosition].Address != move.Asset.Contract {
			return nftreceipts.CapturedStakes{}, errors.New("NFT movement contract differs from receipt position")
		}
		if move.From != profile.Gateway || move.NFTType != nfttypes.NftType_NFT721 {
			continue
		}
		assignment, err := s.bindings.LookupCustodyNFTBinding(ctx, move.Asset.Contract.Bytes(), move.Asset.TokenID.Bytes())
		if errors.Is(err, nativekeeper.ErrNotFound) {
			continue
		} // New/transient admission stays NFT-planner-owned; no lowest-ID fallback.
		if err != nil {
			return nftreceipts.CapturedStakes{}, err
		}
		var position *types.NFTPositionIdentity
		var gateway []byte
		if assignment.Stake != nil {
			position = assignment.Stake.NFTPosition
			gateway = assignment.Stake.Gateway
		} else if assignment.Frozen != nil {
			position = assignment.Frozen.NFTPosition
			gateway = assignment.Frozen.Gateway
		}
		if position == nil || !bytes.Equal(gateway, profile.Gateway.Bytes()) || !bytes.Equal(position.Custody.Contract, move.Asset.Contract.Bytes()) || !bytes.Equal(position.Custody.TokenID, move.Asset.TokenID.Bytes()) || len(position.Custody.NativeSubTokenIDs) != int(move.Quantity) {
			return nftreceipts.CapturedStakes{}, errors.New("escrow transfer differs from persisted exact custody units")
		}
		ref, err := assignment.Reference()
		if err != nil {
			return nftreceipts.CapturedStakes{}, err
		}
		key, err := captureRefKey(ref)
		if err != nil {
			return nftreceipts.CapturedStakes{}, err
		}
		references[key] = ref
		if len(references) > types.MaxBindingBatchSize {
			return nftreceipts.CapturedStakes{}, errors.New("stake capture source reference count exceeds bound")
		}
		selections[move.Ref] = append([]uint32(nil), position.Custody.NativeSubTokenIDs...)
	}
	keys := make([]string, 0, len(references))
	for key := range references {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	refs := make([]nativekeeper.BindingReference, 0, len(keys))
	for _, key := range keys {
		refs = append(refs, references[key])
	}
	token, err := s.bindings.CaptureBindingState(ctx, refs)
	if err != nil {
		return nftreceipts.CapturedStakes{}, err
	}
	fingerprint := captureFingerprint(token, profile, common.Hash(receiptSum), selections)
	s.state = &stakeCaptureState{execution: detached, profile: profile, token: bytes.Clone(token), fingerprint: fingerprint, selections: copyCaptureSelections(selections)}
	return nftreceipts.CapturedStakes{Selections: copyCaptureSelections(selections), PrestateFingerprint: fingerprint}, nil
}
func (s *StakeCaptureSession) ValidateCapture(ctx sdk.Context, capture nftreceipts.CapturedStakes) error {
	if s == nil || s.state == nil {
		return errors.New("native stake capture was not prepared")
	}
	state := s.state
	if state.noop {
		if capture.PrestateFingerprint != (common.Hash{}) || len(capture.Selections) != 0 {
			return errors.New("noop stake capture gained selections")
		}
		return nil
	}
	if capture.PrestateFingerprint != state.fingerprint || len(capture.Selections) != len(state.selections) {
		return errors.New("native stake capture result changed")
	}
	for key, want := range state.selections {
		got, ok := capture.Selections[key]
		if !ok || len(got) != len(want) {
			return errors.New("native stake capture units changed")
		}
		for i := range want {
			if got[i] != want[i] {
				return errors.New("native stake capture units changed")
			}
		}
	}
	profile, err := s.auth.AuthenticateStakeSource(ctx, state.execution)
	if err != nil {
		return err
	}
	if profile != state.profile {
		return errors.New("approved native stake source changed")
	}
	return s.bindings.ValidateBindingCapture(ctx, state.token)
}
