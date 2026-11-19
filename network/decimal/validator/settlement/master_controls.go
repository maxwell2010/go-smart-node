package settlement

import (
	"bytes"
	"errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// ValidatorControlProof is authenticated by the pinned Master and exact logs.
type ValidatorControlProof struct{ Action, Metadata string }

func (e *Executor) VerifySignedValidatorControl(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) (ValidatorControlProof, error) {
	var proof ValidatorControlProof
	if e == nil || msg.To == nil || *msg.To != e.roles.MasterValidator || msg.From == (common.Address{}) || msg.Value == nil || msg.Value.Sign() != 0 || len(msg.SetCodeAuthorizations) != 0 || len(msg.Data) < 4 || len(msg.Data) > 4096 {
		return proof, errors.New("validator control requires a bounded direct signed Master call")
	}
	method, err := e.master.MethodById(msg.Data[:4])
	if err != nil {
		return proof, err
	}
	args, err := method.Inputs.Unpack(msg.Data[4:])
	if err != nil {
		return proof, err
	}
	encoded, err := method.Inputs.Pack(args...)
	if err != nil || !bytes.Equal(encoded, msg.Data[4:]) {
		return proof, errors.New("noncanonical validator control calldata")
	}
	switch method.Name {
	case "pauseSelf":
		proof.Action = "disable"
	case "unpauseSelf":
		proof.Action = "enable"
	case "pauseValidator", "unpauseValidator":
		if len(args) != 1 || args[0].(common.Address) != msg.From {
			return proof, errors.New("operator cannot control another validator")
		}
		proof.Action = "disable"
		if method.Name == "unpauseValidator" {
			proof.Action = "enable"
		}
	case "updateValidatorMeta":
		if len(args) != 2 || args[0].(common.Address) != msg.From {
			return proof, errors.New("operator cannot change another validator metadata")
		}
		proof.Action = "metadata"
		proof.Metadata = args[1].(string)
	default:
		return proof, errors.New("unsupported validator control")
	}
	if receipt == nil || receipt.Status != ethtypes.ReceiptStatusSuccessful || len(receipt.Logs) != 1 || receipt.Logs[0] == nil {
		return proof, errors.New("validator control requires one successful exact state event")
	}
	if err := signedMetadata(ctx, receipt); err != nil {
		return proof, err
	}
	if _, err := e.auth.AuthenticateStakeProfile(ctx); err != nil {
		return proof, err
	}
	if err := e.requireInert(ctx, msg.From); err != nil {
		return proof, err
	}
	raw, err := e.read(ctx, e.roles.MasterValidator, e.master, "getValidatorState", 3*common.HashLength, msg.From)
	if err != nil {
		return proof, err
	}
	status, paused := common.BytesToHash(raw[:32]), common.BytesToHash(raw[32:64])
	if status != smallWord(1) && status != smallWord(2) {
		return proof, errors.New("validator control requires a current Master member")
	}
	event := "ValidatorUpdated"
	expected := raw[:64]
	if proof.Action == "metadata" {
		event = "ValidatorMetaUpdated"
		expected, err = e.master.Events[event].Inputs.NonIndexed().Pack(proof.Metadata)
		if err != nil {
			return proof, err
		}
	} else {
		want := common.Hash{}
		if proof.Action == "disable" {
			want = smallWord(1)
		}
		if paused != want {
			return proof, errors.New("Master pause state differs from selected control")
		}
	}
	if err := exactValidatorControlLog(receipt.Logs[0], e.roles.MasterValidator, msg.From, e.master.Events[event].ID, expected); err != nil {
		return proof, err
	}
	return proof, nil
}

func exactValidatorControlLog(entry *ethtypes.Log, master, operator common.Address, topic common.Hash, expected []byte) error {
	if entry == nil || entry.Removed || entry.Address != master || len(entry.Topics) != 2 || entry.Topics[0] != topic || entry.Topics[1] != common.BytesToHash(operator[:]) || !bytes.Equal(entry.Data, expected) {
		return errors.New("validator control event differs from authenticated caller/state")
	}
	return nil
}
