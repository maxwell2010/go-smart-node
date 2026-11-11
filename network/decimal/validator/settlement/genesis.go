package settlement

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// GenesisLimits bound the entire cold attestation, including the deployed
// frozen array's completed history and the native NFT custody checks.
type GenesisLimits struct {
	MaxRows       uint32
	SDKGas        uint64
	DELValidators []common.Address
	ActiveDEL     []validatortypes.EVMStakeBinding
	NFTOrigins    []NFTGenesisOrigin
}

// AttestGenesis verifies a bounded settlement snapshot against the entire
// approved Gateway frozen array in already initialized genesis state. Active
// withdrawals need exact native rows. A completed row may have a matching
// inactive native record or no record. The supported fresh profile has no
// legacy frozen layout or active frozen transfers.
//
// The caller supplies the genesis context and a deterministic existing SDK
// staking proposer for the EVM getters. A concrete NFT attestor is required for
// each active NFT row. It runs in a discarded cache under the same work meter.
func (e *Executor) AttestGenesis(ctx sdk.Context, rows []validatortypes.EVMFrozenBinding, limits GenesisLimits, attestNFT func(sdk.Context, validatortypes.EVMFrozenBinding) error) (err error) {
	if e == nil || e.evm == nil || limits.MaxRows == 0 || limits.MaxRows > validatortypes.MaxBindingPageSize || limits.SDKGas == 0 ||
		len(rows) > int(limits.MaxRows) || len(limits.DELValidators) > int(limits.MaxRows) || len(limits.NFTOrigins) > int(limits.MaxRows) ||
		(len(limits.DELValidators) != 0 && len(rows) != 0) || (len(limits.NFTOrigins) != 0 && len(limits.DELValidators) == 0) ||
		len(limits.ActiveDEL) > int(limits.MaxRows) || (len(limits.ActiveDEL) != 0 && len(limits.DELValidators) == 0) {
		return errors.New("invalid Gateway genesis attestation limits or row count")
	}
	if ctx.BlockHeight() < 0 || ctx.BlockTime().Unix() <= 0 || len(ctx.BlockHeader().ProposerAddress) != common.AddressLength {
		return errors.New("Gateway genesis attestation requires a genesis time and an initialized consensus proposer")
	}
	child, _ := ctx.CacheContext()
	meter := storetypes.NewGasMeter(limits.SDKGas)
	child = child.WithGasMeter(meter)
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(storetypes.ErrorOutOfGas); !ok {
				panic(recovered)
			}
			err = errors.New("Gateway genesis attestation exceeded the SDK gas budget")
		}
		ctx.GasMeter().ConsumeGas(meter.GasConsumedToLimit(), "Gateway genesis attestation")
	}()
	if _, err = e.auth.AuthenticateStakeProfile(child); err != nil {
		return err
	}
	if err = e.requireInert(child, Sender()); err != nil {
		return fmt.Errorf("invalid reserved settlement sender: %w", err)
	}
	count, err := e.genesisFrozenCount(child, limits.MaxRows)
	if err != nil {
		return err
	}
	byIndex := make(map[common.Hash]int, len(rows))
	for i, row := range rows {
		index, _, err := e.withdrawalIdentity(row)
		if err != nil {
			return fmt.Errorf("invalid native genesis withdrawal at row %d: %w", i, err)
		}
		if row.UpdatedHeight > ctx.BlockHeight() {
			return fmt.Errorf("native genesis withdrawal index %s has provenance newer than the genesis height", index)
		}
		if index.Big().Cmp(count) >= 0 {
			return fmt.Errorf("native genesis withdrawal index %s is outside the deployed frozen array", index)
		}
		if _, exists := byIndex[index]; exists {
			return fmt.Errorf("duplicate native genesis withdrawal index %s", index)
		}
		byIndex[index] = i
	}
	claims := new(genesisClaims)
	custody := make(map[common.Hash]struct{})
	for i := uint64(0); i < count.Uint64(); i++ {
		index := common.BigToHash(new(big.Int).SetUint64(i))
		words, err := e.readFrozen(child, index)
		if err != nil {
			return fmt.Errorf("read deployed genesis frozen index %d: %w", i, err)
		}
		nativeIndex, bound := byIndex[index]
		status, err := genesisFrozenStatus(words)
		if err != nil {
			return fmt.Errorf("deployed genesis frozen index %d: %w", i, err)
		}
		if status == statusCompleted && !bound {
			continue
		}
		if !bound || rows[nativeIndex].Active != (status == statusFrozen) {
			return fmt.Errorf("deployed genesis frozen index %d has missing or conflicting native status", i)
		}
		row := rows[nativeIndex]
		claim, err := e.matchFrozen(words, row, status)
		if err != nil {
			return fmt.Errorf("deployed genesis frozen index %d: %w", i, err)
		}
		if status == statusCompleted {
			continue
		}
		if err = e.requireInert(child, claim.beneficiary); err != nil {
			return fmt.Errorf("genesis withdrawal %d beneficiary: %w", i, err)
		}
		if !claim.nft {
			claims.gatewayWrapped.Add(&claims.gatewayWrapped, claim.amount.Big())
			continue
		}
		if _, exists := custody[claim.tokenID]; exists {
			return fmt.Errorf("duplicate deployed genesis NFT custody %s", claim.tokenID)
		}
		custody[claim.tokenID] = struct{}{}
		if err = claims.addNFT(row, e.roles.WrappedDEL); err != nil {
			return err
		}
		if _, err = e.payoutState(child, claim, e.roles.Gateway); err != nil {
			return fmt.Errorf("genesis withdrawal %d EVM custody: %w", i, err)
		}
		if attestNFT == nil {
			return errors.New("genesis NFT withdrawal requires native custody attestation")
		}
		read, _ := child.CacheContext()
		if err = attestNFT(read, row); err != nil {
			return fmt.Errorf("genesis withdrawal %d native custody: %w", i, err)
		}
	}
	if len(limits.DELValidators) != 0 {
		if len(limits.ActiveDEL) != 0 && count.Sign() != 0 {
			return errors.New("active DEL genesis cannot import Gateway withdrawal history")
		}
		if err := e.attestDELGenesis(child, limits.DELValidators, limits.ActiveDEL); err != nil {
			return err
		}
		if len(limits.NFTOrigins) != 0 {
			return e.attestFreshNFTGenesis(child, limits.NFTOrigins)
		}
		return nil
	}
	return e.verifyGenesisBacking(child, claims)
}

