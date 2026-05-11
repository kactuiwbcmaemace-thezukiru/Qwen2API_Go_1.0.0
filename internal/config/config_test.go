package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qwen2api/internal/prompts"
)

func TestLoadReadsQwenWeb2ControlPrompt(t *testing.T) {
	t.Setenv("QWEN_WEB2_CONTROL_PROMPT", `line one\nline two`)

	cfg := Load()
	if cfg.QwenWeb2ControlPrompt != "line one\nline two" {
		t.Fatalf("QwenWeb2ControlPrompt = %q, want decoded newline", cfg.QwenWeb2ControlPrompt)
	}
}

func TestLoadDefaultsQwenWeb2ControlPromptToEmpty(t *testing.T) {
	t.Setenv("QWEN_WEB2_CONTROL_PROMPT", "")

	cfg := Load()
	if cfg.QwenWeb2ControlPrompt != "" {
		t.Fatalf("QwenWeb2ControlPrompt = %q, want empty", cfg.QwenWeb2ControlPrompt)
	}
}

func TestRuntimeSnapshotToEnvIncludesQwenWeb2ControlPrompt(t *testing.T) {
	values := RuntimeSnapshotToEnv(RuntimeSnapshot{QwenWeb2ControlPrompt: "line one\nline two"})

	if values["QWEN_WEB2_CONTROL_PROMPT"] != `line one\nline two` {
		t.Fatalf("QWEN_WEB2_CONTROL_PROMPT = %q, want escaped newline", values["QWEN_WEB2_CONTROL_PROMPT"])
	}
}

func TestLoadReadsPromptOverridesJSON(t *testing.T) {
	t.Setenv("PROMPT_OVERRIDES_JSON", `{"frontend.debug.system":"custom\nsystem"}`)
	t.Setenv("QWEN_WEB2_CONTROL_PROMPT", "")

	cfg := Load()
	if got := prompts.Resolve(cfg.PromptOverrides, prompts.IDAdminDebugSystem); got != "custom\nsystem" {
		t.Fatalf("debug prompt = %q", got)
	}
}

func TestLoadLegacyQwenPromptInitializesPromptOverride(t *testing.T) {
	t.Setenv("PROMPT_OVERRIDES_JSON", "{}")
	t.Setenv("QWEN_WEB2_CONTROL_PROMPT", `legacy\nprompt`)

	cfg := Load()
	if got := prompts.Resolve(cfg.PromptOverrides, prompts.IDQwenWeb2Control); got != "legacy\nprompt" {
		t.Fatalf("qwen prompt = %q", got)
	}
}

func TestSaveDotEnvValuesPersistsPromptOverridesJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("PROMPT_OVERRIDES_JSON={}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	values := RuntimeSnapshotToEnv(RuntimeSnapshot{
		PromptOverrides: map[string]string{
			prompts.IDAdminDebugSystem: "line one\nline two",
		},
	})
	if err := SaveDotEnvValues(path, values); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `PROMPT_OVERRIDES_JSON={"frontend.debug.system":"line one\nline two"}`) {
		t.Fatalf("unexpected .env content:\n%s", raw)
	}
}
