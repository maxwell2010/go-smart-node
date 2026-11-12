package keeper

import (
	"bytes"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

func TestNativeDowntimePlanRequiresFullAuthenticatedWindow(t *testing.T) {
	for _, test := range []struct {
		name      string
		size      int64
		minimum   string
		duration  time.Duration
		violation bool
	}{
		{"exact ratio below minimum", 3, "0.4", 45 * time.Second, true},
		{"equality meets minimum", 4, "0.5", 0, false},
		{"zero default jail duration", 3, "0.4", types.DefaultParams().DowntimeJailDuration, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, _, k := environment(t)
			g := genesis(t)
			g.Params.SignedBlocksWindow = test.size
			g.Params.MinSignedPerWindow = sdkmath.LegacyMustNewDecFromStr(test.minimum)
			g.Params.DowntimeJailDuration = test.duration
			require.NoError(t, k.InitGenesis(ctx, g))
			set, err := ConsensusSet(g.Validators)
			require.NoError(t, err)
			require.NoError(t, k.BootstrapABCIConsensus(ctx, 1, g.Validators, initialABCIUpdates(t, set)))
			delayed := g.Validators[0]
			consensus, err := delayed.GetConsAddr()
			require.NoError(t, err)
			eligible := []string{delayed.OperatorAddress, g.Validators[1].OperatorAddress}
			var latest sdk.Context
			for h := int64(1); h <= test.size+1; h++ {
				commit := cometPreviousCommit(h, 1, set)
				for i := range commit.Votes {
					if !bytes.Equal(commit.Votes[i].Validator.Address, consensus) {
						continue
					}
					switch h {
					case 2:
						commit.Votes[i].BlockIdFlag = cmtproto.BlockIDFlagAbsent
					case 3:
						commit.Votes[i].BlockIdFlag = cmtproto.BlockIDFlagNil
					}
				}
				latest = ctx.WithExecMode(sdk.ExecModeFinalize).WithBlockHeader(cmtproto.Header{
					ChainID: "native-downtime-test", Height: h, Time: time.Unix(2000+h, 0).UTC(),
					NextValidatorsHash: set.Hash(), ProposerAddress: set.Validators[0].Address,
				}).WithHeaderHash(bytes.Repeat([]byte{byte(h)}, 32)).WithVoteInfos(commit.Votes)
				_, err = k.PlanNativeDowntimePenalties(latest)
				require.ErrorContains(t, err, "requires the recorded prior commit")
				_, err = k.RecordABCICommit(latest)
				require.NoError(t, err)
				if h <= test.size {
					plan, err := k.PlanNativeDowntimePenalties(latest)
					require.NoError(t, err)
					require.Empty(t, plan)
					_, err = k.ScheduledABCIEndBlock(latest, eligible)
					require.NoError(t, err)
				}
			}
			plan, err := k.PlanNativeDowntimePenalties(latest)
			require.NoError(t, err)
			if !test.violation {
				require.Empty(t, plan)
				return
			}
			window := SigningWindow{LastHeight: test.size, Size: uint16(test.size), Samples: uint16(test.size), Missed: 2}
			require.Equal(t, []NativeDowntimeCandidate{{
				Operator: delayed.OperatorAddress, ConsensusAddress: consensus, InfractionHeight: test.size,
				ObservedPower: delayed.Stake, Window: window, SlashFraction: g.Params.SlashFractionDowntime,
				JailUntil: latest.BlockTime().Add(test.duration),
			}}, plan)

			// The prior effective set remains authoritative even after a
			// change to current power/status. Jail suppresses a new proposal.
			delayed.Stake, delayed.Status = 1, types.BondStatus_Unbonded
			require.NoError(t, k.SetValidator(latest, delayed))
			retry, err := k.PlanNativeDowntimePenalties(latest)
			require.NoError(t, err)
			require.Equal(t, plan, retry)
			current, err := k.GetSigningWindow(latest, delayed.OperatorAddress)
			require.NoError(t, err)
			require.Equal(t, window, current)
			delayed.Jailed = true
			require.NoError(t, k.SetValidator(latest, delayed))
			jailed, err := k.PlanNativeDowntimePenalties(latest)
			require.NoError(t, err)
			require.Empty(t, jailed)

			wrongVotes := append([]abci.VoteInfo(nil), latest.VoteInfos()...)
			wrongVotes[0].Validator.Power++
			_, err = k.PlanNativeDowntimePenalties(latest.WithVoteInfos(wrongVotes))
			require.ErrorContains(t, err, "prior validator address or power")
			wrongVotes[0].Validator.Power--
			wrongVotes[0].BlockIdFlag = cmtproto.BlockIDFlagAbsent
			_, err = k.PlanNativeDowntimePenalties(latest.WithVoteInfos(wrongVotes))
			require.ErrorContains(t, err, "differs from the recorded vote flag")
		})
	}
}
