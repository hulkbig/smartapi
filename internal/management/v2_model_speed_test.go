package management

import (
	"net/http"
	"testing"
	"time"

	"ferry/internal/domain"
)

func TestV2ModelSpeedListsDetailsAndFallbackRoutes(t *testing.T) {
	f := newV2APIFixture(t)
	assertSpeed := func(item map[string]any, want any, samples float64) {
		t.Helper()
		value, exists := item["output_tokens_per_second"]
		if !exists || value != want || item["speed_samples"] != samples {
			t.Fatalf("speed contract: %+v want=%v samples=%v", item, want, samples)
		}
	}
	rec, empty := f.do(http.MethodGet, "/api/models/smart", nil)
	requireStatus(t, rec, http.StatusOK)
	assertSpeed(empty["traffic"].(map[string]any), nil, 0)
	assertSpeed(empty["default_route"].([]any)[0].(map[string]any), nil, 0)
	rec, provider := f.do(http.MethodGet, "/api/providers/provider-a", nil)
	requireStatus(t, rec, http.StatusOK)
	if len(provider["model_speeds"].([]any)) != 2 {
		t.Fatalf("catalog missing null samples: %+v", provider)
	}
	for _, raw := range provider["model_speeds"].([]any) {
		assertSpeed(raw.(map[string]any), nil, 0)
	}

	cfg := nativeV2Config()
	cfg.Providers = append(cfg.Providers, domain.Provider{ID: "provider-b", Name: "B", Type: domain.ProviderCustom, BaseURL: "https://b.test/v1", Enabled: true, Connections: []domain.ConnectionRef{{ID: "connection-b"}}})
	cfg.Connections = append(cfg.Connections, domain.Connection{ID: "connection-b", ProviderID: "provider-b", Enabled: true, Credential: domain.CredentialRef{SecretRef: "env:FERRY_V2_TEST_B"}, Models: []domain.ConnectionModel{{UpstreamModel: "upstream-model", Protocols: []domain.Protocol{domain.ProtocolOpenAIChat}, Capabilities: domain.Capabilities{Text: true}}}})
	cfg.LogicalModels[0].DefaultRoute.Steps = append(cfg.LogicalModels[0].DefaultRoute.Steps,
		domain.RouteStep{ProviderID: "provider-a", UpstreamModel: "upstream-model-2", RecoverOn: []domain.FailureType{domain.FailProvider5xx}},
		domain.RouteStep{ProviderID: "provider-b", UpstreamModel: "upstream-model"})
	if err := f.server.publishConfig(cfg); err != nil {
		t.Fatal(err)
	}
	to := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	from := to.Add(-time.Hour)
	window := "?from=" + from.Format(time.RFC3339) + "&to=" + to.Format(time.RFC3339)
	add := func(id, logical, provider, model string, at time.Time, output, duration int64) {
		f.ledger.BeginRequest(domain.RequestRecord{ID: id, Ts: at, LogicalModelID: logical, ProviderID: provider, UpstreamModel: model, Status: domain.ReqSuccess, Attempts: 1, DurationMS: duration * 2, Usage: domain.Usage{OutputTokens: output}})
		f.ledger.AddAttempt(domain.AttemptRecord{RequestID: id, Seq: 1, Ts: at, ProviderID: provider, Model: model, Status: domain.ReqSuccess, LatencyMS: duration, UsageState: "known", Usage: domain.Usage{OutputTokens: output}})
		f.ledger.FinishRequest(id, nil)
	}
	add("primary", "smart", "provider-a", "upstream-model", from, 40, 1000)
	// No final request from smart used this fallback: global provider/model
	// attempt evidence must still be shown for the configured fallback route.
	add("fallback-a", "another-logical", "provider-a", "upstream-model-2", from, 90, 3000)
	add("fallback-b", "another-logical", "provider-b", "upstream-model", from, 200, 1000)
	add("exclusive-end", "smart", "provider-a", "upstream-model", to, 9999, 1)
	deadline := time.Now().Add(3 * time.Second)
	for {
		r, err := f.ledger.QueryRequest("exclusive-end")
		if err != nil {
			t.Fatal(err)
		}
		if r != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ledger did not flush")
		}
		time.Sleep(time.Millisecond)
	}
	checkModel := func(model map[string]any) {
		t.Helper()
		assertSpeed(model["traffic"].(map[string]any), float64(20), 1)
		routes := model["default_route"].([]any)
		if len(routes) != 3 {
			t.Fatalf("routes: %+v", routes)
		}
		for i, want := range []float64{40, 30, 200} {
			assertSpeed(routes[i].(map[string]any), want, 1)
		}
	}
	rec, list := f.do(http.MethodGet, "/api/models"+window, nil)
	requireStatus(t, rec, http.StatusOK)
	checkModel(list["models"].([]any)[0].(map[string]any))
	rec, detail := f.do(http.MethodGet, "/api/models/smart"+window, nil)
	requireStatus(t, rec, http.StatusOK)
	checkModel(detail)
	for _, path := range []string{"/api/providers", "/api/providers/provider-a", "/api/providers/provider-b"} {
		rec, payload := f.do(http.MethodGet, path+window, nil)
		requireStatus(t, rec, http.StatusOK)
		providers := []any{payload}
		if path == "/api/providers" {
			providers = payload["providers"].([]any)
		}
		for _, raw := range providers {
			p := raw.(map[string]any)
			for _, s := range p["model_speeds"].([]any) {
				speed := s.(map[string]any)
				want := float64(40)
				if speed["upstream_model"] == "upstream-model-2" {
					want = 30
				}
				if p["id"] == "provider-b" {
					want = 200
				}
				assertSpeed(speed, want, 1)
			}
		}
	}
}
