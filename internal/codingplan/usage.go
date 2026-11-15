// Package codingplan reads subscription usage without issuing inference calls.
package codingplan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Snapshot struct {
	Provider   string    `json:"provider"`
	PlanName   string    `json:"plan_name,omitempty"`
	Windows    []Window  `json:"windows"`
	ObservedAt time.Time `json:"observed_at"`
}

// Pointers distinguish an actual zero balance from an unreported quantity.
type Window struct {
	ID                 string           `json:"id"`
	Label              string           `json:"label"`
	UsedPercent        *float64         `json:"used_percent,omitempty"`
	Used               *float64         `json:"used,omitempty"`
	Remaining          *float64         `json:"remaining,omitempty"`
	Limit              *float64         `json:"limit,omitempty"`
	Unit               string           `json:"unit,omitempty"`
	LimitWindowSeconds int64            `json:"limit_window_seconds,omitempty"`
	ResetAt            int64            `json:"reset_at,omitempty"`
	Breakdown          []UsageComponent `json:"breakdown,omitempty"`
}

// UsageComponent consumes part of its parent window's allowance. It does not
// establish another independent quota pool or reset schedule.
type UsageComponent struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	UsedPercent *float64 `json:"used_percent,omitempty"`
}

type Source struct {
	Provider string
	URL      string
}

// Only official HTTPS Coding Plan origins receive the configured key. The
// inference base path identifies the product; ordinary API balances differ.
func SourceForURL(baseURL string) (Source, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme != "https" || u.User != nil || strings.ContainsAny(baseURL, "?#") || (u.Port() != "" && u.Port() != "443") || u.RawPath != "" {
		return Source{}, fmt.Errorf("unsupported Coding Plan endpoint")
	}
	host, path := strings.ToLower(u.Hostname()), strings.TrimRight(u.Path, "/")
	within := func(prefix string) bool { return path == prefix || strings.HasPrefix(path, prefix+"/") }
	switch {
	case (host == "api.kimi.com" || host == "api.kimi.ai") && within("/coding"):
		return Source{Provider: "kimi-coding", URL: "https://" + host + "/coding/v1/usages"}, nil
	case host == "open.bigmodel.cn" && (within("/api/coding") || within("/api/anthropic") || within("/api/v1")):
		return Source{Provider: "glm-coding", URL: "https://open.bigmodel.cn/api/monitor/usage/quota/limit"}, nil
	case host == "api.z.ai" && (within("/api/coding") || within("/api/anthropic") || within("/api/v1")):
		return Source{Provider: "zai-coding", URL: "https://api.z.ai/api/monitor/usage/quota/limit"}, nil
	default:
		return Source{}, fmt.Errorf("unsupported Coding Plan endpoint")
	}
}

func Fetch(ctx context.Context, client *http.Client, source Source, secret string, observedAt time.Time) (*Snapshot, error) {
	// Validate the source again so a caller cannot pass an arbitrary usage URL.
	valid := false
	for _, base := range []string{"https://api.kimi.com/coding/v1", "https://api.kimi.ai/coding/v1", "https://open.bigmodel.cn/api/coding/paas/v4", "https://api.z.ai/api/coding/paas/v4"} {
		expected, _ := SourceForURL(base)
		if expected == source {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("unsupported Coding Plan usage source")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid quota request")
	}
	authorization := secret
	if source.Provider == "kimi-coding" {
		authorization = "Bearer " + secret
	}
	if secret != "" {
		request.Header.Set("Authorization", authorization)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "SmartAPI/1.0")
	bounded := http.Client{Timeout: 15 * time.Second}
	if client != nil {
		bounded = *client
	}
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := bounded.Do(request)
	if err != nil {
		// Transport errors and upstream bodies may contain the Authorization
		// value. Only fixed diagnostics cross the management API boundary.
		return nil, fmt.Errorf("quota request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("quota endpoint returned HTTP %d", response.StatusCode)
	}
	const maxBody = 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return nil, fmt.Errorf("quota response could not be read within its size limit")
	}
	var usage *Snapshot
	if source.Provider == "kimi-coding" {
		usage, err = parseKimi(body)
	} else {
		usage, err = parseGLM(body)
	}
	if err != nil {
		return nil, fmt.Errorf("quota endpoint returned invalid usage data")
	}
	usage.Provider, usage.ObservedAt = source.Provider, observedAt
	return usage, nil
}

type number float64

func (n *number) UnmarshalJSON(raw []byte) error {
	value := strings.TrimSpace(string(raw))
	if strings.HasPrefix(value, "\"") {
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("invalid quota number")
		}
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return fmt.Errorf("invalid quota number")
	}
	*n = number(parsed)
	return nil
}

func quantity(n *number) *float64 {
	if n == nil || *n < 0 {
		return nil
	}
	value := float64(*n)
	return &value
}

func usedPercent(used, remaining, limit, percent *float64) *float64 {
	if limit != nil && *limit > 0 {
		if used != nil {
			if *used >= *limit {
				value := float64(100)
				return &value
			}
			value := *used / *limit * 100
			return &value
		}
		if remaining != nil && *remaining <= *limit {
			value := (1 - *remaining / *limit) * 100
			return &value
		}
	}
	return percent
}

func resetTime(raw string) int64 {
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil && parsed.Unix() > 0 {
		return parsed.Unix()
	}
	return 0
}
