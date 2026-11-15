package management

import (
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ferry/internal/codingplan"
	"ferry/internal/config"
	"ferry/internal/domain"
	"ferry/internal/nativeauth"
	"ferry/internal/secretstore"
)

func codingUsageFixture(t *testing.T) (*v2APIFixture, *string, *int) {
	t.Helper()
	f := newV2APIFixture(t)
	f.server.deps.Config.Providers[0].BaseURL = "https://api.kimi.com/coding/v1"
	f.server.deps.Config.Connections[0].Credential.SecretRef = "store:CODING_USAGE_TEST"
	secret, calls := "synthetic-coding-key", 0
	f.server.deps.Resolver = &config.Resolver{Store: func(name string) (string, bool) {
		return secret, name == "CODING_USAGE_TEST"
	}}
	f.server.deps.SubscriptionUsageHTTPClient = &http.Client{Transport: modelCatalogRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("usage request did not use the connection credential")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"usage":{"limit":100,"used":20,"remaining":80,"resetTime":"2026-11-10T00:00:00Z"},"limits":[]}`))}, nil
	})}
	return f, &secret, &calls
}

const codingUsagePath = "/api/providers/provider-a/connections/connection-a/usage"

func TestConnectionCodingUsageCacheAndCredentialReplacement(t *testing.T) {
	f, secret, calls := codingUsageFixture(t)
	rec, body := f.do(http.MethodGet, codingUsagePath+"?refresh=false", nil)
	requireStatus(t, rec, http.StatusOK)
	if body["usage"] != nil || *calls != 0 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("an empty cache must not query upstream or invent usage")
	}
	rec, body = f.do(http.MethodGet, codingUsagePath, nil)
	requireStatus(t, rec, http.StatusOK)
	usage := body["usage"].(map[string]any)
	if usage["provider"] != "kimi-coding" || *calls != 1 {
		t.Fatalf("unexpected usage provider or request count: %v, %d", usage["provider"], *calls)
	}
	if strings.Contains(rec.Body.String(), *secret) {
		t.Fatal("management response exposed credential")
	}
	rec, body = f.do(http.MethodGet, codingUsagePath+"?refresh=false", nil)
	requireStatus(t, rec, http.StatusOK)
	if body["usage"] == nil || *calls != 1 {
		t.Fatal("cached read should return the snapshot without upstream traffic")
	}
	*secret = "replacement-synthetic-key"
	rec, body = f.do(http.MethodGet, codingUsagePath+"?refresh=false", nil)
	requireStatus(t, rec, http.StatusOK)
	if body["usage"] != nil || *calls != 1 {
		t.Fatal("replacing a key under the same reference must invalidate the cached identity")
	}
	rec, _ = f.do(http.MethodGet, codingUsagePath, nil)
	requireStatus(t, rec, http.StatusOK)
	if *calls != 2 {
		t.Fatal("the new account should query its own usage")
	}
}

func TestConnectionCodingUsageKeepsLastSuccessOnFailure(t *testing.T) {
	f, secret, _ := codingUsageFixture(t)
	rec, _ := f.do(http.MethodGet, codingUsagePath, nil)
	requireStatus(t, rec, http.StatusOK)
	f.server.deps.SubscriptionUsageHTTPClient = &http.Client{Transport: modelCatalogRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(*secret))}, nil
	})}
	rec, _ = f.do(http.MethodGet, codingUsagePath, nil)
	requireStatus(t, rec, http.StatusBadGateway)
	if strings.Contains(rec.Body.String(), *secret) {
		t.Fatal("upstream error response exposed credential")
	}
	rec, body := f.do(http.MethodGet, codingUsagePath+"?refresh=false", nil)
	requireStatus(t, rec, http.StatusOK)
	if body["usage"] == nil {
		t.Fatal("a failed refresh must retain the last successful snapshot")
	}
}

func TestConnectionCodingUsageEffectiveEndpointAndMembership(t *testing.T) {
	f, _, calls := codingUsageFixture(t)
	for _, path := range []string{
		"/api/providers/missing/connections/connection-a/usage",
		"/api/providers/provider-a/connections/missing/usage",
	} {
		rec, _ := f.do(http.MethodGet, path, nil)
		requireStatus(t, rec, http.StatusNotFound)
	}
	for _, endpoint := range []string{"https://example.test/v1", "https://api.kimi.com/v1", "http://api.kimi.com/coding/v1"} {
		f.server.deps.Config.Connections[0].BaseURL = endpoint
		rec, _ := f.do(http.MethodGet, codingUsagePath, nil)
		requireStatus(t, rec, http.StatusUnprocessableEntity)
	}
	f.server.deps.Config.Connections[0].BaseURL = ""
	f.server.deps.Config.Connections[0].FullURL = "https://example.test/chat/completions"
	rec, _ := f.do(http.MethodGet, codingUsagePath, nil)
	requireStatus(t, rec, http.StatusUnprocessableEntity)
	if *calls != 0 {
		t.Fatal("unsupported connections must not send credentials upstream")
	}
	f.server.deps.Config.Providers[0].Type = domain.ProviderCliproxyCodex
	rec, _ = f.do(http.MethodGet, codingUsagePath, nil)
	requireStatus(t, rec, http.StatusServiceUnavailable) // Existing OAuth handler, not Coding Plan lookup.
}

func TestCodingUsageRefreshJoinsInFlightRequest(t *testing.T) {
	f, _, _ := codingUsageFixture(t)
	identity := sha256.Sum256([]byte("synthetic-identity"))
	flight := &codingUsageFlight{done: make(chan struct{})}
	f.server.codingUsage = map[[32]byte]*codingUsageEntry{identity: {flight: flight}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan *codingplan.Snapshot, 1)
	go func() {
		usage, err := f.server.readCodingUsage(ctx, identity, codingplan.Source{}, codingUsageAuth{secret: "synthetic"}, true)
		if err != nil {
			t.Error(err)
		}
		result <- usage
	}()
	want := &codingplan.Snapshot{Provider: "kimi-coding"}
	flight.usage = want
	close(flight.done)
	if got := <-result; got != want {
		t.Fatal("a concurrent refresh should reuse the in-flight result")
	}
}

func TestConnectionCodingUsageRejectsReplacedIdentityDuringFetch(t *testing.T) {
	f, secret, _ := codingUsageFixture(t)
	base := f.server.deps.SubscriptionUsageHTTPClient.Transport
	f.server.deps.SubscriptionUsageHTTPClient.Transport = modelCatalogRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(r)
		*secret = "new-synthetic-key"
		return response, err
	})
	rec, _ := f.do(http.MethodGet, codingUsagePath, nil)
	requireStatus(t, rec, http.StatusConflict)
	rec, body := f.do(http.MethodGet, codingUsagePath+"?refresh=false", nil)
	requireStatus(t, rec, http.StatusOK)
	if body["usage"] != nil {
		t.Fatal("the replaced identity must not receive the old in-flight snapshot")
	}
}

type codingUsageKimiDriver struct{ offlineNativeDriver }

func (*codingUsageKimiDriver) Info() nativeauth.Info {
	return nativeauth.Info{ID: "kimi", Mode: "browser", BaseURL: "https://api.kimi.com/coding/v1", Protocol: "openai-chat"}
}

func TestConnectionCodingUsageNativeKimiAccount(t *testing.T) {
	f := newV2APIFixture(t)
	store, err := secretstore.OpenSQLite(filepath.Join(t.TempDir(), "native-secrets.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := nativeauth.NewManager(store, &codingUsageKimiDriver{})
	login := func() {
		t.Helper()
		if _, err := manager.Begin(context.Background(), "kimi", "coding", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Complete(context.Background(), "kimi", "coding", "callback"); err != nil {
			t.Fatal(err)
		}
	}
	login()
	f.server.deps.NativeOAuth = manager
	f.server.deps.Config.Providers[0].BaseURL = "https://api.kimi.com/coding/v1"
	f.server.deps.Config.Connections[0].Credential.SecretRef = nativeauth.Ref("kimi", "coding")
	calls := 0
	f.server.deps.SubscriptionUsageHTTPClient = &http.Client{Transport: modelCatalogRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.String() != "https://api.kimi.com/coding/v1/usages" || r.Header.Get("Authorization") != "Bearer private-access" {
			t.Error("native Kimi usage did not use the bound quota client")
		}
		return modelCatalogResponse(http.StatusOK, `{"usages":{"limit_5h":{"used_ratio":0.25}}}`), nil
	})}
	rec, body := f.do(http.MethodGet, codingUsagePath+"?refresh=false", nil)
	requireStatus(t, rec, http.StatusOK)
	if body["usage"] != nil || calls != 0 {
		t.Fatal("native cached reads must not refresh tokens or query upstream")
	}
	rec, body = f.do(http.MethodGet, codingUsagePath, nil)
	requireStatus(t, rec, http.StatusOK)
	if body["usage"] == nil || calls != 1 || strings.Contains(rec.Body.String(), "private-access") || strings.Contains(rec.Body.String(), "private-refresh") {
		t.Fatal("native usage was unavailable or exposed credential material")
	}
	rec, body = f.do(http.MethodGet, codingUsagePath+"?refresh=false", nil)
	requireStatus(t, rec, http.StatusOK)
	if body["usage"] == nil || calls != 1 {
		t.Fatal("native usage cache was not reused")
	}
	// The fake driver returns the same token on login. The login generation,
	// rather than token material, must still invalidate the old account cache.
	login()
	rec, body = f.do(http.MethodGet, codingUsagePath+"?refresh=false", nil)
	requireStatus(t, rec, http.StatusOK)
	if body["usage"] != nil || calls != 1 {
		t.Fatal("replacing a native login must invalidate the cached account")
	}
	f.server.deps.Config.Connections[0].BaseURL = "https://api.kimi.ai/coding/v1"
	rec, _ = f.do(http.MethodGet, codingUsagePath, nil)
	requireStatus(t, rec, http.StatusUnprocessableEntity)
	f.server.deps.Config.Connections[0].BaseURL = ""
	f.server.deps.Config.Connections[0].FullURL = "https://api.kimi.com/coding/v1/usages"
	rec, _ = f.do(http.MethodGet, codingUsagePath, nil)
	requireStatus(t, rec, http.StatusUnprocessableEntity)
	if calls != 1 {
		t.Fatal("native credentials escaped the registered endpoint")
	}
}

func TestCodingQuotaGroupsFollowCredentialAndScope(t *testing.T) {
	f, secret, calls := codingUsageFixture(t)
	cfg := f.server.deps.Config
	cfg.Providers[0].BaseURL = "https://open.bigmodel.cn/api/coding/paas/v4"
	base := cfg.Connections[0]
	for i, endpoint := range []string{"https://open.bigmodel.cn/api/anthropic/v1", "https://open.bigmodel.cn/api/v1"} {
		connection := base
		connection.ID = []string{"anthropic", "responses"}[i]
		connection.BaseURL = endpoint
		connection.Credential.SecretRef = "store:ALIAS"
		cfg.Connections = append(cfg.Connections, connection)
		cfg.Providers[0].Connections = append(cfg.Providers[0].Connections, domain.ConnectionRef{ID: connection.ID})
	}
	f.server.deps.Resolver = &config.Resolver{Store: func(name string) (string, bool) {
		switch name {
		case "CODING_USAGE_TEST", "ALIAS":
			return *secret, true
		case "OTHER":
			return "independent-key", true
		}
		return "", false
	}}
	f.server.deps.SubscriptionUsageHTTPClient = &http.Client{Transport: modelCatalogRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		*calls++
		return modelCatalogResponse(http.StatusOK, `{"data":{"limits":[{"type":"TOKENS_LIMIT","percentage":20}]}}`), nil
	})}
	group, scope := f.server.codingQuotaMembership("provider-a", "connection-a")
	if group == "" || scope != "glm-coding" {
		t.Fatal("supported quota needs opaque membership")
	}
	for _, id := range []string{"anthropic", "responses"} {
		got, _ := f.server.codingQuotaMembership("provider-a", id)
		if got != group {
			t.Fatal("protocols and secret aliases must share the same effective quota pool")
		}
	}
	rec, _ := f.do(http.MethodGet, codingUsagePath, nil)
	requireStatus(t, rec, http.StatusOK)
	rec, body := f.do(http.MethodGet, "/api/providers/provider-a/connections/anthropic/usage?refresh=false", nil)
	requireStatus(t, rec, http.StatusOK)
	if body["usage"] == nil || *calls != 1 || body["quota_group_id"] != group {
		t.Fatal("protocols must share one cached snapshot")
	}
	// Identical quota amounts are not evidence of shared ownership.
	cfg.Connections[1].Credential.SecretRef = "store:OTHER"
	other, _ := f.server.codingQuotaMembership("provider-a", "anthropic")
	if other == group || other == "" {
		t.Fatal("independent credentials must remain independent")
	}
	cfg.Connections[1].Credential.SecretRef = "store:ALIAS"
	cfg.Connections[1].BaseURL = "https://api.z.ai/api/coding/paas/v4"
	region, _ := f.server.codingQuotaMembership("provider-a", "anthropic")
	if region == group || region == "" {
		t.Fatal("product/region quota endpoints must remain independent")
	}
	// Provider identity is not quota ownership.
	copyProvider := cfg.Providers[0]
	copyProvider.ID = "shared-provider"
	copyProvider.Connections = []domain.ConnectionRef{{ID: "shared-connection"}}
	shared := base
	shared.ID = "shared-connection"
	shared.ProviderID = copyProvider.ID
	cfg.Providers = append(cfg.Providers, copyProvider)
	cfg.Connections = append(cfg.Connections, shared)
	sharedGroup, _ := f.server.codingQuotaMembership(copyProvider.ID, shared.ID)
	if sharedGroup != group {
		t.Fatal("shared quota across providers must retain one identity")
	}
	old := group
	*secret = "replacement-key"
	group, _ = f.server.codingQuotaMembership("provider-a", "connection-a")
	if group == old {
		t.Fatal("replaced credentials must invalidate the old membership")
	}
	rec, _ = f.do(http.MethodGet, codingUsagePath+"?quota_group_id="+old, nil)
	requireStatus(t, rec, http.StatusConflict)

	rec, body = f.do(http.MethodGet, codingUsagePath+"?refresh=false", nil)
	requireStatus(t, rec, http.StatusOK)
	if body["usage"] != nil {
		t.Fatal("replacement must not reuse the prior account snapshot")
	}
	if strings.Contains(rec.Body.String(), *secret) || strings.Contains(group, *secret) {
		t.Fatal("opaque metadata exposed credential material")
	}
}
