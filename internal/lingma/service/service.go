package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"qwen2api/internal/lingma/remote"
	"qwen2api/internal/lingma/toolemulation"
	"qwen2api/internal/prompts"
)

type Config struct {
	RemoteBaseURL         string
	RemoteAuthFile        string
	RemoteVersion         string
	RemoteService         string
	RemoteFetchKeys       string
	RemoteChatTask        string
	CredentialProvider    remote.CredentialProvider
	Model                 string
	Timeout               time.Duration
	RemoteFallbackEnabled bool
	RemoteFallbackModels  []string
}

type Image struct {
	MediaType string // e.g. "image/jpeg", "image/png"
	Data      string // base64 encoded data without prefix
	URL       string // optional original URL
}

type ChatMessage struct {
	Role       string
	Text       string
	Images     []Image
	ToolCallID string
	ToolCalls  []toolemulation.ToolCall
}

type ChatRequest struct {
	Model             string
	System            string
	Messages          []ChatMessage
	Tools             []toolemulation.ToolDef
	ToolChoice        toolemulation.ToolChoice
	ParallelToolCalls *bool
	PromptOverrides   map[string]string

	// Generation parameters (passed through for API compatibility;
	// actual effect depends on Lingma backend support)
	Temperature      *float64
	TopP             *float64
	TopK             int
	Stop             []string
	PresencePenalty  float64
	FrequencyPenalty float64
	MaxTokens        int
	Seed             int
	User             string
	ReasoningEffort  string
	ResponseFormat   string // "json" or "json_schema"
}

type ChatResult struct {
	Text          string
	Model         string
	InputTokens   int
	OutputTokens  int
	SessionID     string
	RequestID     string
	FinishReason  string
	StopReason    string
	UsedTokens    int
	LimitTokens   int
	Endpoint      string
	Transport     string
	ToolCalls     []toolemulation.ToolCall
	CredentialSrc string
}

type StreamEvent struct {
	Delta string
}

type StreamResult struct {
	Result *ChatResult
	Err    error
}

type Model struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Scene      string `json:"scene,omitempty"`
	InternalID string `json:"-"`
}

type State struct {
	Endpoint  string `json:"endpoint,omitempty"`
	Transport string `json:"transport,omitempty"`
	Connected bool   `json:"connected"`
}

type Service struct {
	cfg          Config
	mu           sync.Mutex
	remoteClient *remote.Client
}

func New(cfg Config) *Service {
	cfg.Model = strings.TrimSpace(cfg.Model)
	if len(cfg.RemoteFallbackModels) == 0 {
		cfg.RemoteFallbackModels = DefaultRemoteFallbackModels()
	}
	cfg.Model = normalizeModelForBackend(cfg.Model)
	return &Service{cfg: cfg}
}

func DefaultRemoteFallbackModels() []string {
	return []string{
		"kmodel",
		"mmodel",
		"dashscope_qwen3_coder",
		"dashscope_qmodel",
		"dashscope_qwen_max_latest",
		"dashscope_qwen_plus_20250428_thinking",
	}
}

func (s *Service) SetDefaultModel(model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Model = normalizeModelForBackend(model)
}

func (s *Service) DefaultModel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.cfg.Model)
}

func (s *Service) Warmup(ctx context.Context) error {
	return s.remoteClientLocked().Warmup(ctx)
}

func (s *Service) Close() error {
	return nil
}

func contextWithOptionalTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return State{
		Endpoint:  remote.ResolveBaseURL(s.cfg.RemoteBaseURL),
		Transport: "remote",
		Connected: s.remoteClient != nil,
	}
}

func (s *Service) ListModels(ctx context.Context) ([]Model, error) {
	models, err := s.remoteClientLocked().ListModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(models))
	seen := map[string]bool{}
	for _, model := range models {
		id := strings.TrimSpace(model.Key)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		name := strings.TrimSpace(model.DisplayName)
		if name == "" {
			name = id
		}
		out = append(out, Model{ID: id, Name: name})
	}
	return out, nil
}

func (s *Service) Generate(ctx context.Context, req ChatRequest) (*ChatResult, error) {
	return s.generateRemote(ctx, req, nil)
}

