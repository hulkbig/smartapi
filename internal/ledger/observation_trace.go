package ledger

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"time"

	"ferry/internal/domain"
)

const maxObservationEvents = 512

func traceEventKey(event domain.ExecutionTraceEvent) [32]byte {
	event.Seq = 0
	// An event may receive failure details after its boundary is published.
	// Such enrichment updates the fact rather than adding a second failure.
	event.Message = ""
	event.FailureType = ""
	event.FailureScope = ""
	data, _ := json.Marshal(event)
	return sha256.Sum256(data)
}

// Later scheduler publications cannot erase concurrent client delivery facts.
func mergeObservationTraceLocked(p *pendingRequest, tr *domain.TraceRecord) {
	if p.observationEventKeys == nil {
		p.observationEventKeys = map[[32]byte]uint64{}
	}
	for _, event := range tr.Events {
		key := traceEventKey(event)
		if seq := p.observationEventKeys[key]; seq != 0 {
			for i := range p.observationEvents {
				if p.observationEvents[i].Seq == seq {
					event.Seq = seq
					p.observationEvents[i] = event
					break
				}
			}
			continue
		}
		p.observationSeq++
		p.observationEventKeys[key] = p.observationSeq
		event.Seq = p.observationSeq
		p.observationEvents = append(p.observationEvents, event)
		if len(p.observationEvents) > maxObservationEvents {
			copy(p.observationEvents[128:], p.observationEvents[129:])
			p.observationEvents = p.observationEvents[:maxObservationEvents]
			p.observationEventsTruncated++
		}
	}
	if len(p.observationEventKeys) > maxObservationEvents*2 {
		retained := map[[32]byte]bool{}
		for _, event := range tr.Events {
			retained[traceEventKey(event)] = true
		}
		for _, event := range p.observationEvents {
			retained[traceEventKey(event)] = true
		}
		for key := range p.observationEventKeys {
			if !retained[key] {
				delete(p.observationEventKeys, key)
			}
		}
	}
	p.observationRevision++
	tr.Events = append([]domain.ExecutionTraceEvent(nil), p.observationEvents...)
	tr.SchemaVersion = 1
	tr.Revision = p.observationRevision
	tr.EventsTruncated = p.observationEventsTruncated
}

func (l *Ledger) appendObservationEventLocked(p *pendingRequest, event domain.ExecutionTraceEvent) {
	tr := cloneTraceRecord(p.trace)
	if tr == nil {
		tr = &domain.TraceRecord{}
	}
	if len(p.observationEvents) == 0 && len(tr.Events) > 0 {
		mergeObservationTraceLocked(p, tr)
	}
	tr.Events = []domain.ExecutionTraceEvent{event}
	mergeObservationTraceLocked(p, tr)
	p.trace = tr
}

func persistObservationTrace(db queryExecer, record *domain.RequestRecord, tr *domain.TraceRecord) error {
	if tr == nil || tr.SchemaVersion < 1 {
		return nil
	}
	if err := insertTrace(db, record.ID, record.Ts, tr); err != nil {
		return err
	}
	o, err := loadObservation(db, record.ID)
	if err != nil || o == nil {
		return err
	}
	o.TraceRevision = tr.Revision
	o.LastEventSeq = 0
	if n := len(tr.Events); n > 0 {
		o.LastEventSeq = tr.Events[n-1].Seq
	}
	if tr.ConversationID != "" {
		o.ConversationID = tr.ConversationID
	}
	o.UpdatedAt = time.Now()
	o.SelectedProviderID = record.ProviderID
	o.ActivityPhase = string(record.Status)
	o.ActiveProviderID = ""
	for _, event := range tr.Events {
		switch event.Type {
		case "UPSTREAM_DISPATCHED":
			o.ActiveProviderID = event.ProviderID
		case "ATTEMPT_FAILED", "ATTEMPT_SUCCEEDED", "COMPLETE", "RUNTIME_RESTARTED":
			o.ActiveProviderID = ""
		}
	}
	return saveObservation(db, o)
}

func finalizeStrandedObservation(db queryExecer, record *domain.RequestRecord) error {
	o, err := loadObservation(db, record.ID)
	if err != nil || o == nil {
		return err
	}
	var raw string
	err = db.QueryRow(`SELECT trace FROM decision_traces WHERE request_id=?`, record.ID).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	tr := &domain.TraceRecord{SchemaVersion: 1}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), tr); err != nil {
			return err
		}
	}
	seq := o.LastEventSeq + 1
	tr.Events = append(tr.Events, domain.ExecutionTraceEvent{Seq: seq, At: time.Now(), Category: domain.TraceOutput, Type: "RUNTIME_RESTARTED", FailureType: domain.FailClientCancel})
	if len(tr.Events) > maxObservationEvents {
		tr.Events = append(tr.Events[:128], tr.Events[129:]...)
		tr.EventsTruncated++
	}
	tr.SchemaVersion = 1
	tr.Revision = o.TraceRevision + 1
	return persistObservationTrace(db, record, tr)
}

// The HTTP handler calls this after completing its downstream writes.
func (l *Ledger) RecordDelivery(id string, failed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.requests[id]
	if p == nil {
		return
	}
	typ := "CLIENT_DELIVERY_COMPLETED"
	if failed {
		typ = "CLIENT_DELIVERY_INTERRUPTED"
		if p.rec.Status == domain.ReqSuccess {
			p.rec.Status = domain.ReqCanceled
			p.rec.Outcome = domain.OutcomeFailed
			p.rec.FailureType = domain.FailClientCancel
			p.rec.FailureMsg = "client delivery interrupted"
		}
	}
	l.appendObservationEventLocked(p, domain.ExecutionTraceEvent{At: time.Now(), Category: domain.TraceOutput, Type: typ})
	l.persistLiveRequest(p)
}
