package inbound

import (
	"context"
	"testing"

	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/maxwell2010/go-smart-node/network/bridge/ledger"
	"github.com/maxwell2010/go-smart-node/network/bridge/workgas"
)

type gasObservation struct {
	before     uint64
	amount     uint64
	descriptor string
}

// Recording wraps the actual SDK meter. It does not replace gas calculations
// or refund consumed gas when the keeper discards its state cache.
type recordingInboundMeter struct {
	storetypes.GasMeter
	charges []gasObservation
}

func (meter *recordingInboundMeter) ConsumeGas(amount uint64, descriptor string) {
	meter.charges = append(meter.charges, gasObservation{before: meter.GasConsumed(), amount: amount, descriptor: descriptor})
	meter.GasMeter.ConsumeGas(amount, descriptor)
}

const afterBankSendDescriptor = "test/inbound/after-real-bank-send"

// The real SDK bank mutation completes in the transaction's child context
// before this test-only charge. This is not a fake bank or production callback.
type gasAfterRealBankSend struct {
	ledger.BankKeeper
	staged *bool
}

func (bank gasAfterRealBankSend) SendCoinsFromModuleToAccount(ctx context.Context, module string,
	recipient sdk.AccAddress, coins sdk.Coins) error {
	if err := bank.BankKeeper.SendCoinsFromModuleToAccount(ctx, module, recipient, coins); err != nil {
		return err
	}
	*bank.staged = true
	sdk.UnwrapSDKContext(ctx).GasMeter().ConsumeGas(1, afterBankSendDescriptor)
	return nil
}

func gasTestEnvironment(t *testing.T, withUpdate, afterSend bool) (environment, sourceFixture, *bool) {
	t.Helper()
	env, source := openEnvironment(t, ""), sourceProof(t)
	t.Cleanup(func() { require.NoError(t, env.db.Close()) })
	initFixture(t, env, source)
	if !withUpdate {
		_, err := env.clients.Update(env.ctx, "decimal-mainnet", source.candidate)
		require.NoError(t, err)
	}
	staged := new(bool)
	if afterSend {
		deposits, err := ledger.NewKeeper(runtime.NewKVStoreService(env.serviceKey), gasAfterRealBankSend{env.bank, staged})
		require.NoError(t, err)
		env.inbound, err = NewKeeper(runtime.NewKVStoreService(env.serviceKey), env.clients, deposits)
		require.NoError(t, err)
	}
	return env, source, staged
}

func gasTestReceive(env environment, source sourceFixture, ctx sdk.Context, withUpdate bool) error {
	if withUpdate {
		return env.inbound.ReceiveWithUpdate(ctx, "decimal-del", source.candidate, source.deposit,
			10, source.accountAny, source.accountProof, source.proof)
	}
	return env.inbound.Receive(ctx, "decimal-del", source.deposit,
		10, source.accountAny, source.accountProof, source.proof)
}

// Find semantic CPU/bank stages in a successful real trace. No test copies
// workgas's tariff arithmetic or assumes a specific SDK storage gas price.
func gasStage(t *testing.T, charges []gasObservation, stage string) gasObservation {
	t.Helper()
	if stage == "entry" {
		require.NotEmpty(t, charges)
		require.Equal(t, workgas.TariffVersion+"/deposit/base", charges[0].descriptor)
		return charges[0]
	}
	if stage == "after-bank-send" {
		for _, charge := range charges {
			if charge.descriptor == afterBankSendDescriptor {
				return charge
			}
		}
	}
	index := 0
	for _, charge := range charges {
		if charge.descriptor == workgas.TariffVersion+"/membership/reserved-work" {
			index++
			if (stage == "before-account" && index == 1) || (stage == "before-storage" && index == 2) {
				return charge
			}
		}
	}
	t.Fatalf("successful receive trace did not contain stage %q", stage)
	return gasObservation{}
}

func captureGasPanic(action func() error) (panicValue any, returned error) {
	defer func() { panicValue = recover() }()
	returned = action()
	return
}

