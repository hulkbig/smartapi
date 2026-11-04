package ledger

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ferry/internal/domain"
	"ferry/internal/observation"
)

type queryReader interface {
	queryExecer
	Query(string, ...any) (*sql.Rows, error)
}

type ObservedRequestPage struct {
	AsOf         time.Time
	Requests     []domain.RequestRecord
	Observations map[string]*domain.RequestObservation
	Total        int
}

func (l *Ledger) QueryObservedRequests(f domain.LogFilter) (*ObservedRequestPage, error) {
	tx, err := l.read.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	where, args := buildWhere(f)
	page := &ObservedRequestPage{AsOf: time.Now().UTC(), Requests: []domain.RequestRecord{}}
	if err := tx.QueryRow(liveRequestsCTE+"SELECT COUNT(*) FROM logical_requests "+where, args...).Scan(&page.Total); err != nil {
		return nil, err
	}
	rows, err := tx.Query(liveRequestsCTE+"SELECT "+requestCols+" FROM logical_requests "+where+" ORDER BY ts DESC,id DESC LIMIT ? OFFSET ?", append(args, limitOf(f.Limit), f.Offset)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		rec, err := scanRequest(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		page.Requests = append(page.Requests, *rec)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	page.Observations, err = queryRequestObservations(tx, page.Requests)
	return page, err
}

// One batch query supplies metadata, legacy Conversation IDs, and the previous
// Session excerpt. The indexed predecessor is independent of current filters or
// pagination; no request-level Trace fetch is needed for a list page.
func queryRequestObservations(db queryReader, records []domain.RequestRecord) (map[string]*domain.RequestObservation, error) {
	out := map[string]*domain.RequestObservation{}
	if len(records) == 0 {
		return out, nil
	}
	args := make([]any, len(records))
	for i, rec := range records {
		args[i] = rec.ID
		out[rec.ID] = legacyRequestObservation(rec)
	}
	rows, err := db.Query(`SELECT ids.id,COALESCE(ro.observation,''),COALESCE(ro.content_mode,'metadata_only'),
	 COALESCE(prev.observation,''),COALESCE(json_extract(dt.trace,'$.conversation_id'),'')
	 FROM (`+requestIDSelect(len(records))+`) ids
	 LEFT JOIN request_observations ro ON ro.request_id=ids.id
	 LEFT JOIN request_observations prev ON prev.request_id=(SELECT p.request_id FROM request_observations p
	 WHERE ro.session_id!='' AND p.session_id=ro.session_id AND
	 (p.request_ts<ro.request_ts OR (p.request_ts=ro.request_ts AND p.request_id<ro.request_id))
	 ORDER BY p.request_ts DESC,p.request_id DESC LIMIT 1)
	 LEFT JOIN decision_traces dt ON dt.request_id=ids.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw, mode, previous, conversation string
		if err := rows.Scan(&id, &raw, &mode, &previous, &conversation); err != nil {
			return nil, err
		}
		o := out[id]
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), o); err != nil {
				return nil, fmt.Errorf("decode observation: %w", err)
			}
			o.ContentMode = mode
		}
		if o.ConversationID == "" {
			o.ConversationID = conversation
		}
		var prior *domain.ContentPreview
		if previous != "" {
			var p domain.RequestObservation
			if err := json.Unmarshal([]byte(previous), &p); err != nil {
				return nil, err
			}
			prior = &p.ContentPreview
		}
		o.ContentPreview = observation.SelectPreview(o.ContentPreview, prior)
	}
	return out, rows.Err()
}

func requestIDSelect(n int) string {
	return "SELECT column1 AS id FROM (VALUES " + strings.TrimSuffix(strings.Repeat("(?),", n), ",") + ")"
}

func legacyRequestObservation(rec domain.RequestRecord) *domain.RequestObservation {
	usage, cost := "unknown", "unknown"
	if rec.Usage.InputTokens != 0 || rec.Usage.OutputTokens != 0 || rec.Usage.CacheReadTokens != 0 || rec.Usage.CacheWriteTokens != 0 {
		usage = "partial"
	}
	if rec.ServingCost != 0 || rec.RecoveryOverheadCost != 0 || rec.EstCashCost != 0 {
		cost = "partial"
	}
	return &domain.RequestObservation{RequestID: rec.ID, RequestAt: rec.Ts, AssociationState: "legacy_unavailable", UsageState: usage, CostState: cost, ContentPreview: domain.ContentPreview{ContentState: "legacy_unavailable"}}
}

type RequestObservationBundle struct {
	AsOf        time.Time
	Request     *domain.RequestRecord
	Observation *domain.RequestObservation
	Attempts    []domain.AttemptRecord
	Trace       *domain.TraceRecord
}

func (l *Ledger) QueryRequestObservationBundle(id string) (*RequestObservationBundle, error) {
	tx, err := l.read.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	bundle := &RequestObservationBundle{AsOf: time.Now().UTC(), Attempts: []domain.AttemptRecord{}}
	rec, err := scanRequest(tx.QueryRow(liveRequestsCTE+"SELECT "+requestCols+" FROM logical_requests WHERE id=?", id))
	if err == sql.ErrNoRows {
		return bundle, nil
	}
	if err != nil {
		return nil, err
	}
	bundle.Request = rec
	metadata, err := queryRequestObservations(tx, []domain.RequestRecord{*rec})
	if err != nil {
		return nil, err
	}
	bundle.Observation = metadata[id]
	bundle.Attempts, err = queryAttempts(tx, id)
	if err != nil {
		return nil, err
	}
	var raw string
	err = tx.QueryRow(`SELECT trace FROM decision_traces WHERE request_id=?`, id).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if raw != "" {
		bundle.Trace = &domain.TraceRecord{}
		if err := json.Unmarshal([]byte(raw), bundle.Trace); err != nil {
			return nil, err
		}
	}
	// Legacy publishers intentionally keep pending traces in memory. Preserve
	// that API behavior; new schema-1 observations use only committed bundles.
	if bundle.Observation.SchemaVersion == 0 {
		l.mu.Lock()
		if pending := l.requests[id]; pending != nil && pending.trace != nil {
			bundle.Trace = cloneTraceRecord(pending.trace)
		}
		l.mu.Unlock()
	}
	return bundle, nil
}
