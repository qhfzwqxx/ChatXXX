package httpapi

import (
	"bufio"
	"bytes"
	"chatxxx/backend/internal/db"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	streamDeltaChunkRunes  = 4
	streamDeltaDelay       = 55 * time.Millisecond
	streamKeepAliveEvery   = 15 * time.Second
	maxResponsesToolRounds = 8

	defaultWebReaderMaxWords = 12000
	maxWebReaderMaxWords     = 5000000
	maxWebReaderOutputRunes  = 60000
	maxSearchingOutputRunes  = 60000
)

const runtimeSystemToolPrinciple = "Tool use principle: use available tools whenever they materially help the user's request; call tools serially, one at a time; choose a reasonable order so each tool result informs the next step."

const responsesToolPacingInstruction = "Tool pacing rule: if you need more than one tool in the same assistant reply, do not call tools back-to-back from the user's perspective. After any tool result, write a brief user-visible sentence before calling another tool."

const responsesToolPacingRetryInstruction = "Tool pacing rule: you just received a tool result, so you must write a brief user-visible sentence before calling another tool. Do not call a tool again until you have emitted that visible assistant text."

const responsesStreamReconnectRetryInstruction = "The previous streamed assistant response was interrupted before a tool call was completed. Continue from the already-visible assistant text without repeating it. If the user's request requires an available tool, call that tool now."

const responsesBaseToolInstruction = "You have local tools available in this chat runtime. Do not say you lack tools when the user asks for current web information or asks you to use an available tool; call the appropriate tool instead.\n\n" + runtimeSystemToolPrinciple + "\n\n" + responsesToolPacingInstruction

const responsesWorkspaceToolInstruction = "\n\nWorkspace files: user uploads are stored in a per-user workspace. User messages may list workspace file names, paths, MIME type, size, and dimensions. Image tool routing rules:\n1. In Responses image mode, the only image tool is response_image. Use it for text-to-image and image-to-image/reference generation.\n2. In Chat Completions image mode, the only image tool is chat_image. Use it for text-to-image and image-to-image/reference generation.\n3. In Image API mode, image_generate creates images and reference-based image-to-image generations; image_edit is only for explicit mask/mask image/mask_index/mask_path edits.\n4. For 图生图, 以这张图为参考生成, 以这张图为基础生成, 参考这张图重新画, 按这张图的构图/主体/姿势生成, 换风格生成, 风格化, generate from this image, use this image as reference, or style transfer, pass a URL or workspace path in the image argument of the available generation tool.\n5. Never ask the user to paste base64 image data and never put base64 in image tool arguments.\n\nGenerated-image display rule: image tool results are already displayed to the user in dedicated image tool cards by the chat UI. After an image tool returns images, do not repeat the generated images in the assistant text with Markdown image syntax, HTML img tags, raw base64, or duplicate image URLs. You may still write ordinary text around the result if it is useful."

const responsesUniFuncsToolInstruction = responsesBaseToolInstruction + responsesWorkspaceToolInstruction + "\n\nCurrent web tool mode: UniFuncs. Available web tools are web_search and web_reader. The searching tool is not available in this mode.\n\nFor web_search, infer the user's real intent before calling the tool. If the wording is misspelled, transliterated, abbreviated, or mixed-language, resolve it to the most likely real-world person, place, event, product, or topic in your own reasoning, then search that intended target. Do not mechanically copy the user's raw text into the query. Ask for clarification only when multiple interpretations are equally plausible or the target cannot be inferred with reasonable confidence.\n\nFor web_reader, call it when there is a concrete URL to read, including URLs returned by web_search. Prefer format=markdown unless plain text is specifically better.\n\nFor web/current/research questions, prefer the combined workflow whenever useful: first call web_search to discover candidate pages, then call web_reader on the most relevant 1 to 3 result URLs before giving the final answer. Use this search-then-read workflow especially for news, recent facts, product/company/person details, documentation lookups, and any answer that benefits from page-level evidence."

const responsesSearchingToolInstruction = responsesBaseToolInstruction + responsesWorkspaceToolInstruction + "\n\nCurrent web tool mode: Searching. The only available web search tool is searching. web_search and web_reader are not available in this mode.\n\nFor web/current/research questions, call searching with a concise query and use the returned content as the browsing/search evidence before giving the final answer."

var errGenerationStopped = errors.New("generation stopped")

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
	Name          string `json:"name"`
	Type          string `json:"type"`
	Size          int64  `json:"size"`
	Content       string `json:"content,omitempty"`
	Error         string `json:"error,omitempty"`
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
	OriginalName  string `json:"original_name,omitempty"`
	OriginalType  string `json:"original_type,omitempty"`
	OriginalSize  int64  `json:"original_size,omitempty"`
	Preview       string `json:"preview,omitempty"`
	WorkspacePath string `json:"workspace_path,omitempty"`
	URL           string `json:"url,omitempty"`
}

type responseInputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responseOutputContent struct {
	Type        string        `json:"type"`
	Text        string        `json:"text"`
	Annotations []interface{} `json:"annotations"`
	Logprobs    []interface{} `json:"logprobs"`
}

type responseInputMessage struct {
	Type    string      `json:"type"`
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
	Status  string      `json:"status,omitempty"`
	ID      string      `json:"id,omitempty"`
}

type responseOutputItem struct {
	Type             string                  `json:"type"`
	ID               string                  `json:"id,omitempty"`
	CallID           string                  `json:"call_id,omitempty"`
	Name             string                  `json:"name,omitempty"`
	Arguments        string                  `json:"arguments,omitempty"`
	Role             string                  `json:"role,omitempty"`
	Status           string                  `json:"status,omitempty"`
	Content          []responseOutputContent `json:"content,omitempty"`
	Summary          json.RawMessage         `json:"summary,omitempty"`
	EncryptedContent string                  `json:"encrypted_content,omitempty"`
}

type responseFunctionCallOutput struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type responseToolCall struct {
	ID        string
	CallID    string
	Name      string
	Arguments string
}

type responseToolStep struct {
	Name          string `json:"name"`
	CallID        string `json:"call_id"`
	Status        string `json:"status"`
	Arguments     string `json:"arguments,omitempty"`
	Output        string `json:"output,omitempty"`
	Timestamp     string `json:"timestamp"`
	ContentOffset int    `json:"content_offset"`
}

type streamPersistFunc func(text string, force bool)

