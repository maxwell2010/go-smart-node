package nativeexec

import (
	"errors"
	"math/big"
	"testing"

	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

type prepareFunc func(sdk.Context, Execution) (Prepared, error)

func (f prepareFunc) Prepare(ctx sdk.Context, e Execution) (Prepared, error) { return f(ctx, e) }

type finalFunc func(sdk.Context, Execution) error

func (f finalFunc) ValidateFinal(ctx sdk.Context, e Execution) error { return f(ctx, e) }

type plan struct {
	validate func(sdk.Context) error
	apply    func(sdk.Context) error
}

func (p *plan) Validate(ctx sdk.Context) error { return p.validate(ctx) }
func (p *plan) Apply(ctx sdk.Context) error    { return p.apply(ctx) }

func fixture() (sdk.Context, *storetypes.KVStoreKey, Execution) {
	key := storetypes.NewKVStoreKey("pipeline")
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("pipeline-t")).
		WithChainID("mintcandy_990001-1").WithBlockHeight(10)
	from := common.HexToAddress("0x1234")
	e := Execution{EntryPoint: SignedEthereum, Sender: from,
		Message: core.Message{From: from, Value: big.NewInt(1), GasLimit: 100000, Data: []byte{1},
			AccessList: ethtypes.AccessList{{Address: from, StorageKeys: []common.Hash{{1}}}}},
		Receipt: &ethtypes.Receipt{Status: 1, GasUsed: 21000, BlockNumber: big.NewInt(10),
			Logs: []*ethtypes.Log{{Address: from, Topics: []common.Hash{{1}}, Data: []byte{1}}}}}
	return ctx, key, e
}

func TestStagesValidateBeforeWritesAndCommitEventsOnce(t *testing.T) {
	ctx, key, e := fixture()
	var order []int
	prepared, validated := 0, 0
	var coordinator *Coordinator
	stages := [3]Preparer{}
	for i := range stages {
		stages[i] = prepareFunc(func(read sdk.Context, input Execution) (Prepared, error) {
			prepared++
			// Each component gets detached input and a disposable planning cache.
			require.Equal(t, byte(1), input.Message.Data[0])
			require.Equal(t, int64(1), input.Message.Value.Int64())
			require.Equal(t, byte(1), input.Receipt.Logs[0].Data[0])
			require.Equal(t, int64(10), input.Receipt.BlockNumber.Int64())
			require.Equal(t, byte(1), input.Message.AccessList[0].StorageKeys[0][0])
			input.Message.Data[0], input.Receipt.Logs[0].Data[0] = 9, 9
			input.Message.AccessList[0].StorageKeys[0][0] = 9
			input.Message.Value.SetInt64(9)
			input.Receipt.BlockNumber.SetInt64(9)
			read.KVStore(key).Set([]byte("draft"), []byte{1})
			return &plan{validate: func(read sdk.Context) error {
				require.Equal(t, 3, prepared)
				require.Empty(t, order)
				require.Nil(t, read.KVStore(key).Get([]byte("draft")))
				validated++
				return nil
			}, apply: func(write sdk.Context) error {
				require.Equal(t, 3, validated)
				require.Error(t, coordinator.Process(write, e), "ordinary Go stage cannot reenter the same execution")
				order = append(order, i)
				write.KVStore(key).Set([]byte{byte(i)}, []byte{1})
				write.EventManager().EmitEvent(sdk.NewEvent("native_stage"))
				return nil
			}}, nil
		})
	}
	final := finalFunc(func(read sdk.Context, input Execution) error {
		require.Equal(t, []int{0, 1, 2}, order)
		for i := range 3 {
			require.Equal(t, []byte{1}, read.KVStore(key).Get([]byte{byte(i)}))
		}
		require.Equal(t, byte(1), input.Receipt.Logs[0].Data[0])
		return nil
	})
	var err error
	coordinator, err = New(stages[0], stages[1], stages[2], final, Limits{MaxLogs: 8, MaxBytes: 4096, MaxCopyWeight: 65536})
	require.NoError(t, err)
	beforeGas := ctx.GasMeter().GasConsumed()
	require.NoError(t, coordinator.Process(ctx, e))
	require.Greater(t, ctx.GasMeter().GasConsumed(), beforeGas)
	require.Nil(t, ctx.KVStore(key).Get([]byte("draft")))
	require.Len(t, ctx.EventManager().Events(), 3, "cache commits must not duplicate native events")
	require.Equal(t, byte(1), e.Message.Data[0])
	require.Equal(t, int64(1), e.Message.Value.Int64())
	require.Equal(t, int64(10), e.Receipt.BlockNumber.Int64())
	require.Equal(t, byte(1), e.Message.AccessList[0].StorageKeys[0][0])
}

