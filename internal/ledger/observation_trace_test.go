package ledger

import (
	"fmt"
	"testing"
	"time"

	"ferry/internal/domain"
)

func TestObservationTraceMergesDeliveryAndEnrichment(t *testing.T) {
	l := openTemp(t)
	at := time.Now()
	l.BeginIngressRequest(domain.RequestRecord{ID: "trace", Ts: at, Status: "active", ProviderID: "p"})
	l.BeginObservation(domain.ObservationInput{RequestID: "trace", At: at, CallerIdentityID: "caller", ResponsesRoot: true})
	value := 1.0
	tr := &domain.TraceRecord{SchemaVersion: 1, RouteSnapshot: &domain.RouteSnapshot{SchemaVersion: 1, Steps: []domain.RouteStepSnapshot{{ProviderID: "p", ProviderName: "Original"}}},
		Candidates: []domain.CandidateRecord{{ProviderID: "p", Conditions: []domain.CandidateCondition{{Code: "health", Result: "pass", Value: &value}}}},
		Events:     []domain.ExecutionTraceEvent{{At: at, Type: "UPSTREAM_DISPATCHED", ProviderID: "p"}}}
	l.SetTrace("trace", tr)
	l.RecordFirstOutput("trace", at.Add(time.Millisecond))
	failure := domain.ExecutionTraceEvent{At: at.Add(2 * time.Millisecond), Type: "ATTEMPT_FAILED", ProviderID: "p", Attempt: 1}
	tr.Events = append(tr.Events, failure)
	l.SetTrace("trace", tr)
	tr.Events[1].FailureType = domain.FailRateLimit
	tr.Events[1].Message = "rate limited"
	l.SetTrace("trace", tr)
	l.FinishRequest("trace", func(r *domain.RequestRecord) {
		r.Status = domain.ReqSuccess
		r.Outcome = domain.OutcomeRecoveredSuccess
	})
	l.RecordDelivery("trace", true)
	l.EndIngressRequest("trace")
	// Both source-owned mutable descendants must remain detached from storage.
	tr.RouteSnapshot.Steps[0].ProviderName = "Changed"
	*tr.Candidates[0].Conditions[0].Value = 99
	flushLiveTest(t, l.Ledger)
	got, err := l.QueryTrace("trace")
	if err != nil || got == nil {
		t.Fatalf("trace: %+v %v", got, err)
	}
	if len(got.Events) != 4 {
		t.Fatalf("events duplicated or lost: %+v", got.Events)
	}
	for i, event := range got.Events {
		if event.Seq != uint64(i+1) {
			t.Fatalf("sequence: %+v", got.Events)
		}
	}
	if got.Events[1].Type != "CLIENT_FIRST_OUTPUT" || got.Events[2].FailureType != domain.FailRateLimit || got.Events[3].Type != "CLIENT_DELIVERY_INTERRUPTED" {
		t.Fatalf("facts: %+v", got.Events)
	}
	if got.RouteSnapshot.Steps[0].ProviderName != "Original" || *got.Candidates[0].Conditions[0].Value != 1 {
		t.Fatal("trace shares caller-owned data")
	}
	record, err := l.QueryRequest("trace")
	if err != nil || record.Status != domain.ReqCanceled {
		t.Fatalf("delivery failure reported success: %+v %v", record, err)
	}
	o := mustObservation(t, l.Ledger, "trace")
	if o.TraceRevision != got.Revision || o.LastEventSeq != 4 || o.ActiveProviderID != "" {
		t.Fatalf("torn observation: %+v", o)
	}
}

func TestObservationAttemptCompletionIsOneCommittedSnapshot(t *testing.T) {
	l := openTemp(t)
	at := time.Now()
	l.BeginIngressRequest(domain.RequestRecord{ID: "atomic-completion", Ts: at, Status: "active", ProviderID: "p"})
	l.BeginObservation(domain.ObservationInput{RequestID: "atomic-completion", At: at, CallerIdentityID: "caller", ResponsesRoot: true})
	l.SetTrace("atomic-completion", &domain.TraceRecord{SchemaVersion: 1, Events: []domain.ExecutionTraceEvent{{At: at, Type: "UPSTREAM_DISPATCHED", ProviderID: "p", ConnectionID: "c", Attempt: 1}}})
	flushLiveTest(t, l.Ledger)
	l.AddAttemptObservation(domain.AttemptRecord{RequestID: "atomic-completion", Seq: 1, Ts: at, ProviderID: "p", ConnectionID: "c", Status: domain.ReqFailed, UpstreamDispatched: true, CostState: "known"}, domain.ExecutionTraceEvent{At: at.Add(time.Millisecond), Type: "ATTEMPT_FAILED", ProviderID: "p", ConnectionID: "c", Attempt: 1})
	deadline := time.Now().Add(time.Second)
	for {
		bundle, err := l.QueryRequestObservationBundle("atomic-completion")
		if err != nil {
			t.Fatal(err)
		}
		if len(bundle.Attempts) > 0 {
			if bundle.Observation.ActiveProviderID != "" || bundle.Observation.CostState != "known" || bundle.Trace.Events[len(bundle.Trace.Events)-1].Type != "ATTEMPT_FAILED" || bundle.Trace.Revision != bundle.Observation.TraceRevision {
				t.Fatalf("mixed completion snapshot: %+v trace=%+v", bundle.Observation, bundle.Trace)
			}
			break
		}
		if bundle.Observation.ActiveProviderID != "p" || time.Now().After(deadline) {
			t.Fatal("inconsistent prior snapshot or completion did not commit")
		}
	}
}