type responsesRoundResult struct {
	Text      string
	Output    []responseOutputItem
	ToolCalls []responseToolCall
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
	generationCtx := context.Background()
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
	w = &lockedResponseWriter{ResponseWriter: w}
	stopKeepAlive := startSSEKeepAlive(r.Context(), w)
	defer stopKeepAlive()
	runID := uuid.NewString()
	content := strings.TrimSpace(req.Content)
	mode := strings.TrimSpace(req.Mode)
	preparedAttachments, err := s.prepareWorkspaceAttachments(user.ID, req.Attachments)
	if err != nil {
		sendSSE(w, "error", map[string]interface{}{"code": "ATTACHMENT_SAVE_FAILED", "message": err.Error()})
		sendSSE(w, "done", map[string]interface{}{"ok": false})
		return
	}
	attachmentsJSON := attachmentsMetadata(preparedAttachments)
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
	promptContent := strings.TrimSpace(userMessage.Content)
	assistant, err := s.insertMessage(req.ConversationID, user.ID, "assistant", "", "streaming", "{}")
	if err != nil {
		sendSSE(w, "error", map[string]interface{}{"code": "SERVER_ERROR", "message": "创建回复失败"})
		sendSSE(w, "done", map[string]interface{}{"ok": false})
		return
	}
	now := db.Now()
	_, _ = s.store.DB.Exec(`INSERT INTO generation_runs (run_id, conversation_id, assistant_message_id, user_id, status, metadata, created_at, updated_at) VALUES (?, ?, ?, ?, 'running', '{}', ?, ?)`, runID, req.ConversationID, assistant.ID, user.ID, now, now)

	sendSSE(w, "message_start", map[string]interface{}{"run_id": runID, "user_message": userMessage, "assistant_message": assistant})
	if promptContent != "" {
		firstUserMessage := s.firstUserMessageContent(req.ConversationID, user.ID, promptContent)
		go s.maybeGenerateConversationTitle(context.Background(), req.ConversationID, user.ID, firstUserMessage)
	}

	provider, _ := s.getRuntimeProvider(req.ProviderID)
	var fullText string
	toolSteps := make([]responseToolStep, 0)
	var memoryHits memoryHitPayload
	persistPartial := s.partialMessagePersister(assistant.ID, user.ID)
	stopped := false
	if provider == nil || provider.Key == "" || provider.Base == "" || provider.Model == "" {
		fullText = "这是 ChatXXX 的本地流式占位回复。你还没有配置可用的 OpenAI-compatible provider；请使用管理员账号到设置里添加模型。"
		text, err := streamLocalText(generationCtx, w, fullText, persistPartial, func() bool { return s.isGenerationStopping(runID, user.ID) })
		fullText = text
		stopped = errors.Is(err, errGenerationStopped)
	} else {
		conversation, _ := s.getConversationForUser(req.ConversationID, user.ID)
		persistToolMetadata := func(steps []responseToolStep) {
			metadata := assistantMetadataWithToolStepsAndMemoryHits(steps, memoryHits)
			s.updateMessageMetadata(assistant.ID, metadata)
		}
		publicBaseURL := s.publicBaseURLForRequest(r)
		text, err := s.streamOpenAICompatible(generationCtx, w, *provider, conversation, req.ConversationID, user.ID, promptContent, publicBaseURL, &toolSteps, &memoryHits, persistPartial, persistToolMetadata, func() bool {
			return s.isGenerationStopping(runID, user.ID)
		})
		if errors.Is(err, errGenerationStopped) {
			fullText = text
			stopped = true
		} else if err != nil {
			if hasSuccessfulImageToolStep(toolSteps) {
				fullText = successfulImageToolFallbackText(text)
			} else {
				fullText = "模型调用失败：" + userFacingModelStreamError(err)
				sendSSE(w, "error", map[string]interface{}{"code": "LLM_REQUEST_FAILED", "message": fullText})
			}
		} else {
			fullText = text
		}
	}

	if s.isGenerationStopping(runID, user.ID) {
		stopped = true
	}
	if stopped {
		if stoppedContent, ok := s.stoppedMessageContent(assistant.ID, user.ID); ok && strings.TrimSpace(stoppedContent) != "" {
			fullText = stoppedContent
		}
	}
	if strings.TrimSpace(fullText) == "" && stopped {
		fullText = "已停止生成"
	} else if strings.TrimSpace(fullText) == "" && hasSuccessfulImageToolStep(toolSteps) {
		fullText = ""
	} else if strings.TrimSpace(fullText) == "" {
		fullText = "没有收到模型输出。"
	}
	persistPartial(fullText, true)
	assistantMetadata := assistantMetadataWithToolStepsAndMemoryHits(toolSteps, memoryHits)
	finalStatus := "completed"
	if stopped {
		finalStatus = "stopped"
	}
	s.updateMessageContentWithMetadata(assistant.ID, fullText, finalStatus, assistantMetadata)
	_, _ = s.store.DB.Exec(`UPDATE generation_runs SET status=?, updated_at=? WHERE run_id=?`, finalStatus, db.Now(), runID)
	assistant.Content = fullText
	assistant.Status = finalStatus
	assistant.Metadata = assistantMetadata
	sendSSE(w, "message_end", map[string]interface{}{"message": assistant})
	sendSSE(w, "done", map[string]interface{}{"ok": !stopped, "status": finalStatus})
	if !stopped && finalStatus == "completed" && strings.TrimSpace(promptContent) != "" && strings.TrimSpace(fullText) != "" && !strings.HasPrefix(fullText, "模型调用失败：") {
		s.runAutoMemoryAfterReply(req.ConversationID, user.ID, userMessage, assistant)
	}
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
	user := currentUser(r)
	var req struct {
		RunID              string `json:"run_id"`
		AssistantMessageID int64  `json:"assistant_message_id"`
		Content            string `json:"content"`
	}
	_ = readJSON(r, &req)
	if strings.TrimSpace(req.RunID) != "" {
		_, _ = s.store.DB.Exec(`UPDATE generation_runs SET status='stopping', updated_at=? WHERE run_id=? AND user_id=?`, db.Now(), req.RunID, user.ID)
		if req.AssistantMessageID <= 0 {
			_ = s.store.DB.QueryRow(`SELECT assistant_message_id FROM generation_runs WHERE run_id=? AND user_id=?`, req.RunID, user.ID).Scan(&req.AssistantMessageID)
		}
	} else if req.AssistantMessageID > 0 {
		_ = s.store.DB.QueryRow(`SELECT run_id FROM generation_runs WHERE assistant_message_id=? AND user_id=? AND status IN ('running','stopping') ORDER BY created_at DESC LIMIT 1`, req.AssistantMessageID, user.ID).Scan(&req.RunID)
		if strings.TrimSpace(req.RunID) != "" {
			_, _ = s.store.DB.Exec(`UPDATE generation_runs SET status='stopping', updated_at=? WHERE run_id=? AND user_id=?`, db.Now(), req.RunID, user.ID)
		}
	}
	if req.AssistantMessageID > 0 {
		content := req.Content
		if strings.TrimSpace(content) == "" {
			content = "已停止生成"
		}
		_, _ = s.store.DB.Exec(`UPDATE messages SET content=?, status='stopped', updated_at=? WHERE id=? AND user_id=? AND role='assistant'`, content, db.Now(), req.AssistantMessageID, user.ID)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) isGenerationStopping(runID string, userID int64) bool {
	if strings.TrimSpace(runID) == "" {
		return false
	}
	var status string
	if err := s.store.DB.QueryRow(`SELECT status FROM generation_runs WHERE run_id=? AND user_id=?`, runID, userID).Scan(&status); err != nil {
		return false
	}
	return status == "stopping" || status == "stopped"
}

func (s *Server) stoppedMessageContent(messageID, userID int64) (string, bool) {
	var content, status string
	if err := s.store.DB.QueryRow(`SELECT content, status FROM messages WHERE id=? AND user_id=? AND role='assistant'`, messageID, userID).Scan(&content, &status); err != nil {
		return "", false
	}
	return content, status == "stopped"
}

func startSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

type lockedResponseWriter struct {
	http.ResponseWriter
	mu sync.Mutex
}

func (w *lockedResponseWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Write(data)
}

func (w *lockedResponseWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func startSSEKeepAlive(ctx context.Context, w http.ResponseWriter) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(streamKeepAliveEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sendSSE(w, "ping", map[string]interface{}{"time": time.Now().UTC().Format(time.RFC3339)})
			}
		}
	}()
	return cancel
}

func sendSSE(w http.ResponseWriter, event string, data interface{}) {
	payload, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func streamLocalText(ctx context.Context, w http.ResponseWriter, text string, persist streamPersistFunc, shouldStop func() bool) (string, error) {
	return streamDeltaText(ctx, w, text, persist, shouldStop)
}

func streamDeltaText(ctx context.Context, w http.ResponseWriter, text string, persist streamPersistFunc, shouldStop func() bool) (string, error) {
	var full strings.Builder
	runes := []rune(text)
	for i := 0; i < len(runes); i += streamDeltaChunkRunes {
		if streamShouldStop(ctx, shouldStop) {
			return full.String(), errGenerationStopped
		}
		end := i + streamDeltaChunkRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunk := string(runes[i:end])
		full.WriteString(chunk)
		sendSSE(w, "delta", map[string]interface{}{"text": chunk})
		if persist != nil {
			persist(full.String(), false)
		}
		time.Sleep(streamDeltaDelay)
	}
	if persist != nil {
		persist(full.String(), true)
	}
	return full.String(), nil
}

func (s *Server) partialMessagePersister(messageID, userID int64) streamPersistFunc {
	var lastText string
	var lastWrite time.Time
	return func(text string, force bool) {
		if messageID <= 0 || userID <= 0 {
			return
		}
		if text == lastText && !force {
			return
		}
		now := time.Now()
		if !force && now.Sub(lastWrite) < 400*time.Millisecond && len([]rune(text))-len([]rune(lastText)) < 12 {
			return
		}
		_, _ = s.store.DB.Exec(`UPDATE messages SET content=?, status='streaming', updated_at=? WHERE id=? AND user_id=? AND role='assistant' AND status='streaming'`, text, db.Now(), messageID, userID)
		lastText = text
		lastWrite = now
	}
}

func prefixedPersist(prefix string, persist streamPersistFunc) streamPersistFunc {
	if persist == nil {
		return nil
	}
	return func(text string, force bool) {
		persist(prefix+text, force)
	}
}

func streamShouldStop(ctx context.Context, shouldStop func() bool) bool {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return true
		default:
		}
	}
	return shouldStop != nil && shouldStop()
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
	generatedImageText := generatedImagePromptText(msg.Metadata)
	if attachmentText == "" {
		if generatedImageText == "" {
			return content
		}
		if content == "" {
			return generatedImageText
		}
		return content + "\n\n" + generatedImageText
	}
	if content == "" {
		if generatedImageText == "" {
			return attachmentText
		}
		return attachmentText + "\n\n" + generatedImageText
	}
	if generatedImageText == "" {
		return content + "\n\n" + attachmentText
	}
	return content + "\n\n" + attachmentText + "\n\n" + generatedImageText
}

