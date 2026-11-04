package management

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"testing"
	"time"

	"ferry/internal/domain"
)

// A temporary loopback-only server for browser acceptance of the Go embed and
// actual management HTTP adapter. It never reads production state or calls an
// upstream. Run explicitly; Ctrl-C shuts down and removes both test databases.
func TestObservationBrowserPreview(t *testing.T) {
	if os.Getenv("SMARTAPI_OBSERVATION_BROWSER") != "1" {
		t.Skip("opt-in isolated browser fixture")
	}
	f := newV2APIFixture(t)
	f.server.deps.Security = f.control
	rec, _ := f.do(http.MethodPost, "/api/auth/initialize", map[string]any{"username": "observation-test", "password": "synthetic preview password"})
	requireStatus(t, rec, http.StatusCreated)
	if err := f.ledger.SetObservationContentMode("excerpt"); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute)
	for i := range 6 {
		id := []string{"http_direct", "http_followup", "http_waiting", "http_streaming", "http_failed", "http_independent"}[i]
		ts := at.Add(time.Duration(i) * time.Second)
		status := domain.ReqSuccess
		outcome := domain.OutcomeDirectSuccess
		if i == 2 {
			status = "waiting"
			outcome = ""
		}
		if i == 3 {
			status = "streaming"
			outcome = ""
		}
		if i >= 4 {
			status = domain.ReqFailed
			outcome = domain.OutcomeFailed
		}
		f.ledger.BeginRequest(domain.RequestRecord{ID: id, Ts: ts, ConsumerKey: "Synthetic caller", LogicalModelID: "smart", ProviderID: "provider-a", ConnectionID: "connection-a", Status: status, Outcome: outcome, DurationMS: 1000, TTFTMS: 180, Usage: domain.Usage{InputTokens: 10, OutputTokens: 5}})
		var evidence []domain.AffinityEvidence
		if i < 5 {
			thread := "synthetic-one"
			if i > 2 {
				thread = "synthetic-two"
			}
			evidence = []domain.AffinityEvidence{domain.NewAffinityEvidence("codex.thread", domain.AffinityConversation, thread, domain.ConfHigh)}
		}
		f.ledger.BeginObservation(domain.ObservationInput{RequestID: id, At: ts, CallerIdentityID: "synthetic-caller", Evidence: evidence})
		f.ledger.CaptureRequestExcerpt(id, &domain.RequestContract{Messages: []domain.Message{{Role: domain.RoleUser, Blocks: []domain.Block{{Kind: domain.BlockText, Text: "检查 Session 与请求流转的一致性"}}}}})
		if i != 2 {
			f.ledger.CaptureResponseExcerpt(id, domain.ContentPreview{ResponseExcerpt: "已核对真实管理接口与历史候选快照。 " + id, ContentState: "recorded"})
		}
		events := []domain.ExecutionTraceEvent{{At: ts, Type: "REQUEST_RECEIVED"}, {At: ts.Add(time.Millisecond), Type: "CONNECTION_SELECTED", RouteStep: 1, ProviderID: "provider-a", ConnectionID: "connection-a"}, {At: ts.Add(2 * time.Millisecond), Type: "ATTEMPT_STARTED", RouteStep: 1, ProviderID: "provider-a", ConnectionID: "connection-a"}}
		if i != 2 {
			events = append(events, domain.ExecutionTraceEvent{At: ts.Add(3 * time.Millisecond), Type: "UPSTREAM_DISPATCHED", RouteStep: 1, ProviderID: "provider-a", ConnectionID: "connection-a"})
			f.ledger.AddAttempt(domain.AttemptRecord{RequestID: id, Seq: 1, Ts: ts, ProviderID: "provider-a", ConnectionID: "connection-a", Status: status, Serving: status == domain.ReqSuccess, UpstreamDispatched: true, UsageState: "known", CostState: "known"})
		}
		if status == domain.ReqSuccess {
			events = append(events, domain.ExecutionTraceEvent{At: ts.Add(time.Second), Type: "ATTEMPT_SUCCEEDED", RouteStep: 1, ProviderID: "provider-a", ConnectionID: "connection-a"}, domain.ExecutionTraceEvent{At: ts.Add(time.Second), Type: "CLIENT_DELIVERY_COMPLETED"})
		}
		if status == domain.ReqFailed {
			events = append(events, domain.ExecutionTraceEvent{At: ts.Add(time.Second), Type: "ATTEMPT_FAILED", RouteStep: 1, ProviderID: "provider-a", ConnectionID: "connection-a", FailureType: domain.FailConnect}, domain.ExecutionTraceEvent{At: ts.Add(time.Second), Type: "INGRESS_FAILED"})
		}
		if status == "streaming" {
			events = append(events, domain.ExecutionTraceEvent{At: ts.Add(time.Second), Type: "CLIENT_FIRST_OUTPUT"})
		}
		f.ledger.SetTrace(id, &domain.TraceRecord{SchemaVersion: 1, RouteSnapshot: &domain.RouteSnapshot{SchemaVersion: 1, RuntimeRevision: 1, CapturedAt: ts, Coverage: "complete", Steps: []domain.RouteStepSnapshot{{RouteStep: 1, ProviderID: "provider-a", ProviderName: "Provider A at request time", CandidateCount: 1, State: "eligible"}, {RouteStep: 2, ProviderID: "historical-provider", ProviderName: "Historical Provider", CandidateCount: 1, State: "excluded"}}}, Candidates: []domain.CandidateRecord{{CandidateID: "candidate-a", RouteStep: 1, ProviderID: "provider-a", ProviderName: "Provider A at request time", ConnectionID: "connection-a", ConnectionName: "Primary", Eligible: true, Conditions: []domain.CandidateCondition{{Code: "connection_enabled", Result: "pass"}, {Code: "quota", Result: "unknown"}}}, {CandidateID: "candidate-b", RouteStep: 2, ProviderID: "historical-provider", ProviderName: "Historical Provider", ConnectionID: "historical-account", ConnectionName: "Disabled account", Conditions: []domain.CandidateCondition{{Code: "connection_enabled", Result: "fail"}}}}, Events: events})
		if status == domain.ReqSuccess || status == domain.ReqFailed {
			f.ledger.FinishRequest(id, nil)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:4179")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: f.server.Routes(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	t.Log("isolated Go-embedded HTTP fixture ready on loopback port 4179")
	<-ctx.Done()
	shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	_ = server.Shutdown(shutdown)
}
