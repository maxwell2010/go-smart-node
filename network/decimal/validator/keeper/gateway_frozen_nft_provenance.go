package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const gatewayFrozenNFTProvenancePrefix byte = 0x5f

func gatewayFrozenNFTProvenanceKey(binding *types.EVMFrozenBinding) []byte {
	return append(append([]byte{gatewayFrozenNFTProvenancePrefix}, binding.Gateway...), binding.FrozenIndex...)
}

func (k *StateKeeper) recordGatewayFrozenNFTProvenance(ctx sdk.Context, binding *types.EVMFrozenBinding, entryHeight int64) error {
	enabled, err := k.NativeStakeProvenanceEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	if entryHeight <= 0 || entryHeight > binding.CreatedHeight {
		return errors.New("invalid NFT stake entry height for the frozen withdrawal")
	}
	state := k.service.OpenKVStore(ctx)
	key := gatewayFrozenNFTProvenanceKey(binding)
	exists, err := state.Has(key)
	if err != nil {
		return err
	}
	if exists {
		return errors.New("frozen withdrawal already has NFT stake provenance")
	}
	data := binary.BigEndian.AppendUint64(nil, uint64(entryHeight))
	return state.Set(key, append(data, binding.SourceStakeID...))
}

// GatewayFrozenNFTStakeEntryHeight returns the original staking height for an
// exact pending native NFT withdrawal with fresh-history provenance.
func (k *StateKeeper) GatewayFrozenNFTStakeEntryHeight(ctx sdk.Context, expected types.EVMFrozenBinding) (int64, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return 0, err
	}
	enabled, err := k.NativeStakeProvenanceEnabled(ctx)
	if err != nil {
		return 0, err
	}
	if !enabled {
		return 0, ErrNotFound
	}
	preimage, err := recordFromFrozen(&expected)
	if err != nil {
		return 0, err
	}
	if !expected.Active || expected.SourceKind != gatewayNFTBinding || expected.FreezeType != 1 ||
		expected.NativeTarget.StakeType != types.StakeType_NFT || expected.ReserveAsset.ReserveKind != nfttypes.IsolatedReserveDEL ||
		expected.ReserveAsset.NativeDenom != identity.BaseDenom || expected.NFTPosition.Mode != types.NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT ||
		expected.UpdatedHeight > ctx.BlockHeight() {
		return 0, errors.New("NFT stake provenance requires an active isolated DEL withdrawal")
	}
	current, err := k.readBindingRow(ctx, preimage.key)
	if err != nil {
		return 0, err
	}
	if !bytes.Equal(current.data, preimage.data) {
		return 0, errors.New("frozen NFT binding differs from the expected provenance preimage")
	}
	data, err := k.service.OpenKVStore(ctx).Get(gatewayFrozenNFTProvenanceKey(&expected))
	if err != nil {
		return 0, err
	}
	if data == nil {
		return 0, ErrNotFound
	}
	if len(data) != 8+common.HashLength || !bytes.Equal(data[8:], expected.SourceStakeID) {
		return 0, errors.New("invalid frozen NFT stake provenance record")
	}
	height := int64(binary.BigEndian.Uint64(data[:8]))
	if height <= 0 || height > expected.CreatedHeight {
		return 0, errors.New("invalid frozen NFT stake provenance height")
	}
	_, pending, i, err := k.matchWithdrawalEntry(ctx, current)
	if err != nil {
		return 0, err
	}
	entry := pending.Entries[i]
	if entry.CreationHeight != expected.CreatedHeight || len(entry.Stake.Holds) != 0 ||
		!entry.Stake.Stake.Amount.Equal(expected.ReportedReserveAmount) {
		return 0, errors.New("native undelegation differs from the frozen NFT stake provenance")
	}
	return height, nil
}
