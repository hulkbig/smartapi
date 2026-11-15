package ledger

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"ferry/internal/domain"
)

// APIKeyHistory reports retained evidence, not a promise that earlier history
// is complete. Coverage remains explicitly global because legacy rollups have
// no caller dimension. LastRequestAt includes retained in-flight requests.
type APIKeyHistory struct {
	Timeline               []APIKeyHistoryBucket `json:"timeline"`
	Models                 []APIKeyHistoryModel  `json:"models"`
	Coverage               ReportingCoverage     `json:"coverage"`
	LastRequestAt          *time.Time            `json:"last_request_at"`
	TokensObservedRequests int64                 `json:"tokens_observed_requests"`
	From                   time.Time             `json:"from"`
	To                     time.Time             `json:"to"`
}

type APIKeyHistoryMetric struct {
	Requests       int64 `json:"requests"`
	Succeeded      int64 `json:"succeeded"`
	Failed         int64 `json:"failed"`
	Canceled       int64 `json:"canceled"`
	InputTokens    int64 `json:"input_tokens"`
	OutputTokens   int64 `json:"output_tokens"`
	TokensObserved bool  `json:"tokens_observed"`
}
type APIKeyHistoryBucket struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	APIKeyHistoryMetric
}
type APIKeyHistoryModel struct {
	ID string `json:"id"`
	APIKeyHistoryMetric
}
type APIKeyHistoryReport struct {
	Usage   UsageSummary  `json:"usage"`
	History APIKeyHistory `json:"history"`
}

// A stable ID always wins, including when it belongs to an unknown/deleted key.
// Only rows without one may fall back to an unambiguous current display name.
const apiKeyAttributionSQL = `COALESCE(ro.caller_identity_id,''),
 CASE WHEN COALESCE(ro.caller_identity_id,'')='' THEN COALESCE(lr.consumer_key,'') ELSE '' END`

func apiKeyHistoryInterval(span time.Duration) time.Duration {
	step := time.Hour
	if span > 24*time.Hour {
		step = 24 * time.Hour
	}
	// Bound arbitrary windows too; ordinary 24h/7d/30d windows yield 24/7/30 bins.
	if span > 90*step {
		step = (span/90 + time.Millisecond - 1) / time.Millisecond * time.Millisecond
	}
	return step
}

func (m *APIKeyHistoryMetric) add(s UsageSummary) {
	m.Requests += s.Requests
	m.Succeeded += s.Succeeded
	m.Failed += s.Failed
	m.Canceled += s.Canceled
	m.InputTokens += s.InputTokens
	m.OutputTokens += s.OutputTokens
	m.TokensObserved = m.TokensObserved || s.TokensObserved
}

