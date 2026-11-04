package observation

import (
	"strings"
	"testing"
)

func TestDeliveredContentExcludesReasoningAndToolArguments(t *testing.T) {
	for _, tc := range []struct{ event, wire, text, tool string }{
		{"", `{"choices":[{"delta":{"content":"visible","reasoning_content":"hidden"}}]}`, "visible", ""},
		{"content_block_delta", `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"hidden"}}`, "", ""},
		{"content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"visible"}}`, "visible", ""},
		{"response.output_text.delta", `{"type":"response.output_text.delta","delta":"visible"}`, "visible", ""},
		{"response.output_item.added", `{"type":"response.output_item.added","item":{"type":"function_call","name":"search","arguments":"hidden"}}`, "", "search"},
	} {
		text, tool, recognized := StreamVisibleContent(tc.event, []byte(tc.wire))
		if !recognized || text != tc.text || tool != tc.tool {
			t.Errorf("%s: %q %q %t", tc.wire, text, tool, recognized)
		}
	}
	if _, _, recognized := StreamVisibleContent("opaque", []byte(strings.Repeat("x", 65537))); recognized {
		t.Fatal("oversized opaque frame parsed")
	}
}

func TestTerminalContentUsesOnlyVisibleFields(t *testing.T) {
	text, tools := TerminalVisibleContent("response.completed", []byte(`{"response":{"output":[{"type":"reasoning","content":[{"type":"output_text","text":"hidden"}]},{"type":"message","content":[{"type":"output_text","text":"visible"}]},{"type":"function_call","name":"search","arguments":"hidden"}]}}`))
	if text != "visible" || len(tools) != 1 || tools[0] != "search" {
		t.Fatalf("terminal visible fields: %q %v", text, tools)
	}
}
