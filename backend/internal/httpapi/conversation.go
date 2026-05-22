package httpapi

import (
	"chatxxx/backend/internal/db"
	"database/sql"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
)

type Conversation struct {
	ID                int64  `json:"id"`
	SessionID         string `json:"session_id"`
	UserID            int64  `json:"user_id"`
	Title             string `json:"title"`
	SystemPrompt      string `json:"system_prompt"`
	Summary           string `json:"summary"`
	MemoryEnabled     bool   `json:"memory_enabled"`
	Pinned            bool   `json:"pinned"`
	Archived          bool   `json:"archived"`
	ArchiveCategoryID int64  `json:"archive_category_id"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

type Message struct {
	ID                  int64   `json:"id"`
	ConversationID      int64   `json:"conversation_id"`
	UserID              int64   `json:"user_id"`
	Role                string  `json:"role"`
	Content             string  `json:"content"`
	ReasoningContent    string  `json:"reasoning_content"`
	Status              string  `json:"status"`
	Attachments         string  `json:"attachments"`
	Metadata            string  `json:"metadata"`
	VersionGroupID      int64   `json:"version_group_id"`
	VersionIndex        int64   `json:"version_index"`
	IsActiveVersion     bool    `json:"is_active_version"`
	ParentUserMessageID int64   `json:"parent_user_message_id"`
	SortOrder           float64 `json:"sort_order"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
}

type conversationRequest struct {
	Title             string `json:"title"`
	SystemPrompt      string `json:"system_prompt"`
	MemoryEnabled     *bool  `json:"memory_enabled"`
	Pinned            *bool  `json:"pinned"`
	Archived          *bool  `json:"archived"`
	ArchiveCategoryID int64  `json:"archive_category_id"`
}

var conversationSessionIDPattern = regexp.MustCompile(`^[a-f0-9]{32,64}$`)

func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	archived := r.URL.Query().Get("archived") == "1"
	rows, err := s.store.DB.Query(`
		SELECT id, session_id, user_id, title, system_prompt, summary, memory_enabled, pinned, archived, archive_category_id, created_at, updated_at
		FROM conversations
		WHERE user_id = ? AND deleted_at IS NULL AND archived = ?
		ORDER BY pinned DESC, updated_at DESC, id DESC
	`, user.ID, boolInt(archived))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "读取会话失败")
		return
	}
	defer rows.Close()
	items := make([]Conversation, 0)
	for rows.Next() {
		if c, err := scanConversationRows(rows); err == nil {
			items = append(items, c)
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"conversations": items})
}

func (s *Server) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	var req conversationRequest
	_ = readJSON(r, &req)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "新对话"
	}
	now := db.Now()
	sessionID := s.newConversationSessionID()
	res, err := s.store.DB.Exec(`
		INSERT INTO conversations (session_id, user_id, title, system_prompt, summary, memory_enabled, pinned, archived, archive_category_id, created_at, updated_at)
		VALUES (?, ?, ?, '', '', 1, 0, 0, 0, ?, ?)
	`, sessionID, user.ID, title, now, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "创建会话失败")
		return
	}
	id, _ := res.LastInsertId()
	c, _ := s.getConversationForUser(id, user.ID)
	writeJSON(w, http.StatusCreated, map[string]interface{}{"conversation": c})
}

func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, ok := paramID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "无效会话 ID")
		return
	}
	c, err := s.getConversationForUser(id, user.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "会话不存在")
		return
	}
	messages, err := s.listMessages(id, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "读取消息失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"conversation": c, "messages": messages})
}

func (s *Server) handleGetConversationBySession(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	sessionID := strings.TrimSpace(chi.URLParam(r, "sessionID"))
	if !validConversationSessionID(sessionID) {
		writeError(w, http.StatusBadRequest, "INVALID_SESSION", "无效会话 Session")
		return
	}
	c, err := s.getConversationForUserBySession(sessionID, user.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "会话不存在")
		return
	}
	messages, err := s.listMessages(c.ID, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "读取消息失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"conversation": c, "messages": messages})
}