func (s *Service) GenerateStream(ctx context.Context, req ChatRequest) (<-chan StreamEvent, <-chan StreamResult, error) {
	events := make(chan StreamEvent, 256)
	done := make(chan StreamResult, 1)

	go func() {
		result, err := s.generateRemote(ctx, req, func(delta string) {
			if delta == "" {
				return
			}
			select {
			case events <- StreamEvent{Delta: delta}:
			case <-ctx.Done():
			}
		})

		close(events)
		done <- StreamResult{Result: result, Err: err}
		close(done)
	}()

	return events, done, nil
}

func (s *Service) generateRemote(
	ctx context.Context,
	req ChatRequest,
	onDelta func(string),
) (*ChatResult, error) {
	if strings.TrimSpace(req.Model) == "" {
		req.Model = s.DefaultModel()
	}
	req.Model = normalizeModelForBackend(req.Model)
	prompt, err := buildLingmaPrompt(req, false)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("empty user message")
	}

	models := s.remoteAttemptModels(ctx, req.Model)
	client := s.remoteClientLocked()
	var lastErr error
	for i, model := range models {
		attemptCtx, cancel := contextWithOptionalTimeout(ctx, s.cfg.Timeout)
		result, emitted, err := s.generateRemoteWithModel(attemptCtx, client, req, prompt, model, onDelta)
		cancel()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if i == len(models)-1 || emitted || !isRemoteFallbackError(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (s *Service) generateRemoteWithModel(
	ctx context.Context,
	client *remote.Client,
	req ChatRequest,
	prompt string,
	model string,
	onDelta func(string),
) (*ChatResult, bool, error) {
	emitted := false
	delta := func(text string) {
		if text != "" {
			emitted = true
		}
		if onDelta != nil {
			onDelta(text)
		}
	}
	remoteResult, err := client.Chat(ctx, remote.ChatRequest{
		Model:       model,
		Prompt:      prompt,
		Messages:    remoteMessagesFromRequest(req),
		Images:      remoteImagesFromRequest(req),
		Stream:      onDelta != nil,
		Temperature: req.Temperature,
		Tools:       req.Tools,
		ToolChoice:  req.ToolChoice,
	}, delta)
	if err != nil {
		return nil, emitted, err
	}
	if len(remoteResult.ToolCalls) == 0 && shouldRetryRemoteNativeTool(req, remoteResult.Text) {
		retryResult, retryErr := client.Chat(ctx, remote.ChatRequest{
			Model:       model,
			Prompt:      prompt,
			Messages:    remoteMessagesFromRequest(req),
			Images:      remoteImagesFromRequest(req),
			Stream:      false,
			Temperature: req.Temperature,
			Tools:       req.Tools,
			ToolChoice:  toolemulation.ToolChoice{Mode: "any"},
		}, nil)
		if retryErr == nil && len(retryResult.ToolCalls) > 0 {
			remoteResult = retryResult
			emitted = false
		}
	}

	result := &ChatResult{
		Text:          remoteResult.Text,
		Model:         valueOr(strings.TrimSpace(model), "lingma"),
		InputTokens:   remoteResult.InputTokens,
		OutputTokens:  remoteResult.OutputTokens,
		SessionID:     "",
		RequestID:     remoteResult.RequestID,
		FinishReason:  "stop",
		StopReason:    "stop",
		Endpoint:      remote.ResolveBaseURL(s.cfg.RemoteBaseURL),
		Transport:     "remote",
		ToolCalls:     remoteResult.ToolCalls,
		CredentialSrc: remoteResult.CredentialSrc,
	}
	return result, emitted, nil
}

func remoteMessagesFromRequest(req ChatRequest) []remote.Message {
	out := make([]remote.Message, 0, len(req.Messages)+1)
	if system := strings.TrimSpace(req.System); system != "" {
		out = append(out, remote.Message{Role: "system", Content: system})
	}
	for _, message := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "" {
			continue
		}
		content := strings.TrimSpace(message.Text)
		if content == "" && len(message.Images) == 0 && len(message.ToolCalls) == 0 {
			continue
		}
		out = append(out, remote.Message{
			Role:       role,
			Content:    content,
			Images:     remoteImagesFromChatMessage(message),
			ToolCallID: strings.TrimSpace(message.ToolCallID),
			ToolCalls:  message.ToolCalls,
		})
	}
	return out
}

func remoteImagesFromChatMessage(message ChatMessage) []remote.Image {
	if len(message.Images) == 0 {
		return nil
	}
	images := make([]remote.Image, 0, len(message.Images))
	for _, img := range message.Images {
		if strings.TrimSpace(img.Data) == "" && strings.TrimSpace(img.URL) == "" {
			continue
		}
		images = append(images, remote.Image{
			MediaType: strings.TrimSpace(img.MediaType),
			Data:      img.Data,
			URL:       strings.TrimSpace(img.URL),
		})
	}
	return images
}

func remoteImagesFromRequest(req ChatRequest) []remote.Image {
	var images []remote.Image
	for _, message := range req.Messages {
		for _, img := range message.Images {
			if strings.TrimSpace(img.Data) == "" && strings.TrimSpace(img.URL) == "" {
				continue
			}
			images = append(images, remote.Image{
				MediaType: strings.TrimSpace(img.MediaType),
				Data:      img.Data,
				URL:       strings.TrimSpace(img.URL),
			})
		}
	}
	return images
}

func imagePromptFallback(req ChatRequest, imageMessageIndex int) string {
	for i := imageMessageIndex - 1; i >= 0; i-- {
		message := req.Messages[i]
		if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			if text := strings.TrimSpace(message.Text); text != "" {
				return prompts.Render(req.PromptOverrides, prompts.IDLingmaImageQuestion, map[string]string{"text": text})
			}
		}
	}
	system := strings.TrimSpace(req.System)
	if system != "" && len([]rune(system)) <= 1000 {
		return prompts.Render(req.PromptOverrides, prompts.IDLingmaImageSystem, map[string]string{"system": system})
	}
	return prompts.Resolve(req.PromptOverrides, prompts.IDLingmaImageDescribe)
}

