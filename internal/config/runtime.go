package config

import "sync"

import "qwen2api/internal/prompts"

type Runtime struct {
	mu                    sync.RWMutex
	batchLoginConcurrency int
	autoRefresh           bool
	autoRefreshInterval   int
	outThink              bool
	searchInfoMode        string
	simpleModelMap        bool
	chatCleanupMode       int
	promptOverrides       map[string]string
}

func NewRuntime(cfg Config) *Runtime {
	promptOverrides := prompts.CloneOverrides(cfg.PromptOverrides)
	if cfg.QwenWeb2ControlPrompt != "" {
		if _, ok := promptOverrides[prompts.IDQwenWeb2Control]; !ok {
			promptOverrides[prompts.IDQwenWeb2Control] = cfg.QwenWeb2ControlPrompt
		}
	}
	return &Runtime{
		batchLoginConcurrency: cfg.BatchLoginConcurrency,
		autoRefresh:           cfg.AutoRefresh,
		autoRefreshInterval:   cfg.AutoRefreshInterval,
		outThink:              cfg.OutThink,
		searchInfoMode:        cfg.SearchInfoMode,
		simpleModelMap:        cfg.SimpleModelMap,
		chatCleanupMode:       cfg.ChatCleanupMode,
		promptOverrides:       prompts.NormalizeOverrides(promptOverrides),
	}
}

func (r *Runtime) Snapshot() RuntimeSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return RuntimeSnapshot{
		BatchLoginConcurrency: r.batchLoginConcurrency,
		AutoRefresh:           r.autoRefresh,
		AutoRefreshInterval:   r.autoRefreshInterval,
		OutThink:              r.outThink,
		SearchInfoMode:        r.searchInfoMode,
		SimpleModelMap:        r.simpleModelMap,
		ChatCleanupMode:       r.chatCleanupMode,
		PromptOverrides:       prompts.CloneOverrides(r.promptOverrides),
		QwenWeb2ControlPrompt: prompts.Resolve(r.promptOverrides, prompts.IDQwenWeb2Control),
	}
}

type RuntimeSnapshot struct {
	BatchLoginConcurrency int
	AutoRefresh           bool
	AutoRefreshInterval   int
	OutThink              bool
	SearchInfoMode        string
	SimpleModelMap        bool
	ChatCleanupMode       int
	QwenWeb2ControlPrompt string
	PromptOverrides       map[string]string
}

func (r *Runtime) SetAutoRefresh(enabled bool, interval int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.autoRefresh = enabled
	r.autoRefreshInterval = interval
}

func (r *Runtime) SetBatchLoginConcurrency(v int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batchLoginConcurrency = v
}

func (r *Runtime) SetOutThink(v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outThink = v
}

func (r *Runtime) SetSearchInfoMode(v string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.searchInfoMode = v
}

func (r *Runtime) SetSimpleModelMap(v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.simpleModelMap = v
}

func (r *Runtime) SetChatCleanupMode(v int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chatCleanupMode = v
}

func (r *Runtime) SetQwenWeb2ControlPrompt(v string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	overrides := prompts.CloneOverrides(r.promptOverrides)
	overrides[prompts.IDQwenWeb2Control] = v
	r.promptOverrides = prompts.NormalizeOverrides(overrides)
}

func (r *Runtime) SetPromptOverrides(v map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.promptOverrides = prompts.NormalizeOverrides(v)
}

func (r *Runtime) ApplySnapshot(snapshot RuntimeSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batchLoginConcurrency = snapshot.BatchLoginConcurrency
	r.autoRefresh = snapshot.AutoRefresh
	r.autoRefreshInterval = snapshot.AutoRefreshInterval
	r.outThink = snapshot.OutThink
	r.searchInfoMode = snapshot.SearchInfoMode
	r.simpleModelMap = snapshot.SimpleModelMap
	r.chatCleanupMode = snapshot.ChatCleanupMode
	r.promptOverrides = prompts.NormalizeOverrides(snapshot.PromptOverrides)
}
