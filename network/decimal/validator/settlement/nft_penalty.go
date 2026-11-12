package settlement

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	nftview "github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/maxwell2010/go-smart-node/network/nativeburn"
)

// ActiveNFTPenaltyProof carries the exact companion prestate and isolated
// reserve delta for a native keeper projection.
type ActiveNFTPenaltyProof struct {
	Companion                 DELTransition
	CompanionCursor           common.Hash
	BeforeCursor, AfterCursor common.Hash
	RemainingReserve, Burned  sdkmath.Int
}

// The extra Gateway library and Vault frames need to forward gas to the
// native burn. The enclosing operation charges actual pre-refund work.
const nftPenaltyEVMGas = 2_000_000

type activeNFTPenaltyKey struct{}
type activeNFTPenaltyCall struct {
	executor *Executor
	height   int64
	id       common.Hash
}

// AuthorizeActiveNFTPenalty is an internal EVM commit capability for exactly
// one authenticated Gateway position. Signed calls do not receive it.
func (e *Executor) AuthorizeActiveNFTPenalty(ctx sdk.Context, msg core.Message) bool {
	call, ok := ctx.Value(activeNFTPenaltyKey{}).(activeNFTPenaltyCall)
	if !ok || e == nil || call.executor != e || call.height != ctx.BlockHeight() ||
		ctx.ExecMode() != sdk.ExecModeFinalize || msg.From != Sender() ||
		msg.To == nil || *msg.To != e.roles.Gateway || msg.Value == nil ||
		msg.Value.Sign() != 0 || len(msg.SetCodeAuthorizations) != 0 {
		return false
	}
	data, err := e.gateway.Pack("applyPenaltiesToNFTStakes", [][32]byte{call.id})
	return err == nil && bytes.Equal(msg.Data, data)
}