func (e *Executor) attestDELGenesis(ctx sdk.Context, validators []common.Address, positions []validatortypes.EVMStakeBinding) error {
	state, err := e.payoutState(ctx, payout{asset: e.roles.WrappedDEL, beneficiary: Sender()}, common.Address{})
	if err != nil {
		return err
	}
	queue, err := e.read(ctx, e.roles.Gateway, e.gateway, "autoUnbondQueueLength", common.HashLength)
	if err != nil {
		return err
	}
	if common.BytesToHash(queue) != (common.Hash{}) {
		return errors.New("DEL delegation genesis requires an empty automatic unbond queue")
	}
	freeze, err := e.read(ctx, e.roles.Gateway, e.gateway, "getFreezeTime", common.HashLength, uint8(freezeWithdraw))
	if err != nil {
		return err
	}
	delay := new(big.Int).SetBytes(freeze)
	if !delay.IsInt64() || delay.Sign() == 0 {
		return errors.New("DEL delegation genesis requires a positive withdrawal freeze time")
	}
	reserves := make(map[common.Address]*big.Int, len(validators))
	for _, validator := range validators {
		reserves[validator] = new(big.Int)
	}
	var total big.Int
	seen := make(map[common.Hash]bool, len(positions))
	for _, row := range positions {
		validator, beneficiary := common.BytesToAddress(row.Validator), common.BytesToAddress(row.Beneficiary)
		reserve, ok := reserves[validator]
		if !ok || beneficiary == (common.Address{}) || beneficiary == Sender() || !row.Active || row.RawAmount.IsNil() || !row.RawAmount.IsPositive() {
			return errors.New("invalid active DEL genesis position")
		}
		if err := e.requireInert(ctx, beneficiary); err != nil {
			return fmt.Errorf("active DEL genesis beneficiary: %w", err)
		}
		var zero [2 * common.HashLength]byte
		id := crypto.Keccak256Hash(validator[:], beneficiary[:], e.roles.WrappedDEL[:], zero[:])
		if seen[id] || !bytes.Equal(id[:], row.StakeID) {
			return errors.New("duplicate or inconsistent active DEL genesis position ID")
		}
		seen[id] = true
		if err := e.verifyDELStakeCursor(ctx, id); err != nil {
			return err
		}
		want := [8]common.Hash{common.BytesToHash(validator[:]), common.BytesToHash(beneficiary[:]),
			common.BytesToHash(e.roles.WrappedDEL[:]), common.BigToHash(row.RawAmount.BigInt()), {}, smallWord(tokenDEL), {}, {}}
		got, err := e.read(ctx, e.roles.Gateway, e.gateway, "getStake", len(want)*common.HashLength, validator, beneficiary, e.roles.WrappedDEL)
		if err != nil {
			return err
		}
		for i, word := range want {
			if !bytes.Equal(word[:], got[i*common.HashLength:(i+1)*common.HashLength]) {
				return errors.New("active DEL genesis position differs from deployed Gateway")
			}
		}
		_, nft, err := e.directDELPoststate(ctx, validator, beneficiary, 0, row.RawAmount.BigInt())
		if err != nil || nft.Sign() != 0 {
			return errors.New("active DEL genesis position cannot contain NFT backing")
		}
		reserve.Add(reserve, row.RawAmount.BigInt())
		total.Add(&total, row.RawAmount.BigInt())
	}
	if state.wrapped.Cmp(&total) != 0 {
		return errors.New("DEL genesis Gateway WDEL custody differs from attested active positions")
	}
	for _, validator := range validators {
		if err := e.verifyUnpenalizedValidator(ctx, validator); err != nil {
			return err
		}
		reserve, err := e.read(ctx, e.roles.Gateway, e.gateway, "getValidatorReserve", 2*common.HashLength, validator, e.roles.WrappedDEL)
		if err != nil {
			return err
		}
		if common.BytesToHash(reserve[:common.HashLength]) != (common.Hash{}) ||
			new(big.Int).SetBytes(reserve[common.HashLength:]).Cmp(reserves[validator]) != 0 {
			return errors.New("DEL genesis validator WDEL reserve differs from attested active positions")
		}
	}
	return nil
}

