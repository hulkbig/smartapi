package ledger

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"ferry/internal/domain"
)

var ErrObservationCursor = errors.New("invalid or mismatched observation cursor")

type ObservationQuery struct {
	Filter       domain.LogFilter
	Mode         string
	SessionsOnly bool
	Limit        int
	Cursor       string
	// Context binds a cursor to the selected window control, even when its
	// concrete from/to anchor is carried forward from the first response.
	Context string
}

type observationCursor struct {
	Signature string    `json:"signature"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Active    int       `json:"active"`
	LastAt    int64     `json:"last_at"`
	ID        string    `json:"id"`
}

func DecodeObservationCursorWindow(raw string) (time.Time, time.Time, error) {
	c, err := decodeObservationCursor(raw)
	return c.From, c.To, err
}

func decodeObservationCursor(raw string) (observationCursor, error) {
	var c observationCursor
	if len(raw) > 2048 {
		return c, ErrObservationCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(data, &c) != nil || c.ID == "" || (c.Active != 0 && c.Active != 1) || c.LastAt <= 0 || c.To.Before(c.From) {
		return c, ErrObservationCursor
	}
	return c, nil
}

func observationQuerySignature(q ObservationQuery) string {
	q.Cursor = ""
	q.Limit = 0
	q.Filter.Limit = 0
	q.Filter.Offset = 0
	data, _ := json.Marshal(q)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type ObservationStats struct {
	Sessions              int64        `json:"sessions"`
	ActiveSessions        int64        `json:"active_sessions"`
	FailedSessions        int64        `json:"failed_sessions"`
	RecoveredSessions     int64        `json:"recovered_sessions"`
	IndependentRequests   int64        `json:"independent_requests"`
	AssociatedRequests    int64        `json:"associated_requests"`
	Requests              int64        `json:"requests"`
	ActiveRequests        int64        `json:"active_requests"`
	Succeeded             int64        `json:"succeeded"`
	Failed                int64        `json:"failed"`
	Canceled              int64        `json:"canceled"`
	Recovered             int64        `json:"recovered"`
	ParticipatingRequests int64        `json:"participating_requests"`
	PendingRequests       int64        `json:"pending_requests"`
	AverageRequests       *float64     `json:"average_requests_per_session"`
	SuccessRate           *float64     `json:"success_rate"`
	KnownCost             *float64     `json:"known_cost"`
	CostState             string       `json:"cost_state"`
	CostKnownRequests     int64        `json:"cost_known_requests"`
	CostPartialRequests   int64        `json:"cost_partial_requests"`
	Usage                 domain.Usage `json:"usage"`
	UsageState            string       `json:"usage_state"`
	UsageKnownRequests    int64        `json:"usage_known_requests"`
	UsagePartialRequests  int64        `json:"usage_partial_requests"`
	AssociationCoverage   *float64     `json:"association_coverage"`
}

type ObservationSource struct {
	SourceID              string                     `json:"source_id"`
	SourceKind            string                     `json:"source_kind"`
	SessionID             string                     `json:"session_id,omitempty"`
	Title                 string                     `json:"title"`
	CallerIdentityID      string                     `json:"caller_identity_id"`
	Caller                string                     `json:"caller"`
	ClientKind            string                     `json:"client_kind"`
	FirstAt               time.Time                  `json:"first_at"`
	LastAt                time.Time                  `json:"last_at"`
	RequestCount          int64                      `json:"request_count"`
	ActiveRequests        int64                      `json:"active_requests"`
	Succeeded             int64                      `json:"succeeded"`
	Failed                int64                      `json:"failed"`
	Canceled              int64                      `json:"canceled"`
	Recovered             int64                      `json:"recovered"`
	ParticipatingRequests int64                      `json:"participating_request_count"`
	PendingRequests       int64                      `json:"pending_request_count"`
	Usage                 domain.Usage               `json:"usage"`
	UsageState            string                     `json:"usage_state"`
	Cost                  *float64                   `json:"known_cost"`
	CostState             string                     `json:"cost_state"`
	CostScope             string                     `json:"cost_scope"`
	Models                []string                   `json:"logical_models"`
	Providers             []string                   `json:"provider_ids"`
	SelectedRequestID     string                     `json:"selected_request_id"`
	MatchingRequestID     string                     `json:"matching_request_id"`
	Request               *domain.RequestRecord      `json:"-"`
	Observation           *domain.RequestObservation `json:"-"`
	SummaryPreview        domain.ContentPreview      `json:"-"`
	activeRank            int
	lastMillis            int64
}

type ObservationSourcePage struct {
	AsOf       time.Time           `json:"as_of"`
	Scope      map[string]any      `json:"scope"`
	Stats      ObservationStats    `json:"stats"`
	Total      int                 `json:"total"`
	Sources    []ObservationSource `json:"sources"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

func observationScope(q ObservationQuery) map[string]any {
	f := q.Filter
	return map[string]any{"from": f.From.UTC(), "to": f.To.UTC(), "q": f.Search, "caller_id": f.CallerIdentityID,
		"api_key": f.ConsumerKey, "logical_model": f.LogicalModel, "participating_provider": f.ParticipatingProvider,
		"session_id": f.SessionID, "status": f.Status, "outcome": f.Outcome, "mode": q.Mode}
}

// matched contains a narrow projection of one final-wins request set. All
// aggregation and selected-card reads run in the same SQLite read transaction.
func observationCTE(q ObservationQuery) (string, []any) {
	f := q.Filter
	search, provider := f.Search, f.ParticipatingProvider
	f.Search = ""
	f.ParticipatingProvider = ""
	where, args := buildWhere(f)
	if where == "" {
		where = "WHERE 1=1"
	}
	participating := `(EXISTS(SELECT 1 FROM attempts pa WHERE pa.request_id=logical_requests.id) OR COALESCE(ro.active_provider_id,'')!='')`
	pending := `(COALESCE(ro.activity_phase,'')='waiting')`
	if provider != "" {
		participating = `(EXISTS(SELECT 1 FROM attempts pa WHERE pa.request_id=logical_requests.id AND pa.provider_id=?) OR COALESCE(ro.active_provider_id,'')=?)`
		pending = `(COALESCE(ro.activity_phase,'')='waiting' AND COALESCE(ro.selected_provider_id,'')=?)`
		// These values occur in SELECT before the WHERE's parameters.
	}
	selectArgs := []any{}
	if provider != "" {
		selectArgs = []any{provider, provider, provider}
	}
	if search != "" {
		like := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(search) + "%"
		where += ` AND (logical_requests.id=? OR (NOT EXISTS(SELECT 1 FROM logical_requests exact_request WHERE exact_request.id=?) AND
		 (logical_requests.id LIKE ? ESCAPE '\' OR COALESCE(ro.session_id,'') LIKE ? ESCAPE '\'
		 OR logical_requests.consumer_key LIKE ? ESCAPE '\' OR COALESCE(ro.client_kind,'') LIKE ? ESCAPE '\'
		 OR COALESCE(json_extract(ro.observation,'$.content_preview.request_excerpt'),'') LIKE ? ESCAPE '\'
		 OR COALESCE(json_extract(ro.observation,'$.content_preview.response_excerpt'),'') LIKE ? ESCAPE '\'
		 OR EXISTS(SELECT 1 FROM observation_sessions os WHERE os.id=ro.session_id AND os.title_excerpt LIKE ? ESCAPE '\'))))`
		args = append(args, search, search)
		for range 7 {
			args = append(args, like)
		}
	}
	group := `CASE WHEN COALESCE(ro.session_id,'')!='' THEN 'session:'||ro.session_id ELSE 'request:'||logical_requests.id END`
	if q.Mode == "request" {
		group = `'request:'||logical_requests.id`
	}
	// Session metrics revisit the final-wins source for a few matching IDs.
	// Materializing its wide SELECT * would copy the entire retained ledger to
	// SQLite temporary storage before applying those IDs, even for one Session.
	cte := "WITH logical_requests AS NOT MATERIALIZED " + strings.TrimSpace(liveRequestsSource) + `, observed AS (SELECT logical_requests.id,logical_requests.ts,logical_requests.consumer_key,
	 COALESCE(NULLIF(logical_requests.logical_model_id,''),logical_requests.model_requested) AS logical_model,
	 logical_requests.provider_id,logical_requests.status,logical_requests.outcome,
	 logical_requests.connection_id,logical_requests.connection_rebind_count,logical_requests.route_fallback_count,logical_requests.recovery_added_latency_ms,
	 logical_requests.input_tokens,logical_requests.output_tokens,logical_requests.cache_read_tokens,logical_requests.cache_write_tokens,
	 logical_requests.reasoning_tokens,logical_requests.duration_ms,logical_requests.ttft_ms,
	 CASE WHEN logical_requests.serving_cost!=0 OR logical_requests.recovery_overhead_cost!=0
	 THEN logical_requests.serving_cost+logical_requests.recovery_overhead_cost ELSE logical_requests.est_cash_cost END AS request_cost,
	 COALESCE(NULLIF(json_extract(ro.observation,'$.cost_state'),''),CASE WHEN logical_requests.est_cash_cost!=0 OR logical_requests.serving_cost!=0 OR logical_requests.recovery_overhead_cost!=0 THEN 'partial' ELSE 'unknown' END) AS cost_state,
	 COALESCE(NULLIF(json_extract(ro.observation,'$.usage_state'),''),CASE WHEN logical_requests.input_tokens!=0 OR logical_requests.output_tokens!=0 THEN 'partial' ELSE 'unknown' END) AS usage_state,
	 COALESCE(ro.session_id,'') AS session_id,COALESCE(ro.caller_identity_id,'') AS caller_identity_id,COALESCE(ro.client_kind,'api') AS client_kind,
	 logical_requests.status NOT IN ('success','failed','canceled') AS is_active,
	 ` + participating + ` AS participating,` + pending + ` AS pending,` + group + ` AS group_id
	 FROM logical_requests LEFT JOIN request_observations ro ON ro.request_id=logical_requests.id ` + where + `),
	 matched AS MATERIALIZED (SELECT * FROM observed`
	if provider != "" {
		cte += ` WHERE participating OR pending`
	}
	cte += `) `
	return cte, append(selectArgs, args...)
}

const observationAggregate = `COUNT(*),COALESCE(SUM(is_active),0),COALESCE(SUM(status='success'),0),COALESCE(SUM(status='failed'),0),
 COALESCE(SUM(status='canceled'),0),COALESCE(SUM(outcome='RECOVERED_SUCCESS' AND status='success'),0),
 COALESCE(SUM(participating),0),COALESCE(SUM(pending),0),
 COALESCE(SUM(CASE WHEN cost_state IN ('known','partial') THEN request_cost ELSE 0 END),0),
 COALESCE(SUM(cost_state='known'),0),COALESCE(SUM(cost_state='partial'),0),
 COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(cache_read_tokens),0),COALESCE(SUM(cache_write_tokens),0),COALESCE(SUM(reasoning_tokens),0),
 COALESCE(SUM(usage_state='known'),0),COALESCE(SUM(usage_state='partial'),0)`

func coverageState(total, known, partial int64) string {
	if total > 0 && known == total {
		return "known"
	}
	if known > 0 || partial > 0 {
		return "partial"
	}
	return "unknown"
}

func queryObservationStats(tx *sql.Tx, cte string, args []any) (ObservationStats, error) {
	var s ObservationStats
	var cost float64
	err := tx.QueryRow(cte+`SELECT COUNT(DISTINCT NULLIF(session_id,'')),COUNT(DISTINCT CASE WHEN is_active THEN NULLIF(session_id,'') END),
	 COUNT(DISTINCT CASE WHEN status='failed' THEN NULLIF(session_id,'') END),
	 COUNT(DISTINCT CASE WHEN status='success' AND outcome='RECOVERED_SUCCESS' THEN NULLIF(session_id,'') END),
	 COALESCE(SUM(session_id=''),0),COALESCE(SUM(session_id!=''),0),`+observationAggregate+` FROM matched`, args...).Scan(
		&s.Sessions, &s.ActiveSessions, &s.FailedSessions, &s.RecoveredSessions, &s.IndependentRequests, &s.AssociatedRequests,
		&s.Requests, &s.ActiveRequests, &s.Succeeded, &s.Failed, &s.Canceled, &s.Recovered, &s.ParticipatingRequests, &s.PendingRequests,
		&cost, &s.CostKnownRequests, &s.CostPartialRequests, &s.Usage.InputTokens, &s.Usage.OutputTokens, &s.Usage.CacheReadTokens, &s.Usage.CacheWriteTokens, &s.Usage.ReasoningTokens, &s.UsageKnownRequests, &s.UsagePartialRequests)
	if err != nil {
		return s, err
	}
	if s.Sessions > 0 {
		n := float64(s.AssociatedRequests) / float64(s.Sessions)
		s.AverageRequests = &n
	}
	if n := s.Succeeded + s.Failed; n > 0 {
		rate := float64(s.Succeeded) / float64(n)
		s.SuccessRate = &rate
	}
	if s.Requests > 0 {
		coverage := float64(s.AssociatedRequests) / float64(s.Requests)
		s.AssociationCoverage = &coverage
	}
	if s.CostKnownRequests+s.CostPartialRequests > 0 {
		s.KnownCost = &cost
	}
	s.CostState = coverageState(s.Requests, s.CostKnownRequests, s.CostPartialRequests)
	s.UsageState = coverageState(s.Requests, s.UsageKnownRequests, s.UsagePartialRequests)
	return s, nil
}

func (l *Ledger) QueryObservationSources(q ObservationQuery) (*ObservationSourcePage, error) {
	tx, err := l.read.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return queryObservationSources(tx, q)
}

func queryObservationSources(tx *sql.Tx, q ObservationQuery) (*ObservationSourcePage, error) {
	if q.Mode == "" {
		q.Mode = "session"
	}
	if q.Mode != "session" && q.Mode != "request" {
		return nil, fmt.Errorf("mode must be session or request")
	}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	q.Limit = min(q.Limit, 200)
	var cursor observationCursor
	if q.Cursor != "" {
		var err error
		cursor, err = decodeObservationCursor(q.Cursor)
		if err != nil || cursor.Signature != observationQuerySignature(q) {
			return nil, ErrObservationCursor
		}
	}
	page := &ObservationSourcePage{AsOf: time.Now().UTC(), Scope: observationScope(q), Sources: []ObservationSource{}}
	cte, args := observationCTE(q)
	stats, err := queryObservationStats(tx, cte, args)
	page.Stats = stats
	if err != nil {
		return nil, fmt.Errorf("observation stats: %w", err)
	}
	groupWhere := ""
	if q.SessionsOnly {
		groupWhere = "WHERE session_id!=''"
	}
	page.Total = int(stats.Sessions + stats.IndependentRequests)
	if q.SessionsOnly {
		page.Total = int(stats.Sessions)
	} else if q.Mode == "request" {
		page.Total = int(stats.Requests)
	}
	groups := cte + `, groups AS (SELECT group_id,MAX(session_id) AS session_id,MAX(caller_identity_id) AS caller_identity_id,
	 MAX(consumer_key) AS caller,MAX(client_kind) AS client_kind,MIN(ts) AS first_at,MAX(ts) AS last_at,MAX(is_active) AS active_rank,
	 COUNT(*) AS requests,SUM(is_active) AS active_requests,SUM(status='success') AS succeeded,SUM(status='failed') AS failed,
	 SUM(status='canceled') AS canceled,SUM(status='success' AND outcome='RECOVERED_SUCCESS') AS recovered,
	 SUM(participating) AS participating_requests,SUM(pending) AS pending_requests,
	 SUM(CASE WHEN cost_state IN ('known','partial') THEN request_cost ELSE 0 END) AS cost,
	 SUM(cost_state='known') AS cost_known,SUM(cost_state='partial') AS cost_partial,
	 SUM(input_tokens) AS input_tokens,SUM(output_tokens) AS output_tokens,SUM(cache_read_tokens) AS cache_read_tokens,
	 SUM(cache_write_tokens) AS cache_write_tokens,SUM(reasoning_tokens) AS reasoning_tokens,
	 SUM(usage_state='known') AS usage_known,SUM(usage_state='partial') AS usage_partial,json_group_array(DISTINCT logical_model) AS models
	 FROM matched ` + groupWhere + ` GROUP BY group_id), page_groups AS MATERIALIZED (SELECT * FROM groups g`
	queryArgs := append([]any(nil), args...)
	if q.Cursor != "" {
		groups += ` WHERE g.active_rank<? OR (g.active_rank=? AND g.last_at<?) OR (g.active_rank=? AND g.last_at=? AND g.group_id>?)`
		queryArgs = append(queryArgs, cursor.Active, cursor.Active, cursor.LastAt, cursor.Active, cursor.LastAt, cursor.ID)
	}
	groups += ` ORDER BY g.active_rank DESC,g.last_at DESC,g.group_id ASC LIMIT ?),
	 page_requests AS MATERIALIZED (SELECT m.* FROM matched m JOIN page_groups g ON g.group_id=m.group_id),
	 selected_requests AS (SELECT group_id,id,ROW_NUMBER() OVER(PARTITION BY group_id ORDER BY (id=?) DESC,is_active DESC,ts DESC,id DESC) AS ordinal FROM page_requests),
	 latest_responses AS (SELECT m.group_id,ro.observation,ROW_NUMBER() OVER(PARTITION BY m.group_id ORDER BY m.ts DESC,m.id DESC) AS ordinal
	 FROM page_requests m JOIN request_observations ro ON ro.request_id=m.id WHERE COALESCE(json_extract(ro.observation,'$.content_preview.response_excerpt'),'')!=''),
	 participations AS (SELECT m.group_id,a.provider_id FROM page_requests m JOIN attempts a ON a.request_id=m.id WHERE a.provider_id!=''
	 UNION SELECT group_id,provider_id FROM page_requests WHERE provider_id!='' AND (is_active OR participating)),
	 provider_groups AS (SELECT group_id,json_group_array(DISTINCT provider_id) AS providers FROM participations GROUP BY group_id) `
	queryArgs = append(queryArgs, q.Limit+1, q.Filter.Search)
	query := groups + `SELECT g.group_id,g.session_id,g.caller_identity_id,g.caller,g.client_kind,g.first_at,g.last_at,g.active_rank,
	 g.requests,g.active_requests,g.succeeded,g.failed,g.canceled,g.recovered,g.participating_requests,g.pending_requests,
	 g.cost,g.cost_known,g.cost_partial,g.input_tokens,g.output_tokens,g.cache_read_tokens,g.cache_write_tokens,g.reasoning_tokens,g.usage_known,g.usage_partial,
	 g.models,COALESCE(pg.providers,'[]'),COALESCE(os.title_excerpt,''),sr.id,COALESCE(lr.observation,'')
	 FROM page_groups g LEFT JOIN observation_sessions os ON os.id=g.session_id LEFT JOIN provider_groups pg ON pg.group_id=g.group_id
	 LEFT JOIN selected_requests sr ON sr.group_id=g.group_id AND sr.ordinal=1
	 LEFT JOIN latest_responses lr ON lr.group_id=g.group_id AND lr.ordinal=1
	 ORDER BY g.active_rank DESC,g.last_at DESC,g.group_id ASC`
	rows, err := tx.Query(query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("observation sources: %w", err)
	}
	for rows.Next() {
		var s ObservationSource
		var first, last int64
		var cost float64
		var ck, cp, uk, up int64
		var models, providers, latestResponse string
		err := rows.Scan(&s.SourceID, &s.SessionID, &s.CallerIdentityID, &s.Caller, &s.ClientKind, &first, &last, &s.activeRank,
			&s.RequestCount, &s.ActiveRequests, &s.Succeeded, &s.Failed, &s.Canceled, &s.Recovered, &s.ParticipatingRequests, &s.PendingRequests,
			&cost, &ck, &cp, &s.Usage.InputTokens, &s.Usage.OutputTokens, &s.Usage.CacheReadTokens, &s.Usage.CacheWriteTokens, &s.Usage.ReasoningTokens, &uk, &up,
			&models, &providers, &s.Title, &s.SelectedRequestID, &latestResponse)
		if err != nil {
			rows.Close()
			return nil, err
		}
		s.lastMillis = last
		s.FirstAt = time.UnixMilli(first).UTC()
		s.LastAt = time.UnixMilli(last).UTC()
		s.SourceKind = "request"
		if strings.HasPrefix(s.SourceID, "session:") {
			s.SourceKind = "session"
		}
		s.CostState = coverageState(s.RequestCount, ck, cp)
		s.UsageState = coverageState(s.RequestCount, uk, up)
		if ck+cp > 0 {
			s.Cost = &cost
		}
		s.CostScope = "participating_requests_total"
		if err := json.Unmarshal([]byte(models), &s.Models); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(providers), &s.Providers); err != nil {
			rows.Close()
			return nil, err
		}
		s.MatchingRequestID = s.SelectedRequestID
		if s.SourceKind == "session" && latestResponse != "" {
			var latest domain.RequestObservation
			if err := json.Unmarshal([]byte(latestResponse), &latest); err != nil {
				rows.Close()
				return nil, err
			}
			s.SummaryPreview = latest.ContentPreview
			s.SummaryPreview.RequestExcerpt = s.Title
			s.SummaryPreview.PreviewRole = "response"
			s.SummaryPreview.PreviewText = s.SummaryPreview.ResponseExcerpt
			s.SummaryPreview.PreviewReason = "session_latest_response"
		}
		page.Sources = append(page.Sources, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(page.Sources) > q.Limit {
		page.Sources = page.Sources[:q.Limit]
		last := page.Sources[len(page.Sources)-1]
		encoded, _ := json.Marshal(observationCursor{Signature: observationQuerySignature(q), From: q.Filter.From, To: q.Filter.To, Active: last.activeRank, LastAt: last.lastMillis, ID: last.SourceID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}
	ids := make([]any, len(page.Sources))
	for i, s := range page.Sources {
		ids[i] = s.SelectedRequestID
	}
	if len(ids) > 0 {
		requestRows, err := tx.Query(liveRequestsCTE+`SELECT `+requestCols+` FROM logical_requests WHERE id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, ids...)
		if err != nil {
			return nil, err
		}
		records := []domain.RequestRecord{}
		byID := map[string]*domain.RequestRecord{}
		for requestRows.Next() {
			rec, err := scanRequest(requestRows)
			if err != nil {
				requestRows.Close()
				return nil, err
			}
			records = append(records, *rec)
			byID[rec.ID] = rec
		}
		if err := requestRows.Err(); err != nil {
			requestRows.Close()
			return nil, err
		}
		requestRows.Close()
		metadata, err := queryRequestObservations(tx, records)
		if err != nil {
			return nil, err
		}
		for i := range page.Sources {
			s := &page.Sources[i]
			s.Request = byID[s.SelectedRequestID]
			s.Observation = metadata[s.SelectedRequestID]
			if s.Request != nil {
				s.Caller = s.Request.ConsumerKey
			}
			if s.Observation != nil && s.Observation.ClientKind != "" {
				s.ClientKind = s.Observation.ClientKind
			}
			if s.Title == "" && s.Observation != nil {
				s.Title = s.Observation.ContentPreview.RequestExcerpt
			}
			if s.Title == "" {
				s.Title = s.ClientKind + " · " + strings.TrimPrefix(s.SourceID, s.SourceKind+":")
			}
		}
	}
	return page, nil
}