// ApplyActiveNFTPenalty realizes one announced penalty on an ordinary active
// isolated NFT with native DEL in the Vault. The WDEL companion is a logical
// mirror of that backing. The projection must succeed in the same cache as
// the EVM execution and native bank burn. The caller must reconcile every
// eligible position before committing the enclosing Master announcement.
func (e *Executor) ApplyActiveNFTPenalty(ctx sdk.Context, row validatortypes.EVMStakeBinding, percentage uint16,
	nativeSupply func(sdk.Context) sdkmath.Int, project func(sdk.Context, ActiveNFTPenaltyProof) error,
) error {
	if e == nil || e.evm == nil || ctx.ExecMode() != sdk.ExecModeFinalize || ctx.BlockHeight() <= 0 ||
		percentage == 0 || percentage > 100 || nativeSupply == nil || project == nil {
		return errors.New("active NFT penalty requires finalized block, approved percentage, supply and native projection")
	}
	if err := row.Validate(); err != nil {
		return err
	}
	id := common.BytesToHash(row.StakeID)
	validator, beneficiary := common.BytesToAddress(row.Validator), common.BytesToAddress(row.Beneficiary)
	if !row.Active || row.Kind != validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT ||
		row.NativeTarget.StakeType != validatortypes.StakeType_NFT ||
		row.NFTPosition.Mode != validatortypes.NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT ||
		row.NFTPosition.Origin.Standard != 1155 || common.BytesToHash(row.NFTPosition.ParentPositionID) != (common.Hash{}) ||
		common.BytesToHash(row.HoldTimestamp) != (common.Hash{}) || row.ReserveAsset.ReserveKind != nfttypes.IsolatedReserveDEL ||
		row.ReserveAsset.NativeDenom != identity.BaseDenom || common.BytesToAddress(row.Gateway) != e.roles.Gateway ||
		common.BytesToAddress(row.TokenContract) != e.roles.PositionVault || beneficiary == Sender() || row.UpdatedHeight > ctx.BlockHeight() {
		return errors.New("penalty requires one active ordinary isolated NFT binding with native DEL backing")
	}
	beforeCursor := common.BytesToHash(row.PenaltyCursor)
	next := new(big.Int).Add(beforeCursor.Big(), big.NewInt(1))
	if next.BitLen() > 256 {
		return errors.New("active NFT penalty cursor overflow")
	}
	afterCursor := common.BigToHash(next)
	child, write := ctx.CacheContext()
	source, err := e.authenticatePenaltySource(child, validator, percentage)
	if err != nil {
		return err
	}
	if err := e.requireInert(child, Sender()); err != nil {
		return err
	}
	status, err := e.verifyAnnouncedPenalty(child, validator, beforeCursor, afterCursor, percentage)
	if err != nil {
		return err
	}
	before, err := e.readActiveNFTPenaltyState(child, row, row.ReportedReserveAmount, beforeCursor, nativeSupply)
	if err != nil {
		return err
	}
	burned := new(big.Int).Mul(row.ReportedReserveAmount.BigInt(), new(big.Int).SetUint64(uint64(percentage)))
	burned.Quo(burned, big.NewInt(100))
	if burned.BitLen() > 255 {
		return errors.New("active NFT penalty exceeds signed Gateway event amount")
	}
	loss := sdkmath.NewIntFromBigInt(burned)
	remaining := row.ReportedReserveAmount.Sub(loss)
	data, err := e.gateway.Pack("applyPenaltiesToNFTStakes", [][32]byte{id})
	if err != nil {
		return err
	}
	authorized := child.WithValue(activeNFTPenaltyKey{}, activeNFTPenaltyCall{e, child.BlockHeight(), id})
	result, err := e.invokePenaltyWithGas(authorized, Sender(), e.roles.Gateway, data, nftPenaltyEVMGas)
	if err != nil {
		return err
	}
	if len(result.Ret) != 0 {
		return errors.New("Gateway NFT penalty returned unexpected data")
	}
	after, err := e.readActiveNFTPenaltyState(child, row, remaining, afterCursor, nativeSupply)
	if err != nil {
		return err
	}
	if err := verifyActiveNFTPenaltyDeltas(before, after, loss); err != nil {
		return err
	}
	receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(result.Logs)}
	if err := e.verifyActiveNFTPenaltyLogs(receipt, id, before.position.ReserveAmount, after, percentage, burned); err != nil {
		return err
	}
	if err := e.confirmPenaltySource(child, source); err != nil {
		return err
	}
	afterStatus, err := e.verifyAnnouncedPenalty(child, validator, beforeCursor, afterCursor, percentage)
	if err != nil {
		return err
	}
	if afterStatus != status {
		return errors.New("active NFT penalty changed the Master status")
	}
	proof := ActiveNFTPenaltyProof{Companion: before.companion, CompanionCursor: before.coinCursor,
		BeforeCursor: beforeCursor, AfterCursor: afterCursor, RemainingReserve: remaining, Burned: loss}
	if err := project(child, proof); err != nil {
		return fmt.Errorf("project active NFT penalty: %w", err)
	}
	write()
	return nil
}

type activeNFTPenaltyState struct {
	position         nftview.PositionSnapshot
	companion        DELTransition
	coin             []byte
	coinCursor       common.Hash
	validatorReserve common.Hash
	validatorCursor  common.Hash
	payoutState
	bankSupply sdkmath.Int
}

