package app

import (
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type userNFTReserve struct {
	Token       common.Address
	Amount      *big.Int
	ReserveType uint8
}

func userNFTEvent(contract abi.ABI, target common.Address, name string, indexed []common.Hash, args ...interface{}) (*ethtypes.Log, error) {
	event, ok := contract.Events[name]
	if !ok || len(event.Inputs)-len(event.Inputs.NonIndexed()) != len(indexed) {
		return nil, errors.New("missing or differently indexed pinned NFT event")
	}
	data, err := event.Inputs.NonIndexed().Pack(args...)
	if err != nil {
		return nil, err
	}
	return &ethtypes.Log{Address: target, Topics: append([]common.Hash{event.ID}, indexed...), Data: data}, nil
}

func requireUserNFTLogs(actual, expected []*ethtypes.Log) error {
	if !equalReservelessLogs(actual, expected) {
		return errors.New("NFT events differ from the exact pinned lifecycle")
	}
	return nil
}

func userNFTCreationLogs(factory abi.ABI, center, target, beacon common.Address, kind uint8, metadata userNFTMetadata) ([]*ethtypes.Log, error) {
	created, err := userNFTEvent(factory, center, "NFTCreated", []common.Hash{common.BigToHash(new(big.Int).SetUint64(uint64(kind)))}, target, metadata)
	if err != nil {
		return nil, err
	}
	return []*ethtypes.Log{
		{Address: target, Topics: []common.Hash{crypto.Keccak256Hash([]byte("BeaconUpgraded(address)")), customAddressWord(beacon)}},
		{Address: target, Topics: []common.Hash{crypto.Keccak256Hash([]byte("ContractURIUpdated()"))}},
		{Address: target, Topics: []common.Hash{crypto.Keccak256Hash([]byte("Initialized(uint64)"))}, Data: common.BigToHash(big.NewInt(1)).Bytes()},
		created,
	}, nil
}

// These sequences follow the pinned implementations, including the ERC1155
// reserve snapshot emitted before mint and the final isolated snapshot. Every
// topic, token ID, URI, reserve amount, supply and ordering is authenticated.
func userNFTMintLogs(asset abi.ABI, target, sender, recipient common.Address, family byte, id common.Hash,
	quantity uint32, uri string, payment *big.Int, fresh bool, postSupply *big.Int,
) ([]*ethtypes.Log, error) {
	if family < 2 || family > 5 || quantity == 0 || quantity > 128 || payment == nil || payment.Sign() < 0 {
		return nil, errors.New("invalid NFT mint event inputs")
	}
	is1155 := family == 3 || family == 5
	reserved := family < 4
	if !is1155 && quantity != 1 || !reserved && payment.Sign() != 0 {
		return nil, errors.New("NFT mint events have incompatible quantity or payment")
	}
	var expected []*ethtypes.Log
	appendEvent := func(name string, indexed []common.Hash, args ...interface{}) error {
		log, err := userNFTEvent(asset, target, name, indexed, args...)
		if err == nil {
			expected = append(expected, log)
		}
		return err
	}
	if !is1155 {
		if err := appendEvent("Transfer", []common.Hash{{}, customAddressWord(recipient), id}); err != nil {
			return nil, err
		}
		if err := appendEvent("MetadataUpdate", nil, id.Big()); err != nil {
			return nil, err
		}
		if err := appendEvent("URI", []common.Hash{id}, uri); err != nil {
			return nil, err
		}
		if reserved {
			if payment.Sign() == 0 {
				return nil, errors.New("backed NFT mint events require native reserve")
			}
			if err := appendEvent("ReserveUpdated", nil, id.Big(), userNFTReserve{Amount: payment, ReserveType: 1}); err != nil {
				return nil, err
			}
		}
		return expected, nil
	}
	if family == 5 || fresh {
		if err := appendEvent("URI", []common.Hash{id}, uri); err != nil {
			return nil, err
		}
	}
	var reserve userNFTReserve
	if reserved {
		count := new(big.Int).SetUint64(uint64(quantity))
		if postSupply == nil || postSupply.Cmp(count) < 0 || fresh && postSupply.Cmp(count) != 0 {
			return nil, errors.New("NFT mint event supply differs from fresh or existing state")
		}
		unit, remainder := new(big.Int).QuoRem(payment, count, new(big.Int))
		if unit.Sign() <= 0 || remainder.Sign() != 0 {
			return nil, errors.New("NFT mint events require uniform native backing")
		}
		reserve = userNFTReserve{Amount: new(big.Int).Mul(unit, postSupply), ReserveType: 1}
		beforeSupply := new(big.Int).Sub(postSupply, count)
		if err := appendEvent("ReserveUpdated", nil, id.Big(), beforeSupply, reserve); err != nil {
			return nil, err
		}
	}
	if err := appendEvent("TransferSingle", []common.Hash{customAddressWord(sender), {}, customAddressWord(recipient)}, id.Big(), new(big.Int).SetUint64(uint64(quantity))); err != nil {
		return nil, err
	}
	if reserved {
		if err := appendEvent("ReserveUpdated", nil, id.Big(), postSupply, reserve); err != nil {
			return nil, err
		}
	}
	return expected, nil
}
