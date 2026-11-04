package scheduler

import (
	"context"
	"testing"
	"time"

	"ferry/internal/domain"
)

func TestRoutingObservationPreservesExcludedAndEmptySteps(t *testing.T) {
	f := newV2Fixture(t)
	f.publish(func(s *domain.CompiledSnapshot) {
		s.Providers["provider-1"].Name = "Original provider"
		s.Connections["connection-b"].Enabled = false
		s.LogicalModels["smart"].DefaultRoute.Steps = append(s.LogicalModels["smart"].DefaultRoute.Steps, domain.RouteStep{ProviderID: "provider-2", UpstreamModel: "no-connections"})
	})
	d, failure := f.sch.decide(context.Background(), chatReq("smart"), RequestMeta{BearerToken: "sk"}, true)
	if failure != nil || d == nil {
		t.Fatal(failure)
	}
	if len(d.routeSnapshot.Steps) != 3 || d.routeSnapshot.Steps[2].State != "no_connections" {
		t.Fatalf("empty step missing: %+v", d.routeSnapshot)
	}
	if len(d.plan.Candidates) != 3 {
		t.Fatalf("candidate inventory: %+v", d.plan.Candidates)
	}
	found := false
	for _, c := range d.plan.Candidates {
		if c.CandidateID == "" || c.ProviderID == "" || c.ConnectionID == "" || c.RouteStep < 1 {
			t.Fatalf("identity missing: %+v", c)
		}
		if c.ConnectionID == "connection-b" {
			found = true
			if c.Eligible || c.ProviderName != "Original provider" {
				t.Fatalf("excluded snapshot: %+v", c)
			}
			for _, condition := range c.Conditions {
				if condition.Code == "connection_enabled" && condition.Result != "fail" {
					t.Fatalf("gate: %+v", condition)
				}
				if condition.Code == "protocol_compatibility" && condition.Result != "not_evaluated" {
					t.Fatalf("short circuit: %+v", condition)
				}
			}
		}
	}
	if !found {
		t.Fatal("excluded connection hidden")
	}
	f.publish(func(s *domain.CompiledSnapshot) { s.Providers["provider-1"].Name = "Renamed provider" })
	if d.routeSnapshot.Steps[0].ProviderName != "Original provider" {
		t.Fatal("current config rewrote historical provider")
	}
}

func TestRoutingObservationUnknownEvidenceAndActualRecoveryStep(t *testing.T) {
	f := newV2Fixture(t)
	d, failure := f.sch.decide(context.Background(), chatReq("smart"), RequestMeta{BearerToken: "sk"}, true)
	if failure != nil {
		t.Fatal(failure)
	}
	reason, conditions := assessV2Eligibility(d.w, d.w.snap.Targets[d.plan.Primary], domain.ProtocolOpenAIChat, domain.Requirements{}, &domain.TargetState{}, domain.ConnectionState{Enabled: true, Auth: domain.ConnectionAuthUnknown, Reachability: domain.ConnectionReachabilityUnknown}, time.Now())
	if reason != "" {
		t.Fatal(reason)
	}
	for _, c := range conditions {
		if (c.Code == "authentication" || c.Code == "reachability" || c.Code == "quota") && c.Result != "unknown" {
			t.Fatalf("invented pass: %+v", c)
		}
	}
	f.calls["p1-a"].connect = &domain.Failure{Type: domain.FailProvider5xx, Scope: domain.FailureScopeProvider}
	result, failure := f.sch.Execute(context.Background(), chatReq("smart"), RequestMeta{BearerToken: "sk"})
	if failure != nil {
		t.Fatal(failure)
	}
	db := f.ledger.reopen(t)
	trace, err := db.QueryTrace(result.RequestID)
	if err != nil || trace == nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range trace.Events {
		if event.Type == "ATTEMPT_SUCCEEDED" && event.ProviderID == "provider-2" {
			found = true
			if event.RouteStep != 2 {
				t.Fatalf("recovery attached to wrong route step: %+v", event)
			}
		}
	}
	if !found {
		t.Fatal("no successful recovered attempt")
	}
}
