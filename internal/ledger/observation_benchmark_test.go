package ledger

import (
	"fmt"
	"testing"
	"time"

	"ferry/internal/domain"
)

// Includes draining the async writer, so a larger backlog cannot masquerade as
// cheap capture. No network, real credentials or production database is used.
func BenchmarkObservationLifecycle(b *testing.B) {
	for _, mode := range []string{"baseline", "metadata_only", "excerpt"} {
		b.Run(mode, func(b *testing.B) {
			l, err := Open(b.TempDir(), 256, 0)
			if err != nil {
				b.Fatal(err)
			}
			if mode == "excerpt" {
				if err = l.SetObservationContentMode(mode); err != nil {
					b.Fatal(err)
				}
			}
			rc := &domain.RequestContract{Messages: []domain.Message{{Role: domain.RoleUser, Blocks: []domain.Block{{Kind: domain.BlockText, Text: "Synthetic bounded request"}}}}}
			at := time.Now()
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				id := fmt.Sprintf("bench-%d", i)
				l.BeginRequest(domain.RequestRecord{ID: id, Ts: at, Status: domain.ReqSuccess})
				if mode != "baseline" {
					l.BeginObservation(domain.ObservationInput{RequestID: id, At: at, CallerIdentityID: "caller", Evidence: []domain.AffinityEvidence{observationHint("codex.thread", fmt.Sprint(i%10), domain.AffinityConversation)}})
					l.CaptureRequestExcerpt(id, rc)
					l.CaptureResponseExcerpt(id, domain.ContentPreview{ResponseExcerpt: "Synthetic visible response", ContentState: "recorded"})
				}
				attempt := domain.AttemptRecord{RequestID: id, Seq: 1, Ts: at, Status: domain.ReqSuccess, Serving: true, UsageState: "known", CostState: "known"}
				if mode == "baseline" {
					l.AddAttempt(attempt)
				} else {
					l.AddAttemptObservation(attempt, domain.ExecutionTraceEvent{At: at, Type: "ATTEMPT_SUCCEEDED"})
				}
				l.FinishRequest(id, nil)
			}
			if err = l.Close(); err != nil {
				b.Fatal(err)
			}
		})
	}
}