func generatedImagePromptText(raw string) string {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "{}" {
		return ""
	}
	var metadata struct {
		ToolSteps []responseToolStep `json:"tool_steps"`
	}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return ""
	}
	type generatedImageRef struct {
		Tool          string
		URL           string
		WorkspacePath string
	}
	refs := make([]generatedImageRef, 0)
	seen := map[string]bool{}
	for _, step := range metadata.ToolSteps {
		if step.Status != "completed" || !isImageToolName(step.Name) {
			continue
		}
		var output imageToolResult
		if err := json.Unmarshal([]byte(step.Output), &output); err != nil || !output.OK {
			continue
		}
		for _, image := range output.Images {
			urlValue := strings.TrimSpace(image.URL)
			pathValue := strings.TrimSpace(image.WorkspacePath)
			key := firstNonEmpty(pathValue, urlValue)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			refs = append(refs, generatedImageRef{Tool: step.Name, URL: urlValue, WorkspacePath: pathValue})
		}
	}
	if len(refs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("此前由图片工具生成的图片 URL，可作为后续图片工具的 image 参数使用：")
	for index, ref := range refs {
		if index >= 8 {
			break
		}
		b.WriteString("\n- ")
		b.WriteString(ref.Tool)
		b.WriteString("：")
		if ref.WorkspacePath != "" {
			b.WriteString("workspace_path=")
			b.WriteString(ref.WorkspacePath)
			if ref.URL != "" {
				b.WriteString("；url=")
				b.WriteString(ref.URL)
			}
			continue
		}
		b.WriteString(ref.URL)
	}
	return b.String()
}