func (s *Server) handleUpdateConversation(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, ok := paramID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "无效会话 ID")
		return
	}
	current, err := s.getConversationForUser(id, user.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "会话不存在")
		return
	}
	var req conversationRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "请求体必须是 JSON")
		return
	}
	if strings.TrimSpace(req.Title) != "" {
		current.Title = strings.TrimSpace(req.Title)
	}
	current.SystemPrompt = req.SystemPrompt
	if req.MemoryEnabled != nil {
		current.MemoryEnabled = *req.MemoryEnabled
	}
	if req.Pinned != nil {
		current.Pinned = *req.Pinned
	}
	if req.Archived != nil {
		current.Archived = *req.Archived
	}
	if req.ArchiveCategoryID >= 0 {
		current.ArchiveCategoryID = req.ArchiveCategoryID
	}
	_, err = s.store.DB.Exec(`
		UPDATE conversations SET title=?, system_prompt=?, memory_enabled=?, pinned=?, archived=?, archive_category_id=?, updated_at=?
		WHERE id=? AND user_id=?
	`, current.Title, current.SystemPrompt, boolInt(current.MemoryEnabled), boolInt(current.Pinned), boolInt(current.Archived), current.ArchiveCategoryID, db.Now(), id, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "更新会话失败")
		return
	}
	c, _ := s.getConversationForUser(id, user.ID)
	writeJSON(w, http.StatusOK, map[string]interface{}{"conversation": c})
}

func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, ok := paramID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "无效会话 ID")
		return
	}
	_, _ = s.store.DB.Exec(`UPDATE conversations SET deleted_at=?, updated_at=? WHERE id=? AND user_id=?`, db.Now(), db.Now(), id, user.ID)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) handleClearConversations(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	now := db.Now()
	_, _ = s.store.DB.Exec(`UPDATE generation_runs SET status='stopping', updated_at=? WHERE user_id=? AND status IN ('running','stopping')`, now, user.ID)
	res, err := s.store.DB.Exec(`UPDATE conversations SET deleted_at=?, updated_at=? WHERE user_id=? AND deleted_at IS NULL`, now, now, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "清除历史对话失败")
		return
	}
	count, _ := res.RowsAffected()
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "deleted": count})
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, ok := paramID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "无效消息 ID")
		return
	}
	_, _ = s.store.DB.Exec(`UPDATE messages SET deleted_at=?, updated_at=? WHERE id=? AND user_id=?`, db.Now(), db.Now(), id, user.ID)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) handleSwitchMessageVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "message": "message version switching is reserved"})
}

func (s *Server) getConversationForUser(id, userID int64) (*Conversation, error) {
	row := s.store.DB.QueryRow(`
		SELECT id, session_id, user_id, title, system_prompt, summary, memory_enabled, pinned, archived, archive_category_id, created_at, updated_at
		FROM conversations WHERE id=? AND user_id=? AND deleted_at IS NULL
	`, id, userID)
	return scanConversation(row)
}

func (s *Server) getConversationForUserBySession(sessionID string, userID int64) (*Conversation, error) {
	row := s.store.DB.QueryRow(`
		SELECT id, session_id, user_id, title, system_prompt, summary, memory_enabled, pinned, archived, archive_category_id, created_at, updated_at
		FROM conversations WHERE session_id=? AND user_id=? AND deleted_at IS NULL
	`, sessionID, userID)
	return scanConversation(row)
}

func scanConversation(row *sql.Row) (*Conversation, error) {
	var c Conversation
	var memory, pinned, archived int
	err := row.Scan(&c.ID, &c.SessionID, &c.UserID, &c.Title, &c.SystemPrompt, &c.Summary, &memory, &pinned, &archived, &c.ArchiveCategoryID, &c.CreatedAt, &c.UpdatedAt)
	c.MemoryEnabled = memory == 1
	c.Pinned = pinned == 1
	c.Archived = archived == 1
	return &c, err
}

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanConversationRows(row rowScanner) (Conversation, error) {
	var c Conversation
	var memory, pinned, archived int
	err := row.Scan(&c.ID, &c.SessionID, &c.UserID, &c.Title, &c.SystemPrompt, &c.Summary, &memory, &pinned, &archived, &c.ArchiveCategoryID, &c.CreatedAt, &c.UpdatedAt)
	c.MemoryEnabled = memory == 1
	c.Pinned = pinned == 1
	c.Archived = archived == 1
	return c, err
}

