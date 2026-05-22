package httpapi

import (
	"chatxxx/backend/internal/db"
	"database/sql"
	"net/http"
	"strings"
)

type Memory struct {
	ID                 int64  `json:"id"`
	UserID             int64  `json:"user_id"`
	Content            string `json:"content"`
	Source             string `json:"source"`
	Category           string `json:"category"`
	Weight             int64  `json:"-"`
	Origin             string `json:"origin"`
	Tokens             int64  `json:"tokens"`
	Enabled            bool   `json:"enabled"`
	EmbeddingModel     string `json:"embedding_model"`
	EmbeddingDim       int64  `json:"embedding_dim"`
	EmbeddingUpdatedAt string `json:"embedding_updated_at"`
	EmbeddingStatus    string `json:"embedding_status"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

type memoryRequest struct {
	Enabled *bool `json:"enabled"`
}

func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	currentEmbeddingModel := ""
	if provider, err := s.getEmbeddingProvider(); err == nil && provider != nil {
		currentEmbeddingModel = provider.Model
	}
	rows, err := s.store.DB.Query(`
		SELECT id, user_id, content, source, category, weight, origin, tokens, enabled, embedding_model, embedding_dim, embedding_updated_at, (embedding IS NOT NULL), created_at, updated_at
		FROM memories WHERE user_id=? AND deleted_at IS NULL ORDER BY enabled DESC, updated_at DESC, id DESC LIMIT 500
	`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "读取记忆失败")
		return
	}
	defer rows.Close()
	items := make([]Memory, 0)
	var enabledCount int
	var manualCount int
	var autoCount int
	var pendingCount int
	for rows.Next() {
		m, err := scanMemory(rows, currentEmbeddingModel)
		if err != nil {
			continue
		}
		if m.Enabled {
			enabledCount++
		}
		if m.Source == "manual" || m.Source == "imported" {
			manualCount++
		} else {
			autoCount++
		}
		if m.EmbeddingStatus == "pending" || m.EmbeddingStatus == "stale" {
			pendingCount++
		}
		items = append(items, m)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"memories":      items,
		"total":         len(items),
		"enabled_count": enabledCount,
		"manual_count":  manualCount,
		"auto_count":    autoCount,
		"page":          1,
		"per_page":      500,
		"has_more":      false,
		"embedding":     map[string]interface{}{"enabled": currentEmbeddingModel != "", "model": currentEmbeddingModel, "pending_count": pendingCount},
	})
}

func (s *Server) handleCreateMemory(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusForbidden, "MANUAL_MEMORY_DISABLED", "长期记忆由系统自动维护，不能手动新增")
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
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "只能更新 enabled 状态")
		return
	}
	enabled := 0
	if *req.Enabled {
		enabled = 1
	}
	_, err := s.store.DB.Exec(`UPDATE memories SET enabled=?, updated_at=? WHERE id=? AND user_id=? AND deleted_at IS NULL`, enabled, db.Now(), id, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "更新记忆失败")
		return
	}
	m, err := s.memoryForUser(id, user.ID, "")
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "记忆不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"memory": m})
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

func (s *Server) memoryForUser(id, userID int64, currentEmbeddingModel string) (Memory, error) {
	row := s.store.DB.QueryRow(`
		SELECT id, user_id, content, source, category, weight, origin, tokens, enabled, embedding_model, embedding_dim, embedding_updated_at, (embedding IS NOT NULL), created_at, updated_at
		FROM memories
		WHERE id=? AND user_id=? AND deleted_at IS NULL
	`, id, userID)
	return scanMemory(row, currentEmbeddingModel)
}

func scanMemory(row rowScanner, currentEmbeddingModel string) (Memory, error) {
	var m Memory
	var enabled int
	var hasEmbedding int
	var embeddingUpdatedAt sql.NullString
	if err := row.Scan(&m.ID, &m.UserID, &m.Content, &m.Source, &m.Category, &m.Weight, &m.Origin, &m.Tokens, &enabled, &m.EmbeddingModel, &m.EmbeddingDim, &embeddingUpdatedAt, &hasEmbedding, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return Memory{}, err
	}
	m.Enabled = enabled == 1
	if embeddingUpdatedAt.Valid {
		m.EmbeddingUpdatedAt = embeddingUpdatedAt.String
	}
	switch {
	case strings.TrimSpace(currentEmbeddingModel) == "":
		if hasEmbedding == 1 {
			m.EmbeddingStatus = "ready"
		} else {
			m.EmbeddingStatus = "disabled"
		}
	case hasEmbedding == 0:
		m.EmbeddingStatus = "pending"
	case m.EmbeddingModel == currentEmbeddingModel:
		m.EmbeddingStatus = "ready"
	default:
		m.EmbeddingStatus = "stale"
	}
	return m, nil
}
