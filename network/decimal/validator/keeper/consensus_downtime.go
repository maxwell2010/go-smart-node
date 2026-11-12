package keeper

import (
	"errors"
	"fmt"
	"time"

	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// NativeDowntimeCandidate reports a full signing-window violation. The
// infraction height closes that window and need not itself contain a miss.
// ObservedPower is the power at that height in the actual prior commit.
type NativeDowntimeCandidate struct {
	Operator         string
	ConsensusAddress sdk.ConsAddress
	InfractionHeight int64
	ObservedPower    int64
	Window           SigningWindow
	SlashFraction    sdkmath.LegacyDec
	JailUntil        time.Time
}

// PlanNativeDowntimePenalties inspects only the actual H-1 validator set,
// after RecordABCICommit has stored that observation in FinalizeBlock H.
// It is read-only and may return the same candidates when called again in
// the same block. The caller owns any later execution, deduplication and
// signing-window reset. JailUntil equals the block time for a zero duration.
func (k *StateKeeper) PlanNativeDowntimePenalties(ctx sdk.Context) ([]NativeDowntimeCandidate, error) {
	observed, err := k.VerifyABCICommit(ctx)
	if err != nil {
		return nil, err
	}
	progress, err := k.signingProgress(ctx)
	if err != nil {
		return nil, err
	}
	if progress != ctx.BlockHeight() {
		return nil, errors.New("native downtime planning requires the recorded prior commit")
	}
	if len(observed.Votes) == 0 {
		return nil, nil
	}
	params, err := k.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	if err := ValidateSigningWindowSize(params.SignedBlocksWindow); err != nil {
		return nil, err
	}
	// Compare signed atoms directly to size * ratio. Multiplying the
	// decimal threshold by an integer preserves its full precision.
	minimum := params.MinSignedPerWindow.Mul(sdkmath.LegacyNewDec(params.SignedBlocksWindow))
	var candidates []NativeDowntimeCandidate
	for _, vote := range observed.Votes {
		key, err := operatorKey(signingWindowPrefix, vote.Operator)
		if err != nil {
			return nil, err
		}
		window, err := k.readSigningWindow(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("read native downtime window for %s: %w", vote.Operator, err)
		}
		if window.LastHeight != observed.Height || int64(window.Size) != params.SignedBlocksWindow {
			return nil, errors.New("native downtime window differs from the recorded prior commit")
		}
		latest := (window.next + window.Size - 1) % window.Size
		missed := window.misses[latest/8]&(1<<(latest%8)) != 0
		if missed != (vote.Flag != cmtproto.BlockIDFlagCommit) {
			return nil, errors.New("native downtime window differs from the recorded vote flag")
		}
		signed := sdkmath.LegacyNewDec(int64(window.Samples - window.Missed))
		if window.Samples < window.Size || !signed.LT(minimum) {
			continue
		}
		validator, err := k.GetValidator(ctx, vote.Operator)
		if err != nil {
			return nil, err
		}
		if validator.Jailed {
			continue
		}
		consensus, err := validator.GetConsAddr()
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, NativeDowntimeCandidate{
			Operator: vote.Operator, ConsensusAddress: consensus, InfractionHeight: observed.Height,
			ObservedPower: vote.Power, Window: window.SigningWindow, SlashFraction: params.SlashFractionDowntime,
			JailUntil: ctx.BlockTime().Add(params.DowntimeJailDuration).UTC(),
		})
	}
	return candidates, nil
}
