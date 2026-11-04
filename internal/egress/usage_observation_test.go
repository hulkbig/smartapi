package egress

import (
	"encoding/json"
	"testing"
)

func TestUsageEvidenceDistinguishesExplicitZeroAndMissing(t *testing.T) {
	for _, tc := range []struct{ wire, want string }{
		{`null`, "unknown"}, {`{}`, "unknown"}, {`{"input_tokens":null,"output_tokens":null}`, "unknown"},
		{`{"input_tokens":0,"output_tokens":0}`, "known"}, {`{"prompt_tokens":0,"completion_tokens":0}`, "known"},
		{`{"input_tokens":12}`, "partial"}, {`{"output_tokens":0}`, "partial"},
		{`{"input_tokens":"invalid","output_tokens":-1}`, "unknown"},
	} {
		if got := usageEvidence(json.RawMessage(tc.wire)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.wire, got, tc.want)
		}
	}
}
