package httpapi

import (
	"bufio"
	"bytes"
	"chatxxx/backend/internal/db"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	streamDeltaChunkRunes = 4
	streamDeltaDelay      = 55 * time.Millisecond
)

type streamRequest struct {
	ConversationID int64         `json:"conversation_id"`
	Content        string        `json:"content"`
	Mode           string        `json:"mode"`
	MessageID      int64         `json:"message_id"`
	ProviderID     int64         `json:"provider_id"`
	References     []interface{} `json:"references"`
	Attachments    []attachment  `json:"attachments"`
}

type attachment struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Size    int64  `json:"size"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

type runtimeProvider struct {
	ID             int64
	Name           string
	Base           string
	Key            string
	Model          string
	RequestMode    string
	ResponseFormat string
	Active         bool
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	var req streamRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "请求体必须是 JSON")
		return
	}
	if req.ConversationID <= 0 {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "conversation_id 必填")
		return
	}
	if _, err := s.getConversationForUser(req.ConversationID, user.ID); err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "会话不存在")
		return
	}

	startSSE(w)
	runID := uuid.NewString()
	content := strings.TrimSpace(req.Content)
	mode := strings.TrimSpace(req.Mode)
	attachmentsJSON := attachmentsMetadata(req.Attachments)
	if content == "" && len(req.Attachments) == 0 && mode != "regenerate" {
		sendSSE(w, "error", map[string]interface{}{"code": "EMPTY_MESSAGE", "message": "消息不能为空"})
		sendSSE(w, "done", map[string]interface{}{"ok": false})
		return
	}

	userMessage, err := s.prepareUserMessageForStream(req.ConversationID, user.ID, content, mode, req.MessageID, referencesMetadata(req.References), attachmentsJSON)
	if err != nil {
		sendSSE(w, "error", map[string]interface{}{"code": "SERVER_ERROR", "message": err.Error()})
		sendSSE(w, "done", map[string]interface{}{"ok": false})
		return
	}
	assistant, err := s.insertMessage(req.ConversationID, user.ID, "assistant", "", "streaming", "{}")
	if err != nil {
		sendSSE(w, "error", map[string]interface{}{"code": "SERVER_ERROR", "message": "创建回复失败"})
		sendSSE(w, "done", map[string]interface{}{"ok": false})
		return
	}
	now := db.Now()
	_, _ = s.store.DB.Exec(`INSERT INTO generation_runs (run_id, conversation_id, assistant_message_id, user_id, status, metadata, created_at, updated_at) VALUES (?, ?, ?, ?, 'running', '{}', ?, ?)`, runID, req.ConversationID, assistant.ID, user.ID, now, now)

	sendSSE(w, "message_start", map[string]interface{}{"run_id": runID, "user_message": userMessage, "assistant_message": assistant})

	provider, _ := s.getRuntimeProvider(req.ProviderID)
	promptContent := strings.TrimSpace(userMessage.Content)
	var fullText string
	if provider == nil || provider.Key == "" || provider.Base == "" || provider.Model == "" {
		fullText = "这是 ChatXXX 的本地流式占位回复。你还没有配置可用的 OpenAI-compatible provider；请使用管理员账号到设置里添加模型。"
		streamLocalText(w, fullText)
	} else {
		conversation, _ := s.getConversationForUser(req.ConversationID, user.ID)
		text, err := s.streamOpenAICompatible(w, *provider, conversation, req.ConversationID, user.ID, promptContent)
		if err != nil {
			fullText = "模型调用失败：" + err.Error()
			sendSSE(w, "error", map[string]interface{}{"code": "LLM_REQUEST_FAILED", "message": fullText})
		} else {
			fullText = text
		}
	}

	if strings.TrimSpace(fullText) == "" {
		fullText = "没有收到模型输出。"
	}
	s.updateMessageContent(assistant.ID, fullText, "completed")
	_, _ = s.store.DB.Exec(`UPDATE generation_runs SET status='completed', updated_at=? WHERE run_id=?`, db.Now(), runID)
	if promptContent != "" {
		_, _ = s.store.DB.Exec(`UPDATE conversations SET title = CASE WHEN title='新对话' THEN ? ELSE title END, updated_at=? WHERE id=? AND user_id=?`, titleFromContent(promptContent), db.Now(), req.ConversationID, user.ID)
		sendSSE(w, "conversation_title", map[string]interface{}{"conversation_id": req.ConversationID, "title": titleFromContent(promptContent)})
	}
	assistant.Content = fullText
	assistant.Status = "completed"
	sendSSE(w, "message_end", map[string]interface{}{"message": assistant})
	sendSSE(w, "done", map[string]interface{}{"ok": true})
}

func (s *Server) prepareUserMessageForStream(conversationID, userID int64, content, mode string, messageID int64, metadata, attachments string) (Message, error) {
	switch mode {
	case "regenerate":
		assistant, err := s.getMessageForUser(messageID, userID)
		if err != nil || assistant.ConversationID != conversationID || assistant.Role != "assistant" {
			return Message{}, fmt.Errorf("无法重新生成这条回复")
		}
		userMessage, err := s.previousUserMessage(conversationID, userID, assistant.SortOrder)
		if err != nil {
			return Message{}, fmt.Errorf("找不到对应的用户消息")
		}
		if err := s.deleteMessagesFromOrder(conversationID, userID, assistant.SortOrder); err != nil {
			return Message{}, fmt.Errorf("清理旧回复失败")
		}
		return userMessage, nil
	case "edit":
		userMessage, err := s.getMessageForUser(messageID, userID)
		if err != nil || userMessage.ConversationID != conversationID || userMessage.Role != "user" {
			return Message{}, fmt.Errorf("无法编辑这条消息")
		}
		if strings.TrimSpace(content) == "" && strings.TrimSpace(attachments) == "[]" {
			return Message{}, fmt.Errorf("消息不能为空")
		}
		if err := s.deleteMessagesAfterOrder(conversationID, userID, userMessage.SortOrder); err != nil {
			return Message{}, fmt.Errorf("清理后续消息失败")
		}
		if err := s.updateUserMessageContentWithAttachments(userMessage.ID, userID, content, metadata, attachments); err != nil {
			return Message{}, fmt.Errorf("更新用户消息失败")
		}
		userMessage.Content = content
		userMessage.Metadata = metadata
		userMessage.Attachments = attachments
		userMessage.UpdatedAt = db.Now()
		return userMessage, nil
	default:
		userMessage, err := s.insertMessageWithAttachments(conversationID, userID, "user", content, "completed", metadata, attachments)
		if err != nil {
			return Message{}, fmt.Errorf("保存用户消息失败")
		}
		return userMessage, nil
	}
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunID string `json:"run_id"`
	}
	_ = readJSON(r, &req)
	if strings.TrimSpace(req.RunID) != "" {
		_, _ = s.store.DB.Exec(`UPDATE generation_runs SET status='stopping', updated_at=? WHERE run_id=?`, db.Now(), req.RunID)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func startSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

func sendSSE(w http.ResponseWriter, event string, data interface{}) {
	payload, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func streamLocalText(w http.ResponseWriter, text string) {
	streamDeltaText(w, text)
}

func streamDeltaText(w http.ResponseWriter, text string) {
	runes := []rune(text)
	for i := 0; i < len(runes); i += streamDeltaChunkRunes {
		end := i + streamDeltaChunkRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunk := string(runes[i:end])
		sendSSE(w, "delta", map[string]interface{}{"text": chunk})
		time.Sleep(streamDeltaDelay)
	}
}

func referencesMetadata(refs []interface{}) string {
	if len(refs) == 0 {
		return "{}"
	}
	raw, err := json.Marshal(map[string]interface{}{"references": refs})
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func attachmentsMetadata(items []attachment) string {
	if len(items) == 0 {
		return "[]"
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func messagePromptContent(msg Message) string {
	content := strings.TrimSpace(msg.Content)
	attachmentText := attachmentPromptText(msg.Attachments)
	if attachmentText == "" {
		return content
	}
	if content == "" {
		return attachmentText
	}
	return content + "\n\n" + attachmentText
}

func attachmentPromptText(raw string) string {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "[]" {
		return ""
	}
	var items []attachment
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return ""
	}
	var b strings.Builder
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = "未命名文件"
		}
		if b.Len() == 0 {
			b.WriteString("附件内容：")
		}
		b.WriteString("\n\n")
		b.WriteString("文件：")
		b.WriteString(name)
		b.WriteString("\n")
		if strings.TrimSpace(item.Content) != "" {
			b.WriteString(strings.TrimSpace(item.Content))
			continue
		}
		if strings.TrimSpace(item.Error) != "" {
			b.WriteString("文件内容未读取：")
			b.WriteString(strings.TrimSpace(item.Error))
		} else {
			b.WriteString("文件内容未读取。")
		}
	}
	return b.String()
}

func (s *Server) getRuntimeProvider(id int64) (*runtimeProvider, error) {
	query := `SELECT id, name, base_url, api_key, model, request_mode, response_format, is_active FROM providers WHERE is_active=1`
	args := []interface{}{}
	if id > 0 {
		query += ` AND id=?`
		args = append(args, id)
	} else {
		query += ` ORDER BY is_default DESC, id ASC LIMIT 1`
	}
	row := s.store.DB.QueryRow(query, args...)
	var p runtimeProvider
	var active int
	if err := row.Scan(&p.ID, &p.Name, &p.Base, &p.Key, &p.Model, &p.RequestMode, &p.ResponseFormat, &active); err != nil {
		return nil, err
	}
	p.Active = active == 1
	return &p, nil
}

func (s *Server) streamOpenAICompatible(w http.ResponseWriter, provider runtimeProvider, conversation *Conversation, conversationID, userID int64, latest string) (string, error) {
	switch strings.TrimSpace(provider.RequestMode) {
	case "", "chat_completions":
		return s.streamChatCompletions(w, provider, conversation, conversationID, userID, latest)
	case "responses":
		return s.streamResponses(w, provider, conversation, conversationID, userID, latest)
	default:
		return "", fmt.Errorf("不支持的请求模式：%s", provider.RequestMode)
	}
}

func (s *Server) streamChatCompletions(w http.ResponseWriter, provider runtimeProvider, conversation *Conversation, conversationID, userID int64, latest string) (string, error) {
	messages, _ := s.listMessages(conversationID, userID)
	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	bodyMessages := make([]chatMessage, 0, len(messages)+1)
	if conversation != nil && strings.TrimSpace(conversation.SystemPrompt) != "" {
		bodyMessages = append(bodyMessages, chatMessage{Role: "system", Content: strings.TrimSpace(conversation.SystemPrompt)})
	}
	for _, msg := range messages {
		if msg.Role == "user" || msg.Role == "assistant" || msg.Role == "system" {
			if content := messagePromptContent(msg); strings.TrimSpace(content) != "" {
				bodyMessages = append(bodyMessages, chatMessage{Role: msg.Role, Content: content})
			}
		}
	}
	if len(bodyMessages) == 0 && latest != "" {
		bodyMessages = append(bodyMessages, chatMessage{Role: "user", Content: latest})
	}
	requestBody := map[string]interface{}{
		"model":    provider.Model,
		"messages": bodyMessages,
		"stream":   true,
	}
	if rf, ok, err := parseJSONObject(provider.ResponseFormat); err != nil {
		return "", fmt.Errorf("response_format 配置无效：%w", err)
	} else if ok {
		requestBody["response_format"] = rf
	}
	raw, _ := json.Marshal(requestBody)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(provider.Base, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+provider.Key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return readChatCompletionsStream(w, resp.Body)
}

func (s *Server) streamResponses(w http.ResponseWriter, provider runtimeProvider, conversation *Conversation, conversationID, userID int64, latest string) (string, error) {
	messages, _ := s.listMessages(conversationID, userID)
	type responseContent struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type responseInputMessage struct {
		Role    string            `json:"role"`
		Content []responseContent  `json:"content"`
	}
	input := make([]responseInputMessage, 0, len(messages)+1)
	if conversation != nil && strings.TrimSpace(conversation.SystemPrompt) != "" {
		input = append(input, responseInputMessage{
			Role: "system",
			Content: []responseContent{
				{Type: "input_text", Text: strings.TrimSpace(conversation.SystemPrompt)},
			},
		})
	}
	for _, msg := range messages {
		if msg.Role == "user" || msg.Role == "assistant" || msg.Role == "system" {
			if content := messagePromptContent(msg); strings.TrimSpace(content) != "" {
				input = append(input, responseInputMessage{
					Role: msg.Role,
					Content: []responseContent{
						{Type: "input_text", Text: content},
					},
				})
			}
		}
	}
	if len(input) == 0 && latest != "" {
		input = append(input, responseInputMessage{
			Role: "user",
			Content: []responseContent{
				{Type: "input_text", Text: latest},
			},
		})
	}
	requestBody := map[string]interface{}{
		"model":  provider.Model,
		"input":  input,
		"stream": true,
	}
	if conversation != nil && strings.TrimSpace(conversation.SystemPrompt) != "" {
		requestBody["instructions"] = strings.TrimSpace(conversation.SystemPrompt)
	}
	if rf, ok, err := parseJSONObject(provider.ResponseFormat); err != nil {
		return "", fmt.Errorf("response_format 配置无效：%w", err)
	} else if ok {
		requestBody["text"] = map[string]interface{}{"format": rf}
	}
	raw, _ := json.Marshal(requestBody)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(provider.Base, "/")+"/responses", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+provider.Key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return readResponsesStream(w, resp.Body)
}

func readChatCompletionsStream(w http.ResponseWriter, body io.Reader) (string, error) {
	var full strings.Builder
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var event struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		for _, choice := range event.Choices {
			if choice.Delta.ReasoningContent != "" {
				sendSSE(w, "thinking", map[string]interface{}{"text": choice.Delta.ReasoningContent})
			}
			if choice.Delta.Content != "" {
				full.WriteString(choice.Delta.Content)
				streamDeltaText(w, choice.Delta.Content)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return full.String(), err
	}
	return full.String(), nil
}

func readResponsesStream(w http.ResponseWriter, body io.Reader) (string, error) {
	var full strings.Builder
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	eventName := ""
	var payload strings.Builder
	flush := func() error {
		name := strings.TrimSpace(eventName)
		data := strings.TrimSpace(payload.String())
		eventName = ""
		payload.Reset()
		if name == "" || data == "" {
			return nil
		}
		switch name {
		case "response.output_text.delta":
			var event struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				return nil
			}
			if event.Delta != "" {
				full.WriteString(event.Delta)
				sendSSE(w, "delta", map[string]interface{}{"text": event.Delta})
			}
		case "response.reasoning_summary_text.delta":
			var event struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				return nil
			}
			if event.Delta != "" {
				sendSSE(w, "thinking", map[string]interface{}{"text": event.Delta})
			}
		case "response.completed":
			if full.Len() == 0 {
				if text := extractResponseCompletedText(data); text != "" {
					full.WriteString(text)
					sendSSE(w, "delta", map[string]interface{}{"text": text})
				}
			}
		case "error":
			return parseResponseError(data)
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return full.String(), err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			if err := flush(); err != nil {
				return full.String(), err
			}
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if payload.Len() > 0 {
				payload.WriteByte('\n')
			}
			payload.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := flush(); err != nil {
		return full.String(), err
	}
	if err := scanner.Err(); err != nil {
		return full.String(), err
	}
	return full.String(), nil
}

func parseJSONObject(raw string) (map[string]interface{}, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false, nil
	}
	var value map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func extractResponseCompletedText(data string) string {
	var event struct {
		Response struct {
			Output []struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return ""
	}
	var full strings.Builder
	for _, item := range event.Response.Output {
		if item.Type != "message" && item.Role != "assistant" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" && content.Text != "" {
				full.WriteString(content.Text)
			}
		}
	}
	return full.String()
}

func parseResponseError(data string) error {
	var event struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return fmt.Errorf("provider stream error")
	}
	if strings.TrimSpace(event.Error.Message) != "" {
		return fmt.Errorf("%s", strings.TrimSpace(event.Error.Message))
	}
	if strings.TrimSpace(event.Message) != "" {
		return fmt.Errorf("%s", strings.TrimSpace(event.Message))
	}
	return fmt.Errorf("provider stream error")
}

var _ = sql.ErrNoRows
