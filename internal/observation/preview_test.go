package observation

import (
	"strings"
	"testing"
	"unicode/utf8"

	"ferry/internal/domain"
)

func TestRequestPreviewBoundedVisibleUserContent(t *testing.T) {
	rc := &domain.RequestContract{Messages: []domain.Message{
		{Role: domain.RoleSystem, Blocks: []domain.Block{{Kind: domain.BlockText, Text: "hidden-system"}}},
		{Role: domain.RoleUser, Blocks: []domain.Block{{Kind: domain.BlockText, Text: "old-user"}}},
		{Role: domain.RoleUser, Blocks: []domain.Block{{Kind: domain.BlockThinking, Text: "hidden-reasoning"}, {Kind: domain.BlockText, Text: "继续\nsecret=example-secret " + strings.Repeat("你好", 200)}}},
		{Role: domain.RoleTool, Blocks: []domain.Block{{Kind: domain.BlockText, Text: "hidden-tool-result"}}},
	}}
	p := RequestPreview(rc, true)
	if !utf8.ValidString(p.RequestExcerpt) || len([]rune(p.RequestExcerpt)) != ExcerptLimit || !p.Truncated {
		t.Fatalf("not bounded: %+v", p)
	}
	for _, hidden := range []string{"hidden-", "old-user", "example-secret"} {
		if strings.Contains(p.RequestExcerpt, hidden) {
			t.Fatalf("retained %s: %+v", hidden, p)
		}
	}
	if !strings.Contains(p.RequestExcerpt, "[redacted]") {
		t.Fatal("credential not redacted")
	}
	if off := RequestPreview(rc, false); off.RequestExcerpt != "" || off.ContentState != "disabled" {
		t.Fatalf("disabled: %+v", off)
	}
}

func TestPreviewFallbackAndToolOnly(t *testing.T) {
	previous := domain.ContentPreview{RequestExcerpt: "continue"}
	p := SelectPreview(domain.ContentPreview{RequestExcerpt: "continue", ResponseExcerpt: "result", ContentState: "recorded"}, &previous)
	if p.PreviewRole != "response" || p.PreviewText != "result" {
		t.Fatalf("repeat fallback: %+v", p)
	}
	p.Truncated = true
	p = SelectPreview(p, &previous)
	if p.PreviewReason != "shared_prefix" {
		t.Fatalf("prefix reported as full equality: %+v", p)
	}
	p = SelectPreview(domain.ContentPreview{RequestExcerpt: "continue", ContentState: "pending"}, &previous)
	if p.PreviewRole != "request" || p.PreviewReason != "repeated_waiting" {
		t.Fatalf("invented response: %+v", p)
	}
	p = RequestPreview(&domain.RequestContract{Messages: []domain.Message{{Role: domain.RoleAssistant, Blocks: []domain.Block{{Kind: domain.BlockToolUse, Name: "search", Input: []byte(`{"secret":"hidden"}`)}}}}}, true)
	if p.PreviewRole != "tool" || p.PreviewText != "search" {
		t.Fatalf("tool-only: %+v", p)
	}
	p = SelectPreview(domain.ContentPreview{ResponseExcerpt: "visible", ContentState: "partial"}, nil)
	if p.PreviewRole != "response" || p.ContentState != "partial" {
		t.Fatalf("partial lost: %+v", p)
	}
}

func TestRequestPreviewSkipsOnlyRecognizedContextWrappers(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"<environment_context>shared shell metadata</environment_context> Fix pagination", "Fix pagination"},
		{"<system-reminder>hidden context</system-reminder>", ""},
		{"Please explain system-reminder wrappers", "Please explain system-reminder wrappers"},
	} {
		p := RequestPreview(&domain.RequestContract{Messages: []domain.Message{{Role: domain.RoleUser, Blocks: []domain.Block{{Kind: domain.BlockText, Text: tc.text}}}}}, true)
		if p.RequestExcerpt != tc.want {
			t.Errorf("%q: %+v", tc.text, p)
		}
	}
}
