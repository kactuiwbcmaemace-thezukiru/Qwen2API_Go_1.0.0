package openai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"qwen2api/internal/logging"
	"qwen2api/internal/storage"
	"qwen2api/internal/toolcall"
)

const conversationSessionTTL = 24 * time.Hour
const maxCachedConversationMessages = 100

type ConversationSessionService struct {
	store  storage.ConversationStore
	logger *logging.Logger
}

type preparedChatRequest struct {
	RequestedModel       string
	Model                string
	ChatType             string
	ThinkingMode         thinkingMode
	ExpandedMessages     []map[string]any
	FullUpstreamMessages []map[string]any
	LastUpstreamMessages []map[string]any
	ContextHash          string
	ToolNames            []string
	ToolSchemas          []toolcall.ToolSchema
}

func NewConversationSessionService(store storage.ConversationStore, logger *logging.Logger) *ConversationSessionService {
	return &ConversationSessionService{store: store, logger: logger}
}

func (s *ConversationSessionService) Get(contextHash string) (storage.ConversationSession, bool) {
	if s == nil || s.store == nil || strings.TrimSpace(contextHash) == "" {
		return storage.ConversationSession{}, false
	}
	s.cleanupExpired()
	session, ok, err := s.store.GetConversationSession(contextHash)
	if err != nil {
		if s.logger != nil {
			s.logger.WarnModule("OPENAI", "load conversation session failed hash=%s err=%v", contextHash, err)
		}
		return storage.ConversationSession{}, false
	}
	if !ok {
		return storage.ConversationSession{}, false
	}
	if session.UpdatedAt > 0 && time.Since(time.UnixMilli(session.UpdatedAt)) > conversationSessionTTL {
		_ = s.store.DeleteConversationSession(contextHash)
		return storage.ConversationSession{}, false
	}
	return session, true
}

func (s *ConversationSessionService) Save(contextHash, accountEmail, chatID, model, chatType string) {
	s.saveSession(contextHash, accountEmail, chatID, model, chatType, nil, nil, nil, false)
}

// CacheExchange stores the latest OpenAI-style request/response snapshot for the
// admin session viewer. The proxy is usually called with full conversation
// history, so replacing the cached message slice with the newest snapshot avoids
// duplicates while keeping the viewer useful for debugging.
func (s *ConversationSessionService) CacheExchange(contextHash, accountEmail, chatID, model, chatType string, requestMessages []map[string]any, assistantMessage map[string]any, toolNames []string) {
	s.CacheExchangeWithAliases([]string{contextHash}, accountEmail, chatID, model, chatType, requestMessages, assistantMessage, toolNames)
}

// CacheExchangeWithAliases writes the same chat snapshot under multiple context
// hashes. OpenAI-compatible coding clients resend the full visible transcript on
// every request, so the lookup key for the next request is the transcript after
// the assistant response from the previous request. Keeping both the request
// prefix key and the continuation key mapped to the same upstream Qwen chat lets
// tool loops continue in one Qwen conversation instead of creating a fresh chat
// after every tool call.
func (s *ConversationSessionService) CacheExchangeWithAliases(contextHashes []string, accountEmail, chatID, model, chatType string, requestMessages []map[string]any, assistantMessage map[string]any, toolNames []string) {
	hashes := normalizeContextHashAliases(contextHashes, chatID)
	if len(hashes) == 0 {
		return
	}
	for _, hash := range hashes {
		s.saveSession(hash, accountEmail, chatID, model, chatType, requestMessages, assistantMessage, toolNames, true)
	}
}

func normalizeContextHashAliases(contextHashes []string, chatID string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(contextHashes)+1)
	for _, hash := range contextHashes {
		hash = strings.TrimSpace(hash)
		if hash == "" {
			continue
		}
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}
		result = append(result, hash)
	}
	if len(result) == 0 {
		if fallback := cacheContextHash("", chatID); fallback != "" {
			result = append(result, fallback)
		}
	}
	return result
}