func TestValidationApplyFinalAndOOGFailuresNeverCommit(t *testing.T) {
	for _, mode := range []string{"validate", "apply", "final", "oog"} {
		t.Run(mode, func(t *testing.T) {
			ctx, key, e := fixture()
			ctx = ctx.WithGasMeter(storetypes.NewGasMeter(200000))
			applied := 0
			stage := prepareFunc(func(sdk.Context, Execution) (Prepared, error) {
				return &plan{validate: func(sdk.Context) error {
					if mode == "validate" {
						return errors.New("stale capture")
					}
					return nil
				}, apply: func(c sdk.Context) error {
					applied++
					c.KVStore(key).Set([]byte{byte(applied)}, []byte{1})
					c.EventManager().EmitEvent(sdk.NewEvent("discarded"))
					if applied == 2 && mode == "apply" {
						return errors.New("late ordinary write failure")
					}
					if applied == 2 && mode == "oog" {
						c.GasMeter().ConsumeGas(200000, "ordinary bounded work control")
					}
					return nil
				}}, nil
			})
			final := finalFunc(func(sdk.Context, Execution) error { return errors.New("final conservation rejected") })
			c, err := New(stage, stage, stage, final, Limits{MaxLogs: 8, MaxBytes: 4096, MaxCopyWeight: 65536})
			require.NoError(t, err)
			if mode == "oog" {
				require.Panics(t, func() { _ = c.Process(ctx, e) })
				require.True(t, ctx.GasMeter().IsOutOfGas())
			} else {
				require.Error(t, c.Process(ctx, e))
			}
			if mode == "validate" {
				require.Zero(t, applied)
			}
			// Verification reads only; the exhausted business meter stays exhausted.
			read := ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
			for i := 1; i <= 3; i++ {
				require.Nil(t, read.KVStore(key).Get([]byte{byte(i)}))
			}
			require.Empty(t, ctx.EventManager().Events())
		})
	}
}

func TestRoutesBoundsAndIndependentEmptyHashExecutions(t *testing.T) {
	ctx, key, e := fixture()
	called := 0
	stage := prepareFunc(func(sdk.Context, Execution) (Prepared, error) {
		called++
		return &plan{validate: func(sdk.Context) error { return nil }, apply: func(c sdk.Context) error {
			previous := c.KVStore(key).Get([]byte("count"))
			if previous == nil {
				previous = []byte{0}
			}
			c.KVStore(key).Set([]byte("count"), []byte{previous[0] + 1})
			return nil
		}}, nil
	})
	c, err := New(stage, stage, stage, finalFunc(func(sdk.Context, Execution) error { return nil }), Limits{MaxLogs: 1, MaxBytes: 4096, MaxCopyWeight: 65536})
	require.NoError(t, err)
	for _, route := range []EntryPoint{Unspecified, NativeSDK, IBC} {
		input := e
		input.EntryPoint = route
		require.Error(t, c.Process(ctx, input))
	}
	input := e
	input.Receipt = &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{{}, {}}}
	require.Error(t, c.Process(ctx, input))
	input = e
	input.Message.Data = make([]byte, 4097)
	require.Error(t, c.Process(ctx, input))
	memoryLimited, err := New(stage, stage, stage, finalFunc(func(sdk.Context, Execution) error { return nil }), Limits{MaxLogs: 1, MaxBytes: 4096, MaxCopyWeight: 2048})
	require.NoError(t, err)
	require.Error(t, memoryLimited.Process(ctx, e), "copy containers have a separate logical weight bound")
	var unconfigured Coordinator
	require.Error(t, unconfigured.Process(ctx, e))
	wrongHeight := cloneExecution(e)
	wrongHeight.Receipt.BlockNumber.SetInt64(ctx.BlockHeight() + 1)
	require.Error(t, c.Process(ctx, wrongHeight))
	wrongHash := cloneExecution(e)
	wrongHash.Receipt.BlockHash = common.Hash{1}
	require.Error(t, c.Process(ctx, wrongHash))
	require.Zero(t, called)
	input = e
	input.Receipt = &ethtypes.Receipt{Status: 0}
	require.NoError(t, c.Process(ctx, input))
	require.Zero(t, called)
	for range 2 {
		require.NoError(t, c.GovernanceHooks().PostTxProcessing(ctx, e.Sender, e.Message, e.Receipt))
	}
	require.Equal(t, []byte{6}, ctx.KVStore(key).Get([]byte("count")), "empty transaction hash is not a deduplication key")
	require.Equal(t, common.Hash{}, e.Receipt.TxHash)
	var absent *plan
	nilStage := prepareFunc(func(sdk.Context, Execution) (Prepared, error) { return absent, nil })
	c, err = New(nilStage, stage, stage, finalFunc(func(sdk.Context, Execution) error { return nil }), Limits{MaxLogs: 1, MaxBytes: 4096, MaxCopyWeight: 65536})
	require.NoError(t, err)
	require.Error(t, c.Process(ctx, e))
}
