package ledger

import (
	"strings"
	"testing"
	"time"

	"ferry/internal/domain"
)

func TestSessionMetricsDoNotMaterializeRetainedRequestHistory(t *testing.T) {
	l := openTemp(t)
	q := ObservationQuery{Filter: domain.LogFilter{SessionID: "one-session", From: time.Now().Add(-time.Hour), To: time.Now()}, Mode: "session"}
	queries := []string{observationRecoveryMetricsQuery, observationParticipantsQuery}
	for _, search := range []string{"", "exact-request"} {
		q.Filter.Search = search
		cte, args := observationCTE(q)
		for _, query := range queries {
			rows, err := l.read.Query("EXPLAIN QUERY PLAN "+cte+query, args...)
			if err != nil {
				t.Fatal(err)
			}
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				plan = append(plan, detail)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(strings.Join(plan, "\n"), "MATERIALIZE logical_requests") {
				t.Fatalf("Session query copies the entire retained history: %v", plan)
			}
		}
	}
}
