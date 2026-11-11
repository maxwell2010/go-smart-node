package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	coinkeeper "github.com/maxwell2010/go-smart-node/network/decimal/coin/keeper"
	nftview "github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	nftreceipts "github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/withdrawalassets"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

// WithdrawalProfileKey is reserved in the native validator store for the
// canonical profile installed by an authenticated financial bootstrap.
const WithdrawalProfileKey byte = 0x6f

const (
	withdrawalAttempts   = 2
	withdrawalAttemptGas = 15_000_000
	withdrawalMaxLogs    = 32
	withdrawalLogBytes   = 16384
)

// WithdrawalProfile pins the system addresses for the isolated-position profile.
// It is read from consensus state, never from node-local operator settings.
type WithdrawalProfile struct {
	Registry        common.Address `json:"registry"`
	Gateway         common.Address `json:"gateway"`
	Library         common.Address `json:"library"`
	NFTCenter       common.Address `json:"nft_center"`
	PositionVault   common.Address `json:"position_vault"`
	MasterValidator common.Address `json:"master_validator"`
	WrappedDEL      common.Address `json:"wrapped_del"`
}

type withdrawalController struct {
	profile  WithdrawalProfile
	abi      abi.ABI
	topic    common.Hash
	keeper   *validatorkeeper.StateKeeper
	executor *settlement.Executor
	assets   *withdrawalassets.Projector
}

// LoadWithdrawalSettlement wires the profile after store load. An absent
// profile leaves default networks unchanged. Only the financial bootstrap or
// a coordinated state upgrade may install the profile in the validator store.
func (app *App) LoadWithdrawalSettlement(ctx sdk.Context) error {
	if ctx.ChainID() != app.chainID {
		return errors.New("withdrawal profile context differs from the application chain")
	}
	raw := ctx.KVStore(app.keys[validatortypes.StoreKey]).Get([]byte{WithdrawalProfileKey})
	if raw == nil && app.withdrawals == nil {
		return nil
	}
	var profile WithdrawalProfile
	if len(raw) > 1024 || json.Unmarshal(raw, &profile) != nil {
		return errors.New("invalid stored withdrawal profile")
	}
	canonical, err := json.Marshal(profile)
	if err != nil || !bytes.Equal(canonical, raw) {
		return errors.New("noncanonical stored withdrawal profile")
	}
	if app.withdrawals != nil {
		if app.withdrawals.profile != profile {
			return errors.New("withdrawal profile changed without a coordinated restart")
		}
		return nil
	}
	reader, err := nftview.New(app.NativeNFTKeeper, app.EVMKeeper, nftview.Config{
		Protocol: nftview.ProfileIsolatedPositionsV1, ChainID: app.chainID, EVMChainID: app.evmChainID,
		Registry: profile.Registry, Delegation: profile.Gateway, DelegationLibrary: profile.Library,
		NFTCenter: profile.NFTCenter, PositionVault: profile.PositionVault, MasterValidator: profile.MasterValidator,
		WrappedDEL: profile.WrappedDEL, EVMGas: 250_000, SDKGas: 5_000_000, MaxOwners: 4, MaxQuantity: 10000,
	})
	if err != nil {
		return err
	}
	executor, err := settlement.NewExecutor(app.EVMKeeper, reader, settlement.Config{
		EVMGas: 750_000, MaxLogs: withdrawalMaxLogs, MaxLogBytes: withdrawalLogBytes,
	})
	if err != nil {
		return err
	}
	coinABI, err := contracts.CompiledABI("DRC20")
	if err != nil {
		return err
	}
	// This profile settles DEL and receipts backed by DEL. A recognized
	// custom-coin reserve mutation would require a separate admitted reader.
	reserve, err := coinkeeper.NewReserveHook(app.NativeCoinKeeper, coinABI,
		func(sdk.Context, common.Address) (sdkmath.Int, sdkmath.Int, error) {
			return sdkmath.Int{}, sdkmath.Int{}, errors.New("custom reserve mutation is outside the withdrawal profile")
		}, withdrawalMaxLogs)
	if err != nil {
		return err
	}
	projector, err := withdrawalassets.New(reserve, app.NativeNFTKeeper, reader, app.NativeValidatorKeeper, nftreceipts.Limits{
		MaxLogs: withdrawalMaxLogs, MaxBytes: withdrawalLogBytes, MaxAssets: 1, MaxIDs: 1, MaxUnits: 1,
	})
	if err != nil {
		return err
	}
	gatewayABI, err := contracts.CompiledABI("MintCandyIsolatedDelegation")
	if err != nil {
		return err
	}
	controller := &withdrawalController{
		profile: profile, abi: gatewayABI, topic: gatewayABI.Events["WithdrawCompleted"].ID,
		keeper: app.NativeValidatorKeeper, executor: executor, assets: projector,
	}
	app.EVMKeeper.SetInternalCommitGuard(controller.guardInternalCommit)
	app.EVMKeeper.AppendHooks(controller)
	app.withdrawals = controller
	return nil
}

