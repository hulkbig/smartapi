package scheduler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"ferry/internal/domain"
	"ferry/internal/policy"
)

func observationEvidence(meta RequestMeta, rc *domain.RequestContract) []domain.AffinityEvidence {
	evidence := meta.ObservationEvidence
	if len(evidence) == 0 {
		evidence = meta.AffinityEvidence
	}
	out := append([]domain.AffinityEvidence(nil), evidence...)
	if rc == nil || rc.Lineage == nil {
		return out
	}
	for _, item := range []struct{ source, value string }{{"responses.conversation", rc.Lineage.ConversationID}, {"responses.previous_response_id", rc.Lineage.PreviousResponseID}} {
		if item.value == "" {
			continue
		}
		e := domain.NewAffinityEvidence(item.source, domain.AffinityConversation, item.value, domain.ConfHigh)
		found := false
		for _, existing := range out {
			if existing.Source == e.Source && existing.KeyHash == e.KeyHash {
				found = true
			}
		}
		if !found {
			out = append(out, e)
		}
	}
	return out
}

func captureRouteSnapshot(w *world, route domain.RouteSpec, plan *policy.PlanResult, at time.Time) *domain.RouteSnapshot {
	snapshot := &domain.RouteSnapshot{SchemaVersion: 1, RuntimeRevision: w.snap.Version, CapturedAt: at, Coverage: "complete", Steps: []domain.RouteStepSnapshot{}}
	for i, step := range route.Steps {
		row := domain.RouteStepSnapshot{RouteStep: i + 1, ProviderID: step.ProviderID, ProviderName: step.ProviderID, UpstreamModel: step.UpstreamModel, State: "no_connections", Reason: "no matching connections"}
		if provider := w.snap.Providers[step.ProviderID]; provider != nil && provider.Name != "" {
			row.ProviderName = provider.Name
		}
		if plan != nil {
			for _, c := range plan.Candidates {
				if c.RouteStep == i+1 {
					row.CandidateCount++
					if c.Eligible {
						row.State = "eligible"
					}
				}
			}
		}
		if row.CandidateCount > 0 {
			row.Reason = ""
			if row.State != "eligible" {
				row.State = "excluded"
			}
		}
		snapshot.Steps = append(snapshot.Steps, row)
	}
	return snapshot
}

func candidateSnapshot(w *world, target *domain.CompiledTarget, tier string, step int, ingress domain.Protocol) domain.CandidateRecord {
	identity := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s", step, target.ID)))
	row := domain.CandidateRecord{Target: target.ID, Tier: tier, RouteStep: step, CandidateID: "candidate_" + hex.EncodeToString(identity[:12]),
		ProviderID: target.ProviderID, ProviderName: target.ProviderID, ConnectionID: target.ConnectionID, ConnectionName: target.ConnectionID, UpstreamModel: target.Model, Protocol: target.EgressProtocol(ingress)}
	if provider := w.snap.Providers[target.ProviderID]; provider != nil && provider.Name != "" {
		row.ProviderName = provider.Name
	}
	if connection := w.snap.Connections[target.ConnectionID]; connection != nil && connection.Identity != "" {
		row.ConnectionName = connection.Identity
	}
	return row
}
