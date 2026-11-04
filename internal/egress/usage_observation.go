package egress

import "encoding/json"

// Presence is independent of decodeUsage's compatibility zero values.
func usagePresence(raw json.RawMessage) (input, output bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false, false
	}
	has := func(keys ...string) bool {
		for _, key := range keys {
			var value *int64
			if json.Unmarshal(fields[key], &value) == nil && value != nil && *value >= 0 {
				return true
			}
		}
		return false
	}
	return has("input_tokens", "prompt_tokens"), has("output_tokens", "completion_tokens")
}

func usageEvidence(raw json.RawMessage) string {
	i, o := usagePresence(raw)
	return usagePresenceState(i, o)
}

func usagePresenceState(input, output bool) string {
	if input && output {
		return "known"
	}
	if input || output {
		return "partial"
	}
	return "unknown"
}
