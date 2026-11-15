package codingplan

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type kimiQuantity struct {
	Limit     *number `json:"limit"`
	Used      *number `json:"used"`
	Remaining *number `json:"remaining"`
	ResetTime string  `json:"resetTime"`
}

type kimiRatio struct {
	UsedRatio *number `json:"used_ratio"`
	ResetTime string  `json:"reset_time"`
}

func parseKimi(body []byte) (*Snapshot, error) {
	var response struct {
		Usages map[string]*kimiRatio `json:"usages"`
		Usage  *kimiQuantity         `json:"usage"`
		Limits []struct {
			Window struct {
				Duration *number `json:"duration"`
				TimeUnit string  `json:"timeUnit"`
			} `json:"window"`
			Detail *kimiQuantity `json:"detail"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(body, &response); err != nil || (response.Usages == nil && response.Usage == nil && response.Limits == nil) {
		return nil, fmt.Errorf("missing Kimi usage")
	}
	usage := &Snapshot{PlanName: "Kimi Coding", Windows: []Window{}}
	// Match the current official client when the ratio-based schema is present.
	// Legacy balances are a fallback for older API-key responses, never a second
	// source to silently overwrite a current window with a conflicting figure.
	if response.Usages != nil {
		for _, spec := range []struct {
			id, label string
			seconds   int64
		}{
			{"limit_5h", "5-hour limit", 18000},
			{"limit_7d", "Weekly limit", 604800},
			{"limit_month_total", "Monthly limit", 0},
		} {
			ratio := response.Usages[spec.id]
			if ratio == nil {
				continue
			}
			percent := quantity(ratio.UsedRatio)
			if percent != nil {
				*percent = math.Min(1, *percent) * 100
			}
			window := Window{ID: spec.id, Label: spec.label, Unit: "percent", UsedPercent: percent, LimitWindowSeconds: spec.seconds, ResetAt: resetTime(ratio.ResetTime)}
			if spec.id == "limit_month_total" {
				if code := response.Usages["limit_month_code"]; code != nil {
					codePercent := quantity(code.UsedRatio)
					if codePercent != nil {
						*codePercent = math.Min(1, *codePercent) * 100
					}
					window.Breakdown = []UsageComponent{{ID: "coding", Label: "Monthly Coding usage", UsedPercent: codePercent}}
				}
			}
			usage.Windows = append(usage.Windows, window)
		}
		return usage, nil
	}
	if response.Usage != nil {
		usage.Windows = append(usage.Windows, kimiLegacyWindow("usage", "Subscription limit", 0, response.Usage))
	}
	for index, limit := range response.Limits {
		if limit.Detail == nil {
			continue
		}
		var seconds int64
		unit := strings.TrimPrefix(strings.ToUpper(limit.Window.TimeUnit), "TIME_UNIT_")
		if duration := quantity(limit.Window.Duration); duration != nil && *duration > 0 && *duration <= 366*86400 {
			scale := map[string]int64{"SECOND": 1, "MINUTE": 60, "HOUR": 3600, "DAY": 86400, "WEEK": 604800}[unit]
			if scale > 0 {
				seconds = int64(*duration * float64(scale))
			}
		}
		label := windowLabel(seconds, "Coding limit")
		usage.Windows = append(usage.Windows, kimiLegacyWindow("limit-"+strconv.Itoa(index), label, seconds, limit.Detail))
	}
	return usage, nil
}

func kimiLegacyWindow(id, label string, seconds int64, q *kimiQuantity) Window {
	w := Window{ID: id, Label: label, Used: quantity(q.Used), Remaining: quantity(q.Remaining), Limit: quantity(q.Limit), LimitWindowSeconds: seconds, ResetAt: resetTime(q.ResetTime)}
	w.UsedPercent = usedPercent(w.Used, w.Remaining, w.Limit, nil)
	return w
}

func parseGLM(body []byte) (*Snapshot, error) {
	var response struct {
		Code    *number `json:"code"`
		Success *bool   `json:"success"`
		Data    *struct {
			Level  string `json:"level"`
			Limits []struct {
				Type          string  `json:"type"`
				Unit          *number `json:"unit"`
				Number        *number `json:"number"`
				Limit         *number `json:"usage"`
				Used          *number `json:"currentValue"`
				Remaining     *number `json:"remaining"`
				Percentage    *number `json:"percentage"`
				NextResetTime *number `json:"nextResetTime"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Data == nil || response.Data.Limits == nil || (response.Success != nil && !*response.Success) || (response.Code != nil && *response.Code != 200 && *response.Code != 0) {
		return nil, fmt.Errorf("missing GLM usage")
	}
	usage := &Snapshot{PlanName: response.Data.Level, Windows: []Window{}}
	for index, limit := range response.Data.Limits {
		w := Window{ID: strings.ToLower(limit.Type) + "-" + strconv.Itoa(index), Label: "Coding limit", UsedPercent: quantity(limit.Percentage)}
		// Only the observed 5-hour and weekly time-unit pairs are mapped. An
		// unknown upstream enum must not acquire a fabricated reset interval.
		if limit.Unit != nil && limit.Number != nil {
			if *limit.Unit == 3 && *limit.Number == 5 {
				w.LimitWindowSeconds = 18000
			} else if *limit.Unit == 6 && *limit.Number == 1 {
				w.LimitWindowSeconds = 604800
			}
		}
		switch limit.Type {
		case "CREDIT_LIMIT":
			w.Unit, w.Used, w.Remaining, w.Limit = "credits", quantity(limit.Used), quantity(limit.Remaining), quantity(limit.Limit)
		case "TOKENS_LIMIT":
			w.Unit = "percent"
		case "TIME_LIMIT":
			w.Label, w.Unit = "MCP calls limit", "requests"
			w.Used, w.Remaining, w.Limit = quantity(limit.Used), quantity(limit.Remaining), quantity(limit.Limit)
		default:
			continue
		}
		if w.Label == "Coding limit" {
			w.Label = windowLabel(w.LimitWindowSeconds, w.Label)
		}
		if w.UsedPercent == nil {
			w.UsedPercent = usedPercent(w.Used, w.Remaining, w.Limit, nil)
		}
		if reset := quantity(limit.NextResetTime); reset != nil && *reset >= 1e12 && *reset < 1e15 {
			w.ResetAt = int64(*reset / 1000)
		}
		usage.Windows = append(usage.Windows, w)
	}
	return usage, nil
}

func windowLabel(seconds int64, fallback string) string {
	switch seconds {
	case 18000:
		return "5-hour limit"
	case 604800:
		return "Weekly limit"
	case 86400:
		return "Daily limit"
	case 0:
		return fallback
	default:
		if seconds%3600 == 0 {
			return fmt.Sprintf("%d-hour limit", seconds/3600)
		}
		return fallback
	}
}