func (c *withdrawalController) hasCompletion(ctx sdk.Context, logs []*ethtypes.Log) bool {
	ctx.GasMeter().ConsumeGas(uint64(len(logs))*5, "withdrawal receipt routing")
	for _, log := range logs {
		if log != nil && log.Address == c.profile.Gateway && len(log.Topics) != 0 && log.Topics[0] == c.topic {
			return true
		}
	}
	return false
}

func (c *withdrawalController) checkMessage(msg core.Message, index common.Hash) error {
	expected, err := c.abi.Pack("complete", []*big.Int{index.Big()})
	if err != nil {
		return err
	}
	if msg.To == nil || *msg.To != c.profile.Gateway || msg.Value == nil || msg.Value.Sign() != 0 ||
		len(msg.SetCodeAuthorizations) != 0 || !bytes.Equal(expected, msg.Data) {
		return errors.New("withdrawal requires a direct completion of one admitted Gateway ID")
	}
	return nil
}

func (c *withdrawalController) isCompletionCall(msg core.Message) bool {
	return msg.To != nil && *msg.To == c.profile.Gateway && len(msg.Data) >= 4 &&
		bytes.Equal(msg.Data[:4], c.abi.Methods["complete"].ID)
}

func (c *withdrawalController) PostTxProcessing(ctx sdk.Context, sender common.Address, msg core.Message, receipt *ethtypes.Receipt) error {
	if receipt == nil || receipt.Status != ethtypes.ReceiptStatusSuccessful ||
		(!c.hasCompletion(ctx, receipt.Logs) && !c.isCompletionCall(msg)) {
		return nil
	}
	ids, err := c.executor.CompletionIDs(receipt)
	if err != nil {
		return err
	}
	if sender != msg.From || len(ids) != 1 {
		return errors.New("signed withdrawal must settle one ID")
	}
	if err = c.checkMessage(msg, ids[0]); err != nil {
		return err
	}
	row, err := c.keeper.GetEVMFrozenBinding(ctx, c.profile.Gateway.Bytes(), ids[0].Bytes())
	if err != nil {
		return fmt.Errorf("load signed withdrawal: %w", err)
	}
	proof, err := c.executor.ValidateSigned(ctx, row, receipt)
	if err != nil {
		return err
	}
	return c.project(ctx, row, proof)
}

type automaticWithdrawalKey struct{}

func (c *withdrawalController) guardInternalCommit(ctx sdk.Context, msg core.Message, logs []*ethtypes.Log) error {
	if !c.hasCompletion(ctx, logs) && !c.isCompletionCall(msg) {
		return nil
	}
	id, ok := ctx.Value(automaticWithdrawalKey{}).(common.Hash)
	if !ok || msg.From != settlement.Sender() {
		return errors.New("internal withdrawal requires the block settlement handler")
	}
	ids, err := c.executor.CompletionIDs(&ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: logs})
	if err != nil {
		return err
	}
	if len(ids) != 1 || ids[0] != id {
		return errors.New("internal withdrawal receipt differs from the scheduled ID")
	}
	return c.checkMessage(msg, id)
}

func (c *withdrawalController) settle(ctx sdk.Context) error {
	_, err := c.keeper.SettleMatureWithdrawalsLimit(ctx, withdrawalAttempts, withdrawalAttemptGas,
		func(child sdk.Context, row validatortypes.EVMFrozenBinding) error {
			child = child.WithValue(automaticWithdrawalKey{}, common.BytesToHash(row.FrozenIndex))
			return c.executor.Complete(child, row, func(projectCtx sdk.Context, proof settlement.Proof) error {
				return c.project(projectCtx, row, proof)
			})
		})
	return err
}

func (c *withdrawalController) project(ctx sdk.Context, row validatortypes.EVMFrozenBinding, proof settlement.Proof) error {
	if proof.Source.Gateway != c.profile.Gateway || proof.Index != common.BytesToHash(row.FrozenIndex) {
		return errors.New("withdrawal proof differs from native binding")
	}
	child, commit := ctx.CacheContext()
	if err := c.assets.Project(child, row, proof.Receipt); err != nil {
		return fmt.Errorf("project withdrawal assets: %w", err)
	}
	if err := c.keeper.ApplyWithdrawalCompletion(child, row); err != nil {
		return fmt.Errorf("project native withdrawal: %w", err)
	}
	commit()
	return nil
}
