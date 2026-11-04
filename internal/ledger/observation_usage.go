package ledger

// Coverage is derived from the persisted attempt set so retries of a writer job
// cannot increment counts twice. The attempts primary key bounds this query to
// one request; it never scans request history or alters the billing rollup.
func updateObservationUsage(db queryExecer, id string) error {
	o, err := loadObservation(db, id)
	if err != nil || o == nil {
		return err
	}
	var total, costKnown, costPartial, serving, usageKnown, usagePartial int
	err = db.QueryRow(`SELECT COUNT(*),
	 COALESCE(SUM(cost_state='known'),0),COALESCE(SUM(cost_state='partial'),0),
	 COALESCE(SUM(serving),0),COALESCE(SUM(serving=1 AND usage_state='known'),0),
	 COALESCE(SUM(serving=1 AND usage_state='partial'),0) FROM attempts WHERE request_id=?`, id).
		Scan(&total, &costKnown, &costPartial, &serving, &usageKnown, &usagePartial)
	if err != nil {
		return err
	}
	state := func(count, known, partial int) string {
		if count > 0 && known == count {
			return "known"
		}
		if known > 0 || partial > 0 {
			return "partial"
		}
		return "unknown"
	}
	o.CostState = state(total, costKnown, costPartial)
	o.UsageState = state(serving, usageKnown, usagePartial)
	return saveObservation(db, o)
}