func (e *Executor) genesisFrozenCount(ctx sdk.Context, limit uint32) (*big.Int, error) {
	// The pinned ABI reports the lengths of the live and legacy frozen arrays.
	ret, err := e.read(ctx, e.roles.Gateway, e.gateway, "frozenStakesMigrationProgress", 2*common.HashLength)
	if err != nil {
		return nil, err
	}
	live := new(big.Int).SetBytes(ret[:common.HashLength])
	legacy := new(big.Int).SetBytes(ret[common.HashLength:])
	if legacy.Sign() != 0 {
		return nil, errors.New("Gateway genesis settlement does not admit a legacy frozen layout")
	}
	if live.Cmp(new(big.Int).SetUint64(uint64(limit))) > 0 {
		return nil, errors.New("deployed Gateway frozen array exceeds the genesis census limit")
	}
	return live, nil
}

func genesisFrozenStatus(words frozenWords) (byte, error) {
	if words[0] == (common.Hash{}) || words[1] == (common.Hash{}) || words[2] == (common.Hash{}) || words[3] == (common.Hash{}) || words[10] == (common.Hash{}) {
		return 0, errors.New("deployed frozen record is empty or incomplete")
	}
	switch words[freezeStatusWord] {
	case smallWord(statusCompleted):
		if words[9] == smallWord(freezeWithdraw) || words[9] == smallWord(2) {
			return statusCompleted, nil
		}
	case smallWord(statusFrozen):
		if words[9] == smallWord(freezeWithdraw) {
			return statusFrozen, nil
		}
	}
	return 0, errors.New("deployed frozen record is outside the fresh withdrawal profile")
}

