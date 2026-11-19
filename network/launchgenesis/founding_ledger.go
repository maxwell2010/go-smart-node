// Package launchgenesis assembles cold launch state in a disposable application.
package launchgenesis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/app"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const MaxLedgerBytes = 256 * 1024

type Ledger struct {
	Status        string                      `json:"status"`
	ChainID       string                      `json:"chainId"`
	EVMChainID    uint64                      `json:"evmChainId,string"`
	InitialHeight int64                       `json:"initialHeight"`
	Bank          BankAllocation              `json:"bank"`
	Validators    []validatorDeclaration      `json:"validatorDeclarations"`
	Delegations   []validatortypes.Delegation `json:"delegations"`
	ContractSetup struct {
		Registration struct {
			Method string            `json:"method"`
			Rows   []registrationRow `json:"rows"`
		} `json:"masterValidatorRegistration"`
		Funding struct {
			Gateway    common.Address `json:"gateway"`
			WrappedDEL common.Address `json:"wrappedDel"`
			Method     string         `json:"method"`
			Calls      []fundingCall  `json:"calls"`
		} `json:"gatewayFunding"`
	} `json:"contractSetup"`
	RuntimeInitChainVerified bool `json:"runtimeInitChainVerified"`
	OwnershipProofsVerified  bool `json:"ownershipProofsVerified"`
	PublicGenesisApproved    bool `json:"publicGenesisApproved"`
}

type BankAllocation struct {
	Supply   sdk.Coins           `json:"supply"`
	Balances []banktypes.Balance `json:"balances"`
}

type validatorDeclaration struct {
	Operator string `json:"operator_address"`
	Reward   string `json:"reward_address"`
	PubKey   struct {
		Type string `json:"@type"`
		Key  []byte `json:"key"`
	} `json:"consensus_pubkey"`
	Description validatortypes.Description `json:"description"`
	Commission  sdkmath.LegacyDec          `json:"commission"`
	Online      bool                       `json:"online"`
}

type registrationRow struct {
	Validator common.Address `json:"validator"`
	Meta      string         `json:"meta"`
}

type validatorMetadata struct {
	Operator    string                     `json:"operator_address"`
	Reward      common.Address             `json:"reward_address"`
	PubKey      []byte                     `json:"consensus_pubkey"`
	Description validatortypes.Description `json:"description"`
	Commission  sdkmath.LegacyDec          `json:"commission"`
}

type fundingCall struct {
	Beneficiary common.Address `json:"beneficiary"`
	Validator   common.Address `json:"validator"`
	Amount      sdkmath.Int    `json:"valueAtomic"`
}

type foundingPosition struct {
	validator    validatortypes.Validator
	registration registrationRow
	funding      fundingCall
}

func ReadLedger(source io.Reader) (Ledger, error) {
	var ledger Ledger
	raw, err := io.ReadAll(io.LimitReader(source, MaxLedgerBytes+1))
	if err != nil {
		return ledger, err
	}
	if len(raw) == 0 || len(raw) > MaxLedgerBytes {
		return ledger, fmt.Errorf("invalid founding ledger byte count")
	}
	err = decodeLedgerJSON(raw, &ledger)
	return ledger, err
}

