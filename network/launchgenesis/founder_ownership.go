package launchgenesis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const MaxAllocationPlanBytes = 16 * 1024

type founderAllocationPlan struct {
	ChainID        string `json:"chain_id"`
	EVMChainID     uint64 `json:"evm_chain_id,string"`
	FounderAddress string `json:"founder_evm_address"`
	Decisions      struct {
		AllocationApproved     bool   `json:"allocation_approved"`
		ChainIDApproved        bool   `json:"chain_id_approved"`
		StakeOwnershipApproved bool   `json:"stake_ownership_approved"`
		StakeOwnership         string `json:"selected_stake_ownership"`
		StakeOwner             string `json:"selected_stake_owner_evm_address"`
	} `json:"decisions"`
}

type FounderOwnership struct {
	plan      founderAllocationPlan
	address   common.Address
	digest    string
	challenge string
}

type FounderOwnershipReport struct {
	Status                  string `json:"status"`
	ChainID                 string `json:"chainId"`
	EVMChainID              uint64 `json:"evmChainId,string"`
	FounderAddress          string `json:"founderAddress"`
	PlanSHA256              string `json:"planSha256"`
	Challenge               string `json:"challenge"`
	NetworkLaunchAuthorized bool   `json:"networkLaunchAuthorized"`
}

// NewFounderOwnership binds the proof to a reviewed allocation and a challenge
// issued by its verifier. Approval of the allocation is separate from this proof.
func NewFounderOwnership(raw []byte, approvedPlanSHA256, challenge string) (FounderOwnership, error) {
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	if len(raw) == 0 || len(raw) > MaxAllocationPlanBytes || !strings.EqualFold(digest, approvedPlanSHA256) {
		return FounderOwnership{}, fmt.Errorf("allocation plan differs from the independently approved SHA256")
	}
	if len(challenge) != 64 || challenge != strings.ToLower(challenge) {
		return FounderOwnership{}, fmt.Errorf("challenge must be 32 bytes of lowercase hex")
	}
	if _, err := hex.DecodeString(challenge); err != nil || challenge == strings.Repeat("0", 64) {
		return FounderOwnership{}, fmt.Errorf("invalid challenge")
	}
	var fields map[string]json.RawMessage
	if err := decodeLedgerJSON(raw, &fields); err != nil {
		return FounderOwnership{}, fmt.Errorf("invalid allocation plan: %w", err)
	}
	var plan founderAllocationPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return FounderOwnership{}, err
	}
	if err := identity.ValidateChainIDs(plan.ChainID, plan.EVMChainID); err != nil {
		return FounderOwnership{}, err
	}
	address := common.HexToAddress(plan.FounderAddress)
	if !strings.HasPrefix(plan.FounderAddress, "0x") || !common.IsHexAddress(plan.FounderAddress) ||
		address == (common.Address{}) {
		return FounderOwnership{}, fmt.Errorf("invalid founder EVM address")
	}
	decisions := plan.Decisions
	if !decisions.AllocationApproved || !decisions.ChainIDApproved || !decisions.StakeOwnershipApproved ||
		decisions.StakeOwnership != "founder_owned_delegations" || !strings.EqualFold(decisions.StakeOwner, plan.FounderAddress) {
		return FounderOwnership{}, fmt.Errorf("the reviewed plan must assign the founding positions to this founder")
	}
	return FounderOwnership{plan: plan, address: address, digest: digest, challenge: challenge}, nil
}

func (f FounderOwnership) Payload() []byte {
	return fmt.Appendf(nil, "MintCandy Network - founder wallet control v1\n"+
		"This message proves control of the wallet for the reviewed allocation plan.\n"+
		"Chain ID: %s\nEVM chain ID: %d\nFounder wallet: %s\nAllocation plan SHA256: %s\nChallenge: %s",
		f.plan.ChainID, f.plan.EVMChainID, f.address.Hex(), f.digest, f.challenge)
}

func ReadFounderSignature(source io.Reader) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(source, 141))
	if err != nil {
		return "", err
	}
	signature := strings.TrimSpace(string(raw))
	if len(raw) > 140 || len(signature) != 132 {
		return "", fmt.Errorf("expected one 65-byte 0x-prefixed wallet signature")
	}
	return signature, nil
}

func (f FounderOwnership) Verify(signature string) (FounderOwnershipReport, error) {
	signer, err := recoverMessageSigner(f.Payload(), signature)
	if err != nil {
		return FounderOwnershipReport{}, err
	}
	if signer != f.address {
		return FounderOwnershipReport{}, fmt.Errorf("signature does not match the founder wallet for this allocation and challenge")
	}
	return FounderOwnershipReport{
		Status: "FOUNDER_WALLET_OWNERSHIP_VERIFIED", ChainID: f.plan.ChainID,
		EVMChainID: f.plan.EVMChainID, FounderAddress: f.address.Hex(), PlanSHA256: f.digest, Challenge: f.challenge,
	}, nil
}
