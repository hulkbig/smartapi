package ingress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"ferry/internal/domain"
	sched "ferry/internal/scheduler"
)

type excerptEngine struct {
	*stubEngine
	enabled        bool
	preview        domain.ContentPreview
	deliveryFailed bool
}

func (e *excerptEngine) ObserveIngress(ctx context.Context, _ domain.Protocol, _ sched.RequestMeta) (context.Context, func(int)) {
	return ctx, func(int) {}
}
func (e *excerptEngine) ObservationExcerptEnabled(context.Context) bool { return e.enabled }
func (e *excerptEngine) ObserveResponseExcerpt(_ context.Context, p domain.ContentPreview) {
	e.preview = p
}
func (e *excerptEngine) ObserveDelivery(_ context.Context, failed bool) { e.deliveryFailed = failed }

func TestHTTPDeliveryCapturesBoundedVisibleExcerptOnlyWhenEnabled(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			e := &excerptEngine{stubEngine: newStub(), enabled: enabled}
			visible := "visible secret=private-value " + strings.Repeat("中文", 200)
			e.resp.Content = []domain.Block{{Kind: domain.BlockText, Text: visible}}
			e.evs = []domain.StreamEvent{{Kind: domain.EvTextDelta, TextDelta: visible}, {Kind: domain.EvEnd}}
			body := `{"model":"test","messages":[{"role":"user","content":"hello"}],"stream":false}`
			if stream {
				body = strings.Replace(body, `"stream":false`, `"stream":true`, 1)
			}
			rec := httptest.NewRecorder()
			(&OpenAIChatHandler{Eng: e}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
			if rec.Code != 200 {
				t.Fatal(rec.Code, rec.Body.String())
			}
			if !enabled {
				if e.preview.ContentState != "" {
					t.Fatalf("disabled capture: %+v", e.preview)
				}
				continue
			}
			p := e.preview
			if p.ContentState != "recorded" || strings.Contains(p.ResponseExcerpt, "private-value") || !strings.Contains(p.ResponseExcerpt, "[redacted]") || utf8.RuneCountInString(p.ResponseExcerpt) > 160 || !p.Truncated {
				t.Fatalf("stream=%t: %+v", stream, p)
			}
		}
	}
}

func TestTerminalFallbackDoesNotRepeatAlreadyDeliveredDelta(t *testing.T) {
	w := &observedWriter{ResponseWriter: httptest.NewRecorder(), excerptEnabled: true}
	w.observeSSEOutput("response.output_text.delta", []byte(`{"delta":"visible"}`))
	w.observeSSEOutput("response.output_text.done", []byte(`{"text":"visible"}`))
	w.observeSSEOutput("response.completed", []byte(`{"response":{"output":[{"type":"message","content":[{"type":"output_text","text":"visible"}]}]}}`))
	if w.excerptText != "visible" {
		t.Fatalf("duplicated terminal: %q", w.excerptText)
	}
	empty := &observedWriter{ResponseWriter: httptest.NewRecorder(), excerptEnabled: true}
	empty.observeSSEOutput("response.completed", []byte(`{"response":{"output":[{"type":"message","content":[{"type":"output_text","text":"terminal only"}]}]}}`))
	if empty.excerptText != "terminal only" {
		t.Fatalf("lost terminal: %q", empty.excerptText)
	}
	failed := &observedWriter{ResponseWriter: httptest.NewRecorder(), excerptEnabled: true, writeFailed: true}
	failed.observeSSEOutput("response.output_text.delta", []byte(`{"delta":"undelivered"}`))
	if failed.excerptText != "" {
		t.Fatal("captured failed downstream write")
	}
}

func TestObservabilityKeepsConflictingHeaderAliasesWithoutChangingAffinity(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("Thread-Id", "one")
	r.Header.Set("X-Codex-Thread-Id", "two")
	m := requestMeta(r, nil)
	if len(m.AffinityEvidence) != 1 || len(m.ObservationEvidence) != 2 {
		t.Fatalf("routing=%+v observation=%+v", m.AffinityEvidence, m.ObservationEvidence)
	}
	if m.AffinityEvidence[0].KeyHash != domain.NewAffinityEvidence("codex.thread", domain.AffinityConversation, "one", domain.ConfHigh).KeyHash {
		t.Fatal("observability changed routing affinity precedence")
	}
}
