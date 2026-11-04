package management

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ferry/internal/controlstore"
	"ferry/internal/domain"
)

func TestObservationHTTPRealSessionSummaryAndHistoricalCandidates(t *testing.T) {
	f := newV2APIFixture(t)
	rec, settings := f.do(http.MethodGet, "/api/settings", nil)
	requireStatus(t, rec, http.StatusOK)
	if settings["observability_content_mode"] != "metadata_only" {
		t.Fatal("excerpt default must be metadata_only")
	}
	rec, settings = f.do(http.MethodPatch, "/api/settings", map[string]any{"observability_content_mode": "excerpt"})
	requireStatus(t, rec, http.StatusOK)
	if settings["debug_request_response"] != false {
		t.Fatal("excerpt enabled debug wire capture")
	}
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	for i, id := range []string{"req_one", "req_two"} {
		at := from.Add(time.Duration(i) * time.Second)
		f.ledger.BeginRequest(domain.RequestRecord{ID: id, Ts: at, ConsumerKey: "Display caller", LogicalModelID: "smart", Protocol: domain.ProtocolOpenAIChat, Status: domain.ReqSuccess, Outcome: domain.OutcomeDirectSuccess, DurationMS: int64(100 + i*100), TTFTMS: int64(20 + i*20)})
		f.ledger.BeginObservation(domain.ObservationInput{RequestID: id, At: at, CallerIdentityID: "stable-key", Evidence: []domain.AffinityEvidence{domain.NewAffinityEvidence("codex.thread", domain.AffinityConversation, "private-raw-thread", domain.ConfHigh)}})
		f.ledger.CaptureRequestExcerpt(id, &domain.RequestContract{Messages: []domain.Message{{Role: domain.RoleUser, Blocks: []domain.Block{{Kind: domain.BlockText, Text: "continue secret=private-secret"}}}}})
		f.ledger.CaptureResponseExcerpt(id, domain.ContentPreview{ResponseExcerpt: "answer " + id, ContentState: "recorded"})
		f.ledger.AddAttempt(domain.AttemptRecord{RequestID: id, Seq: 1, Ts: at, ProviderID: "provider-a", ConnectionID: "connection-a", Status: domain.ReqSuccess, Serving: true, UpstreamDispatched: true, UsageState: "known", CostState: "known"})
		f.ledger.SetTrace(id, &domain.TraceRecord{SchemaVersion: 1, RouteSnapshot: &domain.RouteSnapshot{SchemaVersion: 1, Coverage: "complete", CapturedAt: at, Steps: []domain.RouteStepSnapshot{{RouteStep: 1, ProviderID: "provider-a", ProviderName: "Historical A", CandidateCount: 1, State: "eligible"}, {RouteStep: 2, ProviderID: "removed-provider", ProviderName: "Removed provider", CandidateCount: 1, State: "excluded"}}},
			Candidates: []domain.CandidateRecord{{Target: "private-runtime-leaf", CandidateID: "candidate-active", RouteStep: 1, ProviderID: "provider-a", ProviderName: "Historical A", ConnectionID: "connection-a", Eligible: true, Rank: 1},
				{Target: "private-removed-leaf", CandidateID: "candidate-excluded", RouteStep: 2, ProviderID: "removed-provider", ProviderName: "Removed provider", ConnectionID: "removed-connection", ConnectionName: "Historical account", ExclusionReason: "connection disabled", Conditions: []domain.CandidateCondition{{Code: "connection_enabled", Result: "fail"}}}},
			Events: []domain.ExecutionTraceEvent{{At: at, Type: "REQUEST_RECEIVED"}, {At: at.Add(time.Millisecond), Type: "COMPLETE"}}})
		f.ledger.FinishRequest(id, nil)
	}
	bounds := url.Values{"from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}}
	var response map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec, response = f.do(http.MethodGet, "/api/sessions?"+bounds.Encode(), nil)
		requireStatus(t, rec, http.StatusOK)
		if response["stats"].(map[string]any)["cost_known_requests"] == float64(2) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("observation writes did not commit")
		}
		time.Sleep(time.Millisecond)
	}
	sessions := response["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("session grouping: %v", response)
	}
	row := sessions[0].(map[string]any)
	sessionID := row["session_id"].(string)
	if row["caller_identity_id"] != "stable-key" || row["request_count"] != float64(2) || row["cost_state"] != "known" || row["known_cost"] != float64(0) {
		t.Fatalf("identity/known zero: %v", row)
	}
	if strings.Contains(rec.Body.String(), "private-raw-thread") || strings.Contains(rec.Body.String(), "private-secret") {
		t.Fatal("raw evidence or recognizable credential leaked")
	}
	rec, response = f.do(http.MethodGet, "/api/sessions/"+sessionID+"?"+bounds.Encode(), nil)
	requireStatus(t, rec, http.StatusOK)
	timing := response["timing"].(map[string]any)
	if timing["duration_samples"] != float64(2) || timing["duration_p95_ms"] != float64(200) || timing["ttft_p50_ms"] != float64(20) {
		t.Fatalf("timing scope: %v", timing)
	}
	metrics := response["metrics"].(map[string]any)
	if len(metrics["provider_ids"].([]any)) != 1 || len(metrics["connection_ids"].([]any)) != 1 || metrics["recovery_count"] != float64(0) {
		t.Fatalf("participating resources/zero recovery: %v", metrics)
	}
	rec, response = f.do(http.MethodGet, "/api/sessions/"+sessionID+"/requests?"+bounds.Encode()+"&q=req_two", nil)
	requireStatus(t, rec, http.StatusOK)
	requests := response["requests"].([]any)
	if len(requests) != 1 {
		t.Fatal(response)
	}
	preview := requests[0].(map[string]any)["content_preview"].(map[string]any)
	if preview["preview_role"] != "response" || preview["preview_text"] != "answer req_two" {
		t.Fatalf("prefix depended on filter: %v", preview)
	}
	rec, response = f.do(http.MethodGet, "/api/requests/req_two", nil)
	requireStatus(t, rec, http.StatusOK)
	route := response["route_snapshot"].(map[string]any)
	candidates := route["candidates"].([]any)
	if len(candidates) != 2 || candidates[1].(map[string]any)["provider_name"] != "Removed provider" || candidates[0].(map[string]any)["provider_name"] != "Historical A" {
		t.Fatalf("history overwritten by current config: %v", route)
	}
	if strings.Contains(rec.Body.String(), "private-runtime-leaf") || strings.Contains(rec.Body.String(), "private-removed-leaf") {
		t.Fatal("private execution identity exposed")
	}
	for _, path := range []string{"/api/provider-traffic?", "/api/providers/provider-a/traffic?"} {
		rec, response = f.do(http.MethodGet, path+bounds.Encode(), nil)
		requireStatus(t, rec, http.StatusOK)
		if response["total"] != float64(1) {
			t.Fatalf("provider sources: %v", response)
		}
	}
	rec, response = f.do(http.MethodGet, "/api/sessions/"+sessionID+"?from="+url.QueryEscape(to.Format(time.RFC3339))+"&to="+url.QueryEscape(to.Add(time.Hour).Format(time.RFC3339)), nil)
	requireStatus(t, rec, http.StatusOK)
	if response["stats"].(map[string]any)["requests"] != float64(0) {
		t.Fatal("empty time scope expanded to full Session")
	}
	rec, response = f.do(http.MethodGet, "/api/sessions/"+sessionID+"?scope=all", nil)
	requireStatus(t, rec, http.StatusOK)
	if response["stats"].(map[string]any)["requests"] != float64(2) {
		t.Fatal("all-retained scope unavailable")
	}
}