func (e *Executor) readActiveNFTPenaltyState(ctx sdk.Context, row validatortypes.EVMStakeBinding, amount sdkmath.Int,
	cursor common.Hash, nativeSupply func(sdk.Context) sdkmath.Int,
) (activeNFTPenaltyState, error) {
	var out activeNFTPenaltyState
	id := common.BytesToHash(row.TokenID)
	v, owner := common.BytesToAddress(row.Validator), common.BytesToAddress(row.Beneficiary)
	position, err := e.auth.Position(ctx, id)
	if err != nil {
		return out, err
	}
	origin := row.NFTPosition.Origin
	if position.Status != nftview.PositionActive || position.Owner != e.roles.Gateway ||
		position.Asset.Contract != e.roles.PositionVault || position.Asset.TokenID != id ||
		position.ParentPositionID != (common.Hash{}) ||
		position.Origin.Contract != common.BytesToAddress(origin.Contract) || position.Origin.TokenID != common.BytesToHash(origin.TokenID) ||
		position.OriginQuantity != uint32(len(origin.NativeSubTokenIDs)) ||
		position.ReserveKind != nfttypes.IsolatedReserveDEL || position.ReserveToken != (common.Address{}) ||
		position.ReserveAmount != common.BigToHash(amount.BigInt()) {
		return out, errors.New("Vault NFT penalty position differs from its exact active custody or DEL reserve")
	}
	stakeID, err := e.verifyNFTStakeAtCursor(ctx, position, v, owner, true, cursor)
	if err != nil {
		return out, err
	}
	if stakeID != common.BytesToHash(row.StakeID) {
		return out, errors.New("Gateway NFT penalty differs from the native stake identity")
	}
	companion, coin, coinCursor, err := e.readNFTCompanionState(ctx, v, owner)
	if err != nil {
		return out, err
	}
	if companion.NFTBacked.LT(amount) ||
		(row.ReportedReserveAmount.IsPositive() && !bytes.Equal(row.CoinCompanionStakeID, companion.StakeID[:])) ||
		(row.ReportedReserveAmount.IsZero() && len(row.CoinCompanionStakeID) != 0) {
		return out, errors.New("Gateway NFT penalty differs from the exact DEL companion identity or backing")
	}
	reserve, err := e.read(ctx, e.roles.Gateway, e.gateway, "getValidatorReserve", 2*common.HashLength, v, e.roles.WrappedDEL)
	if err != nil {
		return out, err
	}
	validatorReserve := common.BytesToHash(reserve[common.HashLength:])
	if validatorReserve.Big().Cmp(common.BytesToHash(coin[3*common.HashLength:4*common.HashLength]).Big()) < 0 {
		return out, errors.New("Gateway validator reserve is below the NFT companion total")
	}
	balances, err := e.payoutState(ctx, payout{asset: e.roles.WrappedDEL, beneficiary: owner}, common.Address{})
	if err != nil {
		return out, err
	}
	supply := nativeSupply(ctx)
	if supply.IsNil() || supply.IsNegative() {
		return out, errors.New("invalid native DEL supply for the isolated NFT penalty")
	}
	return activeNFTPenaltyState{position: position, companion: companion, coin: coin, coinCursor: coinCursor,
		validatorReserve: validatorReserve, validatorCursor: common.BytesToHash(reserve[:common.HashLength]),
		payoutState: balances, bankSupply: supply}, nil
}

func verifyActiveNFTPenaltyDeltas(before, after activeNFTPenaltyState, loss sdkmath.Int) error {
	if !before.companion.Remaining.Equal(after.companion.Remaining) ||
		before.coinCursor != after.coinCursor || before.validatorCursor != after.validatorCursor ||
		!before.companion.NFTBacked.Sub(after.companion.NFTBacked).Equal(loss) {
		return errors.New("active NFT penalty changed DEL principal or companion backing unexpectedly")
	}
	if new(big.Int).Sub(before.validatorReserve.Big(), after.validatorReserve.Big()).Cmp(loss.BigInt()) != 0 {
		return errors.New("active NFT penalty did not reduce validator reserve by the exact loss")
	}
	return verifyNFTVaultPenaltyDeltas(before.position, after.position, before.payoutState, after.payoutState, before.bankSupply, after.bankSupply, loss)
}

