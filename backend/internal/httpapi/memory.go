package httpapi

import (
	"chatxxx/backend/internal/db"
	"net/http"
	"strings"
)

type Memory struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	Content   string `json:"content"`
	Source    string `json:"source"`
	Category  string `json:"category"`
	Weight    int64  `json:"weight"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type memoryRequest struct {
	Content  string `json:"content"`
	Category string `json:"category"`
	Weight   int64  `json:"weight"`
	Enabled  *bool  `json:"enabled"`
}

func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	rows, err := s.store.DB.Query(`
		SELECT id, user_id, content, source, category, weight, enabled, created_at, updated_at
		FROM memories WHERE user_id=? AND deleted_at IS NULL ORDER BY updated_at DESC, id DESC LIMIT 200
	`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "读取记忆失败")
		return
	}
	defer rows.Close()
	items := make([]Memory, 0)
	var enabledCount int
	for rows.Next() {
		var m Memory
		var enabled int
		_ = rows.Scan(&m.ID, &m.UserID, &m.Content, &m.Source, &m.Category, &m.Weight, &enabled, &m.CreatedAt, &m.UpdatedAt)
		m.Enabled = enabled == 1
		if m.Enabled {
			enabledCount++
		}
		items = append(items, m)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"memories":      items,
		"total":         len(items),
		"enabled_count": enabledCount,
		"manual_count":  len(items),
		"auto_count":    0,
		"page":          1,
		"per_page":      200,
		"has_more":      false,
		"embedding":     map[string]interface{}{"enabled": false, "model": "", "pending_count": 0},
	})
}

func (s *Server) handleCreateMemory(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	var req memoryRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "请求体必须是 JSON")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "记忆内容不能为空")
		return
	}
	now := db.Now()
	res, err := s.store.DB.Exec(`
		INSERT INTO memories (user_id, content, source, category, weight, enabled, created_at, updated_at)
		VALUES (?, ?, 'manual', ?, ?, 1, ?, ?)
	`, user.ID, content, strings.TrimSpace(req.Category), req.Weight, now, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "保存记忆失败")
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]interface{}{"memory": Memory{ID: id, UserID: user.ID, Content: content, Source: "manual", Category: req.Category, Weight: req.Weight, Enabled: true, CreatedAt: now, UpdatedAt: now}})
}

func (s *Server) handleUpdateMemory(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, ok := paramID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "无效记忆 ID")
		return
	}
	var req memoryRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "请求体必须是 JSON")
		return
	}
	enabled := 1
	if req.Enabled != nil && !*req.Enabled {
		enabled = 0
	}
	_, err := s.store.DB.Exec(`UPDATE memories SET content=?, category=?, weight=?, enabled=?, updated_at=? WHERE id=? AND user_id=?`, strings.TrimSpace(req.Content), strings.TrimSpace(req.Category), req.Weight, enabled, db.Now(), id, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "更新记忆失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, ok := paramID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "无效记忆 ID")
		return
	}
	_, _ = s.store.DB.Exec(`UPDATE memories SET deleted_at=?, updated_at=? WHERE id=? AND user_id=?`, db.Now(), db.Now(), id, user.ID)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}