func TestFiniteGasFailureDoesNotCommitOrRefundAndRetryMintsOnce(t *testing.T) {
	for _, withUpdate := range []bool{false, true} {
		method := "Receive"
		if withUpdate {
			method = "ReceiveWithUpdate"
		}
		for _, stage := range []string{"entry", "before-account", "before-storage", "after-bank-send"} {
			t.Run(method+"/"+stage, func(t *testing.T) {
				afterSend := stage == "after-bank-send"
				baseline, baselineSource, baselineStaged := gasTestEnvironment(t, withUpdate, afterSend)
				trace := &recordingInboundMeter{GasMeter: storetypes.NewInfiniteGasMeter()}
				require.NoError(t, gasTestReceive(baseline, baselineSource, baseline.ctx.WithGasMeter(trace), withUpdate))
				if stage == "entry" {
					t.Logf("%s current four-validator fixture baseline SDK gas=%d", method, trace.GasConsumed())
				}
				target := gasStage(t, trace.charges, stage)
				require.Positive(t, target.amount)
				if afterSend {
					require.True(t, *baselineStaged)
				}
				// A successful baseline determines a finite budget failing at this
				// exact semantic stage, rather than a brittle hardcoded gas count.
				limit := target.before + target.amount - 1
				env, source, staged := gasTestEnvironment(t, withUpdate, afterSend)
				before, err := env.clients.LatestCheckpoint(env.ctx, "decimal-mainnet")
				require.NoError(t, err)
				beforeEvents := len(env.ctx.EventManager().Events())
				meter := &recordingInboundMeter{GasMeter: storetypes.NewGasMeter(limit)}
				panicValue, returned := captureGasPanic(func() error {
					return gasTestReceive(env, source, env.ctx.WithGasMeter(meter), withUpdate)
				})
				require.Nil(t, returned, "out of gas must propagate as the SDK panic, not a success or ordinary return")
				require.IsType(t, storetypes.ErrorOutOfGas{}, panicValue)
				require.Equal(t, target.descriptor, panicValue.(storetypes.ErrorOutOfGas).Descriptor)
				require.Greater(t, meter.GasConsumed(), limit, "discarding state must not refund already consumed work gas")
				consumed := meter.GasConsumed()
				if afterSend {
					require.True(t, *staged, "the real bank send must precede the injected gas failure")
				} else {
					require.False(t, *staged)
				}
				after, err := env.clients.LatestCheckpoint(env.ctx, "decimal-mainnet")
				require.NoError(t, err)
				require.Equal(t, before, after, "failed receive must not advance its stored source checkpoint")
				assertMint(t, env, source, 0)
				require.Len(t, env.ctx.EventManager().Events(), beforeEvents)
				require.Equal(t, consumed, meter.GasConsumed())
				// The exhausted meter itself remains exhausted. Retrying with a
				// new funded finite meter must use the same durable operation ID.
				retry := storetypes.NewGasMeter(trace.GasConsumed() * 2)
				require.NoError(t, gasTestReceive(env, source, env.ctx.WithGasMeter(retry), withUpdate))
				assertMint(t, env, source, 25)
				require.Positive(t, retry.GasConsumed())
				require.Less(t, retry.GasConsumed(), retry.Limit())
				checkpoint, err := env.clients.LatestCheckpoint(env.ctx, "decimal-mainnet")
				require.NoError(t, err)
				require.EqualValues(t, 11, checkpoint.Height)
				duplicate := storetypes.NewGasMeter(trace.GasConsumed() * 2)
				require.ErrorIs(t, env.inbound.Receive(env.ctx.WithGasMeter(duplicate), "decimal-del", source.deposit,
					10, source.accountAny, source.accountProof, source.proof), ledger.ErrAlreadyProcessed)
				assertMint(t, env, source, 25)
				require.Equal(t, consumed, meter.GasConsumed(), "a successful retry must not reset the prior meter")
			})
		}
	}
}

// This is the gas budget of the current isolated four-validator fixture, not
// a guarantee that every allowed MaxValidators=1000 proof fits one block.
// Larger valid inputs may be refused by the finite meter before verification.
func TestCurrentInboundFixtureFitsFiniteTwentyFourMillionGas(t *testing.T) {
	for _, withUpdate := range []bool{false, true} {
		method := "Receive"
		if withUpdate {
			method = "ReceiveWithUpdate"
		}
		t.Run(method, func(t *testing.T) {
			env, source, _ := gasTestEnvironment(t, withUpdate, false)
			meter := storetypes.NewGasMeter(24_000_000)
			var err error
			require.NotPanics(t, func() {
				err = gasTestReceive(env, source, env.ctx.WithGasMeter(meter), withUpdate)
			})
			require.NoError(t, err)
			require.Positive(t, meter.GasConsumed())
			require.LessOrEqual(t, meter.GasConsumed(), meter.Limit())
			assertMint(t, env, source, 25)
			checkpoint, err := env.clients.LatestCheckpoint(env.ctx, "decimal-mainnet")
			require.NoError(t, err)
			require.EqualValues(t, 11, checkpoint.Height)
			t.Logf("%s current four-validator fixture SDK gas=%d finite budget=%d", method, meter.GasConsumed(), meter.Limit())
		})
	}
}
