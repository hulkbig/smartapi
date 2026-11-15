package ledger

import (
	"context"
	"fmt"
	"testing"
	"time"

	"ferry/internal/domain"
)

func TestAPIKeyHistoryAttributionAndWindow(t *testing.T) {
	l := openTemp(t)
	ctx := context.Background()
	from := time.Date(2026, 11, 1, 0, 15, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	revoked := to
	keys := []domain.APIKeyIdentity{{ID: "a", Name: "renamed", RevokedAt: &revoked}, {ID: "b", Name: "other"}, {ID: "empty", Name: "unused"}}
	add := func(id, name, caller, secret, model string, at time.Time, status domain.RequestStatus, tokens int64) {
		l.BeginRequest(domain.RequestRecord{ID: id, Ts: at, ConsumerKey: name, APIKeySecretID: secret, LogicalModelID: model, Status: status, Usage: domain.Usage{InputTokens: tokens, OutputTokens: tokens / 4}, DurationMS: 100, EstCashCost: 1})
		if caller != "no-observation" {
			l.BeginObservation(domain.ObservationInput{RequestID: id, CallerIdentityID: caller, At: at})
		}
		l.FinishRequest(id, nil)
	}
	add("old-secret", "old-name", "a", "secret-1", "m1", from, domain.ReqSuccess, 12)
	add("new-secret", "renamed", "a", "secret-2", "m2", from.Add(time.Hour), domain.ReqFailed, 0)
	add("legacy-absent", "renamed", "no-observation", "", "m1", from.Add(time.Minute), domain.ReqSuccess, 0)
	add("legacy-empty", "renamed", "", "", "m2", from.Add(2*time.Minute), domain.ReqCanceled, 0)
	add("other-identity", "renamed", "b", "", "m1", from, domain.ReqSuccess, 100)
	add("unknown-identity", "renamed", "deleted-key", "", "m1", from, domain.ReqSuccess, 100)
	add("old-name-without-id", "old-name", "no-observation", "", "m1", from, domain.ReqSuccess, 100)
	add("before", "old-name", "a", "", "m1", from.Add(-time.Millisecond), domain.ReqSuccess, 100)
	add("exclusive-end", "renamed", "a", "", "m1", to, domain.ReqSuccess, 100)
	add("newest-outside", "renamed", "a", "", "m1", to.Add(time.Hour), domain.ReqSuccess, 100)
	l.BeginIngressRequest(domain.RequestRecord{ID: "live", Ts: from.Add(3 * time.Hour), ConsumerKey: "renamed", Status: "streaming"})
	l.BeginObservation(domain.ObservationInput{RequestID: "live", CallerIdentityID: "a", At: from.Add(3 * time.Hour)})
	flushLiveTest(t, l.Ledger)
	reports, err := l.QueryAPIKeyHistory(ctx, keys, from, to, true)
	if err != nil {
		t.Fatal(err)
	}
	a := reports["a"]
	if a.Usage.Requests != 4 || a.Usage.Succeeded != 2 || a.Usage.Failed != 1 || a.Usage.Canceled != 1 {
		t.Fatalf("final logical outcomes: %+v", a.Usage)
	}
	if a.Usage.InputTokens != 12 || a.Usage.OutputTokens != 3 || !a.Usage.TokensObserved || a.Usage.TotalCost != 4 || a.Usage.EstCashCost != 4 || a.Usage.AvgLatencyMS != 100 {
		t.Fatalf("usage compatibility: %+v", a.Usage)
	}
	if reports["b"].Usage.Requests != 1 || reports["empty"].Usage.Requests != 0 || reports["empty"].History.LastRequestAt != nil {
		t.Fatalf("cross-key attribution: %+v", reports)
	}
	h := a.History
	if h.TokensObservedRequests != 1 {
		t.Fatalf("partial token coverage: %+v", h)
	}
	if h.LastRequestAt == nil || !h.LastRequestAt.Equal(to.Add(time.Hour)) {
		t.Fatalf("all retained last use: %+v", h)
	}
	if len(h.Timeline) != 24 || !h.Timeline[0].From.Equal(from) || !h.Timeline[23].To.Equal(to) {
		t.Fatalf("hourly boundaries: %+v", h.Timeline)
	}
	var total, succeeded, failed, canceled int64
	for i, b := range h.Timeline {
		total += b.Requests
		succeeded += b.Succeeded
		failed += b.Failed
		canceled += b.Canceled
		if i > 1 && (b.Requests != 0 || b.TokensObserved || b.InputTokens != 0) {
			t.Fatalf("gap %d: %+v", i, b)
		}
	}
	if total != 4 || succeeded != 2 || failed != 1 || canceled != 1 {
		t.Fatalf("timeline totals: %d/%d/%d/%d", total, succeeded, failed, canceled)
	}
	if len(h.Models) != 2 || h.Models[0].ID != "m1" || h.Models[0].Requests != 2 || h.Models[1].ID != "m2" || h.Models[1].TokensObserved {
		t.Fatalf("model evidence: %+v", h.Models)
	}
	recent, err := l.QueryAPIKeyRequests(ctx, keys[0], from, to, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"old-secret": true, "new-secret": true, "legacy-absent": true, "legacy-empty": true, "live": true}
	if len(recent) != len(want) {
		t.Fatalf("recent cohort: %+v", recent)
	}
	for _, r := range recent {
		if !want[r.ID] {
			t.Fatalf("wrong identity/window: %s", r.ID)
		}
	}
}

func TestAPIKeyHistoryBinsAndTokenEvidence(t *testing.T) {
	l := openTemp(t)
	ctx := context.Background()
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	keys := []domain.APIKeyIdentity{{ID: "a", Name: "a"}, {ID: "b", Name: "b"}, {ID: "unused", Name: "unused"}}
	for _, id := range []string{"a", "b"} {
		l.BeginRequest(domain.RequestRecord{ID: id, Ts: from, ConsumerKey: id, Status: domain.ReqSuccess})
		l.FinishRequest(id, nil)
	}
	flushLiveTest(t, l.Ledger)
	// Explicit zero-token evidence is different from a missing token observation.
	if err := saveObservation(l.db, &domain.RequestObservation{RequestID: "b", CallerIdentityID: "b", RequestAt: from, UsageState: "known"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		span time.Duration
		bins int
	}{{time.Millisecond, 1}, {24 * time.Hour, 24}, {7 * 24 * time.Hour, 7}, {30 * 24 * time.Hour, 30}, {90 * 24 * time.Hour, 90}, {365 * 24 * time.Hour, 90}} {
		t.Run(fmt.Sprint(tc.span), func(t *testing.T) {
			reports, err := l.QueryAPIKeyHistory(ctx, keys, from, from.Add(tc.span), true)
			if err != nil {
				t.Fatal(err)
			}
			a, b, unused := reports["a"], reports["b"], reports["unused"]
			if a.History.TokensObservedRequests != 0 || b.History.TokensObservedRequests != 1 || a.Usage.TokensObserved || !b.Usage.TokensObserved || a.Usage.Requests != 1 || a.Usage.Succeeded != 1 {
				t.Fatalf("missing versus observed zero: a=%+v b=%+v", a.Usage, b.Usage)
			}
			if len(unused.History.Timeline) != tc.bins || unused.History.Models == nil {
				t.Fatalf("empty history shape: %+v", unused.History)
			}
			for _, bin := range unused.History.Timeline {
				if bin.Requests != 0 || bin.TokensObserved {
					t.Fatalf("nonzero empty bin: %+v", bin)
				}
			}
		})
	}
}

func TestAPIKeyHistoryCoverageUsesRollupEvidence(t *testing.T) {
	l := openTemp(t)
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		id := fmt.Sprint(i)
		l.BeginRequest(domain.RequestRecord{ID: id, Ts: from.Add(time.Duration(i) * time.Hour), ConsumerKey: "a", Status: domain.ReqSuccess})
		l.FinishRequest(id, nil)
	}
	flushLiveTest(t, l.Ledger)
	if _, err := l.db.Exec("DELETE FROM logical_requests WHERE id='0'"); err != nil {
		t.Fatal(err)
	}
	reports, err := l.QueryAPIKeyHistory(context.Background(), []domain.APIKeyIdentity{{ID: "a", Name: "a"}}, from, from.Add(24*time.Hour), false)
	if err != nil {
		t.Fatal(err)
	}
	c := reports["a"].History.Coverage
	if c.Scope != "global" || c.Basis != "retained_request_details" || c.RollupOnlyRequests != 1 || c.DetailRequests != 1 || c.RollupRequests != 2 {
		t.Fatalf("retention gap hidden: %+v", c)
	}
	if reports["a"].Usage.Requests != 1 {
		t.Fatal("unattributed rollup must not fabricate key history")
	}
}