func decodeLedgerJSON(raw []byte, target any) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("founding JSON must be UTF-8")
	}
	scan := json.NewDecoder(bytes.NewReader(raw))
	scan.UseNumber()
	if err := checkLedgerJSONValue(scan, 0); err != nil {
		return err
	}
	if _, err := scan.Token(); err != io.EOF {
		return fmt.Errorf("unexpected data after founding JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func checkLedgerJSONValue(scan *json.Decoder, depth int) error {
	if depth > 12 {
		return fmt.Errorf("founding JSON is nested too deeply")
	}
	token, err := scan.Token()
	if err != nil {
		return err
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	fields := make(map[string]bool)
	for scan.More() {
		if delim == '{' {
			key, err := scan.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			name = strings.ToLower(name) // encoding/json matches struct fields case-insensitively.
			if !ok || fields[name] {
				return fmt.Errorf("invalid or duplicate founding JSON field")
			}
			fields[name] = true
		}
		if err := checkLedgerJSONValue(scan, depth+1); err != nil {
			return err
		}
	}
	_, err = scan.Token()
	return err
}

func nativeAddress(bech32, prefix string) (common.Address, error) {
	codec := address.NewBech32Codec(prefix)
	raw, err := codec.StringToBytes(bech32)
	addr := common.BytesToAddress(raw)
	if err != nil || len(raw) != common.AddressLength || addr == (common.Address{}) {
		return common.Address{}, fmt.Errorf("invalid %s address", prefix)
	}
	canonical, err := codec.BytesToString(raw)
	if err != nil || canonical != bech32 {
		return common.Address{}, fmt.Errorf("noncanonical %s address", prefix)
	}
	return addr, nil
}

func (l Ledger) verify(profile app.WithdrawalProfile) ([]foundingPosition, sdkmath.Int, error) {
	if err := identity.ValidateChainIDs(l.ChainID, l.EVMChainID); err != nil {
		return nil, sdkmath.Int{}, err
	}
	reg, funding := l.ContractSetup.Registration, l.ContractSetup.Funding
	if l.Status != "FOUNDING_LEDGER_PREPARED" || l.InitialHeight != 1 ||
		l.RuntimeInitChainVerified || l.OwnershipProofsVerified || l.PublicGenesisApproved ||
		len(l.Validators) != 4 || len(l.Delegations) != 4 || len(reg.Rows) != 4 || len(funding.Calls) != 4 ||
		reg.Method != "addValidators" || funding.Method != "delegateDELTo" ||
		funding.Gateway == (common.Address{}) || funding.WrappedDEL == (common.Address{}) ||
		profile.MasterValidator == (common.Address{}) ||
		funding.Gateway != profile.Gateway || funding.WrappedDEL != profile.WrappedDEL {
		return nil, sdkmath.Int{}, fmt.Errorf("founding ledger domain, roles, status or rows differ")
	}
	founder := funding.Calls[0].Beneficiary
	positions := make([]foundingPosition, len(l.Validators))
	seen := make(map[string]bool, len(positions))
	for i, row := range l.Validators {
		delegation, registration, call := l.Delegations[i], reg.Rows[i], funding.Calls[i]
		position, err := verifyPosition(row, delegation, registration, call, founder)
		if err != nil {
			return nil, sdkmath.Int{}, fmt.Errorf("founding validator %d: %w", i+1, err)
		}
		if seen[string(row.PubKey.Key)] || (i > 0 && row.Operator <= l.Validators[i-1].Operator) {
			return nil, sdkmath.Int{}, fmt.Errorf("founding operator or consensus key is duplicated or unsorted")
		}
		seen[string(row.PubKey.Key)] = true
		positions[i] = position
	}
	reserve := sdkmath.NewIntWithDecimal(40_000, 18)
	if err := l.Bank.verify(founder, funding.WrappedDEL, reserve); err != nil {
		return nil, sdkmath.Int{}, err
	}
	return positions, reserve, nil
}

func verifyPosition(row validatorDeclaration, delegation validatortypes.Delegation, registration registrationRow,
	call fundingCall, founder common.Address,
) (foundingPosition, error) {
	var empty foundingPosition
	operator, err := nativeAddress(row.Operator, identity.Bech32PrefixValAddr)
	if err != nil {
		return empty, err
	}
	reward, err := nativeAddress(row.Reward, identity.Bech32PrefixAccAddr)
	if err != nil {
		return empty, err
	}
	owner, err := nativeAddress(delegation.Delegator, identity.Bech32PrefixAccAddr)
	if err != nil {
		return empty, err
	}
	commission := sdkmath.LegacyNewDecWithPrec(app.NativeValidatorMinCommissionPercent, 2)
	var meta validatorMetadata
	if err := decodeLedgerJSON([]byte(registration.Meta), &meta); err != nil {
		return empty, fmt.Errorf("invalid Master validator metadata: %w", err)
	}
	if founder == operator || owner != founder || call.Beneficiary != founder ||
		operator != registration.Validator || operator != call.Validator || row.Operator != delegation.Validator ||
		row.Operator != meta.Operator || reward != meta.Reward || row.Description != meta.Description ||
		row.PubKey.Type != "/cosmos.crypto.ed25519.PubKey" || len(row.PubKey.Key) != ed25519.PubKeySize ||
		bytes.Equal(row.PubKey.Key, make([]byte, ed25519.PubKeySize)) || !bytes.Equal(row.PubKey.Key, meta.PubKey) ||
		row.Commission.IsNil() || !row.Commission.Equal(commission) ||
		meta.Commission.IsNil() || !meta.Commission.Equal(row.Commission) || !row.Online {
		return empty, fmt.Errorf("native registration, Master metadata and funding identities differ")
	}
	stake := sdkmath.NewIntWithDecimal(10_000, 18)
	if call.Amount.IsNil() || !call.Amount.Equal(stake) ||
		delegation.Stake.Stake.Amount.IsNil() ||
		!delegation.Stake.Equal(validatortypes.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, stake))) {
		return empty, fmt.Errorf("founding delegation must fund exactly 10000 MSP")
	}
	validator, err := validatortypes.NewValidator(row.Operator, row.Reward, &ed25519.PubKey{Key: row.PubKey.Key},
		row.Description, row.Commission)
	if err != nil {
		return empty, err
	}
	validator.Online = true
	return foundingPosition{validator, registration, call}, nil
}