func shouldRetryRemoteNativeTool(req ChatRequest, text string) bool {
	if len(req.Tools) == 0 || req.ToolChoice.Mode == "none" {
		return false
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || len([]rune(trimmed)) > 180 {
		return false
	}
	lower := strings.ToLower(trimmed)
	cues := []string{
		"让我", "我来", "我将", "接下来", "继续", "查看", "检查", "搜索", "读取", "运行", "执行",
		"let me", "i'll", "i will", "next", "continue", "check", "inspect", "search", "read", "run",
	}
	hasCue := false
	for _, cue := range cues {
		if strings.Contains(lower, cue) {
			hasCue = true
			break
		}
	}
	if !hasCue {
		return false
	}
	return strings.HasSuffix(trimmed, ":") ||
		strings.HasSuffix(trimmed, "：") ||
		strings.Contains(trimmed, "：\n") ||
		strings.Contains(lower, "use ") ||
		strings.Contains(lower, "call ") ||
		strings.Contains(trimmed, "工具")
}

func (s *Service) remoteAttemptModels(ctx context.Context, primary string) []string {
	primary = normalizeModelForBackend(primary)
	models := []string{primary}
	if !s.cfg.RemoteFallbackEnabled {
		return models
	}

	availableCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	remoteModels, err := s.remoteClientLocked().ListModels(availableCtx)
	cancel()
	if err != nil {
		return models
	}

	available := make(map[string]bool, len(remoteModels))
	for _, model := range remoteModels {
		key := normalizeModelForBackend(model.Key)
		if key != "" {
			available[key] = true
		}
	}

	fallbackModels := s.cfg.RemoteFallbackModels
	if len(fallbackModels) == 0 {
		fallbackModels = DefaultRemoteFallbackModels()
	}
	ordered := make([]string, 0, len(fallbackModels))
	seen := map[string]bool{primary: true}
	primaryIndex := -1
	for _, candidate := range fallbackModels {
		model := normalizeModelForBackend(candidate)
		if model == "" {
			continue
		}
		if model == primary && primaryIndex == -1 {
			primaryIndex = len(ordered)
		}
		ordered = append(ordered, model)
	}

	start := 0
	if primaryIndex >= 0 {
		start = primaryIndex + 1
	}
	for _, model := range ordered[start:] {
		if seen[model] || !available[model] {
			continue
		}
		seen[model] = true
		models = append(models, model)
	}
	return models
}

func isRemoteFallbackError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "client.timeout") ||
		strings.Contains(msg, "timeout awaiting response") ||
		strings.Contains(msg, "remote chat status 5") ||
		strings.Contains(msg, "remote chat status 429") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "unexpected eof")
}

