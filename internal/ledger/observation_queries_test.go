package ledger

import (
	"errors"
	"testing"
	"time"

	"ferry/internal/domain"
)

func TestObservationQueriesScopeStatisticsAndProviderParticipation(t *testing.T) {
	l := openTemp(t)
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	seed := func(id, session string, status domain.RequestStatus, outcome domain.RequestOutcome, provider, model string, at time.Time) {
		l.BeginRequest(domain.RequestRecord{ID: id, Ts: at, ConsumerKey: "Display caller", Status: status, Outcome: outcome, ProviderID: provider, LogicalModelID: model, Usage: domain.Usage{InputTokens: 10, OutputTokens: 5}})
		var evidence []domain.AffinityEvidence
		if session != "" {
			evidence = []domain.AffinityEvidence{observationHint("codex.thread", session, domain.AffinityConversation)}
		}
		l.BeginObservation(domain.ObservationInput{RequestID: id, At: at, CallerIdentityID: "caller", Evidence: evidence})
		if status != "waiting" {
			l.FinishRequest(id, nil)
		}
	}
	seed("a1", "a", domain.ReqSuccess, domain.OutcomeRecoveredSuccess, "p2", "smart-a", from)
	seed("a2", "a", "waiting", "", "p1", "smart-b", from.Add(time.Second))
	seed("b1", "b", domain.ReqCanceled, domain.OutcomeFailed, "p3", "smart-a", from)
	seed("b2", "b", domain.ReqFailed, domain.OutcomeFailed, "p2", "smart-a", from)
	seed("u1", "", domain.ReqSuccess, domain.OutcomeDirectSuccess, "p2", "smart-a", from)
	seed("excluded-end", "a", domain.ReqSuccess, domain.OutcomeDirectSuccess, "p1", "smart-a", to)
	for _, a := range []domain.AttemptRecord{
		{RequestID: "a1", Seq: 1, Ts: from, ProviderID: "p1", Status: domain.ReqFailed, UsageState: "known", CostState: "known", UpstreamDispatched: true},
		{RequestID: "a1", Seq: 2, Ts: from, ProviderID: "p2", Status: domain.ReqSuccess, UsageState: "known", CostState: "known", UpstreamDispatched: true, Serving: true},
		{RequestID: "b2", Seq: 1, Ts: from, ProviderID: "p2", Status: domain.ReqFailed, UsageState: "known", CostState: "known", UpstreamDispatched: true},
	} {
		l.AddAttempt(a)
	}
	l.UpdateRequest("a2", func(r *domain.RequestRecord) { r.Status = "waiting" })
	l.SetObservationActivity("a2", "p1", "", "waiting")
	flushLiveTest(t, l.Ledger)
	q := ObservationQuery{Filter: domain.LogFilter{From: from, To: to}, Mode: "session", SessionsOnly: true, Limit: 50}
	page, err := l.QueryObservationSources(q)
	if err != nil {
		t.Fatal(err)
	}
	s := page.Stats
	if page.Total != 2 || len(page.Sources) != 2 || s.Requests != 5 || s.Sessions != 2 || s.IndependentRequests != 1 || s.ActiveSessions != 1 || s.FailedSessions != 1 || s.RecoveredSessions != 1 || s.Canceled != 1 {
		t.Fatalf("scope/stats: %+v rows=%+v", s, page.Sources)
	}
	if s.AverageRequests == nil || *s.AverageRequests != 2 || s.KnownCost == nil || *s.KnownCost != 0 || s.CostState != "partial" || s.CostKnownRequests != 2 {
		t.Fatalf("zero/partial/average: %+v", s)
	}
	if page.Sources[0].SelectedRequestID != "a2" || page.Sources[0].ActiveRequests != 1 || len(page.Sources[0].Models) != 2 || len(page.Sources[0].Providers) != 2 {
		t.Fatalf("active source/cross-model/recovery: %+v", page.Sources[0])
	}
	q.SessionsOnly = false
	q.Filter.ParticipatingProvider = "p1"
	page, err = l.QueryObservationSources(q)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Stats.Requests != 2 || page.Sources[0].ParticipatingRequests != 1 || page.Sources[0].PendingRequests != 1 {
		t.Fatalf("participation includes unused/excludes waiting: %+v", page)
	}
	q.Filter.Search = "a1"
	page, err = l.QueryObservationSources(q)
	if err != nil {
		t.Fatal(err)
	}
	if page.Stats.Requests != 1 || page.Sources[0].SelectedRequestID != "a1" {
		t.Fatalf("search did not focus exact request: %+v", page)
	}
	q.Filter.Search = "does-not-exist"
	page, err = l.QueryObservationSources(q)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Sources) != 0 || page.Stats.KnownCost != nil || page.Stats.AverageRequests != nil {
		t.Fatalf("empty/unknown: %+v", page)
	}
}