func (s *Server) newConversationSessionID() string {
	for i := 0; i < 8; i++ {
		sessionID := randomID()
		var exists int
		err := s.store.DB.QueryRow(`SELECT 1 FROM conversations WHERE session_id=? LIMIT 1`, sessionID).Scan(&exists)
		if err == sql.ErrNoRows {
			return sessionID
		}
	}
	return randomID()
}

func validConversationSessionID(sessionID string) bool {
	return conversationSessionIDPattern.MatchString(strings.TrimSpace(sessionID))
}

func (s *Server) listMessages(conversationID, userID int64) ([]Message, error) {
	rows, err := s.store.DB.Query(`
		SELECT id, conversation_id, user_id, role, content, reasoning_content, status, attachments, metadata, version_group_id, version_index, is_active_version, parent_user_message_id, sort_order, created_at, updated_at
		FROM messages
		WHERE conversation_id=? AND user_id=? AND deleted_at IS NULL AND is_active_version=1
		ORDER BY sort_order ASC, id ASC
	`, conversationID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Message, 0)
	for rows.Next() {
		var m Message
		var active int
		err := rows.Scan(&m.ID, &m.ConversationID, &m.UserID, &m.Role, &m.Content, &m.ReasoningContent, &m.Status, &m.Attachments, &m.Metadata, &m.VersionGroupID, &m.VersionIndex, &active, &m.ParentUserMessageID, &m.SortOrder, &m.CreatedAt, &m.UpdatedAt)
		if err == nil {
			m.IsActiveVersion = active == 1
			m.Metadata = sanitizeMessageMetadataForClient(m.Metadata)
			if strings.HasPrefix(strings.TrimSpace(m.Content), "模型调用失败：") && metadataHasSuccessfulImageToolStep(m.Metadata) {
				m.Content = "图片已生成。"
			}
			items = append(items, m)
		}
	}
	return items, nil
}

func (s *Server) insertMessage(conversationID, userID int64, role, content, status, metadata string) (Message, error) {
	return s.insertMessageWithAttachments(conversationID, userID, role, content, status, metadata, "[]")
}

func (s *Server) insertMessageWithAttachments(conversationID, userID int64, role, content, status, metadata, attachments string) (Message, error) {
	now := db.Now()
	if strings.TrimSpace(attachments) == "" {
		attachments = "[]"
	}
	var maxOrder sql.NullFloat64
	_ = s.store.DB.QueryRow(`SELECT MAX(sort_order) FROM messages WHERE conversation_id=?`, conversationID).Scan(&maxOrder)
	sortOrder := maxOrder.Float64 + 10
	if !maxOrder.Valid {
		sortOrder = 10
	}
	res, err := s.store.DB.Exec(`
		INSERT INTO messages (conversation_id, user_id, role, content, status, metadata, attachments, version_group_id, version_index, is_active_version, sort_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, 1, 1, ?, ?, ?)
	`, conversationID, userID, role, content, status, metadata, attachments, sortOrder, now, now)
	if err != nil {
		return Message{}, err
	}
	id, _ := res.LastInsertId()
	_, _ = s.store.DB.Exec(`UPDATE messages SET version_group_id=id WHERE id=?`, id)
	_, _ = s.store.DB.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, now, conversationID)
	msg := Message{
		ID: id, ConversationID: conversationID, UserID: userID, Role: role, Content: content,
		Status: status, Metadata: metadata, Attachments: attachments, VersionGroupID: id, VersionIndex: 1,
		IsActiveVersion: true, SortOrder: sortOrder, CreatedAt: now, UpdatedAt: now,
	}
	return msg, nil
}

func (s *Server) getMessageForUser(id, userID int64) (Message, error) {
	row := s.store.DB.QueryRow(`
		SELECT id, conversation_id, user_id, role, content, reasoning_content, status, attachments, metadata, version_group_id, version_index, is_active_version, parent_user_message_id, sort_order, created_at, updated_at
		FROM messages
		WHERE id=? AND user_id=? AND deleted_at IS NULL
	`, id, userID)
	return scanMessage(row)
}

func (s *Server) previousUserMessage(conversationID, userID int64, beforeOrder float64) (Message, error) {
	row := s.store.DB.QueryRow(`
		SELECT id, conversation_id, user_id, role, content, reasoning_content, status, attachments, metadata, version_group_id, version_index, is_active_version, parent_user_message_id, sort_order, created_at, updated_at
		FROM messages
		WHERE conversation_id=? AND user_id=? AND role='user' AND deleted_at IS NULL AND is_active_version=1 AND sort_order < ?
		ORDER BY sort_order DESC, id DESC
		LIMIT 1
	`, conversationID, userID, beforeOrder)
	return scanMessage(row)
}

