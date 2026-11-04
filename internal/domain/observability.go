package domain

import "time"

// ContentPreview contains only bounded, explicitly enabled display excerpts.
// It is independent of debug wire capture and never contains hidden reasoning.
type ContentPreview struct {
	RequestExcerpt  string   `json:"request_excerpt,omitempty"`
	ResponseExcerpt string   `json:"response_excerpt,omitempty"`
	ToolNames       []string `json:"tool_names,omitempty"`
	PreviewRole     string   `json:"preview_role,omitempty"`
	PreviewText     string   `json:"preview_text,omitempty"`
	PreviewReason   string   `json:"preview_reason,omitempty"`
	ContentState    string   `json:"content_state"`
	Truncated       bool     `json:"truncated"`
}

// RequestObservation is a projection alongside the immutable billing ledger.
// CallerIdentityID is APIKeyIdentity.ID, never a rotating secret or display name.
type RequestObservation struct {
	SchemaVersion      int            `json:"schema_version"`
	RequestID          string         `json:"request_id"`
	SessionID          string         `json:"session_id,omitempty"`
	CallerIdentityID   string         `json:"caller_identity_id,omitempty"`
	ClientKind         string         `json:"client_kind,omitempty"`
	ConversationID     string         `json:"conversation_id,omitempty"`
	RequestAt          time.Time      `json:"request_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	AssociationState   string         `json:"association_state"`
	AssociationSource  string         `json:"association_source,omitempty"`
	ContentMode        string         `json:"-"`
	ContentPreview     ContentPreview `json:"content_preview"`
	UsageState         string         `json:"usage_state"`
	CostState          string         `json:"cost_state"`
	SelectedProviderID string         `json:"selected_provider_id,omitempty"`
	ActiveProviderID   string         `json:"active_provider_id,omitempty"`
	ActivityPhase      string         `json:"activity_phase,omitempty"`
	TraceRevision      uint64         `json:"trace_revision"`
	LastEventSeq       uint64         `json:"last_event_seq"`
}

// ObservationInput carries only already-hashed evidence into the single writer.
type ObservationInput struct {
	ContentMode                 string
	RequestID, CallerIdentityID string
	At                          time.Time
	Protocol                    Protocol
	Evidence                    []AffinityEvidence
	// ResponsesRoot is true only for a normalized root Responses request.
	ResponsesRoot bool
}

type CandidateCondition struct {
	Code       string    `json:"condition_code"`
	Result     string    `json:"result"` // pass | fail | unknown | not_evaluated
	Reason     string    `json:"reason,omitempty"`
	Value      *float64  `json:"value,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
	Source     string    `json:"source,omitempty"`
}

type RouteStepSnapshot struct {
	RouteStep      int    `json:"route_step"`
	ProviderID     string `json:"provider_id"`
	ProviderName   string `json:"provider_name"`
	UpstreamModel  string `json:"upstream_model"`
	CandidateCount int    `json:"candidate_count"`
	State          string `json:"state"`
	Reason         string `json:"reason,omitempty"`
}

type RouteSnapshot struct {
	SchemaVersion   int                 `json:"schema_version"`
	RuntimeRevision int64               `json:"runtime_revision"`
	CapturedAt      time.Time           `json:"captured_at"`
	Coverage        string              `json:"coverage"`
	Steps           []RouteStepSnapshot `json:"route_steps"`
}