func (s *Service) remoteClientLocked() *remote.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remoteClient == nil {
		s.remoteClient = remote.New(remote.Config{
			BaseURL:            s.cfg.RemoteBaseURL,
			AuthFile:           s.cfg.RemoteAuthFile,
			CosyVersion:        s.cfg.RemoteVersion,
			Service:            s.cfg.RemoteService,
			FetchKeys:          s.cfg.RemoteFetchKeys,
			ChatTask:           s.cfg.RemoteChatTask,
			CredentialProvider: s.cfg.CredentialProvider,
			Timeout:            s.cfg.Timeout,
		})
	}
	return s.remoteClient
}

func buildLingmaPrompt(req ChatRequest, emulateTools bool) (string, error) {
	messages := filteredMessages(req.Messages, req.PromptOverrides)
	var lastUser string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUser = messages[i].Text
			break
		}
	}
	if strings.TrimSpace(lastUser) == "" {
		if idx := latestImageMessageIndex(req.Messages); idx >= 0 {
			lastUser = imagePromptFallback(req, idx)
			messages = append(messages, ChatMessage{Role: "user", Text: lastUser})
		} else {
			return "", errors.New("no user message found in request")
		}
	}

	system := strings.TrimSpace(req.System)
	if emulateTools && len(req.Tools) > 0 && req.ToolChoice.Mode != "none" {
		system = toolemulation.InjectToolingWithOverrides(system, req.Tools, req.ToolChoice, req.ParallelToolCalls, req.PromptOverrides)
	}

	if system == "" && len(messages) == 1 {
		return lastUser, nil
	}

	if emulateTools && len(req.Tools) > 0 {
		parts := make([]string, 0, len(messages)+3)
		for _, message := range messages {
			role := "User"
			if message.Role == "assistant" {
				role = "Assistant"
			}
			parts = append(parts, fmt.Sprintf("%s: %s", role, message.Text))
		}
		if system != "" {
			// Append tool prompt right before the final "Assistant:" so it
			// is the last thing the model sees before generating a reply.
			parts = append(parts, system)
		}
		parts = append(parts, "Assistant:")
		return strings.Join(parts, "\n\n"), nil
	}

	parts := make([]string, 0, len(messages)+4)
	systemBlock := ""
	if system != "" {
		systemBlock = strings.TrimSpace("System instructions:\n\n" + system)
	}
	for _, message := range messages {
		role := "User"
		if message.Role == "assistant" {
			role = "Assistant"
		}
		parts = append(parts, fmt.Sprintf("%s: %s", role, message.Text))
	}
	return prompts.Render(req.PromptOverrides, prompts.IDLingmaTranscript, map[string]string{
		"system_block": systemBlock,
		"conversation": strings.Join(parts, "\n\n"),
	}), nil
}

func latestImageMessageIndex(messages []ChatMessage) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") {
			continue
		}
		if len(remoteImagesFromChatMessage(messages[i])) > 0 {
			return i
		}
	}
	return -1
}

func filteredMessages(messages []ChatMessage, promptOverrides map[string]string) []ChatMessage {
	out := make([]ChatMessage, 0, len(messages))
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		if role == "tool" {
			text = toolemulation.ActionOutputPromptWithOverrides(message.ToolCallID, text, promptOverrides)
			role = "user"
		}
		if role != "user" && role != "assistant" {
			continue
		}
		out = append(out, ChatMessage{Role: role, Text: text})
	}
	return out
}

func estimateTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 1
	}
	return max(1, (len([]rune(text))+2)/3)
}

func valueOr(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func normalizeModelForBackend(model string) string {
	model = strings.TrimSpace(model)
	switch strings.ToLower(model) {
	case "":
		return ""
	case "kimi-k2.6":
		return "kimi-k2.6"
	case "minimax-m2.7":
		return "minimax-m2.7"
	case "qwen3-coder":
		return "qwen3-coder"
	case "qwen3-max":
		return "qwen3-max"
	case "qwen3-thinking":
		return "qwen3-thinking"
	case "qwen3.6-plus":
		return "qwen3.6-plus"
	case "auto":
		return "org_auto"
	default:
		return model
	}
}
