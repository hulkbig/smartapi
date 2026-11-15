package management

import (
	"net/http"
	"testing"
	"time"

	"ferry/internal/domain"
)

func TestAPIKeyUsageZeroAndWindowedCount(t *testing.T) {
	f := newV2APIFixture(t)
	to := time.Now().UTC().Truncate(time.Second)
	from := to.Add(-time.Minute)
	window := "?from=" + from.Format(time.RFC3339) + "&to=" + to.Format(time.RFC3339)
	rec, list := f.do(http.MethodGet, "/api/api-keys"+window, nil)
	requireStatus(t, rec, http.StatusOK)
	items := list["api_keys"].([]any)
	if len(items) != 1 {
		t.Fatalf("API key fixture: %+v", list)
	}
	item := items[0].(map[string]any)
	id := item["identity"].(map[string]any)["id"].(string)
	assertUsage := func(payload map[string]any, requests float64) {
		t.Helper()
		usage, ok := payload["usage"].(map[string]any)
		if !ok || usage["requests"] != requests {
			t.Fatalf("known usage must be numeric, want %v: %+v", requests, payload)
		}
	}
	assertUsage(item, 0)
	if list["from"] != from.Format(time.RFC3339) || list["to"] != to.Format(time.RFC3339) {
		t.Fatalf("list omitted actual usage window: %+v", list)
	}
	rec, detail := f.do(http.MethodGet, "/api/api-keys/"+id+window, nil)
	requireStatus(t, rec, http.StatusOK)
	assertUsage(detail, 0)
	if detail["last_request_at"] != nil {
		t.Fatalf("unused key has last request: %+v", detail)
	}
	for _, r := range []domain.RequestRecord{
		{ID: "inside", Ts: from.Add(time.Second), ConsumerKey: "initial"},
		{ID: "outside", Ts: to, ConsumerKey: "initial"},
		{ID: "other-key", Ts: from.Add(time.Second), ConsumerKey: "other"},
	} {
		f.ledger.BeginRequest(r)
		f.ledger.FinishRequest(r.ID, func(r *domain.RequestRecord) { r.Status = domain.ReqSuccess; r.Outcome = domain.OutcomeDirectSuccess })
	}
	// An unfinished ingress is visible in request logs, but not finalized usage.
	f.ledger.BeginIngressRequest(domain.RequestRecord{ID: "live", Ts: from.Add(time.Second), ConsumerKey: "initial", Status: "streaming"})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rec, list = f.do(http.MethodGet, "/api/api-keys"+window, nil)
		requireStatus(t, rec, http.StatusOK)
		item = list["api_keys"].([]any)[0].(map[string]any)
		if item["usage"].(map[string]any)["requests"] == float64(1) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	assertUsage(item, 1)
	rec, detail = f.do(http.MethodGet, "/api/api-keys/"+id+window, nil)
	requireStatus(t, rec, http.StatusOK)
	assertUsage(detail, 1)
}

func TestAPIKeyUsageUnavailableIsNotZero(t *testing.T) {
	for _, unavailable := range []string{"missing", "query-failed"} {
		t.Run(unavailable, func(t *testing.T) {
			f := newV2APIFixture(t)
			identities, _, err := f.control.APIKeys()
			if err != nil || len(identities) != 1 {
				t.Fatalf("identities: %+v %v", identities, err)
			}
			want := http.StatusServiceUnavailable
			if unavailable == "missing" {
				f.server.deps.Ledger = nil
			} else {
				if err := f.ledger.Close(); err != nil {
					t.Fatal(err)
				}
				want = http.StatusInternalServerError
			}
			for _, path := range []string{"/api/api-keys", "/api/api-keys/" + identities[0].ID} {
				rec, payload := f.do(http.MethodGet, path, nil)
				requireStatus(t, rec, want)
				if payload["usage"] != nil || payload["api_keys"] != nil {
					t.Fatalf("unavailable usage fabricated as zero: %+v", payload)
				}
			}
		})
	}
}