func TestObservationQueryCursorAndModes(t *testing.T) {
	l := openTemp(t)
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"a", "b", "c"} {
		l.BeginRequest(domain.RequestRecord{ID: id, Ts: from, Status: domain.ReqSuccess})
		l.BeginObservation(domain.ObservationInput{RequestID: id, At: from, CallerIdentityID: "caller"})
		l.FinishRequest(id, nil)
	}
	flushLiveTest(t, l.Ledger)
	q := ObservationQuery{Mode: "session", Filter: domain.LogFilter{From: from, To: from.Add(time.Hour)}, Limit: 1}
	seen := map[string]bool{}
	for range 3 {
		p, err := l.QueryObservationSources(q)
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 3 || len(p.Sources) != 1 {
			t.Fatalf("page: %+v", p)
		}
		id := p.Sources[0].SourceID
		if seen[id] {
			t.Fatalf("duplicate source: %s", id)
		}
		seen[id] = true
		if p.Sources[0].SourceKind != "request" {
			t.Fatal("independent requests merged")
		}
		q.Cursor = p.NextCursor
	}
	if len(seen) != 3 || q.Cursor != "" {
		t.Fatal("cursor did not finish")
	}
	p, err := l.QueryObservationSources(q)
	if err != nil {
		t.Fatal(err)
	}
	q.Cursor = p.NextCursor
	q.Filter.CallerIdentityID = "different"
	if _, err := l.QueryObservationSources(q); !errors.Is(err, ErrObservationCursor) {
		t.Fatalf("mismatched filter cursor accepted: %v", err)
	}
	q.Cursor = "garbage"
	if _, err := l.QueryObservationSources(q); !errors.Is(err, ErrObservationCursor) {
		t.Fatalf("invalid cursor accepted: %v", err)
	}
}

func TestObservedRequestPrefixIndependentOfPageAndSearch(t *testing.T) {
	l := openTemp(t)
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := l.SetObservationContentMode("excerpt"); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"first", "second"} {
		at := from.Add(time.Duration(i) * time.Second)
		l.BeginRequest(domain.RequestRecord{ID: id, Ts: at, Status: domain.ReqSuccess})
		l.BeginObservation(domain.ObservationInput{RequestID: id, At: at, CallerIdentityID: "caller", Evidence: []domain.AffinityEvidence{observationHint("codex.thread", "same", domain.AffinityConversation)}})
		l.CaptureRequestExcerpt(id, &domain.RequestContract{Messages: []domain.Message{{Role: domain.RoleUser, Blocks: []domain.Block{{Kind: domain.BlockText, Text: "continue"}}}}})
		l.CaptureResponseExcerpt(id, domain.ContentPreview{ResponseExcerpt: "answer " + id, ContentState: "recorded"})
		l.FinishRequest(id, nil)
	}
	flushLiveTest(t, l.Ledger)
	page, err := l.QueryObservedRequests(domain.LogFilter{Search: "second", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Observations["second"].ContentPreview.PreviewRole != "response" || page.Observations["second"].ContentPreview.PreviewText != "answer second" {
		t.Fatalf("filter changed prefix: %+v", page)
	}
	metadata, err := queryRequestObservations(l.read, make([]domain.RequestRecord, 1000))
	if err != nil {
		t.Fatalf("1000-row compatibility: %v", err)
	}
	if metadata == nil {
		t.Fatal("nil batch metadata")
	}
}