func TestObservationAssetsAreServedByProductionEmbed(t *testing.T) {
	f := newV2APIFixture(t)
	for _, path := range []string{"observability-model.js", "observability-state.js", "observability-fixtures.js", "route-explanation.js", "app.part15.jsfrag", "styles.part9.css", "observability-icons/openai.svg", "observability-icons/anthropic.svg", "observability-icons/codex-color.svg"} {
		rec, _ := f.do(http.MethodGet, "/"+path, nil)
		requireStatus(t, rec, http.StatusOK)
		if rec.Body.Len() == 0 || strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("asset %s returned shell/empty: %s", path, rec.Header())
		}
	}
}

func TestObservationHTTPRejectsInvalidQueryAndSettings(t *testing.T) {
	f := newV2APIFixture(t)
	for _, query := range []string{"limit=201", "limit=bad", "status=imagined", "outcome=imagined", "mode=imagined", "cursor=garbage", "window=0s", "scope=all", "from=2026-09-01T00:00:00Z", "q=" + strings.Repeat("x", 201)} {
		rec, _ := f.do(http.MethodGet, "/api/sessions?"+query, nil)
		requireStatus(t, rec, http.StatusBadRequest)
	}
	rec, _ := f.do(http.MethodPatch, "/api/settings", map[string]any{"observability_content_mode": "complete-bodies"})
	requireStatus(t, rec, http.StatusBadRequest)
	rec, _ = f.do(http.MethodGet, "/api/sessions/missing", nil)
	requireStatus(t, rec, http.StatusNotFound)
}

func TestObservationEndpointsUseManagementAuthentication(t *testing.T) {
	store, err := controlstore.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := New(Deps{Security: store, Control: store}).Routes()
	init := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "https://smartapi.test/api/auth/initialize", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery staple"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(init, req)
	if init.Code != http.StatusCreated {
		t.Fatalf("initialize: %d %s", init.Code, init.Body.String())
	}
	for _, path := range []string{"/api/sessions", "/api/sessions/id", "/api/sessions/id/requests", "/api/provider-traffic", "/api/providers/provider-a/traffic"} {
		rec := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "https://smartapi.test"+path, nil)
		request.Header.Set("Authorization", "Bearer data-plane-key")
		handler.ServeHTTP(rec, request)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s unprotected: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}