func attachmentPromptText(raw string) string {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "[]" {
		return ""
	}
	if summary := attachmentWorkspaceSummary(raw); summary != "" {
		return summary
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

func (s *Server) streamOpenAICompatible(ctx context.Context, w http.ResponseWriter, provider runtimeProvider, conversation *Conversation, conversationID, userID int64, latest, publicBaseURL string, toolSteps *[]responseToolStep, memoryHits *memoryHitPayload, persist streamPersistFunc, persistToolMetadata func([]responseToolStep), shouldStop func() bool) (string, error) {
	memoryInstruction := ""
	if conversation == nil || conversation.MemoryEnabled {
		retrieval := s.memoriesForInjection(ctx, userID, latest, s.memoryInjectLimit())
		memoryInstruction = memorySystemPrompt(retrieval.Memories)
		if (retrieval.Method == "vector" || retrieval.Method == "vector_rerank") && len(retrieval.Memories) > 0 {
			payload := memoryHitPayload{
				Method:   retrieval.Method,
				Model:    retrieval.Model,
				Dim:      retrieval.Dim,
				Memories: retrieval.Hits,
			}
			if memoryHits != nil {
				*memoryHits = payload
			}
			sendSSE(w, "memory_hits", payload)
		}
	}
	workspaceInstruction := s.workspaceSystemPrompt(userID)
	switch strings.TrimSpace(provider.RequestMode) {
	case "", "chat_completions":
		return s.streamChatCompletions(ctx, w, provider, conversation, conversationID, userID, latest, memoryInstruction, workspaceInstruction, persist, shouldStop)
	case "responses":
		return s.streamResponses(ctx, w, provider, conversation, conversationID, userID, latest, memoryInstruction, workspaceInstruction, publicBaseURL, toolSteps, persist, persistToolMetadata, shouldStop)
	default:
		return "", fmt.Errorf("不支持的请求模式：%s", provider.RequestMode)
	}
}

func (s *Server) streamChatCompletions(ctx context.Context, w http.ResponseWriter, provider runtimeProvider, conversation *Conversation, conversationID, userID int64, latest, memoryInstruction, workspaceInstruction string, persist streamPersistFunc, shouldStop func() bool) (string, error) {
	messages, _ := s.listMessages(conversationID, userID)
	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	bodyMessages := make([]chatMessage, 0, len(messages)+1)
	bodyMessages = append(bodyMessages, chatMessage{Role: "system", Content: runtimeSystemToolPrinciple})
	if conversation != nil && strings.TrimSpace(conversation.SystemPrompt) != "" {
		bodyMessages = append(bodyMessages, chatMessage{Role: "system", Content: strings.TrimSpace(conversation.SystemPrompt)})
	}
	if strings.TrimSpace(memoryInstruction) != "" {
		bodyMessages = append(bodyMessages, chatMessage{Role: "system", Content: strings.TrimSpace(memoryInstruction)})
	}
	if strings.TrimSpace(workspaceInstruction) != "" {
		bodyMessages = append(bodyMessages, chatMessage{Role: "system", Content: strings.TrimSpace(workspaceInstruction)})
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(provider.Base, "/")+"/chat/completions", bytes.NewReader(raw))
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
	return readChatCompletionsStream(ctx, w, resp.Body, persist, shouldStop)
}

func (s *Server) streamResponses(ctx context.Context, w http.ResponseWriter, provider runtimeProvider, conversation *Conversation, conversationID, userID int64, latest, memoryInstruction, workspaceInstruction, publicBaseURL string, toolSteps *[]responseToolStep, persist streamPersistFunc, persistToolMetadata func([]responseToolStep), shouldStop func() bool) (string, error) {
	messages, _ := s.listMessages(conversationID, userID)
	input := buildResponsesInput(messages, latest)
	searchMode := s.searchToolMode()
	imageMode := s.imageToolMode()
	tools := responseToolDefinitionsForImageMode(searchMode, imageMode)
	toolCtx := responseToolContext{Context: ctx, ConversationID: conversationID, UserID: userID, PublicBaseURL: publicBaseURL}
	var full strings.Builder
	needTextBeforeTool := false
	retriedStreamDisconnect := false
	for round := 0; round < maxResponsesToolRounds; round++ {
		persistRound := prefixedPersist(full.String(), persist)
		result, err := s.readResponsesRound(ctx, w, provider, conversation, memoryInstruction, workspaceInstruction, input, tools, searchMode, persistRound, shouldStop)
		textAlreadyAppended := false
		if err != nil {
			hasToolCall := len(result.ToolCalls) > 0
			hasVisibleText := strings.TrimSpace(result.Text) != ""
			if result.Text != "" {
				full.WriteString(result.Text)
				textAlreadyAppended = true
			}
			if hasToolCall {
				// Some providers close the SSE body before response.completed even though the
				// function_call item has arrived. Treat the completed tool call as usable.
			} else if shouldRetryResponsesStreamDisconnect(err, retriedStreamDisconnect, result, shouldStop) {
				retriedStreamDisconnect = true
				if hasVisibleText {
					input = append(input, responseAssistantTextMessage(result.Text))
				}
				input = append(input, responseStreamReconnectRetryMessage())
				continue
			} else if toolSteps != nil && hasSuccessfulImageToolStep(*toolSteps) {
				return successfulImageToolFallbackText(full.String()), nil
			} else {
				return full.String(), err
			}
		}
		hasVisibleText := strings.TrimSpace(result.Text) != ""
		if result.Text != "" && !textAlreadyAppended {
			full.WriteString(result.Text)
		}
		if hasVisibleText {
			needTextBeforeTool = false
		}
		if streamShouldStop(ctx, shouldStop) {
			return full.String(), errGenerationStopped
		}
		if len(result.ToolCalls) == 0 {
			return full.String(), nil
		}
		if needTextBeforeTool && !hasVisibleText {
			input = append(input, responseToolPacingMessage())
			continue
		}
		call := result.ToolCalls[0]
		for _, item := range responseOutputItemsForToolCall(result.Output, call) {
			input = append(input, item)
		}
		if streamShouldStop(ctx, shouldStop) {
			return full.String(), errGenerationStopped
		}
		offset := len([]rune(full.String()))
		running := responseToolStep{
			Name:          call.Name,
			CallID:        call.CallID,
			Status:        "running",
			Arguments:     call.Arguments,
			Timestamp:     db.Now(),
			ContentOffset: offset,
		}
		sendToolStep(w, running)
		appendToolStep(toolSteps, running)
		persistToolMetadataSnapshot(toolSteps, persistToolMetadata)
		output := s.executeResponseToolWithContext(call, searchMode, toolCtx)
		if streamShouldStop(ctx, shouldStop) {
			return full.String(), errGenerationStopped
		}
		completed := responseToolStep{
			Name:          call.Name,
			CallID:        call.CallID,
			Status:        "completed",
			Arguments:     call.Arguments,
			Output:        output,
			Timestamp:     db.Now(),
			ContentOffset: offset,
		}
		sendToolStep(w, completed)
		appendToolStep(toolSteps, completed)
		persistToolMetadataSnapshot(toolSteps, persistToolMetadata)
		modelOutput := toolOutputForModel(call.Name, output)
		input = append(input, responseFunctionCallOutput{
			Type:   "function_call_output",
			CallID: call.CallID,
			Output: modelOutput,
		})
		needTextBeforeTool = true
	}
	return full.String(), fmt.Errorf("工具调用次数过多")
}

func toolOutputForModel(toolName, output string) string {
	if !isImageToolName(toolName) {
		return output
	}
	var payload imageToolResult
	if err := json.Unmarshal([]byte(output), &payload); err != nil || !payload.OK {
		return output
	}
	type modelImage struct {
		URL           string `json:"url,omitempty"`
		WorkspacePath string `json:"workspace_path,omitempty"`
		Omitted       bool   `json:"omitted,omitempty"`
		Kind          string `json:"kind,omitempty"`
	}
	type modelOutput struct {
		OK         bool         `json:"ok"`
		Tool       string       `json:"tool"`
		Created    int64        `json:"created,omitempty"`
		ImageCount int          `json:"image_count"`
		Images     []modelImage `json:"images,omitempty"`
		Note       string       `json:"note,omitempty"`
		Error      string       `json:"error,omitempty"`
	}
	result := modelOutput{
		OK:         payload.OK,
		Tool:       payload.Tool,
		Created:    payload.Created,
		ImageCount: len(payload.Images),
		Error:      payload.Error,
	}
	omitted := 0
	for _, image := range payload.Images {
		urlValue := strings.TrimSpace(image.URL)
		pathValue := strings.TrimSpace(image.WorkspacePath)
		if pathValue != "" {
			item := modelImage{WorkspacePath: pathValue}
			if urlValue != "" && !strings.HasPrefix(strings.ToLower(urlValue), "data:image/") {
				item.URL = urlValue
			}
			result.Images = append(result.Images, item)
			if strings.TrimSpace(image.B64JSON) != "" {
				omitted++
			}
			continue
		}
		if strings.HasPrefix(strings.ToLower(urlValue), "data:image/") {
			omitted++
			result.Images = append(result.Images, modelImage{Kind: "inline_image", Omitted: true})
			continue
		}
		if urlValue != "" {
			result.Images = append(result.Images, modelImage{URL: urlValue})
			continue
		}
		if strings.TrimSpace(image.B64JSON) != "" {
			omitted++
			result.Images = append(result.Images, modelImage{Kind: "inline_image", Omitted: true})
		}
	}
	if omitted > 0 {
		result.Note = "Generated image data was omitted from this model-facing tool result; the user can already see it in the image tool card."
	}
	return mustJSONString(result)
}

func responseToolPacingMessage() responseInputMessage {
	return responseInputMessage{
		Type: "message",
		Role: "system",
		Content: []responseInputContent{
			{Type: "input_text", Text: responsesToolPacingRetryInstruction},
		},
	}
}

func responseStreamReconnectRetryMessage() responseInputMessage {
	return responseInputMessage{
		Type: "message",
		Role: "system",
		Content: []responseInputContent{
			{Type: "input_text", Text: responsesStreamReconnectRetryInstruction},
		},
	}
}

func responseAssistantTextMessage(text string) responseInputMessage {
	return responseInputMessage{
		Type:   "message",
		Role:   "assistant",
		Status: "completed",
		Content: []responseOutputContent{
			{Type: "output_text", Text: text, Annotations: []interface{}{}, Logprobs: []interface{}{}},
		},
	}
}

func shouldRetryResponsesStreamDisconnect(err error, alreadyRetried bool, result responsesRoundResult, shouldStop func() bool) bool {
	if alreadyRetried || !isRetryableProviderStreamDisconnect(err) || len(result.ToolCalls) > 0 {
		return false
	}
	if streamShouldStop(context.Background(), shouldStop) {
		return false
	}
	return true
}

func isRetryableProviderStreamDisconnect(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unexpected eof") || strings.Contains(message, "connection reset") || strings.Contains(message, "stream error")
}

func userFacingModelStreamError(err error) string {
	if err == nil {
		return "模型连接中断，请稍后重试"
	}
	if isRetryableProviderStreamDisconnect(err) {
		return "模型连接中断，请稍后重试"
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "模型连接中断，请稍后重试"
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") {
		return "模型响应超时，请稍后重试"
	}
	if strings.Contains(lower, "service temporarily unavailable") || strings.Contains(lower, "503") {
		return "模型服务暂时不可用，请稍后重试"
	}
	return message
}

func responseOutputItemsForToolCall(items []responseOutputItem, call responseToolCall) []responseOutputItem {
	selected := make([]responseOutputItem, 0, len(items))
	includedCall := false
	for _, item := range items {
		if item.Type != "function_call" {
			selected = append(selected, item)
			continue
		}
		if !includedCall && responseOutputItemMatchesCall(item, call) {
			selected = append(selected, item)
			includedCall = true
		}
	}
	if !includedCall {
		selected = append(selected, responseOutputItem{
			Type:      "function_call",
			ID:        call.ID,
			CallID:    call.CallID,
			Name:      call.Name,
			Arguments: call.Arguments,
			Status:    "completed",
		})
	}
	return selected
}

func responseOutputItemMatchesCall(item responseOutputItem, call responseToolCall) bool {
	if call.ID != "" && item.ID == call.ID {
		return true
	}
	if call.CallID != "" && item.CallID == call.CallID {
		return true
	}
	return item.Name == call.Name && item.Arguments == call.Arguments
}

func responseMessageForInput(msg Message, content string) responseInputMessage {
	if msg.Role == "assistant" {
		return responseInputMessage{
			Type:   "message",
			Role:   "assistant",
			Status: "completed",
			ID:     fmt.Sprintf("msg_%d", msg.ID),
			Content: []responseOutputContent{
				{Type: "output_text", Text: content, Annotations: []interface{}{}, Logprobs: []interface{}{}},
			},
		}
	}
	return responseInputMessage{
		Type: "message",
		Role: msg.Role,
		Content: []responseInputContent{
			{Type: "input_text", Text: content},
		},
	}
}

func buildResponsesInput(messages []Message, latest string) []interface{} {
	input := make([]interface{}, 0, len(messages)+1)
	for _, msg := range messages {
		if msg.Role != "user" && msg.Role != "assistant" && msg.Role != "system" {
			continue
		}
		content := messagePromptContent(msg)
		if strings.TrimSpace(content) == "" {
			continue
		}
		input = append(input, responseMessageForInput(msg, content))
	}
	if len(input) == 0 && strings.TrimSpace(latest) != "" {
		input = append(input, responseInputMessage{
			Type: "message",
			Role: "user",
			Content: []responseInputContent{
				{Type: "input_text", Text: latest},
			},
		})
	}
	return input
}

func responseToolDefinitions(mode string) []map[string]interface{} {
	return responseToolDefinitionsForImageMode(mode, defaultImageToolMode)
}

func responseToolDefinitionsForImageMode(mode, imageMode string) []map[string]interface{} {
	tools := []map[string]interface{}{
		{
			"type":        "function",
			"name":        "get_current_time",
			"description": "Get the current time for an optional IANA timezone.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"timezone": map[string]interface{}{
						"type":        "string",
						"description": "Optional IANA timezone name such as Asia/Shanghai.",
					},
				},
				"required":             []string{},
				"additionalProperties": false,
			},
		},
	}
	tools = append(tools, imageToolDefinitions(imageMode)...)
	if normalizeSearchToolMode(mode) == searchToolModeSearching {
		return append(tools, map[string]interface{}{
			"type":        "function",
			"name":        "searching",
			"description": "Call the configured web-enabled search LLM API and return its complete search answer as tool result data. Use this for web/current/research questions when Searching mode is active.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Concise web search query or research question.",
					},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		})
	}
	return append(tools,
		map[string]interface{}{
			"type":        "function",
			"name":        "web_search",
			"description": "Search the web for current information through the configured UniFuncs Web Search API. Use this first for web/current/research questions to discover candidate pages, then usually follow with web_reader on the most relevant result URLs. First infer the user's intended target or topic from the conversation, then form the most appropriate search query. Do not just echo the user's raw wording when it is noisy, misspelled, transliterated, abbreviated, or mixed-language.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Semantic search query chosen after resolving the user's intended target or topic.",
					},
					"freshness": map[string]interface{}{
						"type":        "string",
						"description": "Optional freshness filter supported by UniFuncs, such as Day, Week, or Month.",
					},
					"include_images": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether to include image results.",
					},
					"page": map[string]interface{}{
						"type":        "integer",
						"description": "Result page number, starting from 1.",
					},
					"count": map[string]interface{}{
						"type":        "integer",
						"description": "Number of web results to request.",
					},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		},
		map[string]interface{}{
			"type":        "function",
			"name":        "web_reader",
			"description": "Read a specific webpage URL through the configured UniFuncs Web Reader API and return extracted page content. Use this after web_search to read the most relevant result URLs before answering, or directly when the user provides a concrete URL.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "The absolute webpage URL to read.",
					},
					"format": map[string]interface{}{
						"type":        "string",
						"description": "Output format requested from UniFuncs. Use md or markdown for Markdown, text or txt for plain text.",
						"enum":        []string{"markdown", "md", "text", "txt"},
					},
					"lite_mode": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether to enable UniFuncs liteMode for faster, lighter extraction.",
					},
					"include_images": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether extracted Markdown should keep image references when available.",
					},
					"max_words": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum words requested from UniFuncs before local output trimming.",
					},
					"topic": map[string]interface{}{
						"type":        "string",
						"description": "Optional topic hint for extracting the most relevant content from the page.",
					},
				},
				"required":             []string{"url"},
				"additionalProperties": false,
			},
		},
	)
}