func (s *ConversationSessionService) saveSession(contextHash, accountEmail, chatID, model, chatType string, requestMessages []map[string]any, assistantMessage map[string]any, toolNames []string, updateMessages bool) {
	if s == nil || s.store == nil || strings.TrimSpace(accountEmail) == "" || strings.TrimSpace(chatID) == "" {
		return
	}
	contextHash = cacheContextHash(contextHash, chatID)
	if contextHash == "" {
		return
	}
	now := time.Now().UnixMilli()
	session := storage.ConversationSession{
		ContextHash:  contextHash,
		AccountEmail: accountEmail,
		ChatID:       chatID,
		Model:        model,
		ChatType:     chatType,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if existing, ok, err := s.store.GetConversationSession(contextHash); err == nil && ok {
		session.CreatedAt = existing.CreatedAt
		if session.CreatedAt <= 0 {
			session.CreatedAt = now
		}
		if !updateMessages {
			session.LastMessage = existing.LastMessage
			session.MessageCount = existing.MessageCount
			session.HasTools = existing.HasTools
			session.ToolsUsed = append([]string(nil), existing.ToolsUsed...)
			session.Messages = append([]storage.CachedChatMessage(nil), existing.Messages...)
		}
	}
	if updateMessages {
		session.Messages = buildCachedMessages(requestMessages, assistantMessage)
		session.MessageCount = len(session.Messages)
		session.LastMessage = lastCachedMessagePreview(session.Messages)
		session.ToolsUsed = cachedToolsUsed(session.Messages, toolNames)
		session.HasTools = len(session.ToolsUsed) > 0
	}
	if err := s.store.SaveConversationSession(session); err != nil && s.logger != nil {
		s.logger.WarnModule("OPENAI", "save conversation session failed hash=%s account=%s chat_id=%s err=%v", contextHash, accountEmail, chatID, err)
	}
}

func cacheContextHash(contextHash, chatID string) string {
	contextHash = strings.TrimSpace(contextHash)
	if contextHash != "" {
		return contextHash
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("chat:" + chatID))
	return "chat:" + hex.EncodeToString(sum[:])
}

func buildCachedMessages(requestMessages []map[string]any, assistantMessage map[string]any) []storage.CachedChatMessage {
	now := time.Now().UnixMilli()
	messages := make([]storage.CachedChatMessage, 0, len(requestMessages)+1)
	for i, message := range requestMessages {
		cached := cachedMessageFromMap(message, now+int64(i))
		if strings.TrimSpace(cached.Role) == "" {
			cached.Role = "message"
		}
		cached.ID = fmt.Sprintf("msg-%d-%d", now, len(messages)+1)
		messages = append(messages, cached)
	}
	if assistantMessage != nil {
		cached := cachedMessageFromMap(assistantMessage, now+int64(len(messages)))
		if strings.TrimSpace(cached.Role) == "" {
			cached.Role = "assistant"
		}
		cached.ID = fmt.Sprintf("msg-%d-%d", now, len(messages)+1)
		messages = append(messages, cached)
	}
	if len(messages) > maxCachedConversationMessages {
		messages = messages[len(messages)-maxCachedConversationMessages:]
	}
	return messages
}

func cachedMessageFromMap(message map[string]any, createdAt int64) storage.CachedChatMessage {
	if message == nil {
		return storage.CachedChatMessage{CreatedAt: createdAt}
	}
	cached := storage.CachedChatMessage{
		Role:             strings.TrimSpace(fmt.Sprint(message["role"])),
		Content:          cachedContentText(message["content"]),
		ReasoningContent: cachedContentText(message["reasoning_content"]),
		CreatedAt:        createdAt,
	}
	if calls := normalizeToolCalls(message["tool_calls"]); len(calls) > 0 {
		cached.ToolCalls = calls
	}
	metadata := map[string]any{}
	for _, key := range []string{"name", "tool_call_id", "chat_type"} {
		if value, ok := message[key]; ok && strings.TrimSpace(fmt.Sprint(value)) != "" {
			metadata[key] = value
		}
	}
	if len(metadata) > 0 {
		cached.Metadata = metadata
	}
	return cached
}

func cachedContentText(content any) string {
	switch v := content.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if obj, ok := item.(map[string]any); ok {
				typeName := strings.TrimSpace(fmt.Sprint(obj["type"]))
				switch typeName {
				case "text", "input_text":
					parts = append(parts, fmt.Sprint(firstNonEmpty(obj["text"], obj["content"])))
				case "image_url", "input_image":
					parts = append(parts, "[image]")
				case "file", "input_file":
					parts = append(parts, "[file]")
				default:
					if text := strings.TrimSpace(fmt.Sprint(firstNonEmpty(obj["text"], obj["content"], obj["url"]))); text != "" {
						parts = append(parts, text)
					} else if typeName != "" {
						parts = append(parts, "["+typeName+"]")
					}
				}
				continue
			}
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		if raw, err := json.Marshal(v); err == nil {
			return string(raw)
		}
		return fmt.Sprint(v)
	}
}

func firstNonEmpty(values ...any) any {
	for _, value := range values {
		if strings.TrimSpace(fmt.Sprint(value)) != "" && fmt.Sprint(value) != "<nil>" {
			return value
		}
	}
	return ""
}

func normalizeToolCalls(raw any) []any {
	switch v := raw.(type) {
	case nil:
		return nil
	case []any:
		return append([]any(nil), v...)
	case []map[string]any:
		items := make([]any, 0, len(v))
		for _, item := range v {
			items = append(items, item)
		}
		return items
	default:
		return []any{raw}
	}
}

func lastCachedMessagePreview(messages []storage.CachedChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		text := strings.TrimSpace(messages[i].Content)
		if text == "" && len(messages[i].ToolCalls) > 0 {
			text = "[tool calls]"
		}
		if text != "" {
			return truncateForAdmin(text, 240)
		}
	}
	return ""
}

