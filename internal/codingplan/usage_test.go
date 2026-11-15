package codingplan

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSourceForURL(t *testing.T) {
	for _, test := range []struct{ base, provider, usage string }{
		{"https://api.kimi.com/coding/v1", "kimi-coding", "https://api.kimi.com/coding/v1/usages"},
		{"https://api.kimi.ai/coding/v1/", "kimi-coding", "https://api.kimi.ai/coding/v1/usages"},
		{"https://api.kimi.com/coding/v1/messages", "kimi-coding", "https://api.kimi.com/coding/v1/usages"},
		{"https://open.bigmodel.cn/api/coding/paas/v4", "glm-coding", "https://open.bigmodel.cn/api/monitor/usage/quota/limit"},
		{"https://api.z.ai/api/coding/paas/v4", "zai-coding", "https://api.z.ai/api/monitor/usage/quota/limit"},
		{"https://open.bigmodel.cn/api/anthropic/v1", "glm-coding", "https://open.bigmodel.cn/api/monitor/usage/quota/limit"},
		{"https://api.z.ai/api/v1/responses", "zai-coding", "https://api.z.ai/api/monitor/usage/quota/limit"},
		{"https://API.KIMI.COM:443/coding/v1", "kimi-coding", "https://api.kimi.com/coding/v1/usages"},
	} {
		t.Run(test.base, func(t *testing.T) {
			got, err := SourceForURL(test.base)
			if err != nil || got.Provider != test.provider || got.URL != test.usage {
				t.Fatalf("source=%+v error=%v", got, err)
			}
		})
	}
	for _, base := range []string{
		"https://api.kimi.com/v1", "https://api.moonshot.cn/v1", "https://open.bigmodel.cn/api/paas/v4", "https://api.z.ai/api/paas/v4",
		"http://api.kimi.com/coding/v1", "https://api.kimi.com.evil.test/coding/v1", "https://evil.test/coding/v1",
		"https://api.kimi.com/coding-other/v1", "https://api.kimi.com:8443/coding/v1", "https://key@api.kimi.com/coding/v1",
		"https://api.kimi.com/coding/v1?key=synthetic", "https://api.kimi.com/coding/v1#fragment", "https://api.kimi.com/cod%69ng/v1",
		"https://api.kimi.com/coding/v1?", "https://api.kimi.com/coding/v1#",
	} {
		if _, err := SourceForURL(base); err == nil {
			t.Errorf("accepted unsupported endpoint %s", base)
		}
	}
}

func TestFetchUsesReadOnlyQuotaRequest(t *testing.T) {
	source, _ := SourceForURL("https://api.kimi.ai/coding/v1")
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.String() != source.URL || r.Header.Get("Authorization") != "Bearer synthetic-key" || r.Body != nil {
			t.Error("unexpected quota request contract")
		}
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 15*time.Second {
			t.Error("quota request is not bounded")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"usage":{"limit":"100","used":"0","remaining":"100"}}`))}, nil
	})}
	usage, err := Fetch(context.Background(), client, source, "synthetic-key", now)
	if err != nil || usage.Provider != "kimi-coding" || !usage.ObservedAt.Equal(now) || len(usage.Windows) != 1 {
		t.Fatalf("usage=%+v error=%v", usage, err)
	}
	if used := usage.Windows[0].UsedPercent; used == nil || *used != 0 {
		t.Fatal("actual zero usage was not preserved")
	}
}

func TestFetchDoesNotFollowRedirectsOrExposeUpstreamErrors(t *testing.T) {
	source, _ := SourceForURL("https://api.kimi.com/coding/v1")
	for _, test := range []struct {
		name, body string
		status     int
		transport  error
	}{
		{name: "redirect", status: 302, body: "synthetic-key"},
		{name: "unauthorized", status: 401, body: "synthetic-key"},
		{name: "oversize", status: 200, body: strings.Repeat("synthetic-key", 100000)},
		{name: "malformed", status: 200, body: "synthetic-key"},
		{name: "unexpected", status: 200, body: `{"error":"synthetic-key"}`},
		{name: "transport", transport: errors.New("Authorization Bearer synthetic-key")},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if test.transport != nil {
					return nil, test.transport
				}
				return &http.Response{StatusCode: test.status, Header: http.Header{"Location": {"https://evil.test/"}}, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			_, err := Fetch(context.Background(), client, source, "synthetic-key", time.Now())
			if err == nil || strings.Contains(err.Error(), "synthetic-key") || calls != 1 {
				t.Fatalf("unsafe quota failure: calls=%d err=%v", calls, err)
			}
		})
	}
	_, err := Fetch(context.Background(), nil, Source{Provider: "kimi-coding", URL: "https://evil.test/usages"}, "synthetic-key", time.Now())
	if err == nil {
		t.Fatal("arbitrary usage URL was accepted")
	}
}

func TestFetchGLMUsesRawKeyAndRegionalQuotaEndpoint(t *testing.T) {
	for _, base := range []string{"https://open.bigmodel.cn/api/coding/paas/v4", "https://api.z.ai/api/anthropic/v1"} {
		source, _ := SourceForURL(base)
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet || r.URL.String() != source.URL || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "synthetic-key" {
				t.Error("GLM quota requires a raw Authorization key on the matching regional host")
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":200,"success":true,"data":{"limits":[{"type":"CREDIT_LIMIT","percentage":25}]}}`))}, nil
		})}
		usage, err := Fetch(context.Background(), client, source, "synthetic-key", time.Now())
		if err != nil || usage.Provider != source.Provider || len(usage.Windows) != 1 || *usage.Windows[0].UsedPercent != 25 {
			t.Fatalf("regional usage=%+v error=%v", usage, err)
		}
	}
}
