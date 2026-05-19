package httpapi

import (
	"chatxxx/backend/internal/db"
	"database/sql"
	"net/http"
	"strings"
)

type authRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "请求体必须是 JSON")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = req.Email
	}
	if req.Email == "" || len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "邮箱不能为空，密码至少 8 位")
		return
	}
	count, err := s.userCount()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "读取用户失败")
		return
	}
	role := "user"
	if count == 0 {
		role = "admin"
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "密码处理失败")
		return
	}
	now := db.Now()
	res, err := s.store.DB.Exec(`
		INSERT INTO users (email, name, password_hash, role, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', ?, ?)
	`, req.Email, req.Name, hash, role, now, now)
	if err != nil {
		writeError(w, http.StatusConflict, "USER_EXISTS", "用户已存在")
		return
	}
	userID, _ := res.LastInsertId()
	if err := s.createSession(w, userID); err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "创建会话失败")
		return
	}
	user, _ := scanUser(s.store.DB.QueryRow(`SELECT id, email, name, role, status, created_at, updated_at FROM users WHERE id = ?`, userID))
	writeJSON(w, http.StatusCreated, map[string]interface{}{"user": user})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "请求体必须是 JSON")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	var user User
	var hash string
	err := s.store.DB.QueryRow(`
		SELECT id, email, name, password_hash, role, status, created_at, updated_at
		FROM users WHERE email = ?
	`, email).Scan(&user.ID, &user.Email, &user.Name, &hash, &user.Role, &user.Status, &user.CreatedAt, &user.UpdatedAt)
	if err != nil || !checkPassword(hash, req.Password) {
		writeError(w, http.StatusUnauthorized, "LOGIN_FAILED", "邮箱或密码不正确")
		return
	}
	if user.Status != "active" {
		writeError(w, http.StatusForbidden, "USER_DISABLED", "账户已被禁用")
		return
	}
	if err := s.createSession(w, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "创建会话失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"user": user})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSession(w, r)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"user": user})
}

func isNotFound(err error) bool {
	return err == sql.ErrNoRows
}
