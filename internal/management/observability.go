package management

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ferry/internal/domain"
	"ferry/internal/ledger"
)

func v2ObservedRequestSummary(record *domain.RequestRecord, o *domain.RequestObservation) map[string]any {
	conversation := ""
	if o != nil {
		conversation = o.ConversationID
	}
	item := v2RequestSummary(record, conversation)
	if o == nil {
		return item
	}
	item["session_id"] = nil
	if o.SessionID != "" {
		item["session_id"] = o.SessionID
	}
	item["caller_identity_id"] = o.CallerIdentityID
	item["client_kind"] = o.ClientKind
	item["content_preview"] = o.ContentPreview
	item["observation"] = o
	item["cost_observed"] = o.CostState == "known" || o.CostState == "partial"
	item["cost_state"] = o.CostState
	item["usage_state"] = o.UsageState
	return item
}

func publicRouteSnapshot(trace *domain.TraceRecord) map[string]any {
	coverage := "legacy"
	steps := []domain.RouteStepSnapshot{}
	version := 0
	var runtimeRevision int64
	var captured any
	if trace.RouteSnapshot != nil {
		coverage = trace.RouteSnapshot.Coverage
		steps = trace.RouteSnapshot.Steps
		version = trace.RouteSnapshot.SchemaVersion
		runtimeRevision = trace.RouteSnapshot.RuntimeRevision
		captured = trace.RouteSnapshot.CapturedAt
	}
	candidates := make([]map[string]any, 0, len(trace.Candidates))
	for _, c := range trace.Candidates {
		reasonCode := ""
		for _, condition := range c.Conditions {
			if condition.Result == "fail" {
				reasonCode = condition.Code
				break
			}
		}
		candidates = append(candidates, map[string]any{
			"candidate_id": c.CandidateID, "route_step": c.RouteStep, "provider_id": c.ProviderID, "provider_name": c.ProviderName,
			"connection_id": c.ConnectionID, "connection_name": c.ConnectionName, "upstream_model": c.UpstreamModel, "protocol": c.Protocol,
			"eligible": c.Eligible, "exclusion_reason": c.ExclusionReason, "exclusion_code": reasonCode, "tier": c.Tier, "rank": c.Rank,
			"scores": c.Scores, "conditions": c.Conditions, "capability_status": c.CapabilityStatuses,
		})
	}
	return map[string]any{"schema_version": version, "runtime_revision": runtimeRevision, "coverage": coverage, "captured_at": captured, "route_steps": steps, "candidates": candidates}
}

// Execution decisions append evidence; the initial candidate conditions stay
// immutable. This projection never reruns eligibility against current state.
func publicDecisionUpdates(events []domain.ExecutionTraceEvent) []map[string]any {
	updates := []map[string]any{}
	for _, event := range events {
		switch event.Type {
		case "ADMISSION_FAILED", "ATTEMPT_FAILED", "CONNECTION_REBOUND", "ROUTE_FALLBACK", "PROVIDER_EXHAUSTED", "TARGET_SKIPPED":
			updates = append(updates, map[string]any{"seq": event.Seq, "at": event.At, "type": event.Type,
				"route_step": event.RouteStep, "provider_id": event.ProviderID, "connection_id": event.ConnectionID,
				"failure_type": event.FailureType, "reason": event.Message, "source": "execution"})
		}
	}
	return updates
}

type observationSourceView struct {
	ledger.ObservationSource
	ID                    string                `json:"id"`
	SelectedRequest       map[string]any        `json:"selected_request,omitempty"`
	SelectedRequestStatus string                `json:"selected_request_status,omitempty"`
	ContentPreview        domain.ContentPreview `json:"content_preview"`
}

func (s *Server) observationSourceView(source ledger.ObservationSource) observationSourceView {
	id := source.SessionID
	if source.SourceKind == "request" {
		id = source.SelectedRequestID
	}
	view := observationSourceView{ObservationSource: source, ID: id, ContentPreview: domain.ContentPreview{ContentState: "legacy_unavailable"}}
	if source.Request != nil {
		view.SelectedRequest = v2ObservedRequestSummary(source.Request, source.Observation)
		s.addReferencePricing(view.SelectedRequest, source.Request)
		view.SelectedRequestStatus = string(source.Request.Status)
	}
	if source.Observation != nil {
		view.ContentPreview = source.Observation.ContentPreview
	}
	if source.SummaryPreview.ContentState != "" {
		view.ContentPreview = source.SummaryPreview
	}
	return view
}