func (b BankAllocation) verify(founder, wrapped common.Address, reserve sdkmath.Int) error {
	total := sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntWithDecimal(5_041_000, 18)))
	if founder == wrapped || len(b.Balances) != 8 || len(b.Supply) != 1 ||
		b.Supply[0].Amount.IsNil() || !b.Supply.IsValid() || !b.Supply.Equal(total) {
		return fmt.Errorf("invalid founding bank supply or recipient count")
	}
	balances := make(map[common.Address]sdkmath.Int, len(b.Balances))
	sum := sdkmath.ZeroInt()
	var gas []int64
	for _, row := range b.Balances {
		account, err := nativeAddress(row.Address, identity.Bech32PrefixAccAddr)
		if err != nil {
			return err
		}
		if _, exists := balances[account]; exists {
			return fmt.Errorf("duplicate founding bank allocation")
		}
		if len(row.Coins) != 1 || row.Coins[0].Amount.IsNil() || !row.Coins.IsValid() || row.Coins[0].Denom != identity.BaseDenom {
			return fmt.Errorf("invalid founding bank balance")
		}
		coins := row.Coins[0].Amount
		if coins.GT(total[0].Amount) {
			return fmt.Errorf("founding bank balance exceeds supply")
		}
		balances[account] = coins
		sum = sum.Add(coins)
		if account != founder && account != wrapped {
			if coins.Equal(sdkmath.NewIntWithDecimal(100, 18)) {
				gas = append(gas, 100)
			} else if coins.Equal(sdkmath.NewIntWithDecimal(200, 18)) {
				gas = append(gas, 200)
			} else {
				return fmt.Errorf("invalid founding gas allocation")
			}
		}
	}
	slices.Sort(gas)
	free, backing := balances[founder], balances[wrapped]
	if free.IsNil() || !free.Equal(sdkmath.NewIntWithDecimal(5_000_000, 18)) ||
		backing.IsNil() || !backing.Equal(reserve) || !sum.Equal(total[0].Amount) ||
		!slices.Equal(gas, []int64{100, 100, 200, 200, 200, 200}) {
		return fmt.Errorf("founding bank balances differ from the approved split")
	}
	return nil
}