// QueryAPIKeyHistory batches all identities in a constant number of queries.
// SQL aggregates narrow rows before returning them; neither request bodies nor
// attempts are loaded. All report components share the same WAL snapshot.
func (l *Ledger) QueryAPIKeyHistory(ctx context.Context, identities []domain.APIKeyIdentity, from, to time.Time, includeModels bool) (map[string]*APIKeyHistoryReport, error) {
	if to.UnixMilli() <= from.UnixMilli() {
		return nil, errors.New("history window must span at least one millisecond")
	}
	tx, err := l.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	coverage, err := reportingCoverage(ctx, tx, from, to)
	if err != nil {
		return nil, err
	}
	step := apiKeyHistoryInterval(to.Sub(from))
	out := make(map[string]*APIKeyHistoryReport, len(identities))
	names := map[string]string{}
	models := map[string]map[string]*APIKeyHistoryModel{}
	latencySum, latencyCount := map[string]int64{}, map[string]int64{}
	for _, key := range identities {
		if _, exists := names[key.Name]; exists {
			names[key.Name] = ""
		} else {
			names[key.Name] = key.ID
		}
		report := &APIKeyHistoryReport{History: APIKeyHistory{From: from, To: to, Coverage: coverage, Models: []APIKeyHistoryModel{}}}
		for start := from; start.Before(to); start = start.Add(step) {
			end := start.Add(step)
			if end.After(to) {
				end = to
			}
			report.History.Timeline = append(report.History.Timeline, APIKeyHistoryBucket{From: start, To: end})
		}
		out[key.ID] = report
		if includeModels {
			models[key.ID] = map[string]*APIKeyHistoryModel{}
		}
	}
	resolve := func(id, name string) string {
		if id != "" {
			return id
		}
		if name != "" {
			return names[name]
		}
		return ""
	}
	modelSQL := `''`
	if includeModels {
		modelSQL = `COALESCE(NULLIF(lr.logical_model_id,''),NULLIF(lr.model_requested,''),'__unknown__')`
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+apiKeyAttributionSQL+`, (lr.ts-?)/? AS bin, `+modelSQL+` AS model,
 COUNT(*), SUM(lr.status='success'),SUM(lr.status='failed'),SUM(lr.status='canceled'),
 COALESCE(SUM(lr.input_tokens),0),COALESCE(SUM(lr.output_tokens),0),COALESCE(SUM(lr.cache_read_tokens),0),
 COALESCE(SUM(lr.est_cash_cost),0),COALESCE(SUM(lr.quota_units),0),COALESCE(SUM(lr.allocated_sub_cost),0),COALESCE(SUM(lr.shadow_cost),0),
 COALESCE(SUM(lr.serving_cost),0),COALESCE(SUM(lr.recovery_overhead_cost),0),
 COALESCE(SUM(lr.upstream_input_tokens),0),COALESCE(SUM(lr.upstream_output_tokens),0),
 COALESCE(SUM(`+requestTotalCostSQL+`),0),
 SUM(CASE WHEN lr.input_tokens!=0 OR lr.output_tokens!=0 OR lr.upstream_input_tokens!=0 OR lr.upstream_output_tokens!=0
 OR lr.cache_read_tokens!=0 OR lr.cache_write_tokens!=0 OR json_extract(ro.observation,'$.usage_state') IN ('known','partial') THEN 1 ELSE 0 END),
 MAX(CASE WHEN lr.est_cash_cost!=0 OR lr.serving_cost!=0 OR lr.recovery_overhead_cost!=0 THEN 1 ELSE 0 END),
 COALESCE(SUM(CASE WHEN lr.duration_ms>0 THEN lr.duration_ms ELSE 0 END),0),COUNT(NULLIF(lr.duration_ms,0))
 FROM logical_requests lr LEFT JOIN request_observations ro ON ro.request_id=lr.id
 WHERE lr.ts>=? AND lr.ts<? AND lr.status IN ('success','failed','canceled')
 GROUP BY 1,2,3,4`, from.UnixMilli(), step.Milliseconds(), from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, name, model string
		var bin int
		var s UsageSummary
		var latSum, latCount, tokenRows int64
		if err := rows.Scan(&id, &name, &bin, &model, &s.Requests, &s.Succeeded, &s.Failed, &s.Canceled,
			&s.InputTokens, &s.OutputTokens, &s.CacheRead, &s.EstCashCost, &s.QuotaUnits, &s.AllocatedSub, &s.ShadowCost,
			&s.ServingCost, &s.RecoveryOverheadCost, &s.UpstreamInputTokens, &s.UpstreamOutputTokens, &s.TotalCost,
			&tokenRows, &s.CostObserved, &latSum, &latCount); err != nil {
			rows.Close()
			return nil, err
		}
		s.TokensObserved = tokenRows > 0
		id = resolve(id, name)
		report := out[id]
		if report == nil {
			continue
		}
		report.History.TokensObservedRequests += tokenRows
		u := &report.Usage
		u.Requests += s.Requests
		u.Succeeded += s.Succeeded
		u.Failed += s.Failed
		u.Canceled += s.Canceled
		u.InputTokens += s.InputTokens
		u.OutputTokens += s.OutputTokens
		u.CacheRead += s.CacheRead
		u.EstCashCost += s.EstCashCost
		u.QuotaUnits += s.QuotaUnits
		u.AllocatedSub += s.AllocatedSub
		u.ShadowCost += s.ShadowCost
		u.ServingCost += s.ServingCost
		u.RecoveryOverheadCost += s.RecoveryOverheadCost
		u.UpstreamInputTokens += s.UpstreamInputTokens
		u.UpstreamOutputTokens += s.UpstreamOutputTokens
		u.TotalCost += s.TotalCost
		u.TokensObserved = u.TokensObserved || s.TokensObserved
		u.CostObserved = u.CostObserved || s.CostObserved
		latencySum[id] += latSum
		latencyCount[id] += latCount
		report.History.Timeline[bin].add(s)
		if includeModels {
			m := models[id][model]
			if m == nil {
				m = &APIKeyHistoryModel{ID: model}
				models[id][model] = m
			}
			m.add(s)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// All retained requests, independently of the selected statistics window.
	rows, err = tx.QueryContext(ctx, liveRequestsCTE+` SELECT `+apiKeyAttributionSQL+`,MAX(lr.ts)
 FROM logical_requests lr LEFT JOIN request_observations ro ON ro.request_id=lr.id GROUP BY 1,2`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, name string
		var ts int64
		if err := rows.Scan(&id, &name, &ts); err != nil {
			rows.Close()
			return nil, err
		}
		report := out[resolve(id, name)]
		if report == nil {
			continue
		}
		at := time.UnixMilli(ts).UTC()
		if report.History.LastRequestAt == nil || at.After(*report.History.LastRequestAt) {
			report.History.LastRequestAt = &at
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for id, report := range out {
		if latencyCount[id] > 0 {
			report.Usage.AvgLatencyMS = float64(latencySum[id]) / float64(latencyCount[id])
		}
		if includeModels {
			report.History.Models = make([]APIKeyHistoryModel, 0, len(models[id]))
			for _, m := range models[id] {
				report.History.Models = append(report.History.Models, *m)
			}
			sort.Slice(report.History.Models, func(i, j int) bool {
				a, b := report.History.Models[i], report.History.Models[j]
				if a.Requests == b.Requests {
					return a.ID < b.ID
				}
				return a.Requests > b.Requests
			})
		}
	}
	return out, nil
}

// QueryAPIKeyRequests applies exactly the same identity precedence to recent
// request evidence, including in-flight rows, and the same exclusive window end.
func (l *Ledger) QueryAPIKeyRequests(ctx context.Context, identity domain.APIKeyIdentity, from, to time.Time, limit int) ([]domain.RequestRecord, error) {
	rows, err := l.read.QueryContext(ctx, liveRequestsCTE+` SELECT `+requestCols+` FROM logical_requests
 WHERE ts>=? AND ts<? AND (
 EXISTS (SELECT 1 FROM request_observations ro WHERE ro.request_id=logical_requests.id AND ro.caller_identity_id=?)
 OR (consumer_key=? AND consumer_key!='' AND NOT EXISTS (SELECT 1 FROM request_observations ro WHERE ro.request_id=logical_requests.id AND ro.caller_identity_id!='')))
 ORDER BY ts DESC,id DESC LIMIT ?`, from.UnixMilli(), to.UnixMilli(), identity.ID, identity.Name, limitOf(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.RequestRecord{}
	for rows.Next() {
		rec, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}
