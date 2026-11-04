package ledger

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ferry/internal/domain"
)

func observationHint(source, raw string, scope domain.AffinityScope) domain.AffinityEvidence {
	return domain.NewAffinityEvidence(source, scope, raw, domain.ConfHigh)
}

func mustObservation(t *testing.T, l *Ledger, id string) *domain.RequestObservation {
	t.Helper()
	o, err := l.QueryRequestObservation(id)
	if err != nil || o == nil {
		t.Fatalf("observation %s: %+v %v", id, o, err)
	}
	return o
}

func TestObservationCallerIsolationAndConcurrentAliases(t *testing.T) {
	l := openTemp(t)
	at := time.Now()
	hint := observationHint("codex.thread", "same-raw-thread", domain.AffinityConversation)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l.BeginObservation(domain.ObservationInput{RequestID: fmt.Sprint(i), At: at, CallerIdentityID: "stable-caller", Evidence: []domain.AffinityEvidence{hint}})
		}(i)
	}
	wg.Wait()
	l.BeginObservation(domain.ObservationInput{RequestID: "other-caller", At: at, CallerIdentityID: "other", Evidence: []domain.AffinityEvidence{hint}})
	flushLiveTest(t, l.Ledger)
	first := mustObservation(t, l.Ledger, "0")
	if first.SessionID == "" || first.AssociationState != "linked" {
		t.Fatalf("first: %+v", first)
	}
	for i := 1; i < 24; i++ {
		if got := mustObservation(t, l.Ledger, fmt.Sprint(i)); got.SessionID != first.SessionID {
			t.Fatalf("concurrent split: %+v", got)
		}
	}
	if other := mustObservation(t, l.Ledger, "other-caller"); other.SessionID == first.SessionID {
		t.Fatal("cross-caller merge")
	}
	var sessions int
	if err := l.read.QueryRow(`SELECT COUNT(*) FROM observation_sessions`).Scan(&sessions); err != nil || sessions != 2 {
		t.Fatalf("sessions=%d %v", sessions, err)
	}
	var raw int
	if err := l.read.QueryRow(`SELECT COUNT(*) FROM observation_session_aliases WHERE key_hash LIKE '%same-raw-thread%'`).Scan(&raw); err != nil || raw != 0 {
		t.Fatalf("raw hint retained: %d %v", raw, err)
	}
}

func TestObservationLineageOwnershipConflictAndUnlinked(t *testing.T) {
	l := openTemp(t)
	at := time.Now()
	start := func(id, caller string, root bool, e ...domain.AffinityEvidence) {
		l.BeginObservation(domain.ObservationInput{RequestID: id, At: at, CallerIdentityID: caller, ResponsesRoot: root, Evidence: e})
	}
	start("root", "caller", true)
	l.RegisterObservationLineage("root", "resp_smart_owned", "conv_smart_owned")
	previous := observationHint("responses.previous_response_id", "resp_smart_owned", domain.AffinityConversation)
	start("next", "caller", false, previous)
	start("stolen", "other", false, previous)
	start("independent-root", "caller", true)
	start("cache-only", "caller", false, observationHint("codex.session", "cache", domain.AffinityCache), observationHint("anthropic.metadata.user_id", "person", domain.AffinityCaller))
	start("hint-a", "caller", false, observationHint("codex.thread", "a", domain.AffinityConversation))
	start("hint-b", "caller", false, observationHint("codex.thread", "b", domain.AffinityConversation))
	start("conflict", "caller", false, observationHint("codex.thread", "a", domain.AffinityConversation), observationHint("codex.thread", "b", domain.AffinityConversation))
	start("new-conflict", "caller", false, observationHint("codex.thread", "new-a", domain.AffinityConversation), observationHint("claude-code.session", "new-b", domain.AffinityConversation))
	flushLiveTest(t, l.Ledger)
	root := mustObservation(t, l.Ledger, "root")
	if got := mustObservation(t, l.Ledger, "next"); got.SessionID != root.SessionID {
		t.Fatalf("lineage split: %+v", got)
	}
	if got := mustObservation(t, l.Ledger, "stolen"); got.SessionID != "" || got.AssociationState != "unverified_lineage" {
		t.Fatalf("ownership leak: %+v", got)
	}
	if got := mustObservation(t, l.Ledger, "independent-root"); got.SessionID == root.SessionID {
		t.Fatal("root requests merged without evidence")
	}
	if got := mustObservation(t, l.Ledger, "cache-only"); got.SessionID != "" {
		t.Fatal("cache/caller became Session")
	}
	for _, id := range []string{"conflict", "new-conflict"} {
		if got := mustObservation(t, l.Ledger, id); got.SessionID != "" || got.AssociationState != "conflict" {
			t.Fatalf("conflict merged: %+v", got)
		}
	}
	// A later callback may not silently move an existing request across callers.
	start("next", "other", false)
	flushLiveTest(t, l.Ledger)
	if got := mustObservation(t, l.Ledger, "next"); got.SessionID != "" {
		t.Fatalf("caller change leaked Session: %+v", got)
	}
}

