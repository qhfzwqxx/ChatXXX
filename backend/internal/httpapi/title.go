package httpapi

import (
	"bytes"
	"chatxxx/backend/internal/db"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const titleFallbackMaxRunes = 24
const titleLLMMaxRunes = 18

func (s *Server) titleProvider() (*runtimeProvider, error) {
	id := int64(s.settingInt("title_provider_id", 0))
	if id <= 0 {
		id = int64(s.settingInt("memory_provider_id", 0))
	}
	if id <= 0 {
		return nil, fmt.Errorf("title provider is not configured")
	}
	return s.getProviderByID(id)
}

func (s *Server) maybeGenerateConversationTitle(ctx context.Context, conversationID, userID int64, firstUserMessage string) string {
	fallback := titleFromContent(firstUserMessage)
	if strings.TrimSpace(firstUserMessage) == "" {
		return fallback
	}
	conversation, err := s.getConversationForUser(conversationID, userID)
	if err != nil || conversation == nil || strings.TrimSpace(conversation.Title) != "新对话" {
		return strings.TrimSpace(conversationTitleOrFallback(conversation, fallback))
	}
	provider, err := s.titleProvider()
	if err != nil || provider == nil || provider.Key == "" || provider.Base == "" || provider.Model == "" {
		_ = s.updateConversationTitleIfNew(conversationID, userID, fallback)
		return fallback
	}
	title, err := s.generateTitleWithLLM(ctx, *provider, firstUserMessage)
	if err != nil || strings.TrimSpace(title) == "" {
		title = fallback
	}
	title = sanitizeConversationTitle(title)
	if title == "" {
		title = fallback
	}
	if err := s.updateConversationTitleIfNew(conversationID, userID, title); err != nil {
		return fallback
	}
	return title
}

func conversationTitleOrFallback(conversation *Conversation, fallback string) string {
	if conversation != nil && strings.TrimSpace(conversation.Title) != "" {
		return conversation.Title
	}
	return fallback
}

func (s *Server) updateConversationTitleIfNew(conversationID, userID int64, title string) error {
	_, err := s.store.DB.Exec(`
		UPDATE conversations
		SET title=?, updated_at=?
		WHERE id=? AND user_id=? AND title='新对话'
	`, title, db.Now(), conversationID, userID)
	return err
}

func (s *Server) firstUserMessageContent(conversationID, userID int64, fallback string) string {
	var content string
	err := s.store.DB.QueryRow(`
		SELECT content
		FROM messages
		WHERE conversation_id=? AND user_id=? AND role='user' AND deleted_at IS NULL AND is_active_version=1
		ORDER BY sort_order ASC, id ASC
		LIMIT 1
	`, conversationID, userID).Scan(&content)
	if err != nil || strings.TrimSpace(content) == "" {
		return strings.TrimSpace(fallback)
	}
	return strings.TrimSpace(content)
}

func (s *Server) generateTitleWithLLM(ctx context.Context, provider runtimeProvider, firstUserMessage string) (string, error) {
	prompt := "请根据下面这段用户在一个新对话中的第一条消息，为该对话生成一个简短中文标题。\n\n要求：\n- 只输出标题本身，不要解释，不要 Markdown，不要引号。\n- 标题最多 18 个中文字符或 8 个英文单词。\n- 不要使用句号、问号、感叹号结尾。\n\n用户第一条消息：\n" + strings.TrimSpace(firstUserMessage)
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	switch strings.TrimSpace(provider.RequestMode) {
	case "responses":
		body := map[string]interface{}{
			"model":  provider.Model,
			"stream": false,
			"input": []interface{}{
				responseInputMessage{
					Type: "message",
					Role: "user",
					Content: []responseInputContent{
						{Type: "input_text", Text: prompt},
					},
				},
			},
			"instructions": "你是 ChatXXX 的会话标题生成器。你只输出短标题。",
		}
		raw, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(provider.Base, "/")+"/responses", bytes.NewReader(raw))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+provider.Key)
		resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("title provider returned %s: %s", resp.Status, strings.TrimSpace(string(bodyBytes)))
		}
		return extractResponsesText(bodyBytes)
	default:
		requestBody := map[string]interface{}{
			"model":       provider.Model,
			"stream":      false,
			"temperature": 0.2,
			"messages": []map[string]string{
				{"role": "system", "content": "你是 ChatXXX 的会话标题生成器。你只输出短标题。"},
				{"role": "user", "content": prompt},
			},
		}
		raw, _ := json.Marshal(requestBody)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(provider.Base, "/")+"/chat/completions", bytes.NewReader(raw))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+provider.Key)
		resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("title provider returned %s: %s", resp.Status, strings.TrimSpace(string(bodyBytes)))
		}
		return extractSearchingContent(bodyBytes)
	}
}

func sanitizeConversationTitle(raw string) string {
	title := strings.TrimSpace(raw)
	title = strings.Trim(title, "`*_# \t\r\n\"'“”‘’")
	title = strings.TrimSpace(strings.ReplaceAll(title, "\n", " "))
	for strings.Contains(title, "  ") {
		title = strings.ReplaceAll(title, "  ", " ")
	}
	title = strings.TrimRight(title, "。.!！？?；;，,、")
	runes := []rune(title)
	if len(runes) > titleLLMMaxRunes {
		title = string(runes[:titleLLMMaxRunes])
	}
	return strings.TrimSpace(title)
}
