package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

const (
	gatewayFrozenNFTProvenancePrefix  byte = 0x5f
	gatewayFrozenNFTProvenanceVersion byte = 1
	frozenNFTStakeProvenanceSize           = 8 + common.HashLength
	frozenNFTCreationSize                  = 1 + 8 + delProvenanceTimeSize
)

type gatewayFrozenNFTProvenance struct {
	stakeEntryHeight int64
	creation         CommittedEvidenceBlock
}

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
	if binding.CreatedHeight != ctx.BlockHeight() || ctx.BlockTime().Unix() <= 0 {
		return errors.New("invalid canonical block for the frozen NFT withdrawal")
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
	data := make([]byte, frozenNFTStakeProvenanceSize+frozenNFTCreationSize)
	binary.BigEndian.PutUint64(data[:8], uint64(entryHeight))
	copy(data[8:frozenNFTStakeProvenanceSize], binding.SourceStakeID)
	creation := data[frozenNFTStakeProvenanceSize:]
	creation[0] = gatewayFrozenNFTProvenanceVersion
	binary.BigEndian.PutUint64(creation[1:9], uint64(binding.CreatedHeight))
	writeDELProvenanceTime(creation[9:], ctx.BlockTime())
	return state.Set(key, data)
}

// GatewayFrozenNFTStakeEntryHeight returns the original staking height for an
// exact pending native NFT withdrawal with fresh-history provenance.
func (k *StateKeeper) GatewayFrozenNFTStakeEntryHeight(ctx sdk.Context, expected types.EVMFrozenBinding) (int64, error) {
	if expected.SourceKind != gatewayNFTBinding {
		return 0, errors.New("NFT stake provenance requires an active isolated DEL withdrawal")
	}
	if _, err := k.verifyFrozenWithdrawalProvenance(ctx, expected); err != nil {
		return 0, err
	}
	provenance, err := k.readGatewayFrozenNFTProvenance(ctx, expected)
	return provenance.stakeEntryHeight, err
}

func (k *StateKeeper) readGatewayFrozenNFTProvenance(ctx sdk.Context, expected types.EVMFrozenBinding) (gatewayFrozenNFTProvenance, error) {
	var provenance gatewayFrozenNFTProvenance
	data, err := k.service.OpenKVStore(ctx).Get(gatewayFrozenNFTProvenanceKey(&expected))
	if err != nil {
		return provenance, err
	}
	if data == nil {
		return provenance, ErrNotFound
	}
	if (len(data) != frozenNFTStakeProvenanceSize && len(data) != frozenNFTStakeProvenanceSize+frozenNFTCreationSize) ||
		!bytes.Equal(data[8:frozenNFTStakeProvenanceSize], expected.SourceStakeID) {
		return provenance, errors.New("invalid frozen NFT stake provenance record")
	}
	height := int64(binary.BigEndian.Uint64(data[:8]))
	if height <= 0 || height > expected.CreatedHeight {
		return provenance, errors.New("invalid frozen NFT stake provenance height")
	}
	provenance.stakeEntryHeight = height
	if len(data) == frozenNFTStakeProvenanceSize {
		return provenance, nil
	}
	creation := data[frozenNFTStakeProvenanceSize:]
	if creation[0] != gatewayFrozenNFTProvenanceVersion {
		return gatewayFrozenNFTProvenance{}, errors.New("invalid frozen NFT provenance version")
	}
	createdHeight := int64(binary.BigEndian.Uint64(creation[1:9]))
	createdTime, err := readDELProvenanceTime(creation[9:])
	if err != nil || createdTime.IsZero() || createdHeight != expected.CreatedHeight {
		return gatewayFrozenNFTProvenance{}, errors.New("invalid frozen NFT creation block")
	}
	provenance.creation = CommittedEvidenceBlock{Height: createdHeight, Time: createdTime}
	return provenance, nil
}