func observationCoverage(stats ledger.ObservationStats) map[string]any {
	return map[string]any{"associated_requests": stats.AssociatedRequests, "independent_requests": stats.IndependentRequests,
		"association_ratio": stats.AssociationCoverage, "usage_known_requests": stats.UsageKnownRequests,
		"usage_partial_requests": stats.UsagePartialRequests, "cost_known_requests": stats.CostKnownRequests, "cost_partial_requests": stats.CostPartialRequests}
}

func parseObservationQuery(r *http.Request, sessionID string) (ledger.ObservationQuery, error) {
	values := r.URL.Query()
	q := ledger.ObservationQuery{Mode: values.Get("mode"), Cursor: values.Get("cursor"), Limit: 50, Context: values.Get("window") + "|" + values.Get("scope")}
	if q.Mode == "" {
		q.Mode = "session"
	}
	if q.Mode != "session" && q.Mode != "request" {
		return q, fmt.Errorf("mode must be session or request")
	}
	if raw := values.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			return q, fmt.Errorf("limit must be between 1 and 200")
		}
		q.Limit = n
	}
	if len([]rune(values.Get("q"))) > 200 {
		return q, fmt.Errorf("q must contain at most 200 characters")
	}
	var from, to time.Time
	var err error
	if values.Get("scope") == "all" {
		if sessionID == "" || values.Has("from") || values.Has("to") || values.Has("window") || q.Cursor != "" {
			return q, fmt.Errorf("scope=all is available only for one Session without window bounds")
		}
		to = time.Now()
	} else {
		if values.Get("scope") != "" {
			return q, fmt.Errorf("invalid scope")
		}
		if q.Cursor != "" && !values.Has("from") && !values.Has("to") {
			if _, err = v2WindowDuration(r); err != nil {
				return q, err
			}
			from, to, err = ledger.DecodeObservationCursorWindow(q.Cursor)
			if err == nil {
				from, to, err = validateV2Window(from, to, time.Now())
			}
		} else {
			from, to, err = v2Window(r)
		}
		if err != nil {
			return q, err
		}
	}
	q.Filter = domain.LogFilter{From: from, To: to, SessionID: sessionID, Search: values.Get("q"), CallerIdentityID: values.Get("caller_id"),
		ConsumerKey: values.Get("api_key"), LogicalModel: values.Get("logical_model"), ParticipatingProvider: values.Get("participating_provider"),
		Status: domain.RequestStatus(strings.ToLower(values.Get("status"))), Outcome: domain.RequestOutcome(strings.ToUpper(values.Get("outcome")))}
	switch q.Filter.Status {
	case "", "received", "waiting", "active", "recovering", "streaming", "delivering", domain.ReqSuccess, domain.ReqFailed, domain.ReqCanceled:
	default:
		return q, fmt.Errorf("invalid status")
	}
	switch q.Filter.Outcome {
	case "", domain.OutcomeDirectSuccess, domain.OutcomeRecoveredSuccess, domain.OutcomeFailed:
	default:
		return q, fmt.Errorf("invalid outcome")
	}
	return q, nil
}

func (s *Server) handleObservationSessions(w http.ResponseWriter, r *http.Request) {
	if !s.observationAvailable(w) {
		return
	}
	q, err := parseObservationQuery(r, "")
	if err != nil {
		writeMgmtErr(w, http.StatusBadRequest, err.Error())
		return
	}
	q.Mode = "session"
	q.SessionsOnly = true
	page, err := s.deps.Ledger.QueryObservationSources(q)
	if err != nil {
		s.writeObservationError(w, err)
		return
	}
	views := make([]observationSourceView, 0, len(page.Sources))
	for _, source := range page.Sources {
		views = append(views, s.observationSourceView(source))
	}
	writeMgmtJSON(w, http.StatusOK, map[string]any{"as_of": page.AsOf, "scope": page.Scope, "stats": page.Stats, "coverage": observationCoverage(page.Stats), "total": page.Total, "sessions": views, "next_cursor": page.NextCursor})
}

