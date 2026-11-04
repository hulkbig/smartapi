// Package observation builds bounded display metadata without retaining wire
// payloads or changing routing, continuation, or billing semantics.
package observation

import (
	"regexp"
	"strings"
	"unicode"

	"ferry/internal/domain"
)

const ExcerptLimit = 160

var credentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bbearer\s+[^\s,;]+`),
	regexp.MustCompile(`\b(?:sk-|ghp_|github_pat_)[A-Za-z0-9_-]{12,}`),
	regexp.MustCompile(`(?i)\b(?:api[_-]?key|authorization|password|secret|access[_-]?token)\s*[:=]\s*[^\s,;]+`),
}

// Clip redacts recognizable credentials before Unicode-safe truncation. It
// deliberately does not claim to remove arbitrary secrets from natural text.
func Clip(text string, limit int) (string, bool) {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	for _, pattern := range credentialPatterns {
		text = pattern.ReplaceAllString(text, "[redacted]")
	}
	chars := []rune(text)
	if len(chars) > limit {
		return string(chars[:limit]), true
	}
	return text, false
}

func RequestPreview(request *domain.RequestContract, enabled bool) domain.ContentPreview {
	p := domain.ContentPreview{ContentState: "disabled"}
	if !enabled {
		return p
	}
	p.ContentState = "empty"
	if request == nil {
		return p
	}
	for i := len(request.Messages) - 1; i >= 0; i-- {
		m := request.Messages[i]
		if m.Role != domain.RoleUser {
			continue
		}
		var text strings.Builder
		for _, b := range m.Blocks {
			if b.Kind == domain.BlockText {
				visible := stripKnownContext(b.Text)
				// Retain only enough text to determine truncation, not a second body.
				remaining := ExcerptLimit*4 + 16 - text.Len()
				if remaining <= 0 {
					break
				}
				text.WriteString(visible[:min(len(visible), remaining)])
				text.WriteByte(' ')
			}
		}
		p.RequestExcerpt, p.Truncated = Clip(strings.ToValidUTF8(text.String(), ""), ExcerptLimit)
		if p.RequestExcerpt != "" {
			p.ContentState = "recorded"
			break
		}
	}
	if p.RequestExcerpt == "" {
		for i := len(request.Messages) - 1; i >= 0 && len(p.ToolNames) < 3; i-- {
			for _, b := range request.Messages[i].Blocks {
				if b.Kind == domain.BlockToolUse && b.Name != "" && len(p.ToolNames) < 3 {
					name, _ := Clip(b.Name, 64)
					p.ToolNames = append(p.ToolNames, name)
				}
			}
		}
	}
	return SelectPreview(p, nil)
}

// Only recognized client context wrappers at the start are omitted. Ordinary
// instructions and arbitrary user prose are preserved verbatim before Clip.
func stripKnownContext(text string) string {
	for range 8 {
		text = strings.TrimSpace(text)
		matched := false
		for _, tag := range []string{"environment_context", "system-reminder"} {
			if !strings.HasPrefix(text, "<"+tag+">") {
				continue
			}
			end := "</" + tag + ">"
			index := strings.Index(text[:min(len(text), 64*1024)], end)
			if index < 0 {
				return ""
			}
			text = text[index+len(end):]
			matched = true
			break
		}
		if !matched {
			return text
		}
	}
	return text
}

// SelectPreview compares only retained excerpts in the same verified Session.
// The caller supplies the preceding request independently of page/filter state.
func SelectPreview(p domain.ContentPreview, previous *domain.ContentPreview) domain.ContentPreview {
	p.PreviewRole, p.PreviewText, p.PreviewReason = "", "", ""
	if p.ContentState == "disabled" || p.ContentState == "legacy_unavailable" {
		return p
	}
	repeated := previous != nil && p.RequestExcerpt != "" && p.RequestExcerpt == previous.RequestExcerpt
	if p.ResponseExcerpt != "" && (p.RequestExcerpt == "" || repeated) {
		p.PreviewRole, p.PreviewText = "response", p.ResponseExcerpt
		p.PreviewReason = "no_user_text"
		if repeated {
			p.PreviewReason = "repeated_input"
			if p.Truncated || previous.Truncated {
				p.PreviewReason = "shared_prefix"
			}
		}
	} else if p.RequestExcerpt != "" {
		p.PreviewRole, p.PreviewText = "request", p.RequestExcerpt
		if repeated {
			p.PreviewReason = "repeated_waiting"
		}
	} else if len(p.ToolNames) > 0 {
		p.PreviewRole, p.PreviewText, p.PreviewReason = "tool", strings.Join(p.ToolNames, ", "), "tool_only"
	}
	return p
}

func ClientKind(evidence []domain.AffinityEvidence) string {
	for _, e := range evidence {
		switch {
		case strings.HasPrefix(e.Source, "codex."):
			return "codex"
		case strings.HasPrefix(e.Source, "claude-code."):
			return "claude-code"
		case strings.HasPrefix(e.Source, "hermes."):
			return "hermes"
		case strings.HasPrefix(e.Source, "openclaw."):
			return "openclaw"
		}
	}
	return "api"
}
