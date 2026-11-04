package ledger

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ferry/internal/domain"
	"ferry/internal/observation"
)

type queryExecer interface {
	execer
	QueryRow(string, ...any) *sql.Row
}

// atomicJob keeps multi-statement jobs atomic in the writer's individual replay
// path too. Normally the writer already supplies a shared batch transaction.
func atomicJob(fn func(queryExecer) error) writeJob {
	return func(db execer) error {
		if tx, ok := db.(*sql.Tx); ok {
			return fn(tx)
		}
		tx, err := db.(*sql.DB).Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit()
	}
}

func (l *Ledger) migrateObservations() error {
	for _, column := range []struct{ name, definition string }{
		{"usage_state", "TEXT NOT NULL DEFAULT ''"}, {"cost_state", "TEXT NOT NULL DEFAULT ''"},
		{"upstream_dispatched", "INTEGER NOT NULL DEFAULT 0"}, {"serving", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := addColumnIfMissing(l.db, "attempts", column.name, column.definition); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS observation_sessions (
		 id TEXT PRIMARY KEY, caller_identity_id TEXT NOT NULL, created_at INTEGER NOT NULL,
		 title_excerpt TEXT NOT NULL DEFAULT '', title_source TEXT NOT NULL DEFAULT 'metadata',
		 title_request_id TEXT NOT NULL DEFAULT '', title_request_ts INTEGER NOT NULL DEFAULT 0,
		 schema_version INTEGER NOT NULL DEFAULT 1)`,
		`CREATE TABLE IF NOT EXISTS observation_session_aliases (
		 caller_identity_id TEXT NOT NULL, source TEXT NOT NULL, key_hash TEXT NOT NULL,
		 session_id TEXT NOT NULL, created_at INTEGER NOT NULL, last_seen_at INTEGER NOT NULL,
		 PRIMARY KEY(caller_identity_id, source, key_hash))`,
		`CREATE INDEX IF NOT EXISTS idx_observation_alias_session ON observation_session_aliases(session_id)`,
		`CREATE TABLE IF NOT EXISTS request_observations (
		 request_id TEXT PRIMARY KEY, session_id TEXT NOT NULL DEFAULT '', caller_identity_id TEXT NOT NULL DEFAULT '',
		 client_kind TEXT NOT NULL DEFAULT '', request_ts INTEGER NOT NULL, updated_at INTEGER NOT NULL,
		 content_mode TEXT NOT NULL DEFAULT 'metadata_only', selected_provider_id TEXT NOT NULL DEFAULT '',
		 active_provider_id TEXT NOT NULL DEFAULT '', activity_phase TEXT NOT NULL DEFAULT '',
		 observation TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_ro_session ON request_observations(session_id, request_ts, request_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ro_caller ON request_observations(caller_identity_id, request_ts)`,
		`CREATE INDEX IF NOT EXISTS idx_ro_active_provider ON request_observations(active_provider_id, request_ts)`,
		`CREATE INDEX IF NOT EXISTS idx_ro_selected_provider ON request_observations(selected_provider_id, activity_phase, request_ts)`,
		`CREATE INDEX IF NOT EXISTS idx_attempts_provider_request ON attempts(provider_id, request_id)`,
	} {
		if _, err := l.db.Exec(statement); err != nil {
			return fmt.Errorf("migrate observations: %w", err)
		}
	}
	var mode string
	if err := l.db.QueryRow(`SELECT value FROM runtime_settings WHERE key='observability_content_mode'`).Scan(&mode); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	l.excerptCapture.Store(mode == "excerpt")
	return nil
}

func (l *Ledger) ObservationContentMode() string {
	if l != nil && l.excerptCapture.Load() {
		return "excerpt"
	}
	return "metadata_only"
}

func (l *Ledger) SetObservationContentMode(mode string) error {
	if mode != "excerpt" && mode != "metadata_only" {
		return fmt.Errorf("invalid observability_content_mode")
	}
	if _, err := l.db.Exec(`INSERT INTO runtime_settings(key,value,updated_at) VALUES('observability_content_mode',?,?)
	 ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, mode, time.Now().UnixMilli()); err != nil {
		return err
	}
	l.excerptCapture.Store(mode == "excerpt")
	return nil
}

func observationID() (string, error) {
	var bits [16]byte
	if _, err := rand.Read(bits[:]); err != nil {
		return "", err
	}
	return "ses_" + hex.EncodeToString(bits[:]), nil
}

func loadObservation(db queryExecer, id string) (*domain.RequestObservation, error) {
	var data, mode string
	err := db.QueryRow(`SELECT observation,content_mode FROM request_observations WHERE request_id=?`, id).Scan(&data, &mode)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result domain.RequestObservation
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		return nil, err
	}
	result.ContentMode = mode
	return &result, nil
}

func saveObservation(db execer, o *domain.RequestObservation) error {
	data, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO request_observations(request_id,session_id,caller_identity_id,client_kind,request_ts,updated_at,content_mode,selected_provider_id,active_provider_id,activity_phase,observation)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET session_id=excluded.session_id,
	 caller_identity_id=excluded.caller_identity_id,client_kind=excluded.client_kind,updated_at=excluded.updated_at,
	 selected_provider_id=excluded.selected_provider_id,active_provider_id=excluded.active_provider_id,
	 activity_phase=excluded.activity_phase,observation=excluded.observation`,
		o.RequestID, o.SessionID, o.CallerIdentityID, o.ClientKind, o.RequestAt.UnixMilli(), o.UpdatedAt.UnixMilli(), o.ContentMode, o.SelectedProviderID, o.ActiveProviderID, o.ActivityPhase, string(data))
	return err
}

func (l *Ledger) QueryRequestObservation(id string) (*domain.RequestObservation, error) {
	return loadObservation(l.read, id)
}

// BeginObservation may be called again after parsing with richer evidence. The
// first entry's content policy is retained, and aliases are resolved exclusively
// on the existing single writer: no DB read/wait is added to the streaming path.
func (l *Ledger) BeginObservation(input domain.ObservationInput) {
	input.Evidence = slices.Clone(input.Evidence)
	mode := l.ObservationContentMode()
	if input.ContentMode == "metadata_only" || input.ContentMode == "excerpt" {
		mode = input.ContentMode
	}
	l.enqueue(atomicJob(func(db queryExecer) error {
		o, err := loadObservation(db, input.RequestID)
		if err != nil {
			return err
		}
		if o == nil {
			o = &domain.RequestObservation{SchemaVersion: 1, RequestID: input.RequestID, RequestAt: input.At,
				ContentMode: mode, UsageState: "unknown", CostState: "unknown", AssociationState: "unlinked",
				ContentPreview: domain.ContentPreview{ContentState: "disabled"}}
			if mode == "excerpt" {
				o.ContentPreview.ContentState = "pending"
			}
		}
		if o.CallerIdentityID != input.CallerIdentityID {
			o.SessionID = ""
			o.AssociationState = "unlinked"
			o.AssociationSource = ""
		}
		o.UpdatedAt = time.Now()
		o.CallerIdentityID = input.CallerIdentityID
		o.ClientKind = observation.ClientKind(input.Evidence)
		if err := resolveObservationSession(db, o, input); err != nil {
			return err
		}
		return saveObservation(db, o)
	}))
}

func resolveObservationSession(db queryExecer, o *domain.RequestObservation, input domain.ObservationInput) error {
	if input.CallerIdentityID == "" {
		o.SessionID = ""
		o.AssociationState = "unauthenticated"
		return nil
	}
	refs := []domain.AffinityEvidence{}
	seen := map[string]bool{}
	for _, e := range input.Evidence {
		if e.Scope != domain.AffinityConversation || e.Confidence != domain.ConfHigh || e.KeyHash == "" {
			continue
		}
		switch e.Source {
		case "codex.thread", "claude-code.session", "hermes.session_id", "openclaw.session_key", "responses.conversation", "responses.previous_response_id":
		default:
			continue
		}
		key := e.Source + ":" + e.KeyHash
		if !seen[key] {
			refs = append(refs, e)
			seen[key] = true
		}
	}
	bound := map[string]bool{}
	var hints []domain.AffinityEvidence
	unknownLineage := false
	unknownHints := 0
	for _, e := range refs {
		var id string
		err := db.QueryRow(`SELECT session_id FROM observation_session_aliases WHERE caller_identity_id=? AND source=? AND key_hash=?`, input.CallerIdentityID, e.Source, e.KeyHash).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if id != "" {
			bound[id] = true
		}
		if strings.HasPrefix(e.Source, "responses.") {
			if id == "" {
				unknownLineage = true
			}
		} else {
			hints = append(hints, e)
			if id == "" {
				unknownHints++
			}
		}
	}
	if len(bound) > 1 || (len(hints) > 1 && (len(bound) == 0 || unknownHints > 0)) {
		o.SessionID = ""
		o.AssociationState = "conflict"
		o.AssociationSource = "conflicting_evidence"
		return nil
	}
	id := ""
	for candidate := range bound {
		id = candidate
	}
	if id == "" && len(hints) == 0 && unknownLineage {
		o.SessionID = ""
		o.AssociationState = "unverified_lineage"
		o.AssociationSource = "responses.lineage"
		return nil
	}
	if id == "" && len(hints) == 0 && !input.ResponsesRoot {
		// An ingress-only call must not undo a previously proven association.
		return nil
	}
	if id == "" {
		id = o.SessionID
	}
	if id == "" {
		var err error
		id, err = observationID()
		if err != nil {
			return err
		}
		if _, err = db.Exec(`INSERT INTO observation_sessions(id,caller_identity_id,created_at) VALUES(?,?,?)`, id, input.CallerIdentityID, input.At.UnixMilli()); err != nil {
			return err
		}
	}
	for _, e := range hints {
		if _, err := db.Exec(`INSERT INTO observation_session_aliases(caller_identity_id,source,key_hash,session_id,created_at,last_seen_at)
		 VALUES(?,?,?,?,?,?) ON CONFLICT(caller_identity_id,source,key_hash) DO UPDATE SET last_seen_at=excluded.last_seen_at`, input.CallerIdentityID, e.Source, e.KeyHash, id, input.At.UnixMilli(), input.At.UnixMilli()); err != nil {
			return err
		}
	}
	o.SessionID = id
	o.AssociationState = "linked"
	o.AssociationSource = "responses.root"
	if len(refs) > 0 {
		o.AssociationSource = refs[0].Source
	}
	return nil
}

func (l *Ledger) mutateObservation(id string, mutate func(*domain.RequestObservation)) {
	l.enqueue(atomicJob(func(db queryExecer) error {
		o, err := loadObservation(db, id)
		if err != nil || o == nil {
			return err
		}
		mutate(o)
		o.UpdatedAt = time.Now()
		if err := saveObservation(db, o); err != nil {
			return err
		}
		if o.SessionID != "" && o.ContentPreview.RequestExcerpt != "" {
			_, err = db.Exec(`UPDATE observation_sessions SET title_excerpt=?,title_source='request',title_request_id=?,title_request_ts=?
			 WHERE id=? AND (title_request_id='' OR title_request_ts>? OR (title_request_ts=? AND title_request_id>?))`, o.ContentPreview.RequestExcerpt, o.RequestID, o.RequestAt.UnixMilli(), o.SessionID, o.RequestAt.UnixMilli(), o.RequestAt.UnixMilli(), o.RequestID)
		}
		return err
	}))
}

func (l *Ledger) CaptureRequestExcerpt(id string, request *domain.RequestContract) {
	// The extractor is bounded and runs once; no request body enters the queue.
	preview := observation.RequestPreview(request, true)
	l.mutateObservation(id, func(o *domain.RequestObservation) {
		if o.ContentMode == "excerpt" {
			o.ContentPreview = preview
		}
	})
}

func (l *Ledger) CaptureResponseExcerpt(id string, preview domain.ContentPreview) {
	preview.ToolNames = slices.Clone(preview.ToolNames)
	l.mutateObservation(id, func(o *domain.RequestObservation) {
		if o.ContentMode != "excerpt" {
			return
		}
		p := o.ContentPreview
		p.ResponseExcerpt = preview.ResponseExcerpt
		if len(preview.ToolNames) > 0 {
			p.ToolNames = preview.ToolNames
		}
		p.Truncated = p.Truncated || preview.Truncated
		p.ContentState = preview.ContentState
		if p.ContentState == "empty" && p.RequestExcerpt != "" {
			p.ContentState = "recorded"
		}
		o.ContentPreview = observation.SelectPreview(p, nil)
	})
}

// RegisterObservationLineage accepts SmartAPI-owned IDs only. Raw values are
// hashed before entering the job; lookup always includes the stable caller ID.
func (l *Ledger) RegisterObservationLineage(id, responseID, conversationID string) {
	var refs []domain.AffinityEvidence
	if strings.HasPrefix(responseID, "resp_smart_") {
		refs = append(refs, domain.NewAffinityEvidence("responses.previous_response_id", domain.AffinityConversation, responseID, domain.ConfHigh))
	}
	if strings.HasPrefix(conversationID, "conv_smart_") {
		refs = append(refs, domain.NewAffinityEvidence("responses.conversation", domain.AffinityConversation, conversationID, domain.ConfHigh))
	}
	l.enqueue(atomicJob(func(db queryExecer) error {
		o, err := loadObservation(db, id)
		if err != nil || o == nil || o.SessionID == "" || o.CallerIdentityID == "" {
			return err
		}
		for _, ref := range refs {
			_, err = db.Exec(`INSERT INTO observation_session_aliases(caller_identity_id,source,key_hash,session_id,created_at,last_seen_at)
			 VALUES(?,?,?,?,?,?) ON CONFLICT(caller_identity_id,source,key_hash) DO NOTHING`, o.CallerIdentityID, ref.Source, ref.KeyHash, o.SessionID, o.RequestAt.UnixMilli(), time.Now().UnixMilli())
			if err != nil {
				return err
			}
		}
		return nil
	}))
}

func (l *Ledger) SetObservationActivity(id, selected, active, phase string) {
	l.mutateObservation(id, func(o *domain.RequestObservation) {
		o.SelectedProviderID = selected
		o.ActiveProviderID = active
		o.ActivityPhase = phase
	})
}

func (l *Ledger) purgeObservations(cutoff time.Time) error {
	for {
		result, err := l.db.Exec(`DELETE FROM request_observations WHERE request_id IN (SELECT request_id FROM request_observations
		 WHERE request_ts<? AND NOT EXISTS(SELECT 1 FROM live_requests lr WHERE lr.id=request_id) LIMIT ?)`, cutoff.UnixMilli(), purgeBatch)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n < purgeBatch {
			break
		}
	}
	// Never retain a title whose source has left retention. The next retained
	// meaningful input can be selected by Session reads without reviving content.
	if _, err := l.db.Exec(`UPDATE observation_sessions SET title_excerpt='',title_source='metadata',title_request_id='',title_request_ts=0
	 WHERE title_request_id!='' AND NOT EXISTS(SELECT 1 FROM request_observations ro WHERE ro.request_id=title_request_id)`); err != nil {
		return err
	}
	if _, err := l.db.Exec(`WITH retained AS (
	 SELECT ro.session_id,ro.request_id,ro.request_ts,json_extract(ro.observation,'$.content_preview.request_excerpt') AS excerpt,
	 ROW_NUMBER() OVER(PARTITION BY ro.session_id ORDER BY ro.request_ts,ro.request_id) AS ordinal
	 FROM request_observations ro JOIN observation_sessions s ON s.id=ro.session_id
	 WHERE s.title_request_id='' AND COALESCE(json_extract(ro.observation,'$.content_preview.request_excerpt'),'')!='')
	 UPDATE observation_sessions AS s SET title_excerpt=r.excerpt,title_source='request',title_request_id=r.request_id,title_request_ts=r.request_ts
	 FROM retained r WHERE s.id=r.session_id AND r.ordinal=1`); err != nil {
		return err
	}
	if _, err := l.db.Exec(`DELETE FROM observation_session_aliases WHERE NOT EXISTS(SELECT 1 FROM request_observations ro WHERE ro.session_id=observation_session_aliases.session_id)`); err != nil {
		return err
	}
	_, err := l.db.Exec(`DELETE FROM observation_sessions WHERE NOT EXISTS(SELECT 1 FROM request_observations ro WHERE ro.session_id=observation_sessions.id)`)
	return err
}