func responseToolInstructions(mode string) string {
	if normalizeSearchToolMode(mode) == searchToolModeSearching {
		return responsesSearchingToolInstruction
	}
	return responsesUniFuncsToolInstruction
}

func (s *Server) readResponsesRound(ctx context.Context, w http.ResponseWriter, provider runtimeProvider, conversation *Conversation, memoryInstruction, workspaceInstruction string, input []interface{}, tools []map[string]interface{}, mode string, persist streamPersistFunc, shouldStop func() bool) (responsesRoundResult, error) {
	requestBody := map[string]interface{}{
		"model":       provider.Model,
		"input":       input,
		"stream":      true,
		"tool_choice": "auto",
		"tools":       tools,
	}
	instructions := responseToolInstructions(mode)
	if conversation != nil && strings.TrimSpace(conversation.SystemPrompt) != "" {
		instructions = instructions + "\n\n" + strings.TrimSpace(conversation.SystemPrompt)
	}
	if strings.TrimSpace(memoryInstruction) != "" {
		instructions = instructions + "\n\n" + strings.TrimSpace(memoryInstruction)
	}
	if strings.TrimSpace(workspaceInstruction) != "" {
		instructions = instructions + "\n\n" + strings.TrimSpace(workspaceInstruction)
	}
	if strings.TrimSpace(instructions) != "" {
		requestBody["instructions"] = instructions
	}
	if rf, ok, err := parseJSONObject(provider.ResponseFormat); err != nil {
		return responsesRoundResult{}, fmt.Errorf("response_format 配置无效：%w", err)
	} else if ok {
		requestBody["text"] = map[string]interface{}{"format": rf}
	}
	raw, _ := json.Marshal(requestBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(provider.Base, "/")+"/responses", bytes.NewReader(raw))
	if err != nil {
		return responsesRoundResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+provider.Key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return responsesRoundResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return responsesRoundResult{}, fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return readResponsesStream(ctx, w, resp.Body, persist, shouldStop)
}

func readChatCompletionsStream(ctx context.Context, w http.ResponseWriter, body io.Reader, persist streamPersistFunc, shouldStop func() bool) (string, error) {
	var full strings.Builder
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		if streamShouldStop(ctx, shouldStop) {
			return full.String(), errGenerationStopped
		}
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
				persistChunk := prefixedPersist(full.String(), persist)
				sent, err := streamDeltaText(ctx, w, choice.Delta.Content, persistChunk, shouldStop)
				full.WriteString(sent)
				if err != nil {
					return full.String(), err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return full.String(), err
	}
	if persist != nil {
		persist(full.String(), true)
	}
	return full.String(), nil
}

func readResponsesStream(ctx context.Context, w http.ResponseWriter, body io.Reader, persist streamPersistFunc, shouldStop func() bool) (responsesRoundResult, error) {
	var full strings.Builder
	result := responsesRoundResult{}
	outputItems := make([]responseOutputItem, 0)
	outputItemByID := map[string]int{}
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
		case "response.output_item.added", "response.output_item.done":
			var event struct {
				Item responseOutputItem `json:"item"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				return nil
			}
			upsertResponseOutputItem(&outputItems, outputItemByID, event.Item)
		case "response.output_text.delta":
			var event struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				return nil
			}
			if event.Delta != "" {
				persistChunk := prefixedPersist(full.String(), persist)
				sent, err := streamDeltaText(ctx, w, event.Delta, persistChunk, shouldStop)
				full.WriteString(sent)
				if err != nil {
					return err
				}
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
			completed := parseResponseCompleted(data)
			if full.Len() == 0 && completed.Text != "" {
				sent, err := streamDeltaText(ctx, w, completed.Text, persist, shouldStop)
				full.WriteString(sent)
				if err != nil {
					return err
				}
			}
			for _, item := range completed.Output {
				upsertResponseOutputItem(&outputItems, outputItemByID, item)
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
				return responsesRoundResult{Text: full.String(), Output: result.Output, ToolCalls: result.ToolCalls}, err
			}
			if streamShouldStop(ctx, shouldStop) {
				return responsesRoundResult{Text: full.String(), Output: result.Output, ToolCalls: result.ToolCalls}, errGenerationStopped
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			if err := flush(); err != nil {
				return responsesRoundResult{Text: full.String(), Output: result.Output, ToolCalls: result.ToolCalls}, err
			}
			if streamShouldStop(ctx, shouldStop) {
				return responsesRoundResult{Text: full.String(), Output: result.Output, ToolCalls: result.ToolCalls}, errGenerationStopped
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
		return responsesRoundResult{Text: full.String(), Output: result.Output, ToolCalls: result.ToolCalls}, err
	}
	if err := scanner.Err(); err != nil {
		return responsesRoundResult{Text: full.String(), Output: result.Output, ToolCalls: result.ToolCalls}, err
	}
	streamed := parseResponseOutputItems(outputItems)
	if full.Len() == 0 && streamed.Text != "" {
		sent, err := streamDeltaText(ctx, w, streamed.Text, persist, shouldStop)
		full.WriteString(sent)
		if err != nil {
			return responsesRoundResult{Text: full.String(), Output: streamed.Output, ToolCalls: streamed.ToolCalls}, err
		}
	}
	result.Text = full.String()
	result.Output = streamed.Output
	result.ToolCalls = streamed.ToolCalls
	if persist != nil {
		persist(result.Text, true)
	}
	return result, nil
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

func parseResponseCompleted(data string) responsesRoundResult {
	var event struct {
		Response struct {
			Output []responseOutputItem `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return responsesRoundResult{}
	}
	return parseResponseOutputItems(event.Response.Output)
}

func parseResponseOutputItems(items []responseOutputItem) responsesRoundResult {
	var full strings.Builder
	result := responsesRoundResult{Output: items}
	for _, item := range items {
		if item.Type == "function_call" {
			result.ToolCalls = append(result.ToolCalls, responseToolCall{
				ID:        item.ID,
				CallID:    item.CallID,
				Name:      item.Name,
				Arguments: item.Arguments,
			})
			continue
		}
		if item.Type != "message" || item.Role != "assistant" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" && content.Text != "" {
				full.WriteString(content.Text)
			}
		}
	}
	result.Text = full.String()
	return result
}

