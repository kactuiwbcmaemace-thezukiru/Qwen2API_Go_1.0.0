package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"qwen2api/internal/lingma/toolemulation"
	"qwen2api/internal/prompts"
)

func TestNewKeepsZeroTimeoutUnlimited(t *testing.T) {
	svc := New(Config{Timeout: 0})
	if svc.cfg.Timeout != 0 {
		t.Fatalf("timeout = %v, want 0", svc.cfg.Timeout)
	}
}

func TestContextWithOptionalTimeoutZeroDoesNotSetDeadline(t *testing.T) {
	ctx, cancel := contextWithOptionalTimeout(context.Background(), 0)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("zero timeout should not set a deadline")
	}
}

func TestContextWithOptionalTimeoutPositiveSetsDeadline(t *testing.T) {
	ctx, cancel := contextWithOptionalTimeout(context.Background(), time.Second)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("positive timeout should set a deadline")
	}
}

func TestBuildLingmaPromptOnlyInjectsToolingWhenEmulationEnabled(t *testing.T) {
	req := ChatRequest{
		Messages: []ChatMessage{{Role: "user", Text: "查看项目结构"}},
		Tools: []toolemulation.ToolDef{{
			Name: "Bash",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"command": map[string]any{"type": "string"},
				},
				"required": []any{"command"},
			},
		}},
		ToolChoice: toolemulation.ToolChoice{Mode: "auto"},
	}

	remotePrompt, err := buildLingmaPrompt(req, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(remotePrompt, "```json action") || strings.Contains(remotePrompt, "DIRECT tool access") {
		t.Fatalf("remote prompt should not include tool emulation:\n%s", remotePrompt)
	}

	emulatedPrompt, err := buildLingmaPrompt(req, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(emulatedPrompt, "```json action") || !strings.Contains(emulatedPrompt, "DIRECT tool access") {
		t.Fatalf("emulated prompt should include tool instructions:\n%s", emulatedPrompt)
	}
}

func TestBuildLingmaPromptUsesPromptOverrides(t *testing.T) {
	req := ChatRequest{
		Messages: []ChatMessage{{Role: "user", Text: "查看项目结构"}},
		Tools: []toolemulation.ToolDef{{
			Name: "Bash",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"command": map[string]any{"type": "string"},
				},
				"required": []any{"command"},
			},
		}},
		ToolChoice: toolemulation.ToolChoice{Mode: "auto"},
		PromptOverrides: map[string]string{
			prompts.IDLingmaTooling: "CUSTOM TOOLING\n{{tool_lines}}\n{{force_constraint}}",
		},
	}

	prompt, err := buildLingmaPrompt(req, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "CUSTOM TOOLING") || !strings.Contains(prompt, "Bash(command)") {
		t.Fatalf("custom lingma prompt missing rendered values:\n%s", prompt)
	}
}

func TestShouldRetryRemoteNativeToolForContinuationText(t *testing.T) {
	req := ChatRequest{
		Tools: []toolemulation.ToolDef{{Name: "Bash"}},
		ToolChoice: toolemulation.ToolChoice{
			Mode: "auto",
		},
	}
	if !shouldRetryRemoteNativeTool(req, "让我查看一下项目的整体结构，特别是源代码目录：") {
		t.Fatal("expected continuation text to trigger native tool retry")
	}
	if shouldRetryRemoteNativeTool(req, "这是一个 uni-app 项目，核心目录是 src。") {
		t.Fatal("substantive answer should not trigger retry")
	}
	req.ToolChoice = toolemulation.ToolChoice{Mode: "none"}
	if shouldRetryRemoteNativeTool(req, "让我查看一下：") {
		t.Fatal("tool_choice none should not trigger retry")
	}
}

func TestBuildLingmaPromptKeepsToolResultsForEmulation(t *testing.T) {
	req := ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Text: "查看项目"},
			{Role: "assistant", ToolCalls: []toolemulation.ToolCall{{ID: "call_1", Name: "Bash", Arguments: map[string]any{"command": "pwd"}}}},
			{Role: "tool", ToolCallID: "call_1", Text: "/tmp/project"},
		},
		Tools:      []toolemulation.ToolDef{{Name: "Bash"}},
		ToolChoice: toolemulation.ToolChoice{Mode: "auto"},
	}
	prompt, err := buildLingmaPrompt(req, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Tool result for call_1") || !strings.Contains(prompt, "/tmp/project") {
		t.Fatalf("emulated prompt should include tool result:\n%s", prompt)
	}
	if strings.Contains(prompt, "Assistant used tool") {
		t.Fatalf("emulated prompt should not include textualized assistant tool calls:\n%s", prompt)
	}
}

func TestRemoteImagesFromRequest(t *testing.T) {
	req := ChatRequest{Messages: []ChatMessage{{Role: "user", Text: "see", Images: []Image{{MediaType: "image/png", Data: "AAAA"}}}}}
	images := remoteImagesFromRequest(req)
	if len(images) != 1 {
		t.Fatalf("images = %#v", images)
	}
	if images[0].MediaType != "image/png" || images[0].Data != "AAAA" {
		t.Fatalf("unexpected image = %#v", images[0])
	}
}

func TestBuildLingmaPromptUsesImageFallbackForImageOnlyUser(t *testing.T) {
	req := ChatRequest{
		System:   "这张图片是什么？只用两句话回答。",
		Messages: []ChatMessage{{Role: "user", Images: []Image{{URL: "file:///tmp/a.jpg"}}}},
	}

	prompt, err := buildLingmaPrompt(req, false)
	if err != nil {
		t.Fatalf("buildLingmaPrompt returned error: %v", err)
	}
	if !strings.Contains(prompt, "这张图片是什么") {
		t.Fatalf("prompt should include image fallback question, got %q", prompt)
	}
}
