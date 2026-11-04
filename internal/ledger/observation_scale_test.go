package ledger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"ferry/internal/domain"
)

// Explicit opt-in: a synthetic capacity experiment, never a production reader.
// SMARTAPI_OBSERVATION_SCALE=1 go test ./internal/ledger -run TestObservationScale -v -count=1
func TestObservationScale(t *testing.T) {
	if os.Getenv("SMARTAPI_OBSERVATION_SCALE") != "1" {
		t.Skip("opt-in 100k request / 10k Session experiment")
	}
	l := openTemp(t)
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	tx, err := l.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	request, err := tx.Prepare(`INSERT INTO logical_requests(id,ts,consumer_key,logical_model_id,model_requested,provider_id,status,outcome,input_tokens,output_tokens,est_cash_cost) VALUES(?,?,?,?,?,?,'success',?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Close()
	observation, err := tx.Prepare(`INSERT INTO request_observations(request_id,session_id,caller_identity_id,client_kind,request_ts,updated_at,observation) VALUES(?,?,?,'codex',?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer observation.Close()
	session, err := tx.Prepare(`INSERT INTO observation_sessions(id,caller_identity_id,created_at,title_excerpt,title_source,title_request_id,title_request_ts) VALUES(?,?,?,'Synthetic task','request',?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	attempt, err := tx.Prepare(`INSERT INTO attempts(request_id,seq,ts,target_id,provider_id,status,usage_state,cost_state,upstream_dispatched,serving) VALUES(?,?,?,'synthetic',?,'success','known','known',1,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Close()
	started := time.Now()
	for i := range 100000 {
		id, sid, caller := fmt.Sprintf("scale_r_%06d", i), fmt.Sprintf("scale_s_%05d", i/10), fmt.Sprintf("scale_c_%02d", i/10%20)
		at := from.Add(time.Duration(i) * time.Millisecond)
		provider := fmt.Sprintf("p%d", i%5)
		outcome := "DIRECT_SUCCESS"
		if i%4 == 0 {
			outcome = "RECOVERED_SUCCESS"
		}
		if _, err = request.Exec(id, at.UnixMilli(), caller, fmt.Sprintf("m%d", i%3), "model", provider, outcome, 100, 50, .001); err != nil {
			t.Fatal(err)
		}
		o := domain.RequestObservation{SchemaVersion: 1, RequestID: id, SessionID: sid, CallerIdentityID: caller, ClientKind: "codex", RequestAt: at, UpdatedAt: at, AssociationState: "linked", UsageState: "known", CostState: "known", ContentPreview: domain.ContentPreview{RequestExcerpt: "Synthetic task", ResponseExcerpt: "Synthetic visible response", ContentState: "recorded"}}
		data, _ := json.Marshal(o)
		if _, err = observation.Exec(id, sid, caller, at.UnixMilli(), at.UnixMilli(), string(data)); err != nil {
			t.Fatal(err)
		}
		if i%10 == 0 {
			if _, err = session.Exec(sid, caller, at.UnixMilli(), id, at.UnixMilli()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = attempt.Exec(id, 1, at.UnixMilli(), provider, 1); err != nil {
			t.Fatal(err)
		}
		if i%4 == 0 {
			if _, err = attempt.Exec(id, 2, at.UnixMilli(), "p-recovery", 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(l.dir, "ledger.db"))
	wal, _ := os.Stat(filepath.Join(l.dir, "ledger.db-wal"))
	var size int64
	if info != nil {
		size += info.Size()
	}
	if wal != nil {
		size += wal.Size()
	}
	t.Logf("fixture requests=100000 sessions=10000 attempts=125000 seed=%s bytes=%d platform=%s/%s cpus=%d", time.Since(started), size, runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	f := domain.LogFilter{From: from, To: from.Add(time.Hour), Limit: 50}
	q := ObservationQuery{Filter: f, Mode: "session", SessionsOnly: true, Limit: 50}
	run := func(name string, fn func() error) {
		t.Helper()
		var samples []time.Duration
		for range 7 {
			at := time.Now()
			if err := fn(); err != nil {
				t.Fatal(name, err)
			}
			samples = append(samples, time.Since(at))
		}
		cold := samples[0]
		sort.Slice(samples[1:], func(i, j int) bool { return samples[i+1] < samples[j+1] })
		t.Logf("%s first=%s warm_p50=%s warm_p95=%s", name, cold, samples[3], samples[6])
	}
	run("requests_baseline", func() error { _, _, err := l.QueryRequests(f); return err })
	run("requests_with_observations", func() error { _, err := l.QueryObservedRequests(f); return err })
	run("sessions", func() error {
		p, err := l.QueryObservationSources(q)
		if err == nil && (p.Total != 10000 || len(p.Sources) != 50 || p.Stats.Requests != 100000) {
			return fmt.Errorf("wrong scale cohort %+v", p.Stats)
		}
		return err
	})
	providerQuery := q
	providerQuery.SessionsOnly = false
	providerQuery.Limit = 4
	providerQuery.Filter.ParticipatingProvider = "p2"
	run("provider_sources", func() error { _, err := l.QueryObservationSources(providerQuery); return err })
	searchQuery := q
	searchQuery.Filter.Search = "Synthetic"
	run("sessions_search", func() error { _, err := l.QueryObservationSources(searchQuery); return err })
	parallelStart := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for _, query := range []ObservationQuery{q, providerQuery, searchQuery} {
		wg.Add(1)
		go func(query ObservationQuery) { defer wg.Done(); _, err := l.QueryObservationSources(query); errs <- err }(query)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("three_concurrent_views=%s", time.Since(parallelStart))
	cte, args := observationCTE(providerQuery)
	rows, err := l.read.Query("EXPLAIN QUERY PLAN "+cte+`SELECT group_id,COUNT(*) FROM matched GROUP BY group_id`, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plans []string
	for rows.Next() {
		var id, parent, aux int
		var detail string
		if err := rows.Scan(&id, &parent, &aux, &detail); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(plans, "\n")
	t.Log("provider plan:\n" + plan)
	if !strings.Contains(plan, "idx_attempts_provider_request") {
		t.Fatal("provider participation does not use its composite index")
	}
}
