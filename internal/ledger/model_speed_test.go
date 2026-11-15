package ledger

import (
	"context"
	"math"
	"testing"
	"time"

	"ferry/internal/domain"
)

func checkModelSpeed(t *testing.T, got ModelSpeed, samples int64, want float64) {
	t.Helper()
	if got.SpeedSamples != samples {
		t.Fatalf("samples=%d want=%d", got.SpeedSamples, samples)
	}
	if samples == 0 {
		if got.OutputTokensPerSecond != nil {
			t.Fatalf("missing speed fabricated: %v", *got.OutputTokensPerSecond)
		}
		return
	}
	if got.OutputTokensPerSecond == nil || math.Abs(*got.OutputTokensPerSecond-want) > 1e-9 {
		t.Fatalf("speed=%v want=%v", got.OutputTokensPerSecond, want)
	}
}

func TestModelSpeedWeightedRecoveryAndProviderIsolation(t *testing.T) {
	l := openTemp(t)
	from := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	add := func(id, logical, provider string, at time.Time, output, duration, attemptDuration int64, recovered bool) {
		n := 1
		outcome := domain.OutcomeDirectSuccess
		if recovered {
			n = 2
			outcome = domain.OutcomeRecoveredSuccess
		}
		l.BeginRequest(domain.RequestRecord{ID: id, Ts: at, LogicalModelID: logical, ProviderID: provider, UpstreamModel: "decoy-final-model", Status: domain.ReqSuccess, Outcome: outcome, DurationMS: duration, TTFTMS: duration - 1, Attempts: n, Usage: domain.Usage{OutputTokens: output}})
		if recovered {
			l.AddAttempt(domain.AttemptRecord{RequestID: id, Seq: 1, Ts: at, ProviderID: "provider-a", Model: "actual", Status: domain.ReqFailed, LatencyMS: 5000, UsageState: "partial", Usage: domain.Usage{OutputTokens: 9000}})
		}
		// Attempt timestamp lies outside the window: the cohort uses request start.
		l.AddAttempt(domain.AttemptRecord{RequestID: id, Seq: n, Ts: to.Add(time.Hour), ProviderID: provider, Model: "actual", Status: domain.ReqSuccess, LatencyMS: attemptDuration, TTFTMS: attemptDuration - 1, UsageState: "known", Usage: domain.Usage{OutputTokens: output}})
		l.FinishRequest(id, nil)
	}
	add("fast", "logical", "provider-a", from, 100, 1000, 500, false)
	add("slow", "logical", "provider-a", to.Add(-time.Millisecond), 100, 9000, 1500, false)
	add("recovered", "logical", "provider-a", from.Add(time.Minute), 60, 6000, 1000, true)
	add("same-model-other-provider", "other", "provider-b", from, 500, 1000, 1000, false)
	add("other-logical-same-upstream", "alias", "provider-a", from, 40, 2000, 1000, false)
	add("before", "logical", "provider-a", from.Add(-time.Millisecond), 9999, 1, 1, false)
	add("end", "logical", "provider-a", to, 9999, 1, 1, false)
	l.BeginIngressRequest(domain.RequestRecord{ID: "live", Ts: from, LogicalModelID: "logical", Status: "streaming", Usage: domain.Usage{OutputTokens: 9999}, DurationMS: 1})
	l.AddAttempt(domain.AttemptRecord{RequestID: "live", Seq: 1, Ts: from, ProviderID: "provider-a", Model: "actual", Status: domain.ReqSuccess, UsageState: "known", LatencyMS: 1, Usage: domain.Usage{OutputTokens: 9999}})
	flushLiveTest(t, l.Ledger)
	traffic, err := l.TrafficByLogicalModel(from, to)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]TrafficSummary{}
	for _, item := range traffic {
		byID[item.ID] = item
	}
	checkModelSpeed(t, byID["logical"].ModelSpeed, 3, 260.0/16)
	if byID["logical"].Requests != 3 || byID["logical"].RecoveryTraffic != 1 {
		t.Fatalf("retry inflated logical cohort: %+v", byID["logical"])
	}
	speeds, err := l.ProviderModelSpeeds(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(speeds) != 2 {
		t.Fatalf("actual model attribution: %+v", speeds)
	}
	if speeds[0].ProviderID != "provider-a" || speeds[0].UpstreamModel != "actual" || speeds[1].ProviderID != "provider-b" {
		t.Fatalf("provider/model attribution: %+v", speeds)
	}
	checkModelSpeed(t, speeds[0].ModelSpeed, 4, 300.0/4)
	checkModelSpeed(t, speeds[1].ModelSpeed, 1, 500)
}

func TestModelSpeedEvidenceEligibility(t *testing.T) {
	l := openTemp(t)
	from := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name, state, observation string
		status                   domain.RequestStatus
		output, duration         int64
		valid                    bool
	}{
		{"known", "known", "", domain.ReqSuccess, 20, 1000, true},
		{"known-zero", "known", "", domain.ReqSuccess, 0, 1000, true},
		{"legacy-positive", "", "", domain.ReqSuccess, 30, 1000, true},
		{"legacy-zero", "", "", domain.ReqSuccess, 0, 1000, false},
		{"partial", "partial", "", domain.ReqSuccess, 20, 1000, false},
		{"unknown", "unknown", "", domain.ReqSuccess, 20, 1000, false},
		{"observation-partial", "known", "partial", domain.ReqSuccess, 20, 1000, false},
		{"observation-unknown", "known", "unknown", domain.ReqSuccess, 20, 1000, false},
		{"missing-duration", "known", "", domain.ReqSuccess, 20, 0, false},
		{"negative-duration", "known", "", domain.ReqSuccess, 20, -1, false},
		{"negative-output", "known", "", domain.ReqSuccess, -1, 1000, false},
		{"canceled", "known", "", domain.ReqCanceled, 20, 1000, false},
		{"failed", "known", "", domain.ReqFailed, 20, 1000, false},
	}
	for _, c := range cases {
		l.BeginRequest(domain.RequestRecord{ID: c.name, Ts: from, LogicalModelID: c.name, Status: c.status, DurationMS: c.duration, Attempts: 1, Usage: domain.Usage{OutputTokens: c.output}})
		l.AddAttempt(domain.AttemptRecord{RequestID: c.name, Seq: 1, Ts: from, ProviderID: "provider", Model: c.name, Status: c.status, LatencyMS: c.duration, UsageState: c.state, Usage: domain.Usage{OutputTokens: c.output}})
		l.FinishRequest(c.name, nil)
	}
	flushLiveTest(t, l.Ledger)
	for _, c := range cases {
		if c.observation != "" {
			if err := saveObservation(l.db, &domain.RequestObservation{RequestID: c.name, RequestAt: from, UsageState: c.observation}); err != nil {
				t.Fatal(err)
			}
		}
	}
	traffic, err := l.TrafficByLogicalModel(from, from.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ModelSpeed{}
	for _, item := range traffic {
		byID[item.ID] = item.ModelSpeed
	}
	speeds, err := l.ProviderModelSpeeds(context.Background(), from, from.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	attempts := map[string]ModelSpeed{}
	for _, item := range speeds {
		attempts[item.UpstreamModel] = item.ModelSpeed
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			count := int64(0)
			if c.valid {
				count = 1
			}
			checkModelSpeed(t, byID[c.name], count, float64(c.output))
			// An explicitly complete upstream attempt remains measurable even when the
			// client-facing request's usage projection is incomplete.
			if c.observation != "" {
				count = 1
			}
			checkModelSpeed(t, attempts[c.name], count, float64(c.output))
		})
	}
}
