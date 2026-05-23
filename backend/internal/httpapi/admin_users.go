package httpapi

import (
	"chatxxx/backend/internal/db"
	"net/http"
	"strings"
)

type AdminUser struct {
	User
	ConversationCount int64  `json:"conversation_count"`
	MessageCount      int64  `json:"message_count"`
	MemoryCount       int64  `json:"memory_count"`
	SessionCount      int64  `json:"session_count"`
	LastSessionAt     string `json:"last_session_at"`
}

type adminUserRequest struct {
	Role   string `json:"role"`
	Status string `json:"status"`
}

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.DB.Query(`
		SELECT
			u.id, u.email, u.name, u.role, u.status, u.created_at, u.updated_at,
			(SELECT COUNT(*) FROM conversations c WHERE c.user_id = u.id AND c.deleted_at IS NULL) AS conversation_count,
			(SELECT COUNT(*) FROM messages m WHERE m.user_id = u.id AND m.deleted_at IS NULL) AS message_count,
			(SELECT COUNT(*) FROM memories mm WHERE mm.user_id = u.id AND mm.deleted_at IS NULL) AS memory_count,
			(SELECT COUNT(*) FROM sessions ss WHERE ss.user_id = u.id AND ss.expires_at > ?) AS session_count,
			COALESCE((SELECT MAX(ss.created_at) FROM sessions ss WHERE ss.user_id = u.id), '') AS last_session_at
		FROM users u
		ORDER BY
			CASE WHEN u.role = 'admin' THEN 0 ELSE 1 END,
			CASE WHEN u.status = 'active' THEN 0 ELSE 1 END,
			u.created_at DESC,
			u.id DESC
	`, db.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "读取用户失败")
		return
	}
	defer rows.Close()

	items := make([]AdminUser, 0)
	var activeCount, disabledCount, adminCount int64
	for rows.Next() {
		var item AdminUser
		if err := rows.Scan(
			&item.ID,
			&item.Email,
			&item.Name,
			&item.Role,
			&item.Status,
			&item.CreatedAt,
			&item.UpdatedAt,
			&item.ConversationCount,
			&item.MessageCount,
			&item.MemoryCount,
			&item.SessionCount,
			&item.LastSessionAt,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "读取用户失败")
			return
		}
		if item.Status == "active" {
			activeCount++
		} else {
			disabledCount++
		}
		if item.Role == "admin" {
			adminCount++
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "读取用户失败")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"users": items,
		"summary": map[string]int64{
			"total":    int64(len(items)),
			"active":   activeCount,
			"disabled": disabledCount,
			"admins":   adminCount,
		},
	})
}

func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "无效用户 ID")
		return
	}
	current := currentUser(r)
	if current == nil {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
		return
	}

	var req adminUserRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "请求体必须是 JSON")
		return
	}
	req.Role = strings.TrimSpace(req.Role)
	req.Status = strings.TrimSpace(req.Status)
	if req.Role != "admin" && req.Role != "user" {
		writeError(w, http.StatusBadRequest, "INVALID_ROLE", "角色只能是管理员或普通用户")
		return
	}
	if req.Status != "active" && req.Status != "disabled" {
		writeError(w, http.StatusBadRequest, "INVALID_STATUS", "状态只能是启用或禁用")
		return
	}
	if id == current.ID && (req.Role != "admin" || req.Status != "active") {
		writeError(w, http.StatusBadRequest, "SELF_PROTECTED", "不能停用或降级当前登录的管理员")
		return
	}

	existing, err := scanUser(s.store.DB.QueryRow(`SELECT id, email, name, role, status, created_at, updated_at FROM users WHERE id = ?`, id))
	if err != nil {
		writeError(w, http.StatusNotFound, "USER_NOT_FOUND", "用户不存在")
		return
	}
	if existing.Role == "admin" && (req.Role != "admin" || req.Status != "active") {
		var otherActiveAdmins int64
		if err := s.store.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE id <> ? AND role = 'admin' AND status = 'active'`, id).Scan(&otherActiveAdmins); err != nil {
			writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "检查管理员失败")
			return
		}
		if otherActiveAdmins == 0 {
			writeError(w, http.StatusBadRequest, "LAST_ADMIN", "至少需要保留一个启用的管理员")
			return
		}
	}

	_, err = s.store.DB.Exec(`UPDATE users SET role = ?, status = ?, updated_at = ? WHERE id = ?`, req.Role, req.Status, db.Now(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "更新用户失败")
		return
	}
	user, err := scanUser(s.store.DB.QueryRow(`SELECT id, email, name, role, status, created_at, updated_at FROM users WHERE id = ?`, id))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "读取用户失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"user": user})
}