func scanMessage(row rowScanner) (Message, error) {
	var m Message
	var active int
	err := row.Scan(&m.ID, &m.ConversationID, &m.UserID, &m.Role, &m.Content, &m.ReasoningContent, &m.Status, &m.Attachments, &m.Metadata, &m.VersionGroupID, &m.VersionIndex, &active, &m.ParentUserMessageID, &m.SortOrder, &m.CreatedAt, &m.UpdatedAt)
	m.IsActiveVersion = active == 1
	return m, err
}

func (s *Server) updateMessageContent(id int64, content, status string) {
	_, _ = s.store.DB.Exec(`UPDATE messages SET content=?, status=?, updated_at=? WHERE id=?`, content, status, db.Now(), id)
}

func (s *Server) updateMessageContentWithMetadata(id int64, content, status, metadata string) {
	if strings.TrimSpace(metadata) == "" {
		metadata = "{}"
	}
	_, _ = s.store.DB.Exec(`UPDATE messages SET content=?, status=?, metadata=?, updated_at=? WHERE id=?`, content, status, metadata, db.Now(), id)
}

func (s *Server) updateMessageMetadata(id int64, metadata string) {
	if strings.TrimSpace(metadata) == "" {
		metadata = "{}"
	}
	_, _ = s.store.DB.Exec(`UPDATE messages SET metadata=?, updated_at=? WHERE id=?`, metadata, db.Now(), id)
}

func (s *Server) updateUserMessageContent(id, userID int64, content, metadata string) error {
	return s.updateUserMessageContentWithAttachments(id, userID, content, metadata, "")
}

func (s *Server) updateUserMessageContentWithAttachments(id, userID int64, content, metadata, attachments string) error {
	var err error
	if strings.TrimSpace(attachments) == "" {
		_, err = s.store.DB.Exec(`UPDATE messages SET content=?, metadata=?, updated_at=? WHERE id=? AND user_id=? AND role='user'`, content, metadata, db.Now(), id, userID)
		return err
	}
	_, err = s.store.DB.Exec(`UPDATE messages SET content=?, metadata=?, attachments=?, updated_at=? WHERE id=? AND user_id=? AND role='user'`, content, metadata, attachments, db.Now(), id, userID)
	return err
}

func (s *Server) deleteMessagesFromOrder(conversationID, userID int64, fromOrder float64) error {
	_, err := s.store.DB.Exec(`UPDATE messages SET deleted_at=?, updated_at=? WHERE conversation_id=? AND user_id=? AND deleted_at IS NULL AND sort_order >= ?`, db.Now(), db.Now(), conversationID, userID, fromOrder)
	return err
}

func (s *Server) deleteMessagesAfterOrder(conversationID, userID int64, afterOrder float64) error {
	_, err := s.store.DB.Exec(`UPDATE messages SET deleted_at=?, updated_at=? WHERE conversation_id=? AND user_id=? AND deleted_at IS NULL AND sort_order > ?`, db.Now(), db.Now(), conversationID, userID, afterOrder)
	return err
}

func titleFromContent(content string) string {
	title := strings.TrimSpace(content)
	if title == "" {
		return "新对话"
	}
	runes := []rune(title)
	if len(runes) > titleFallbackMaxRunes {
		title = string(runes[:titleFallbackMaxRunes]) + "..."
	}
	return title
}

func markdownExport(c *Conversation, messages []Message) string {
	var b strings.Builder
	b.WriteString("# " + c.Title + "\n\n")
	for _, m := range messages {
		b.WriteString(fmt.Sprintf("## %s\n\n%s\n\n", m.Role, m.Content))
	}
	return b.String()
}

func (s *Server) handleExportConversation(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, ok := paramID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "无效会话 ID")
		return
	}
	c, err := s.getConversationForUser(id, user.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "会话不存在")
		return
	}
	messages, _ := s.listMessages(id, user.ID)
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="conversation.md"`)
	_, _ = w.Write([]byte(markdownExport(c, messages)))
}
