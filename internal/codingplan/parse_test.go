package codingplan

import (
	"testing"
	"time"
)

func TestParseKimiCurrentWindowsAndLegacyFallback(t *testing.T) {
	current := []byte(`{"usages":{"limit_5h":{"used_ratio":"0.125","reset_time":"2026-11-10T00:00:00.123Z"},"limit_7d":{"used_ratio":0},"limit_month_total":{"used_ratio":0.4},"limit_month_code":{"used_ratio":0.3}}}`)
	usage, err := parseKimi(current)
	if err != nil || len(usage.Windows) != 3 {
		t.Fatalf("usage=%+v error=%v", usage, err)
	}
	for index, want := range []struct {
		id      string
		percent float64
		seconds int64
	}{
		{"limit_5h", 12.5, 18000}, {"limit_7d", 0, 604800},
		{"limit_month_total", 40, 0},
	} {
		window := usage.Windows[index]
		if window.ID != want.id || window.UsedPercent == nil || *window.UsedPercent != want.percent || window.LimitWindowSeconds != want.seconds || window.Unit != "percent" || window.Limit != nil || window.Used != nil {
			t.Fatalf("unexpected current window %+v", window)
		}
	}
	reset, _ := time.Parse(time.RFC3339, "2026-11-10T00:00:00Z")
	if usage.Windows[0].ResetAt != reset.Unix() || len(usage.Windows[2].Breakdown) != 1 || usage.Windows[2].Breakdown[0].Label != "Monthly Coding usage" || *usage.Windows[2].Breakdown[0].UsedPercent != 30 {
		t.Fatal("reset time or monthly subcategory is incorrect")
	}
	legacy := []byte(`{"usage":{"limit":"100","used":"42","remaining":"58"},"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"detail":{"limit":"100","used":"25","remaining":"75","resetTime":"2026-11-10T00:00:00Z"}}]}`)
	usage, err = parseKimi(legacy)
	if err != nil || len(usage.Windows) != 2 {
		t.Fatalf("legacy usage=%+v error=%v", usage, err)
	}
	if *usage.Windows[0].UsedPercent != 42 || usage.Windows[0].Label != "Subscription limit" || usage.Windows[0].LimitWindowSeconds != 0 || *usage.Windows[1].UsedPercent != 25 || usage.Windows[1].LimitWindowSeconds != 18000 {
		t.Fatal("legacy quantities or explicit window duration was not preserved")
	}
}

func TestParseKimiMissingAndConflictingFields(t *testing.T) {
	componentOnly, err := parseKimi([]byte(`{"usages":{"limit_month_code":{"used_ratio":0.3}}}`))
	if err != nil || len(componentOnly.Windows) != 0 {
		t.Fatal("a monthly consumption component must not become an independent allowance")
	}
	usage, err := parseKimi([]byte(`{"usages":{"limit_5h":{},"limit_7d":{"used_ratio":-1,"reset_time":"invalid"}}}`))
	if err != nil || len(usage.Windows) != 2 || usage.Windows[0].UsedPercent != nil || usage.Windows[1].UsedPercent != nil || usage.Windows[1].ResetAt != 0 {
		t.Fatalf("missing quota became reported data: %+v error=%v", usage, err)
	}
	usage, err = parseKimi([]byte(`{"usage":{"limit":100,"used":100},"usages":{"limit_5h":{"used_ratio":0}}}`))
	if err != nil || len(usage.Windows) != 1 || usage.Windows[0].ID != "limit_5h" || usage.Windows[0].UsedPercent == nil || *usage.Windows[0].UsedPercent != 0 {
		t.Fatal("current schema should follow the official client without silently mixing legacy figures")
	}
	usage, err = parseKimi([]byte(`{"usage":{"limit":0,"used":0},"limits":[{"window":{"duration":5,"timeUnit":"UNKNOWN"},"detail":{}}]}`))
	if err != nil || len(usage.Windows) != 2 || usage.Windows[0].UsedPercent != nil || usage.Windows[1].UsedPercent != nil || usage.Windows[1].LimitWindowSeconds != 0 {
		t.Fatal("zero total or unknown time unit must not invent a percentage or duration")
	}
}

func TestParseGLMCreditsAndReset(t *testing.T) {
	usage, err := parseGLM([]byte(`{"code":200,"success":true,"data":{"level":"pro","limits":[{"type":"CREDIT_LIMIT","unit":3,"number":5,"usage":12000,"currentValue":0,"remaining":12000,"percentage":0},{"type":"CREDIT_LIMIT","unit":6,"number":1,"usage":60000,"currentValue":60016,"remaining":0,"percentage":100,"nextResetTime":1793830124999}]}}`))
	if err != nil || len(usage.Windows) != 2 || usage.PlanName != "pro" {
		t.Fatalf("usage=%+v error=%v", usage, err)
	}
	short, weekly := usage.Windows[0], usage.Windows[1]
	if short.Unit != "credits" || short.Label != "5-hour limit" || short.Used == nil || *short.Used != 0 || short.UsedPercent == nil || *short.UsedPercent != 0 || *short.Remaining != 12000 || *short.Limit != 12000 {
		t.Fatal("credit amounts or a true zero were not preserved")
	}
	if weekly.Label != "Weekly limit" || weekly.LimitWindowSeconds != 604800 || weekly.ResetAt != 1793830124 || *weekly.Used != 60016 || *weekly.UsedPercent != 100 || *weekly.Remaining != 0 {
		t.Fatal("weekly reset milliseconds or over-limit quantities were not preserved")
	}
}

func TestParseGLMLegacyAndUnknownWindows(t *testing.T) {
	usage, err := parseGLM([]byte(`{"data":{"limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":40,"usage":100,"currentValue":40},{"type":"TIME_LIMIT","usage":"500","currentValue":"100"},{"type":"CREDIT_LIMIT","unit":99,"number":1,"nextResetTime":123},{"type":"UNKNOWN","percentage":0}]}}`))
	if err != nil || len(usage.Windows) != 3 {
		t.Fatalf("usage=%+v error=%v", usage, err)
	}
	if token := usage.Windows[0]; token.Unit != "percent" || token.Used != nil || token.Limit != nil || *token.UsedPercent != 40 {
		t.Fatal("legacy percentage is not an absolute token count")
	}
	if mcp := usage.Windows[1]; mcp.Label != "MCP calls limit" || mcp.Unit != "requests" || *mcp.Used != 100 || *mcp.Limit != 500 || *mcp.UsedPercent != 20 {
		t.Fatal("MCP total and consumption were reversed or mislabeled")
	}
	if unknown := usage.Windows[2]; unknown.LimitWindowSeconds != 0 || unknown.ResetAt != 0 || unknown.UsedPercent != nil || unknown.Used != nil {
		t.Fatal("unknown window data must remain unreported")
	}
}

func TestParseUsageRejectsMalformedAndFailureEnvelopes(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"error":"rejected"}`, `{"usage":{"used":"NaN"}}`, `{"usages":{"limit_5h":{"used_ratio":"invalid"}}}`} {
		if _, err := parseKimi([]byte(body)); err == nil {
			t.Errorf("accepted invalid Kimi response %s", body)
		}
	}
	for _, body := range []string{`null`, `{}`, `{"success":false,"data":{"limits":[]}}`, `{"code":401,"data":{"limits":[]}}`, `{"data":{"limits":[{"type":"CREDIT_LIMIT","usage":"Infinity"}]}}`} {
		if _, err := parseGLM([]byte(body)); err == nil {
			t.Errorf("accepted invalid GLM response %s", body)
		}
	}
}