type genesisClaims struct {
	gatewayWrapped, vaultNative, vaultWrapped big.Int
	hasVaultNative, hasVaultWrapped           bool
}

func (claims *genesisClaims) addNFT(row validatortypes.EVMFrozenBinding, wrapped common.Address) error {
	if row.ReserveAsset.NativeDenom != identity.BaseDenom {
		return errors.New("genesis NFT withdrawal must have a DEL-denominated reserve")
	}
	switch row.ReserveAsset.ReserveKind {
	case 1:
		claims.hasVaultNative = true
		claims.vaultNative.Add(&claims.vaultNative, row.ReportedReserveAmount.BigInt())
	case 2:
		if common.BytesToAddress(row.ReserveAsset.Token) != wrapped {
			return errors.New("genesis NFT withdrawal names an unsupported reserve token")
		}
		claims.hasVaultWrapped = true
		claims.vaultWrapped.Add(&claims.vaultWrapped, row.ReportedReserveAmount.BigInt())
	default:
		return errors.New("genesis NFT withdrawal has an unsupported reserve kind")
	}
	return nil
}

func (e *Executor) verifyGenesisBacking(ctx sdk.Context, claims *genesisClaims) error {
	if claims.hasVaultNative {
		held := e.evm.GetBalance(ctx, e.roles.PositionVault)
		if held == nil {
			return errors.New("invalid genesis vault DEL balance")
		}
		if err := e.verifyVaultClaims(ctx, 1, common.Address{}, &claims.vaultNative, held.ToBig()); err != nil {
			return err
		}
	}
	if claims.gatewayWrapped.Sign() == 0 && !claims.hasVaultWrapped {
		return nil
	}
	supplyRaw, err := e.read(ctx, e.roles.WrappedDEL, e.wrapper, "totalSupply", common.HashLength)
	if err != nil {
		return err
	}
	supply := new(big.Int).SetBytes(supplyRaw)
	backing := e.evm.GetBalance(ctx, e.roles.WrappedDEL)
	if backing == nil || backing.ToBig().Cmp(supply) < 0 {
		return errors.New("genesis WDEL supply exceeds its actual native DEL backing")
	}
	gatewayRaw, err := e.read(ctx, e.roles.WrappedDEL, e.wrapper, "balanceOf", common.HashLength, e.roles.Gateway)
	if err != nil {
		return err
	}
	gateway := new(big.Int).SetBytes(gatewayRaw)
	if gateway.Cmp(&claims.gatewayWrapped) < 0 || gateway.Cmp(supply) > 0 {
		return errors.New("genesis frozen DEL claims exceed the Gateway's backed WDEL holdings")
	}
	if claims.hasVaultWrapped {
		vaultRaw, err := e.read(ctx, e.roles.WrappedDEL, e.wrapper, "balanceOf", common.HashLength, e.roles.PositionVault)
		if err != nil {
			return err
		}
		vault := new(big.Int).SetBytes(vaultRaw)
		if new(big.Int).Add(gateway, vault).Cmp(supply) > 0 {
			return errors.New("genesis Gateway and vault WDEL holdings exceed actual supply")
		}
		if err = e.verifyVaultClaims(ctx, 2, e.roles.WrappedDEL, &claims.vaultWrapped, vault); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) verifyVaultClaims(ctx sdk.Context, kind uint8, token common.Address, claims, held *big.Int) error {
	raw, err := e.read(ctx, e.roles.PositionVault, e.vault, "declaredReserve", common.HashLength, kind, token)
	if err != nil {
		return err
	}
	declared := new(big.Int).SetBytes(raw)
	if claims.Cmp(declared) > 0 || declared.Cmp(held) > 0 {
		return errors.New("genesis frozen NFT claims exceed the vault's declared or actual currency backing")
	}
	return nil
}