func TestCoalescedLiveWritesKeepObservationCreationBeforeTrace(t *testing.T) {
	l := openTemp(t)
	blocker, err := l.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	at := time.Now()
	l.BeginIngressRequest(domain.RequestRecord{ID: "queued-trace", Ts: at, Status: "active", ProviderID: "p"})
	l.BeginObservation(domain.ObservationInput{RequestID: "queued-trace", At: at, CallerIdentityID: "caller", ResponsesRoot: true})
	l.SetTrace("queued-trace", &domain.TraceRecord{SchemaVersion: 1, Events: []domain.ExecutionTraceEvent{{At: at, Type: "UPSTREAM_DISPATCHED", ProviderID: "p"}}})
	for i := 0; i < 100; i++ {
		l.UpdateRequest("queued-trace", func(r *domain.RequestRecord) { r.Status = "streaming" })
	}
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	flushLiveTest(t, l.Ledger)
	bundle, err := l.QueryRequestObservationBundle("queued-trace")
	if err != nil || bundle.Observation == nil || bundle.Trace == nil {
		t.Fatalf("observation and trace must survive queued creation: %+v %v", bundle, err)
	}
	if bundle.Observation.ActiveProviderID != "p" || bundle.Observation.TraceRevision != bundle.Trace.Revision {
		t.Fatalf("trace overtook observation creation: %+v %+v", bundle.Observation, bundle.Trace)
	}
}

func TestObservationTraceCapacityRetainsBoundaryAndTerminal(t *testing.T) {
	l := openTemp(t)
	at := time.Now()
	l.BeginRequest(domain.RequestRecord{ID: "many-events", Ts: at})
	events := make([]domain.ExecutionTraceEvent, 600)
	for i := range events {
		events[i] = domain.ExecutionTraceEvent{At: at.Add(time.Duration(i) * time.Microsecond), Type: fmt.Sprintf("EVENT_%d", i)}
	}
	events[599].Type = "COMPLETE"
	l.SetTrace("many-events", &domain.TraceRecord{SchemaVersion: 1, Events: events})
	got, err := l.QueryTrace("many-events")
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if len(got.Events) != 512 || got.EventsTruncated != 88 || got.Events[0].Seq != 1 || got.Events[511].Seq != 600 || got.Events[511].Type != "COMPLETE" {
		t.Fatalf("capacity: events=%d omitted=%d last=%+v", len(got.Events), got.EventsTruncated, got.Events[len(got.Events)-1])
	}
}

func TestObservationUsageKnownZeroPartialAndIdempotency(t *testing.T) {
	l := openTemp(t)
	at := time.Now()
	l.BeginRequest(domain.RequestRecord{ID: "usage", Ts: at})
	l.BeginObservation(domain.ObservationInput{RequestID: "usage", At: at, CallerIdentityID: "caller", ResponsesRoot: true})
	a := domain.AttemptRecord{RequestID: "usage", Seq: 1, Ts: at, Status: domain.ReqSuccess, Serving: true, UsageState: "known", CostState: "known", UpstreamDispatched: true}
	l.AddAttempt(a)
	l.AddAttempt(a)
	flushLiveTest(t, l.Ledger)
	if o := mustObservation(t, l.Ledger, "usage"); o.UsageState != "known" || o.CostState != "known" {
		t.Fatalf("known zero: %+v", o)
	}
	a.Seq = 2
	a.Serving = false
	a.UsageState = "unknown"
	a.CostState = "unknown"
	a.Status = domain.ReqFailed
	l.AddAttempt(a)
	flushLiveTest(t, l.Ledger)
	if o := mustObservation(t, l.Ledger, "usage"); o.UsageState != "known" || o.CostState != "partial" {
		t.Fatalf("recovery coverage: %+v", o)
	}
	attempts, err := l.QueryAttempts("usage")
	if err != nil || len(attempts) != 2 || attempts[0].UsageState != "known" || !attempts[0].UpstreamDispatched {
		t.Fatalf("attempt evidence: %+v %v", attempts, err)
	}
}
