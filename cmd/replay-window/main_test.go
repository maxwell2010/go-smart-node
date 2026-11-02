package main

import (
	tmtypes "github.com/tendermint/tendermint/types"
	"testing"
)

func TestCometLastCommitVoteSemantics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		flag   tmtypes.BlockIDFlag
		signed bool
	}{{"commit", tmtypes.BlockIDFlagCommit, true}, {"nil vote", tmtypes.BlockIDFlagNil, true}, {"absent", tmtypes.BlockIDFlagAbsent, false}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := signedLastBlock(tmtypes.CommitSig{BlockIDFlag: tc.flag}); got != tc.signed {
				t.Fatalf("SignedLastBlock=%v, want%v", got, tc.signed)
			}
		})
	}
}
