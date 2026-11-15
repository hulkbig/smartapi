package ledger

import (
	"context"
	"database/sql"
	"time"
)

// ModelSpeed is a duration-weighted output rate over successful, completed
// samples: sum(output tokens) / sum(total elapsed seconds). It includes waiting
// and transport; it is not decoding speed. TTFT cannot supply a common output
// interval for streaming, non-streaming and historical requests.
type ModelSpeed struct {
	OutputTokensPerSecond *float64 `json:"output_tokens_per_second"`
	SpeedSamples          int64    `json:"speed_samples"`
}

type ProviderModelSpeed struct {
	ProviderID    string `json:"provider_id"`
	UpstreamModel string `json:"upstream_model"`
	ModelSpeed
}

// New observations explicitly distinguish complete/partial/unknown usage.
// Legacy positive output is evidence when no usage-state metadata exists;
// legacy zero alone is not. Known zero-output completions remain valid samples.
const requestSpeedUsageStateSQL = `COALESCE(NULLIF(json_extract(ro.observation,'$.usage_state'),''),
 (SELECT NULLIF(a.usage_state,'') FROM attempts a WHERE a.request_id=lr.id AND a.seq=lr.attempts),'')`
const requestSpeedValidSQL = `lr.status='success' AND lr.duration_ms>0 AND lr.output_tokens>=0 AND
 (` + requestSpeedUsageStateSQL + `='known' OR (` + requestSpeedUsageStateSQL + `='' AND lr.output_tokens>0))`

// ProviderModelSpeeds uses actual attempt.model, never a logical alias or the
// final request's upstream model. The cohort is finalized successful logical
// requests starting in [from,to); each successful upstream attempt is a sample.
// Failed retries do not contribute tokens or time. A recovered logical request
// is still counted only once by TrafficByLogicalModel using its full duration.
func (l *Ledger) ProviderModelSpeeds(ctx context.Context, from, to time.Time) ([]ProviderModelSpeed, error) {
	rows, err := l.read.QueryContext(ctx, `SELECT a.provider_id,a.model,
 1000.0*SUM(a.output_tokens)/SUM(a.latency_ms),COUNT(*)
 FROM logical_requests lr JOIN attempts a ON a.request_id=lr.id
 WHERE lr.ts>=? AND lr.ts<? AND lr.status='success' AND a.status='success'
 AND a.provider_id!='' AND a.model!='' AND a.latency_ms>0 AND a.output_tokens>=0
 AND (a.usage_state='known' OR (COALESCE(a.usage_state,'')='' AND a.output_tokens>0))
 GROUP BY a.provider_id,a.model ORDER BY a.provider_id,a.model`, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProviderModelSpeed{}
	for rows.Next() {
		var item ProviderModelSpeed
		var speed sql.NullFloat64
		if err := rows.Scan(&item.ProviderID, &item.UpstreamModel, &speed, &item.SpeedSamples); err != nil {
			return nil, err
		}
		if speed.Valid {
			item.OutputTokensPerSecond = &speed.Float64
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