func cachedToolsUsed(messages []storage.CachedChatMessage, toolNames []string) []string {
	seen := map[string]struct{}{}
	for _, name := range toolNames {
		name = strings.TrimSpace(name)
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			for _, name := range namesFromToolCall(call) {
				if name != "" {
					seen[name] = struct{}{}
				}
			}
		}
		if message.Metadata != nil {
			if rawToolCallID, ok := message.Metadata["tool_call_id"]; ok {
				if toolCallID := strings.TrimSpace(fmt.Sprint(rawToolCallID)); toolCallID != "" && toolCallID != "<nil>" {
					seen["tool_result"] = struct{}{}
				}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func namesFromToolCall(call any) []string {
	obj, ok := call.(map[string]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, 2)
	if fn, ok := obj["function"].(map[string]any); ok {
		if name := strings.TrimSpace(fmt.Sprint(fn["name"])); name != "" {
			result = append(result, name)
		}
	}
	if name := strings.TrimSpace(fmt.Sprint(obj["name"])); name != "" {
		result = append(result, name)
	}
	return result
}

func truncateForAdmin(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

func (s *ConversationSessionService) Delete(contextHash string) {
	if s == nil || s.store == nil || strings.TrimSpace(contextHash) == "" {
		return
	}
	contextHash = strings.TrimSpace(contextHash)
	deleted := map[string]struct{}{contextHash: {}}

	if session, ok, err := s.store.GetConversationSession(contextHash); err == nil && ok {
		if strings.TrimSpace(session.ChatID) != "" {
			if sessions, listErr := s.store.ListConversationSessions(); listErr == nil {
				for _, candidate := range sessions {
					if sameConversationSession(session, candidate) {
						deleted[candidate.ContextHash] = struct{}{}
					}
				}
			}
		}
	}

	for hash := range deleted {
		if err := s.store.DeleteConversationSession(hash); err != nil && s.logger != nil {
			s.logger.WarnModule("OPENAI", "delete conversation session failed hash=%s err=%v", hash, err)
		}
	}
}

func (s *ConversationSessionService) CleanupExpired() error {
	if s == nil || s.store == nil {
		return errors.New("会话服务未初始化")
	}
	return s.cleanupExpired()
}

func (s *ConversationSessionService) cleanupExpired() error {
	if s == nil || s.store == nil {
		return nil
	}
	sessions, err := s.store.ListConversationSessions()
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-conversationSessionTTL).UnixMilli()
	for _, session := range sessions {
		if session.UpdatedAt > 0 && session.UpdatedAt < cutoff {
			_ = s.store.DeleteConversationSession(session.ContextHash)
		}
	}
	return nil
}

func computeContextHash(model, chatType string, toolNames []string, expandedMessages []map[string]any) string {
	if len(expandedMessages) <= 1 {
		return ""
	}
	return computeContextHashForPrefix(model, chatType, toolNames, expandedMessages[:len(expandedMessages)-1])
}

func computeContextHashForPrefix(model, chatType string, toolNames []string, prefixMessages []map[string]any) string {
	if len(prefixMessages) == 0 {
		return ""
	}
	payload := map[string]any{
		"model":      strings.TrimSpace(model),
		"chat_type":  strings.TrimSpace(chatType),
		"tool_names": append([]string(nil), toolNames...),
		"messages":   cloneMessageList(prefixMessages),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ListAll returns all conversation sessions from the store.
func (s *ConversationSessionService) ListAll() ([]storage.ConversationSession, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("会话服务未初始化")
	}
	if err := s.cleanupExpired(); err != nil {
		return nil, err
	}
	sessions, err := s.store.ListConversationSessions()
	if err != nil {
		return nil, err
	}
	return dedupeConversationSessionAliases(sessions), nil
}

func dedupeConversationSessionAliases(sessions []storage.ConversationSession) []storage.ConversationSession {
	byChat := map[string]storage.ConversationSession{}
	result := make([]storage.ConversationSession, 0, len(sessions))
	for _, session := range sessions {
		key := conversationAliasKey(session)
		if key == "" {
			result = append(result, session)
			continue
		}
		existing, ok := byChat[key]
		if !ok || sessionIsMoreUseful(session, existing) {
			byChat[key] = session
		}
	}
	for _, session := range byChat {
		result = append(result, session)
	}
	return result
}

func conversationAliasKey(session storage.ConversationSession) string {
	accountEmail := strings.TrimSpace(session.AccountEmail)
	chatID := strings.TrimSpace(session.ChatID)
	if accountEmail == "" || chatID == "" {
		return ""
	}
	return strings.ToLower(accountEmail) + "\x00" + chatID
}

func sameConversationSession(a, b storage.ConversationSession) bool {
	return conversationAliasKey(a) != "" && conversationAliasKey(a) == conversationAliasKey(b)
}

func sessionIsMoreUseful(candidate, existing storage.ConversationSession) bool {
	if candidate.UpdatedAt != existing.UpdatedAt {
		return candidate.UpdatedAt > existing.UpdatedAt
	}
	if candidate.MessageCount != existing.MessageCount {
		return candidate.MessageCount > existing.MessageCount
	}
	return len(candidate.Messages) > len(existing.Messages)
}
