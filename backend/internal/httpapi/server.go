package httpapi

import (
	"chatxxx/backend/internal/config"
	"chatxxx/backend/internal/db"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
)

type contextKey string

const userContextKey contextKey = "user"

type Server struct {
	cfg   config.Config
	store *db.Store
}

type User struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func NewServer(cfg config.Config, store *db.Store) *Server {
	return &Server{cfg: cfg, store: store}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{s.cfg.CORSOrigin},
		AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"service": "chatxxx",
			"time":    db.Now(),
		})
	})
	r.Get("/api/workspace/public/*", s.handlePublicWorkspaceFile)

	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/register", s.handleRegister)
		r.Post("/login", s.handleLogin)
		r.Post("/logout", s.handleLogout)
		r.With(s.requireUser).Get("/me", s.handleMe)
	})

	r.Group(func(r chi.Router) {
		r.Use(s.requireUser)
		r.Get("/api/settings", s.handleGetClientSettings)
		r.Get("/api/workspace/files/*", s.handleWorkspaceFile)
		r.Get("/api/provider-capabilities", s.handleProviderCapabilities)
		r.Get("/api/conversations", s.handleListConversations)
		r.Post("/api/conversations", s.handleCreateConversation)
		r.Delete("/api/conversations", s.handleClearConversations)
		r.Get("/api/conversations/session/{sessionID}", s.handleGetConversationBySession)
		r.Get("/api/conversations/{id}", s.handleGetConversation)
		r.Patch("/api/conversations/{id}", s.handleUpdateConversation)
		r.Delete("/api/conversations/{id}", s.handleDeleteConversation)
		r.Delete("/api/messages/{id}", s.handleDeleteMessage)
		r.Post("/api/messages/{id}/version", s.handleSwitchMessageVersion)
		r.Get("/api/memories", s.handleListMemories)
		r.Post("/api/memories", s.handleCreateMemory)
		r.Patch("/api/memories/{id}", s.handleUpdateMemory)
		r.Delete("/api/memories/{id}", s.handleDeleteMemory)
		r.Post("/api/chat/stream", s.handleStream)
		r.Post("/api/chat/stop", s.handleStop)
		r.Get("/api/export/conversations/{id}", s.handleExportConversation)
	})

	r.Group(func(r chi.Router) {
		r.Use(s.requireUser)
		r.Use(s.requireAdmin)
		r.Get("/api/admin/providers", s.handleAdminListProviders)
		r.Post("/api/admin/providers", s.handleAdminCreateProvider)
		r.Patch("/api/admin/providers/{id}", s.handleAdminUpdateProvider)
		r.Delete("/api/admin/providers/{id}", s.handleAdminDeleteProvider)
		r.Get("/api/admin/settings", s.handleAdminGetSettings)
		r.Patch("/api/admin/settings", s.handleAdminUpdateSettings)
		r.Get("/api/admin/usage", s.handleAdminUsage)
	})

	return r
}

func (s *Server) userFromRequest(r *http.Request) (*User, error) {
	cookie, err := r.Cookie("chatxxx_session")
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return nil, errors.New("missing session")
	}
	row := s.store.DB.QueryRow(`
		SELECT u.id, u.email, u.name, u.role, u.status, u.created_at, u.updated_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id = ? AND s.expires_at > ?
	`, cookie.Value, db.Now())
	var user User
	if err := row.Scan(&user.ID, &user.Email, &user.Name, &user.Role, &user.Status, &user.CreatedAt, &user.UpdatedAt); err != nil {
		return nil, err
	}
	if user.Status != "active" {
		return nil, errors.New("inactive user")
	}
	return &user, nil
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := s.userFromRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := currentUser(r)
		if user == nil || user.Role != "admin" {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "需要管理员权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func currentUser(r *http.Request) *User {
	user, _ := r.Context().Value(userContextKey).(*User)
	return user
}

func (s *Server) createSession(w http.ResponseWriter, userID int64) error {
	sessionID := randomID()
	now := db.Now()
	expires := time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339)
	_, err := s.store.DB.Exec(`INSERT INTO sessions (id, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`, sessionID, userID, expires, now)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "chatxxx_session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(30 * 24 * time.Hour),
	})
	return nil
}

func (s *Server) clearSession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("chatxxx_session"); err == nil {
		_, _ = s.store.DB.Exec(`DELETE FROM sessions WHERE id = ?`, cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "chatxxx_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) userCount() (int64, error) {
	var count int64
	err := s.store.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

func scanUser(row *sql.Row) (*User, error) {
	var user User
	if err := row.Scan(&user.ID, &user.Email, &user.Name, &user.Role, &user.Status, &user.CreatedAt, &user.UpdatedAt); err != nil {
		return nil, err
	}
	return &user, nil
}