func TestAPIKeyHistoryRotationRevocationAndRecentAttribution(t *testing.T) {
	f := newV2APIFixture(t)
	identities, secrets, err := f.control.APIKeys()
	if err != nil || len(identities) != 1 {
		t.Fatalf("identities: %+v %v", identities, err)
	}
	identity := identities[0]
	from := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	to := from.Add(24 * time.Hour)
	window := "?from=" + from.Format(time.RFC3339) + "&to=" + to.Format(time.RFC3339)
	add := func(id, name, caller, secret string, at time.Time, status domain.RequestStatus, tokens int64) {
		f.ledger.BeginRequest(domain.RequestRecord{ID: id, Ts: at, ConsumerKey: name, APIKeySecretID: secret, Status: status, LogicalModelID: "model-a", Usage: domain.Usage{InputTokens: tokens, OutputTokens: tokens / 2}})
		if caller != "" {
			f.ledger.BeginObservation(domain.ObservationInput{RequestID: id, CallerIdentityID: caller, At: at})
		}
		f.ledger.FinishRequest(id, nil)
	}
	add("before-rotation", "historical-name", identity.ID, secrets[identity.ID][0].ID, from, domain.ReqSuccess, 10)
	rec, rotated := f.do(http.MethodPost, "/api/api-keys/"+identity.ID+"/rotate", map[string]any{})
	requireStatus(t, rec, http.StatusOK)
	if rotated["api_key_id"] != identity.ID {
		t.Fatalf("rotation changed identity: %+v", rotated)
	}
	_, secrets, err = f.control.APIKeys()
	if err != nil {
		t.Fatal(err)
	}
	var active string
	for _, secret := range secrets[identity.ID] {
		if secret.State == domain.APIKeySecretActive {
			active = secret.ID
		}
	}
	if active == "" {
		t.Fatal("rotation did not create an active credential")
	}
	add("after-rotation", identity.Name, identity.ID, active, from.Add(time.Hour), domain.ReqFailed, 0)
	add("legacy", identity.Name, "", "", from.Add(2*time.Hour), domain.ReqCanceled, 0)
	add("foreign-id", identity.Name, "different-identity", "", from.Add(3*time.Hour), domain.ReqSuccess, 100)
	add("end-exclusive", identity.Name, identity.ID, active, to, domain.ReqSuccess, 100)
	add("latest-outside", identity.Name, identity.ID, active, to.Add(time.Hour), domain.ReqSuccess, 100)
	deadline := time.Now().Add(3 * time.Second)
	for {
		record, err := f.ledger.QueryRequest("latest-outside")
		if err != nil {
			t.Fatal(err)
		}
		if record != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ledger did not flush")
		}
		time.Sleep(time.Millisecond)
	}
	check := func() {
		t.Helper()
		rec, detail := f.do(http.MethodGet, "/api/api-keys/"+identity.ID+window, nil)
		requireStatus(t, rec, http.StatusOK)
		usage := detail["usage"].(map[string]any)
		if usage["requests"] != float64(3) || usage["succeeded"] != float64(1) || usage["failed"] != float64(1) || usage["canceled"] != float64(1) || usage["input_tokens"] != float64(10) || usage["tokens_observed"] != true {
			t.Fatalf("identity usage: %+v", usage)
		}
		history := detail["history"].(map[string]any)
		if history["tokens_observed_requests"] != float64(1) {
			t.Fatalf("partial token coverage: %+v", history)
		}
		if history["from"] != from.Format(time.RFC3339) || history["to"] != to.Format(time.RFC3339) || len(history["timeline"].([]any)) != 24 || len(history["models"].([]any)) != 1 {
			t.Fatalf("history contract: %+v", history)
		}
		if history["coverage"].(map[string]any)["scope"] != "global" {
			t.Fatalf("coverage scope: %+v", history["coverage"])
		}
		if history["last_request_at"] != to.Add(time.Hour).Format(time.RFC3339) || detail["last_request_at"] != history["last_request_at"] {
			t.Fatalf("last use outside selected window: %+v", detail)
		}
		recent := detail["recent_requests"].([]any)
		if len(recent) != 3 {
			t.Fatalf("recent requests: %+v", recent)
		}
		want := map[string]bool{"before-rotation": true, "after-rotation": true, "legacy": true}
		for _, raw := range recent {
			if !want[raw.(map[string]any)["id"].(string)] {
				t.Fatalf("recent attribution: %+v", raw)
			}
		}
		rec, list := f.do(http.MethodGet, "/api/api-keys"+window, nil)
		requireStatus(t, rec, http.StatusOK)
		item := list["api_keys"].([]any)[0].(map[string]any)
		if item["usage"].(map[string]any)["requests"] != usage["requests"] || item["last_request_at"] != detail["last_request_at"] {
			t.Fatalf("list/detail mismatch: %+v %+v", item, detail)
		}
	}
	check()
	rec, _ = f.do(http.MethodPost, "/api/api-keys/"+identity.ID+"/revoke", nil)
	requireStatus(t, rec, http.StatusOK)
	check()
}

func TestAPIKeyHistoryEmptyWindowsContract(t *testing.T) {
	f := newV2APIFixture(t)
	identities, _, err := f.control.APIKeys()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		window string
		bins   int
	}{{"24h", 24}, {"168h", 7}, {"720h", 30}} {
		rec, detail := f.do(http.MethodGet, "/api/api-keys/"+identities[0].ID+"?window="+tc.window, nil)
		requireStatus(t, rec, http.StatusOK)
		history := detail["history"].(map[string]any)
		timeline := history["timeline"].([]any)
		models, ok := history["models"].([]any)
		if history["tokens_observed_requests"] != float64(0) || len(timeline) != tc.bins || !ok || len(models) != 0 || history["last_request_at"] != nil {
			t.Fatalf("empty history contract: %+v", history)
		}
		for _, raw := range timeline {
			b := raw.(map[string]any)
			for _, field := range []string{"requests", "succeeded", "failed", "canceled", "input_tokens", "output_tokens"} {
				if b[field] != float64(0) {
					t.Fatalf("empty bin %s: %+v", field, b)
				}
			}
			if b["tokens_observed"] != false {
				t.Fatalf("fabricated token evidence: %+v", b)
			}
		}
	}
}