func verifyNFTVaultPenaltyDeltas(before, after nftview.PositionSnapshot, beforeBalance, afterBalance payoutState,
	beforeSupply, afterSupply, loss sdkmath.Int,
) error {
	position := before
	position.ReserveAmount = after.ReserveAmount
	position.DeclaredCurrencyReserve = after.DeclaredCurrencyReserve
	position.HeldCurrencyReserve = after.HeldCurrencyReserve
	if position != after || !beforeSupply.Sub(afterSupply).Equal(loss) {
		return errors.New("NFT penalty changed receipt identity or native supply unexpectedly")
	}
	for _, pair := range [][2]common.Hash{{before.ReserveAmount, after.ReserveAmount},
		{before.DeclaredCurrencyReserve, after.DeclaredCurrencyReserve}, {before.HeldCurrencyReserve, after.HeldCurrencyReserve}} {
		if new(big.Int).Sub(pair[0].Big(), pair[1].Big()).Cmp(loss.BigInt()) != 0 {
			return errors.New("NFT penalty did not reduce Vault position and custody by the exact loss")
		}
	}
	for _, pair := range [][2]*big.Int{{beforeBalance.beneficiary, afterBalance.beneficiary}, {beforeBalance.gateway, afterBalance.gateway},
		{beforeBalance.wrapped, afterBalance.wrapped}, {beforeBalance.backing, afterBalance.backing}, {beforeBalance.supply, afterBalance.supply}} {
		if pair[0].Cmp(pair[1]) != 0 {
			return errors.New("native Vault penalty changed WDEL or the beneficiary or Gateway bank balance")
		}
	}
	return nil
}

func (e *Executor) verifyActiveNFTPenaltyLogs(receipt *ethtypes.Receipt, id, previousAmount common.Hash,
	after activeNFTPenaltyState, percentage uint16, burned *big.Int,
) error {
	markers, err := e.completions(receipt)
	if err != nil {
		return err
	}
	want := 5
	if previousAmount == (common.Hash{}) {
		want = 0
	} else if burned.Sign() > 0 {
		want++
	}
	if len(markers) != 0 || len(receipt.Logs) != want {
		return errors.New("unexpected logs for one active isolated NFT penalty")
	}
	if want == 0 {
		return nil
	}
	logs := receipt.Logs
	vaultLogs := len(logs) - 3
	if err := e.verifyNFTPenaltyVaultLogs(logs[:vaultLogs], after.position, percentage, burned); err != nil {
		return err
	}
	logs = logs[vaultLogs:]
	vaultWord := common.BytesToHash(e.roles.PositionVault[:])
	percent := common.BigToHash(new(big.Int).SetUint64(uint64(percentage)))
	if !matchesNFTEvent(logs[0], e.roles.Gateway, []common.Hash{e.gateway.Events["NFTPenaltyApplied"].ID, id},
		vaultWord, after.position.Asset.TokenID, percent, common.BigToHash(burned)) {
		return errors.New("isolated NFT penalty event differs from the exact Gateway transition")
	}
	return e.checkDELStakeLogs(logs[1:], after.companion.StakeID, after.coin, new(big.Int).Neg(burned), false)
}

func (e *Executor) verifyNFTPenaltyVaultLogs(logs []*ethtypes.Log, p nftview.PositionSnapshot, percentage uint16, burned *big.Int) error {
	burnWord := common.BigToHash(burned)
	vault := e.roles.PositionVault
	if burned.Sign() > 0 {
		topic := crypto.Keccak256Hash([]byte("NativeBurn(address,uint256)"))
		if !matchesNFTEvent(logs[0], common.HexToAddress(nativeburn.AddressString), []common.Hash{topic, common.BytesToHash(vault[:])}, burnWord) {
			return errors.New("isolated NFT penalty must burn native DEL from the authenticated Vault")
		}
		logs = logs[1:]
	}
	positionID, remaining := p.Asset.TokenID, p.ReserveAmount
	percent := common.BigToHash(new(big.Int).SetUint64(uint64(percentage)))
	quantity := common.BigToHash(new(big.Int).SetUint64(uint64(p.OriginQuantity)))
	if !matchesNFTEvent(logs[0], vault, []common.Hash{e.vault.Events["PositionPenalty"].ID, positionID}, percent, burnWord, remaining) ||
		!matchesNFTEvent(logs[1], vault, []common.Hash{e.vault.Events["PositionReserveUpdated"].ID, positionID}, smallWord(byte(nfttypes.IsolatedReserveDEL)), common.Hash{}, remaining, quantity) {
		return errors.New("isolated NFT penalty logs differ from the exact Vault transition")
	}
	return nil
}