func upsertResponseOutputItem(items *[]responseOutputItem, index map[string]int, item responseOutputItem) {
	if item.Type == "" {
		return
	}
	key := strings.TrimSpace(item.ID)
	if key == "" {
		key = fmt.Sprintf("%s:%d", item.Type, len(*items))
	}
	if pos, ok := index[key]; ok {
		(*items)[pos] = mergeResponseOutputItem((*items)[pos], item)
		return
	}
	index[key] = len(*items)
	*items = append(*items, item)
}

func mergeResponseOutputItem(prev, next responseOutputItem) responseOutputItem {
	if next.Type != "" {
		prev.Type = next.Type
	}
	if next.ID != "" {
		prev.ID = next.ID
	}
	if next.CallID != "" {
		prev.CallID = next.CallID
	}
	if next.Name != "" {
		prev.Name = next.Name
	}
	if next.Arguments != "" {
		prev.Arguments = next.Arguments
	}
	if next.Role != "" {
		prev.Role = next.Role
	}
	if next.Status != "" {
		prev.Status = next.Status
	}
	if len(next.Content) > 0 {
		prev.Content = next.Content
	}
	if len(next.Summary) > 0 {
		prev.Summary = next.Summary
	}
	if next.EncryptedContent != "" {
		prev.EncryptedContent = next.EncryptedContent
	}
	return prev
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

func sendToolStep(w http.ResponseWriter, step responseToolStep) {
	sendSSE(w, "tool_steps", map[string]interface{}{"step": step})
}

func appendToolStep(steps *[]responseToolStep, step responseToolStep) {
	if steps == nil {
		return
	}
	*steps = append(*steps, step)
}

func persistToolMetadataSnapshot(steps *[]responseToolStep, persist func([]responseToolStep)) {
	if steps == nil || persist == nil {
		return
	}
	snapshot := append([]responseToolStep(nil), (*steps)...)
	persist(snapshot)
}

func isSuccessfulImageToolOutput(name, output string) bool {
	if !isImageToolName(name) {
		return false
	}
	var payload struct {
		OK     bool                   `json:"ok"`
		Images []imageToolResultImage `json:"images"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return false
	}
	return payload.OK && len(payload.Images) > 0
}

func hasSuccessfulImageToolStep(steps []responseToolStep) bool {
	for _, step := range steps {
		if step.Status == "completed" && isSuccessfulImageToolOutput(step.Name, step.Output) {
			return true
		}
	}
	return false
}

func successfulImageToolFallbackText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed != "" {
		return trimmed
	}
	return "图片已生成。"
}

func assistantMetadataWithToolSteps(steps []responseToolStep) string {
	return assistantMetadataWithToolStepsAndMemoryHits(steps, memoryHitPayload{})
}

func assistantMetadataWithToolStepsAndMemoryHits(steps []responseToolStep, hits memoryHitPayload) string {
	if len(steps) == 0 && len(hits.Memories) == 0 {
		return "{}"
	}
	metadata := map[string]interface{}{}
	if len(steps) > 0 {
		metadata["tool_steps"] = sanitizeToolStepsForMetadata(steps)
	}
	if len(hits.Memories) > 0 {
		metadata["memory_hits"] = hits
	}
	return mustJSONString(metadata)
}

func sanitizeToolStepsForMetadata(steps []responseToolStep) []responseToolStep {
	if len(steps) == 0 {
		return steps
	}
	next := make([]responseToolStep, len(steps))
	for i, step := range steps {
		next[i] = step
		if isImageToolName(step.Name) && step.Output != "" {
			if output, changed := sanitizeImageToolOutputString(step.Output); changed {
				next[i].Output = output
			}
		}
	}
	return next
}

func sanitizeMessageMetadataForClient(raw string) string {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "{}" {
		return raw
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return raw
	}
	steps, ok := metadata["tool_steps"].([]interface{})
	if !ok {
		return raw
	}
	changed := false
	for _, stepValue := range steps {
		step, ok := stepValue.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := step["name"].(string)
		if !isImageToolName(name) {
			continue
		}
		output, _ := step["output"].(string)
		if sanitized, ok := sanitizeImageToolOutputString(output); ok {
			step["output"] = sanitized
			changed = true
		}
	}
	if !changed {
		return raw
	}
	return mustJSONString(metadata)
}

func metadataHasSuccessfulImageToolStep(raw string) bool {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "{}" {
		return false
	}
	var metadata struct {
		ToolSteps []responseToolStep `json:"tool_steps"`
	}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return false
	}
	return hasSuccessfulImageToolStep(metadata.ToolSteps)
}

func sanitizeImageToolOutputString(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return raw, false
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw, false
	}
	images, ok := payload["images"].([]interface{})
	if !ok {
		return raw, false
	}
	changed := false
	for _, imageValue := range images {
		image, ok := imageValue.(map[string]interface{})
		if !ok {
			continue
		}
		if !hasStringField(image, "b64_json") {
			continue
		}
		if hasStringField(image, "url") || hasStringField(image, "workspace_path") {
			delete(image, "b64_json")
			changed = true
		}
	}
	if !changed {
		return raw, false
	}
	return mustJSONString(payload), true
}

func hasStringField(item map[string]interface{}, key string) bool {
	value, ok := item[key].(string)
	return ok && strings.TrimSpace(value) != ""
}

func (s *Server) executeResponseTool(call responseToolCall, mode string) string {
	return s.executeResponseToolWithContext(call, mode, responseToolContext{})
}

func (s *Server) executeResponseToolWithContext(call responseToolCall, mode string, toolCtx responseToolContext) string {
	toolMode := normalizeSearchToolMode(mode)
	switch strings.TrimSpace(call.Name) {
	case "get_current_time":
		return executeGetCurrentTimeTool(call.Arguments)
	case imageToolNameResponseImage:
		if s.imageToolMode() != imageToolModeResponses {
			return disabledToolJSON(imageToolNameResponseImage, "response_image is disabled because Responses image mode is not active")
		}
		return s.executeResponseImageTool(call.Arguments, toolCtx)
	case imageToolNameChatImage:
		if s.imageToolMode() != imageToolModeChatCompletions {
			return disabledToolJSON(imageToolNameChatImage, "chat_image is disabled because Chat Completions image mode is not active")
		}
		return s.executeChatImageTool(call.Arguments, toolCtx)
	case "image_generate":
		if s.imageToolMode() != imageToolModeImageAPI {
			return disabledToolJSON(imageToolNameGenerate, "image_generate is disabled because Image API mode is not active")
		}
		return s.executeImageGenerateToolWithContext(call.Arguments, toolCtx)
	case "image_edit":
		if s.imageToolMode() != imageToolModeImageAPI {
			return disabledToolJSON(imageToolNameEdit, "image_edit is disabled because Image API mode is not active")
		}
		return s.executeImageEditTool(call, toolCtx)
	case "web_search":
		if toolMode != searchToolModeUniFuncs {
			return disabledToolJSON("web_search", "web_search is disabled because Searching mode is active")
		}
		return s.executeWebSearchTool(call.Arguments)
	case "web_reader":
		if toolMode != searchToolModeUniFuncs {
			return disabledToolJSON("web_reader", "web_reader is disabled because Searching mode is active")
		}
		return s.executeWebReaderTool(call.Arguments)
	case "searching":
		if toolMode != searchToolModeSearching {
			return disabledToolJSON("searching", "searching is disabled because UniFuncs mode is active")
		}
		return s.executeSearchingTool(call.Arguments)
	default:
		return mustJSONString(map[string]interface{}{
			"ok":    false,
			"tool":  call.Name,
			"error": "不支持的工具",
		})
	}
}

func disabledToolJSON(tool, message string) string {
	return mustJSONString(map[string]interface{}{
		"ok":    false,
		"tool":  tool,
		"error": message,
	})
}

func executeGetCurrentTimeTool(arguments string) string {
	var args struct {
		Timezone string `json:"timezone"`
	}
	if strings.TrimSpace(arguments) != "" {
		_ = json.Unmarshal([]byte(arguments), &args)
	}
	timezone := strings.TrimSpace(args.Timezone)
	location := time.Local
	resolved := location.String()
	if timezone != "" {
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			return mustJSONString(map[string]interface{}{
				"ok":       false,
				"timezone": timezone,
				"error":    "invalid timezone",
			})
		}
		location = loc
		resolved = timezone
	}
	now := time.Now().In(location)
	return mustJSONString(map[string]interface{}{
		"ok":       true,
		"timezone": resolved,
		"utc":      now.UTC().Format(time.RFC3339),
		"local":    now.Format(time.RFC3339),
		"unix":     now.Unix(),
	})
}

func (s *Server) executeWebSearchTool(arguments string) string {
	var args struct {
		Query         string `json:"query"`
		Freshness     string `json:"freshness"`
		IncludeImages bool   `json:"include_images"`
		Page          int    `json:"page"`
		Count         int    `json:"count"`
	}
	if strings.TrimSpace(arguments) != "" {
		_ = json.Unmarshal([]byte(arguments), &args)
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return mustJSONString(map[string]interface{}{
			"ok":    false,
			"tool":  "web_search",
			"error": "query is required",
		})
	}
	apiKey := strings.TrimSpace(s.getUniFuncsAPIKey())
	if apiKey == "" {
		return mustJSONString(map[string]interface{}{
			"ok":    false,
			"tool":  "web_search",
			"query": query,
			"error": "UNIFUNCS_API_KEY is not configured",
		})
	}
	page := args.Page
	if page <= 0 {
		page = 1
	}
	count := args.Count
	if count <= 0 {
		count = 5
	}
	if displayCount := s.webSearchCardResultCount(); displayCount > count {
		count = displayCount
	}
	if count > 10 {
		count = 10
	}
	endpoint, err := s.uniFuncsSearchEndpoint()
	if err != nil {
		return webSearchErrorJSON(query, err.Error())
	}
	freshness := strings.TrimSpace(args.Freshness)
	body, err := s.runUniFuncsWebSearch(endpoint, apiKey, query, freshness, args.IncludeImages, page, count)
	if err != nil {
		return webSearchErrorJSON(query, err.Error())
	}
	if freshness != "" {
		currentCount := uniFuncsWebSearchResultCount(body)
		if currentCount >= 0 && currentCount < count {
			if retryBody, retryErr := s.runUniFuncsWebSearch(endpoint, apiKey, query, "", args.IncludeImages, page, count); retryErr == nil {
				if retryCount := uniFuncsWebSearchResultCount(retryBody); retryCount > currentCount {
					body = retryBody
				}
			}
		}
	}
	return normalizeWebSearchResponse(query, body)
}

func (s *Server) runUniFuncsWebSearch(endpoint, apiKey, query, freshness string, includeImages bool, page, count int) ([]byte, error) {
	requestBody := map[string]interface{}{
		"query":         query,
		"includeImages": includeImages,
		"page":          page,
		"count":         count,
	}
	if freshness != "" {
		requestBody["freshness"] = freshness
	}
	raw, _ := json.Marshal(requestBody)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func webSearchErrorJSON(query, message string) string {
	return mustJSONString(map[string]interface{}{
		"ok":    false,
		"tool":  "web_search",
		"query": query,
		"error": strings.TrimSpace(message),
	})
}

func (s *Server) executeWebReaderTool(arguments string) string {
	var args struct {
		URL           string `json:"url"`
		Format        string `json:"format"`
		LiteMode      bool   `json:"lite_mode"`
		IncludeImages *bool  `json:"include_images"`
		MaxWords      int    `json:"max_words"`
		Topic         string `json:"topic"`
	}
	if strings.TrimSpace(arguments) != "" {
		_ = json.Unmarshal([]byte(arguments), &args)
	}
	pageURL := strings.TrimSpace(args.URL)
	if pageURL == "" {
		return webReaderErrorJSON("", "url is required")
	}
	apiKey := strings.TrimSpace(s.getUniFuncsAPIKey())
	if apiKey == "" {
		return webReaderErrorJSON(pageURL, "UNIFUNCS_API_KEY is not configured")
	}
	format := strings.ToLower(strings.TrimSpace(args.Format))
	if format == "" {
		format = "md"
	}
	if format != "markdown" && format != "md" && format != "text" && format != "txt" {
		return webReaderErrorJSON(pageURL, "format must be markdown, md, text, or txt")
	}
	maxWords := args.MaxWords
	if maxWords <= 0 {
		maxWords = defaultWebReaderMaxWords
	}
	if maxWords > maxWebReaderMaxWords {
		maxWords = maxWebReaderMaxWords
	}
	endpoint, err := s.uniFuncsWebReaderEndpoint()
	if err != nil {
		return webReaderErrorJSON(pageURL, err.Error())
	}
	body, status, requestID, err := s.runUniFuncsWebReader(endpoint, apiKey, pageURL, format, args.LiteMode, args.IncludeImages, maxWords, strings.TrimSpace(args.Topic))
	if err != nil {
		return webReaderErrorJSON(pageURL, err.Error())
	}
	return normalizeWebReaderResponse(pageURL, format, status, requestID, body)
}

func (s *Server) runUniFuncsWebReader(endpoint, apiKey, pageURL, format string, liteMode bool, includeImages *bool, maxWords int, topic string) ([]byte, string, string, error) {
	requestBody := map[string]interface{}{
		"url":      pageURL,
		"format":   format,
		"liteMode": liteMode,
		"maxWords": maxWords,
	}
	if includeImages != nil {
		requestBody["includeImages"] = *includeImages
	}
	if topic != "" {
		requestBody["topic"] = topic
	}
	raw, _ := json.Marshal(requestBody)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	status := strings.TrimSpace(resp.Header.Get("X-Unifuncs-Status"))
	requestID := strings.TrimSpace(firstNonEmpty(resp.Header.Get("X-Unifuncs-Request-Id"), resp.Header.Get("X-Unifuncs-Request-ID"), resp.Header.Get("X-Request-Id"), resp.Header.Get("X-Request-ID")))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, status, requestID, fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if status != "" && status != "0" {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = "UniFuncs returned status " + status
		}
		return nil, status, requestID, fmt.Errorf("%s", message)
	}
	return body, status, requestID, nil
}

func webReaderErrorJSON(pageURL, message string) string {
	return mustJSONString(map[string]interface{}{
		"ok":    false,
		"tool":  "web_reader",
		"url":   pageURL,
		"error": strings.TrimSpace(message),
	})
}

func (s *Server) executeSearchingTool(arguments string) string {
	var args struct {
		Query string `json:"query"`
	}
	if strings.TrimSpace(arguments) != "" {
		_ = json.Unmarshal([]byte(arguments), &args)
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return searchingErrorJSON("", "query is required")
	}
	baseURL := strings.TrimSpace(s.settingValue("searching_base_url"))
	apiKey := strings.TrimSpace(s.settingValue("searching_api_key"))
	model := strings.TrimSpace(s.settingValue("searching_model"))
	apiID := strings.TrimSpace(s.settingValue("searching_api_id"))
	if baseURL == "" || apiKey == "" || model == "" {
		return searchingErrorJSON(query, "searching_base_url, searching_api_key, and searching_model must be configured")
	}
	endpoint, err := openAICompatibleChatCompletionsEndpoint(baseURL)
	if err != nil {
		return searchingErrorJSON(query, err.Error())
	}
	content, err := s.runSearchingLLM(endpoint, apiKey, model, apiID, query)
	if err != nil {
		return searchingErrorJSON(query, err.Error())
	}
	return normalizeSearchingResponse(query, model, apiID, content)
}

func (s *Server) runSearchingLLM(endpoint, apiKey, model, apiID, query string) (string, error) {
	requestBody := map[string]interface{}{
		"model":  model,
		"stream": false,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are a web-enabled search model. Search the live web when needed, return useful source-aware findings, and include relevant URLs when available.",
			},
			{
				"role":    "user",
				"content": query,
			},
		},
	}
	if apiID != "" {
		requestBody["id"] = apiID
		requestBody["api_id"] = apiID
	}
	raw, _ := json.Marshal(requestBody)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	content, err := extractSearchingContent(body)
	if err != nil {
		return "", err
	}
	return content, nil
}

func openAICompatibleChatCompletionsEndpoint(base string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		parsed.Path = path
	case strings.HasSuffix(path, "/v1"):
		parsed.Path = path + "/chat/completions"
	default:
		parsed.Path = path + "/v1/chat/completions"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func extractSearchingContent(body []byte) (string, error) {
	var payload struct {
		Choices []struct {
			Message struct {
				Content interface{} `json:"content"`
			} `json:"message"`
			Delta struct {
				Content interface{} `json:"content"`
			} `json:"delta"`
			Text string `json:"text"`
		} `json:"choices"`
		Output string `json:"output"`
		Text   string `json:"text"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		content := strings.TrimSpace(string(body))
		if content == "" {
			return "", fmt.Errorf("empty searching response")
		}
		return content, nil
	}
	if strings.TrimSpace(payload.Error.Message) != "" {
		return "", fmt.Errorf("%s", strings.TrimSpace(payload.Error.Message))
	}
	for _, choice := range payload.Choices {
		if content := contentValueToString(choice.Message.Content); content != "" {
			return content, nil
		}
		if content := contentValueToString(choice.Delta.Content); content != "" {
			return content, nil
		}
		if strings.TrimSpace(choice.Text) != "" {
			return strings.TrimSpace(choice.Text), nil
		}
	}
	if strings.TrimSpace(payload.Output) != "" {
		return strings.TrimSpace(payload.Output), nil
	}
	if strings.TrimSpace(payload.Text) != "" {
		return strings.TrimSpace(payload.Text), nil
	}
	content := strings.TrimSpace(string(body))
	if content == "" {
		return "", fmt.Errorf("empty searching response")
	}
	return content, nil
}

func contentValueToString(value interface{}) string {
	switch item := value.(type) {
	case string:
		return strings.TrimSpace(item)
	case []interface{}:
		var parts []string
		for _, part := range item {
			obj, ok := part.(map[string]interface{})
			if !ok {
				continue
			}
			if text, ok := obj["text"].(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
			if text, ok := obj["content"].(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	default:
		return ""
	}
}

func normalizeSearchingResponse(query, model, apiID, content string) string {
	content = strings.TrimSpace(content)
	truncated := false
	runes := []rune(content)
	if len(runes) > maxSearchingOutputRunes {
		content = string(runes[:maxSearchingOutputRunes])
		truncated = true
	}
	result := map[string]interface{}{
		"ok":        true,
		"tool":      "searching",
		"query":     query,
		"model":     model,
		"content":   content,
		"truncated": truncated,
	}
	if apiID != "" {
		result["api_id"] = apiID
	}
	return mustJSONString(result)
}

func searchingErrorJSON(query, message string) string {
	return mustJSONString(map[string]interface{}{
		"ok":    false,
		"tool":  "searching",
		"query": query,
		"error": strings.TrimSpace(message),
	})
}

func (s *Server) getUniFuncsAPIKey() string {
	if value := strings.TrimSpace(s.settingValue("unifuncs_api_key")); value != "" {
		return value
	}
	return strings.TrimSpace(s.cfg.UniFuncsAPIKey)
}

func (s *Server) getUniFuncsBaseURL() string {
	if value := strings.TrimSpace(s.settingValue("unifuncs_base_url")); value != "" {
		return value
	}
	return strings.TrimSpace(s.cfg.UniFuncsBaseURL)
}

func (s *Server) uniFuncsSearchEndpoint() (string, error) {
	base := strings.TrimSpace(s.getUniFuncsBaseURL())
	if base == "" {
		base = "https://api.unifuncs.com"
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/api/web-search/search"):
		parsed.Path = strings.TrimRight(path, "/")
	case strings.HasSuffix(path, "/api"):
		parsed.Path = strings.TrimSuffix(path, "/api") + "/api/web-search/search"
	default:
		parsed.Path = path + "/api/web-search/search"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func (s *Server) uniFuncsWebReaderEndpoint() (string, error) {
	base := strings.TrimSpace(s.getUniFuncsBaseURL())
	if base == "" {
		base = "https://api.unifuncs.com"
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/api/web-reader/read"):
		parsed.Path = strings.TrimRight(path, "/")
	case strings.HasSuffix(path, "/api/web-reader"):
		parsed.Path = path + "/read"
	case strings.HasSuffix(path, "/api/web-search/search"):
		parsed.Path = strings.TrimSuffix(path, "/api/web-search/search") + "/api/web-reader/read"
	case strings.HasSuffix(path, "/api"):
		parsed.Path = strings.TrimSuffix(path, "/api") + "/api/web-reader/read"
	default:
		parsed.Path = path + "/api/web-reader/read"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func (s *Server) settingValue(key string) string {
	if s == nil || s.store == nil || s.store.DB == nil {
		return ""
	}
	var value string
	err := s.store.DB.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return ""
	}
	return value
}

func normalizeWebSearchResponse(query string, body []byte) string {
	var payload struct {
		Code      json.Number `json:"code"`
		Message   string      `json:"message"`
		RequestID string      `json:"requestId"`
		ReqID     string      `json:"request_id"`
		Data      struct {
			WebPages []struct {
				Name       string `json:"name"`
				URL        string `json:"url"`
				DisplayURL string `json:"displayUrl"`
				Snippet    string `json:"snippet"`
				Summary    string `json:"summary"`
				SiteName   string `json:"siteName"`
				SiteIcon   string `json:"siteIcon"`
				Date       string `json:"datePublished"`
			} `json:"webPages"`
			Images []struct {
				Name         string `json:"name"`
				ContentURL   string `json:"contentUrl"`
				ThumbnailURL string `json:"thumbnailUrl"`
				HostPageURL  string `json:"hostPageUrl"`
			} `json:"images"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return webSearchErrorJSON(query, "invalid UniFuncs response: "+err.Error())
	}
	code := 0
	if payload.Code != "" {
		if parsed, err := payload.Code.Int64(); err == nil {
			code = int(parsed)
		}
	}
	requestID := firstNonEmpty(payload.RequestID, payload.ReqID)
	if code != 0 {
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = "UniFuncs returned an error"
		}
		return mustJSONString(map[string]interface{}{
			"ok":         false,
			"tool":       "web_search",
			"query":      query,
			"code":       code,
			"message":    message,
			"request_id": requestID,
		})
	}
	results := make([]map[string]interface{}, 0, len(payload.Data.WebPages))
	for _, item := range payload.Data.WebPages {
		result := map[string]interface{}{
			"title":   strings.TrimSpace(item.Name),
			"url":     strings.TrimSpace(item.URL),
			"snippet": strings.TrimSpace(firstNonEmpty(item.Summary, item.Snippet)),
		}
		if displayURL := strings.TrimSpace(item.DisplayURL); displayURL != "" {
			result["display_url"] = displayURL
		}
		if siteName := strings.TrimSpace(item.SiteName); siteName != "" {
			result["site_name"] = siteName
		}
		if siteIcon := strings.TrimSpace(item.SiteIcon); siteIcon != "" {
			result["site_icon"] = siteIcon
		}
		if date := strings.TrimSpace(item.Date); date != "" {
			result["date"] = date
		}
		results = append(results, result)
	}
	images := make([]map[string]interface{}, 0, len(payload.Data.Images))
	for _, item := range payload.Data.Images {
		images = append(images, map[string]interface{}{
			"title":         strings.TrimSpace(item.Name),
			"content_url":   strings.TrimSpace(item.ContentURL),
			"thumbnail_url": strings.TrimSpace(item.ThumbnailURL),
			"source_url":    strings.TrimSpace(item.HostPageURL),
		})
	}
	return mustJSONString(map[string]interface{}{
		"ok":         true,
		"tool":       "web_search",
		"query":      query,
		"request_id": requestID,
		"results":    results,
		"images":     images,
	})
}

func normalizeWebReaderResponse(pageURL, format, status, requestID string, body []byte) string {
	content := strings.TrimSpace(string(body))
	truncated := false
	runes := []rune(content)
	if len(runes) > maxWebReaderOutputRunes {
		content = string(runes[:maxWebReaderOutputRunes])
		truncated = true
	}
	title := webReaderTitle(content)
	result := map[string]interface{}{
		"ok":        true,
		"tool":      "web_reader",
		"url":       pageURL,
		"format":    format,
		"content":   content,
		"truncated": truncated,
	}
	if title != "" {
		result["title"] = title
	}
	if status != "" {
		result["status"] = status
	}
	if requestID != "" {
		result["request_id"] = requestID
	}
	return mustJSONString(result)
}

func webReaderTitle(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func uniFuncsWebSearchResultCount(body []byte) int {
	var payload struct {
		Code json.Number `json:"code"`
		Data struct {
			WebPages []struct{} `json:"webPages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return -1
	}
	if payload.Code != "" {
		if parsed, err := payload.Code.Int64(); err == nil && parsed != 0 {
			return -1
		}
	}
	return len(payload.Data.WebPages)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func mustJSONString(value interface{}) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return `{"ok":false,"error":"tool serialization failed"}`
	}
	return string(raw)
}

var _ = sql.ErrNoRows