func (s *Server) handleObservationSession(w http.ResponseWriter, r *http.Request) {
	if !s.observationAvailable(w) {
		return
	}
	q, err := parseObservationQuery(r, r.PathValue("id"))
	if err != nil {
		writeMgmtErr(w, http.StatusBadRequest, err.Error())
		return
	}
	detail, err := s.deps.Ledger.QueryObservationSession(r.PathValue("id"), q)
	if err != nil {
		s.writeObservationError(w, err)
		return
	}
	if detail == nil {
		writeMgmtErr(w, http.StatusNotFound, "session not found")
		return
	}
	writeMgmtJSON(w, http.StatusOK, map[string]any{"as_of": detail.AsOf, "scope": detail.Scope, "session": s.observationSourceView(detail.Session), "stats": detail.Stats, "timing": detail.Timing, "metrics": detail.Metrics, "coverage": observationCoverage(detail.Stats)})
}

func (s *Server) handleObservationSessionRequests(w http.ResponseWriter, r *http.Request) {
	if !s.observationAvailable(w) {
		return
	}
	q, err := parseObservationQuery(r, r.PathValue("id"))
	if err != nil {
		writeMgmtErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Request timelines retain the existing Request list's chronological
	// limit/offset contract; Session/source collections use opaque cursors.
	if q.Cursor != "" {
		writeMgmtErr(w, http.StatusBadRequest, "request timelines use limit and offset")
		return
	}
	f := q.Filter
	f.Limit = q.Limit
	f.IncludePendingParticipation = true
	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeMgmtErr(w, http.StatusBadRequest, "invalid offset")
			return
		}
		f.Offset = n
	}
	page, err := s.deps.Ledger.QueryObservedRequests(f)
	if err != nil {
		s.writeObservationError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Requests))
	for i := range page.Requests {
		rec := &page.Requests[i]
		item := v2ObservedRequestSummary(rec, page.Observations[rec.ID])
		s.addReferencePricing(item, rec)
		items = append(items, item)
	}
	writeMgmtJSON(w, http.StatusOK, map[string]any{"as_of": page.AsOf, "scope": observationRequestScope(q), "total": page.Total, "requests": items, "offset": f.Offset, "limit": f.Limit})
}

func observationRequestScope(q ledger.ObservationQuery) map[string]any {
	return map[string]any{"from": q.Filter.From, "to": q.Filter.To, "session_id": q.Filter.SessionID, "participating_provider": q.Filter.ParticipatingProvider, "q": q.Filter.Search}
}

func (s *Server) handleObservationProviderTraffic(w http.ResponseWriter, r *http.Request) {
	if !s.observationAvailable(w) {
		return
	}
	q, err := parseObservationQuery(r, "")
	if err != nil {
		writeMgmtErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if id := r.PathValue("id"); id != "" {
		q.Filter.ParticipatingProvider = id
	}
	page, err := s.deps.Ledger.QueryObservationSources(q)
	if err != nil {
		s.writeObservationError(w, err)
		return
	}
	views := make([]observationSourceView, 0, len(page.Sources))
	for _, source := range page.Sources {
		views = append(views, s.observationSourceView(source))
	}
	writeMgmtJSON(w, http.StatusOK, map[string]any{"as_of": page.AsOf, "scope": page.Scope, "stats": page.Stats, "coverage": observationCoverage(page.Stats), "total": page.Total, "sources": views, "next_cursor": page.NextCursor})
}

func (s *Server) writeObservationError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, ledger.ErrObservationCursor) {
		status = http.StatusBadRequest
	}
	writeMgmtErr(w, status, err.Error())
}

func (s *Server) observationAvailable(w http.ResponseWriter) bool {
	if s.deps.Ledger == nil {
		writeMgmtErr(w, http.StatusServiceUnavailable, "request history is unavailable")
		return false
	}
	return true
}
