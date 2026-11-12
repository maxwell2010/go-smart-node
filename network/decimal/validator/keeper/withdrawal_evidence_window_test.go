package keeper

import (
	"math"
	"testing"
	"time"
)

func TestWithdrawalEvidenceWindowElapsed(t *testing.T) {
	request := CommittedEvidenceBlock{Height: 100, Time: time.Unix(1_800_000_000, 123).UTC()}
	const maxBlocks = 100
	const maxDuration = 48 * time.Hour

	tests := []struct {
		name     string
		blockAge int64
		timeAge  time.Duration
		want     bool
	}{
		{name: "first block after request"},
		{name: "both ages below limit", blockAge: 1, timeAge: time.Second},
		{name: "both ages at limit", blockAge: maxBlocks, timeAge: maxDuration},
		{name: "height at limit", blockAge: maxBlocks, timeAge: maxDuration + time.Nanosecond},
		{name: "time at limit", blockAge: maxBlocks + 1, timeAge: maxDuration},
		{name: "long wall time on slow chain", blockAge: 1, timeAge: 28 * 24 * time.Hour},
		{name: "many blocks in short time", blockAge: maxBlocks * 2, timeAge: time.Second},
		{name: "both limits exceeded", blockAge: maxBlocks + 1, timeAge: maxDuration + time.Nanosecond, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			previous := CommittedEvidenceBlock{
				Height: request.Height + test.blockAge,
				Time:   request.Time.Add(test.timeAge),
			}
			got, err := WithdrawalEvidenceWindowElapsed(previous.Height+1, previous, request, maxBlocks, maxDuration)
			if err != nil || got != test.want {
				t.Fatalf("got (%t, %v), want (%t, nil)", got, err, test.want)
			}
		})
	}
}

func TestWithdrawalEvidenceWindowElapsedRejectsInconsistentMetadata(t *testing.T) {
	request := CommittedEvidenceBlock{Height: 8, Time: time.Unix(1_800_000_000, 123).UTC()}
	previous := CommittedEvidenceBlock{Height: 10, Time: request.Time.Add(time.Hour)}

	tests := []struct {
		name      string
		execution int64
		previous  CommittedEvidenceBlock
		request   CommittedEvidenceBlock
	}{
		{name: "no committed previous block", execution: 1, request: request},
		{name: "previous height does not match execution", execution: 12, previous: previous, request: request},
		{name: "missing previous time", execution: 11, previous: CommittedEvidenceBlock{Height: 10}, request: request},
		{name: "missing request height", execution: 11, previous: previous, request: CommittedEvidenceBlock{Time: request.Time}},
		{name: "missing request time", execution: 11, previous: previous, request: CommittedEvidenceBlock{Height: 8}},
		{name: "request still in execution block", execution: 11, previous: previous, request: CommittedEvidenceBlock{Height: 11, Time: previous.Time.Add(time.Second)}},
		{name: "same height with different time", execution: 11, previous: previous, request: CommittedEvidenceBlock{Height: 10, Time: request.Time}},
		{name: "equal times at different heights", execution: 11, previous: previous, request: CommittedEvidenceBlock{Height: 8, Time: previous.Time}},
		{name: "request time after previous block", execution: 11, previous: previous, request: CommittedEvidenceBlock{Height: 8, Time: previous.Time.Add(time.Nanosecond)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := WithdrawalEvidenceWindowElapsed(test.execution, test.previous, test.request, 1, time.Second)
			if err == nil || got {
				t.Fatalf("got (%t, %v), want (false, error)", got, err)
			}
		})
	}
}

func TestWithdrawalEvidenceWindowElapsedLimitValidation(t *testing.T) {
	request := CommittedEvidenceBlock{Height: 1, Time: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
	previous := CommittedEvidenceBlock{Height: math.MaxInt64 - 1, Time: request.Time.AddDate(400, 0, 0)}
	got, err := WithdrawalEvidenceWindowElapsed(math.MaxInt64, previous, request, 0, time.Second)
	if err == nil || got {
		t.Fatalf("invalid block limit returned (%t, %v)", got, err)
	}
	got, err = WithdrawalEvidenceWindowElapsed(math.MaxInt64, previous, request, 1, 0)
	if err == nil || got {
		t.Fatalf("invalid duration limit returned (%t, %v)", got, err)
	}
	got, err = WithdrawalEvidenceWindowElapsed(math.MaxInt64, previous, request, 1, time.Duration(math.MaxInt64))
	if err != nil || got {
		t.Fatalf("saturated duration returned (%t, %v), want (false, nil)", got, err)
	}
}
