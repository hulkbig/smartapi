package ledger

import (
	"database/sql"
	"encoding/json"
	"time"
)

type ObservationTiming struct {
	DurationSamples int64  `json:"duration_samples"`
	DurationP50MS   *int64 `json:"duration_p50_ms"`
	DurationP95MS   *int64 `json:"duration_p95_ms"`
	TTFTSamples     int64  `json:"ttft_samples"`
	TTFTP50MS       *int64 `json:"ttft_p50_ms"`
	TTFTP95MS       *int64 `json:"ttft_p95_ms"`
}

type ObservationSessionDetail struct {
	AsOf    time.Time                 `json:"as_of"`
	Scope   map[string]any            `json:"scope"`
	Session ObservationSource         `json:"session"`
	Stats   ObservationStats          `json:"stats"`
	Timing  ObservationTiming         `json:"timing"`
	Metrics ObservationSessionMetrics `json:"metrics"`
}

type ObservationSessionMetrics struct {
	RecoveryCount          int64    `json:"recovery_count"`
	RecoveryAddedLatencyMS int64    `json:"recovery_added_latency_ms"`
	ProviderIDs            []string `json:"provider_ids"`
	ConnectionIDs          []string `json:"connection_ids"`
}

const observationRecoveryMetricsQuery = `SELECT COALESCE(SUM(connection_rebind_count+route_fallback_count),0),COALESCE(SUM(recovery_added_latency_ms),0) FROM matched`

const observationParticipantsQuery = `, participants AS (
 SELECT a.provider_id,a.connection_id FROM matched m JOIN attempts a ON a.request_id=m.id
 UNION SELECT ro.active_provider_id,m.connection_id FROM matched m
 JOIN request_observations ro ON ro.request_id=m.id WHERE ro.active_provider_id!='')
 SELECT COALESCE((SELECT json_group_array(DISTINCT provider_id) FROM participants WHERE provider_id!=''),'[]'),
 COALESCE((SELECT json_group_array(DISTINCT connection_id) FROM participants WHERE connection_id!=''),'[]')`

func (l *Ledger) QueryObservationSession(id string, q ObservationQuery) (*ObservationSessionDetail, error) {
	tx, err := l.read.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var title, caller string
	var created int64
	err = tx.QueryRow(`SELECT title_excerpt,caller_identity_id,created_at FROM observation_sessions WHERE id=?`, id).Scan(&title, &caller, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	q.Filter.SessionID = id
	q.Mode = "session"
	q.SessionsOnly = true
	q.Limit = 1
	q.Cursor = ""
	page, err := queryObservationSources(tx, q)
	if err != nil {
		return nil, err
	}
	detail := &ObservationSessionDetail{AsOf: page.AsOf, Scope: page.Scope, Stats: page.Stats,
		Session: ObservationSource{SessionID: id, SourceID: "session:" + id, SourceKind: "session", Title: title, CallerIdentityID: caller, FirstAt: time.UnixMilli(created).UTC(), Models: []string{}, Providers: []string{}, CostState: "unknown", UsageState: "unknown"}}
	if len(page.Sources) > 0 {
		detail.Session = page.Sources[0]
	}
	if detail.Session.Title == "" {
		detail.Session.Title = "Session · " + id
	}
	cte, args := observationCTE(q)
	detail.Metrics.ProviderIDs = []string{}
	detail.Metrics.ConnectionIDs = []string{}
	if err := tx.QueryRow(cte+observationRecoveryMetricsQuery, args...).Scan(&detail.Metrics.RecoveryCount, &detail.Metrics.RecoveryAddedLatencyMS); err != nil {
		return nil, err
	}
	var providers, connections string
	err = tx.QueryRow(cte+observationParticipantsQuery, args...).Scan(&providers, &connections)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(providers), &detail.Metrics.ProviderIDs); err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(connections), &detail.Metrics.ConnectionIDs); err != nil {
		return nil, err
	}
	quantiles := func(column, condition string) (int64, *int64, *int64, error) {
		var samples int64
		var p50, p95 sql.NullInt64
		err := tx.QueryRow(cte+`, samples AS (SELECT `+column+` AS value,ROW_NUMBER() OVER(ORDER BY `+column+`) AS ordinal,
		 COUNT(*) OVER() AS n FROM matched WHERE `+condition+`) SELECT COALESCE(MAX(n),0),
		 MIN(CASE WHEN ordinal>=(n*50+99)/100 THEN value END),MIN(CASE WHEN ordinal>=(n*95+99)/100 THEN value END) FROM samples`, args...).Scan(&samples, &p50, &p95)
		var median, tail *int64
		if p50.Valid {
			median = &p50.Int64
		}
		if p95.Valid {
			tail = &p95.Int64
		}
		return samples, median, tail, err
	}
	detail.Timing.DurationSamples, detail.Timing.DurationP50MS, detail.Timing.DurationP95MS, err = quantiles("duration_ms", `is_active=0 AND duration_ms>0`)
	if err != nil {
		return nil, err
	}
	detail.Timing.TTFTSamples, detail.Timing.TTFTP50MS, detail.Timing.TTFTP95MS, err = quantiles("ttft_ms", `ttft_ms>0`)
	return detail, err
}
