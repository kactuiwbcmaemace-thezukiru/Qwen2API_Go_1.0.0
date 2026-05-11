package openai

import (
	"strings"
	"testing"

	"qwen2api/internal/toolcall"
)

func TestNormalizeMessagesKeepsToolReminderNearLatestTurn(t *testing.T) {
	injected := toolcall.InjectPrompt([]map[string]any{
		{"role": "system", "content": "你是一个助手"},
		{"role": "user", "content": strings.Repeat("历史问题;", 200)},
		{"role": "assistant", "content": strings.Repeat("历史回答;", 200)},
		{"role": "user", "content": "现在请查询天气"},
	}, []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "weather_lookup",
				"description": "query weather",
				"parameters": map[string]any{
					"type": "object",
				},
			},
		},
	}, "auto")
	normalized := normalizeMessages(cloneMessageList(injected.Messages), "t2t", thinkingModeFast)
	if len(normalized) != 1 {
		t.Fatalf("normalized len = %d, want 1", len(normalized))
	}

	content := extractText(normalized[0]["content"])
	for _, snippet := range []string{
		"[ml_tool reminder]",
		"Allowed ml_tool names: weather_lookup.",
		"Ignore built-in/native/platform tools.",
	} {
		if !strings.Contains(content, snippet) {
			t.Fatalf("upstream content missing %q", snippet)
		}
	}

	if !strings.Contains(content, "现在请查询天气") {
		t.Fatalf("upstream content missing latest user turn")
	}
	if strings.Index(content, "[ml_tool reminder]") > strings.Index(content, "现在请查询天气") {
		t.Fatalf("expected tool reminder before latest user turn, got %q", content)
	}
}

func TestInjectQwenWeb2ControlPromptPrependsSystemPrompt(t *testing.T) {
	messages := injectQwenWeb2ControlPrompt([]map[string]any{
		{"role": "system", "content": "user system"},
		{"role": "developer", "content": "developer note"},
		{"role": "user", "content": "hello"},
	}, "backend control")

	injected := toolcall.InjectPrompt(messages, nil, nil)
	normalized := normalizeMessages(cloneMessageList(injected.Messages), "t2t", thinkingModeFast)
	if len(normalized) != 1 {
		t.Fatalf("normalized len = %d, want 1", len(normalized))
	}

	content := extractText(normalized[0]["content"])
	backendIndex := strings.Index(content, "backend control")
	userSystemIndex := strings.Index(content, "user system")
	developerIndex := strings.Index(content, "developer note")
	if backendIndex < 0 || userSystemIndex < 0 || developerIndex < 0 {
		t.Fatalf("content missing expected prompts: %q", content)
	}
	if backendIndex > userSystemIndex || backendIndex > developerIndex {
		t.Fatalf("backend prompt should be first, got %q", content)
	}
}

func TestInjectQwenWeb2ControlPromptEmptyKeepsMessages(t *testing.T) {
	messages := []map[string]any{{"role": "user", "content": "hello"}}
	got := injectQwenWeb2ControlPrompt(messages, "")

	if len(got) != 1 || got[0]["content"] != "hello" {
		t.Fatalf("messages = %#v, want unchanged", got)
	}
}

func TestSelectIncrementalTailMessagesKeepsTrailingToolResultsBeforeLatestTurn(t *testing.T) {
	selected := selectIncrementalTailMessages([]map[string]any{
		{"role": "user", "content": "第一轮问题"},
		{"role": "assistant", "tool_calls": []any{map[string]any{"id": "call_1"}}},
		{"role": "tool", "name": "weather_lookup", "content": "晴天"},
		{"role": "user", "content": "请基于工具结果总结"},
	})

	if len(selected) != 2 {
		t.Fatalf("selected len = %d, want 2", len(selected))
	}
	if role := strings.TrimSpace(selected[0]["role"].(string)); role != "tool" {
		t.Fatalf("first role = %q, want %q", role, "tool")
	}
	if role := strings.TrimSpace(selected[1]["role"].(string)); role != "user" {
		t.Fatalf("second role = %q, want %q", role, "user")
	}
}
