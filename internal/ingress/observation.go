package ingress

import (
	"context"
	"ferry/internal/domain"
	"ferry/internal/observation"
	sched "ferry/internal/scheduler"
	"net/http"
	"strings"
	"time"
)

type ingressObserver interface {
	ObserveIngress(context.Context, domain.Protocol, sched.RequestMeta) (context.Context, func(int))
}
type firstOutputObserver interface {
	ObserveFirstOutput(context.Context)
}
type deliveryObserver interface {
	ObserveDelivery(context.Context, bool)
}
type excerptObserver interface {
	ObservationExcerptEnabled(context.Context) bool
	ObserveResponseExcerpt(context.Context, domain.ContentPreview)
}
type ingressPhaseObserver interface {
	ObserveIngressPhase(context.Context, string, time.Time, error)
}
type ingressPhaseObserverKey struct{}

func observeIngressPhase(ctx context.Context, phase string, err error) {
	if observer, ok := ctx.Value(ingressPhaseObserverKey{}).(ingressPhaseObserver); ok {
		observer.ObserveIngressPhase(ctx, phase, time.Now(), err)
	}
}

func finishIngressPhase(ctx context.Context, phase string, err error) {
	if err != nil {
		observeIngressPhase(ctx, phase+"_FAILED", err)
	} else {
		observeIngressPhase(ctx, phase+"_COMPLETED", nil)
	}
}

type observedWriter struct {
	http.ResponseWriter
	status            int
	writeFailed       bool
	firstOutput       bool
	onFirstOutput     func()
	excerptEnabled    bool
	excerptText       string
	excerptTools      []string
	excerptTruncated  bool
	excerptRecognized bool
}

func (w *observedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *observedWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *observedWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	if err != nil || n != len(b) {
		w.writeFailed = true
	}
	if n > 0 && !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		w.observeOutput()
	}
	return n, err
}
func (w *observedWriter) Flush() {
	_ = w.FlushError()
}
func (w *observedWriter) FlushError() error {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err != nil {
		w.writeFailed = true
	}
	return err
}
func (w *observedWriter) observeOutput() {
	if w.firstOutput || w.writeFailed || w.status < 200 || w.status >= 300 || w.onFirstOutput == nil {
		return
	}
	w.firstOutput = true
	w.onFirstOutput()
}
func (w *observedWriter) observeSSEOutput(event string, payload []byte) {
	if !w.firstOutput && !w.writeFailed && w.onFirstOutput != nil && meaningfulSSEOutput(event, payload) {
		w.observeOutput()
	}
	if w.excerptEnabled && !w.writeFailed {
		text, tool, recognized := observation.StreamVisibleContent(event, payload)
		if w.excerptText == "" && text == "" {
			terminal, tools := observation.TerminalVisibleContent(event, payload)
			text = terminal
			for _, name := range tools {
				w.appendExcerptTool(name)
			}
		}
		w.excerptRecognized = w.excerptRecognized || recognized
		w.appendExcerpt(text)
		w.appendExcerptTool(tool)
	}
}

func (w *observedWriter) appendExcerptTool(tool string) {
	name, _ := observation.Clip(tool, 64)
	if name == "" || len(w.excerptTools) >= 3 {
		return
	}
	for _, existing := range w.excerptTools {
		if existing == name {
			return
		}
	}
	w.excerptTools = append(w.excerptTools, name)
}

func (w *observedWriter) appendExcerpt(text string) {
	remaining := observation.ExcerptLimit*4 + 64 - len(w.excerptText)
	if remaining <= 0 {
		w.excerptTruncated = w.excerptTruncated || text != ""
		return
	}
	w.excerptText += text[:min(len(text), remaining)]
	w.excerptTruncated = w.excerptTruncated || len(text) > remaining
}

func captureCanonicalExcerpt(w http.ResponseWriter, blocks []domain.Block) {
	writer, ok := w.(*observedWriter)
	if !ok || !writer.excerptEnabled || writer.writeFailed || writer.status < 200 || writer.status >= 300 {
		return
	}
	text, tools, truncated := observation.VisibleBlocks(blocks)
	writer.appendExcerpt(text)
	writer.excerptTools = tools
	writer.excerptTruncated = writer.excerptTruncated || truncated
	writer.excerptRecognized = text != "" || len(tools) > 0
}

func observeIngress(eng Engine, protocol domain.Protocol, w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request, func()) {
	if observer, ok := eng.(ingressPhaseObserver); ok {
		r = r.WithContext(context.WithValue(r.Context(), ingressPhaseObserverKey{}, observer))
	}
	observer, ok := eng.(ingressObserver)
	if !ok {
		return w, r, func() {}
	}
	ctx, finish := observer.ObserveIngress(r.Context(), protocol, requestMeta(r, nil))
	writer := &observedWriter{ResponseWriter: w}
	content, hasContent := eng.(excerptObserver)
	if hasContent {
		writer.excerptEnabled = content.ObservationExcerptEnabled(ctx)
	}
	if output, ok := eng.(firstOutputObserver); ok {
		writer.onFirstOutput = func() { output.ObserveFirstOutput(ctx) }
	}
	return writer, r.WithContext(ctx), func() {
		if writer.excerptEnabled {
			text, truncated := observation.Clip(strings.ToValidUTF8(writer.excerptText, ""), observation.ExcerptLimit)
			state := "empty"
			if text != "" || len(writer.excerptTools) > 0 {
				state = "recorded"
			} else if !writer.excerptRecognized {
				state = "unsupported"
			}
			if writer.writeFailed || ctx.Err() != nil {
				state = "partial"
			}
			content.ObserveResponseExcerpt(ctx, domain.ContentPreview{ResponseExcerpt: text, ToolNames: writer.excerptTools, ContentState: state, Truncated: truncated || writer.excerptTruncated})
		}
		if delivery, ok := eng.(deliveryObserver); ok {
			delivery.ObserveDelivery(ctx, writer.writeFailed)
		}
		finish(writer.status)
	}
}