func TestObservationCapturePolicySnapshotPersistenceAndRetention(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, 32, 0)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour)
	rc := &domain.RequestContract{Messages: []domain.Message{{Role: domain.RoleUser, Blocks: []domain.Block{{Kind: domain.BlockText, Text: "hello secret=example-secret"}}}}}
	input := func(id string) domain.ObservationInput {
		return domain.ObservationInput{RequestID: id, At: at, CallerIdentityID: "caller", Evidence: []domain.AffinityEvidence{observationHint("codex.thread", id, domain.AffinityConversation)}}
	}
	l.BeginObservation(input("disabled"))
	if err := l.SetObservationContentMode("excerpt"); err != nil {
		t.Fatal(err)
	}
	l.CaptureRequestExcerpt("disabled", rc)
	l.BeginObservation(input("enabled"))
	if err := l.SetObservationContentMode("metadata_only"); err != nil {
		t.Fatal(err)
	}
	l.CaptureRequestExcerpt("enabled", rc)
	l.BeginRequest(domain.RequestRecord{ID: "live", Ts: at, Status: "waiting"})
	l.BeginObservation(input("live"))
	flushLiveTest(t, l)
	if got := mustObservation(t, l, "disabled"); got.ContentPreview.ContentState != "disabled" || got.ContentPreview.RequestExcerpt != "" {
		t.Fatalf("retroactive enable: %+v", got)
	}
	if got := mustObservation(t, l, "enabled"); !strings.Contains(got.ContentPreview.RequestExcerpt, "[redacted]") || strings.Contains(got.ContentPreview.RequestExcerpt, "example-secret") {
		t.Fatalf("capture/redaction: %+v", got)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l, err = Open(dir, 32, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got := mustObservation(t, l, "enabled"); got.ContentMode != "excerpt" {
		t.Fatal("capture policy did not persist")
	}
	// Reopening finalizes a stranded live request, so it can now age normally.
	if _, err := l.PurgeOlderThan(time.Now()); err != nil {
		t.Fatal(err)
	}
	var aliases, sessions int
	if err := l.read.QueryRow(`SELECT COUNT(*) FROM observation_session_aliases`).Scan(&aliases); err != nil {
		t.Fatal(err)
	}
	if err := l.read.QueryRow(`SELECT COUNT(*) FROM observation_sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if aliases != 0 || sessions != 0 {
		t.Fatalf("orphan retention: aliases=%d sessions=%d", aliases, sessions)
	}
}

func TestObservationRetentionProtectsLiveRequest(t *testing.T) {
	l := openTemp(t)
	at := time.Now().Add(-time.Hour)
	l.BeginRequest(domain.RequestRecord{ID: "live", Ts: at, Status: "waiting"})
	l.BeginObservation(domain.ObservationInput{RequestID: "live", At: at, CallerIdentityID: "caller", ResponsesRoot: true})
	flushLiveTest(t, l.Ledger)
	if _, err := l.PurgeOlderThan(time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := mustObservation(t, l.Ledger, "live"); got.SessionID == "" {
		t.Fatal("live observation purged")
	}
}

func TestObservationRetentionReselectsEarliestRetainedTitle(t *testing.T) {
	l := openTemp(t)
	at := time.Now().Add(-time.Hour)
	if err := l.SetObservationContentMode("excerpt"); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"old", "retained", "newest"} {
		ts := at.Add(time.Duration(i) * time.Minute)
		l.BeginRequest(domain.RequestRecord{ID: id, Ts: ts, Status: domain.ReqSuccess})
		l.BeginObservation(domain.ObservationInput{RequestID: id, At: ts, CallerIdentityID: "caller", Evidence: []domain.AffinityEvidence{observationHint("codex.thread", "same", domain.AffinityConversation)}})
		l.CaptureRequestExcerpt(id, &domain.RequestContract{Messages: []domain.Message{{Role: domain.RoleUser, Blocks: []domain.Block{{Kind: domain.BlockText, Text: id + " title"}}}}})
		l.FinishRequest(id, nil)
	}
	flushLiveTest(t, l.Ledger)
	session := mustObservation(t, l.Ledger, "retained").SessionID
	if _, err := l.PurgeOlderThan(at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var title, source string
	if err := l.read.QueryRow(`SELECT title_excerpt,title_request_id FROM observation_sessions WHERE id=?`, session).Scan(&title, &source); err != nil {
		t.Fatal(err)
	}
	if title != "retained title" || source != "retained" {
		t.Fatalf("title=%q source=%q", title, source)
	}
}

func TestObservationLegacySchemaUpgradeAndRepeatedOpen(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, 16, 0)
	if err != nil {
		t.Fatal(err)
	}
	l.BeginRequest(domain.RequestRecord{ID: "legacy", Ts: time.Now(), Status: domain.ReqSuccess})
	l.FinishRequest("legacy", nil)
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	// Reproduce the pre-observation schema in an isolated fixture database.
	old, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"DROP TABLE request_observations", "DROP TABLE observation_session_aliases", "DROP TABLE observation_sessions", "ALTER TABLE attempts DROP COLUMN usage_state", "ALTER TABLE attempts DROP COLUMN cost_state", "ALTER TABLE attempts DROP COLUMN upstream_dispatched", "ALTER TABLE attempts DROP COLUMN serving"} {
		if _, err = old.Exec(statement); err != nil {
			old.Close()
			t.Fatal(err)
		}
	}
	if err = old.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		l, err = Open(dir, 16, 0)
		if err != nil {
			t.Fatal(err)
		}
		page, err := l.QueryObservedRequests(domain.LogFilter{})
		if err != nil || page.Total != 1 || page.Observations["legacy"].AssociationState != "legacy_unavailable" || page.Observations["legacy"].CostState != "unknown" {
			l.Close()
			t.Fatalf("legacy upgrade: %+v %v", page, err)
		}
		if err = l.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestObservationRestartClearsPendingProviderSource(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, 16, 0)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute)
	l.BeginRequest(domain.RequestRecord{ID: "waiting", Ts: at, Status: "waiting", ProviderID: "p"})
	l.BeginObservation(domain.ObservationInput{RequestID: "waiting", At: at, CallerIdentityID: "caller", ResponsesRoot: true})
	l.SetObservationActivity("waiting", "p", "", "waiting")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l, err = Open(dir, 16, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	o := mustObservation(t, l, "waiting")
	if o.ActivityPhase != "canceled" || o.ActiveProviderID != "" {
		t.Fatalf("stranded source still live: %+v", o)
	}
	p, err := l.QueryObservationSources(ObservationQuery{Filter: domain.LogFilter{From: at.Add(-time.Second), To: time.Now(), ParticipatingProvider: "p"}})
	if err != nil || p.Total != 0 || p.Stats.PendingRequests != 0 {
		t.Fatalf("phantom waiting source: %+v %v", p, err)
	}
	trace, err := l.QueryTrace("waiting")
	if err != nil || trace == nil || trace.Events[len(trace.Events)-1].Type != "RUNTIME_RESTARTED" {
		t.Fatalf("missing restart fact: %+v %v", trace, err)
	}
}
