package observation

import (
	"encoding/json"

	"ferry/internal/domain"
)

// StreamVisibleContent reads only known client-visible fields after a
// successful downstream write. Reasoning and tool arguments are never read.
// Oversized opaque native frames remain explicitly unsupported.
func StreamVisibleContent(event string, payload []byte) (text, tool string, recognized bool) {
	if len(payload) > 64*1024 {
		return "", "", false
	}
	var frame struct {
		Type    string          `json:"type"`
		Delta   json.RawMessage `json:"delta"`
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
		Block struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		} `json:"content_block"`
		Item struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"item"`
	}
	if json.Unmarshal(payload, &frame) != nil {
		return "", "", false
	}
	if event == "" && len(frame.Choices) > 0 {
		for _, choice := range frame.Choices {
			text += choice.Delta.Content
			if tool == "" {
				for _, call := range choice.Delta.ToolCalls {
					if call.Function.Name != "" {
						tool = call.Function.Name
						break
					}
				}
			}
		}
		return text, tool, true
	}
	typ := frame.Type
	if typ == "" {
		typ = event
	}
	switch typ {
	case "response.output_text.delta":
		if json.Unmarshal(frame.Delta, &text) == nil {
			return text, "", true
		}
	case "content_block_delta":
		var delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(frame.Delta, &delta) == nil && delta.Type == "text_delta" {
			return delta.Text, "", true
		}
		return "", "", true
	case "content_block_start":
		if frame.Block.Type == "text" {
			return frame.Block.Text, "", true
		}
		if frame.Block.Type == "tool_use" {
			return "", frame.Block.Name, true
		}
		return "", "", true
	case "response.output_item.added":
		if frame.Item.Type == "function_call" {
			return "", frame.Item.Name, true
		}
		return "", "", true
	case "message_start", "message_stop", "message_delta", "content_block_stop", "ping",
		"response.created", "response.in_progress", "response.completed", "response.output_text.done",
		"response.content_part.added", "response.content_part.done", "response.output_item.done",
		"response.reasoning_summary_text.delta", "response.reasoning_summary_text.done":
		return "", "", true
	}
	return "", "", false
}

// TerminalVisibleContent is a fallback for native Responses streams that
// deliver their visible text only in a terminal frame. Call only before any
// text delta was collected, so the completed response cannot duplicate it.
func TerminalVisibleContent(event string, payload []byte) (string, []string) {
	if len(payload) > 64*1024 {
		return "", nil
	}
	type outputItem struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	var frame struct {
		Type     string     `json:"type"`
		Text     string     `json:"text"`
		Item     outputItem `json:"item"`
		Response struct {
			Output []outputItem `json:"output"`
		} `json:"response"`
	}
	if json.Unmarshal(payload, &frame) != nil {
		return "", nil
	}
	if frame.Type != "" {
		event = frame.Type
	}
	if event == "response.output_text.done" {
		return frame.Text, nil
	}
	var items []outputItem
	switch event {
	case "response.completed":
		items = frame.Response.Output
	case "response.output_item.done":
		items = []outputItem{frame.Item}
	default:
		return "", nil
	}
	var blocks []domain.Block
	for _, item := range items {
		if item.Type == "message" {
			for _, part := range item.Content {
				if part.Type == "output_text" {
					blocks = append(blocks, domain.Block{Kind: domain.BlockText, Text: part.Text})
				}
			}
		}
		if item.Type == "function_call" {
			blocks = append(blocks, domain.Block{Kind: domain.BlockToolUse, Name: item.Name})
		}
	}
	text, tools, _ := VisibleBlocks(blocks)
	return text, tools
}

func VisibleBlocks(blocks []domain.Block) (text string, tools []string, truncated bool) {
	for _, b := range blocks {
		if b.Kind == domain.BlockText {
			remaining := ExcerptLimit*4 + 64 - len(text)
			if remaining <= 0 {
				truncated = true
				continue
			}
			text += b.Text[:min(remaining, len(b.Text))]
			truncated = truncated || len(b.Text) > remaining
		}
		if b.Kind == domain.BlockToolUse && len(tools) < 3 {
			name, _ := Clip(b.Name, 64)
			if name != "" {
				tools = append(tools, name)
			}
		}
	}
	return text, tools, truncated
}
