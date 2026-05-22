package httpapi

import (
	"chatxxx/backend/internal/config"
	"chatxxx/backend/internal/db"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveGenerateConversationTitleWithMemoryProvider(t *testing.T) {
	if os.Getenv("CHATXXX_LIVE_TITLE_TEST") != "1" {
		t.Skip("set CHATXXX_LIVE_TITLE_TEST=1 to call the configured memory LLM")
	}
	source, err := sql.Open("sqlite", liveTitleTestDBPath(t))
	if err != nil {
		t.Fatalf("open source db: %v", err)
	}
	defer source.Close()

	var provider runtimeProvider
	var active int
	err = source.QueryRow(`
		SELECT p.id, p.name, p.base_url, p.api_key, p.model, p.request_mode, p.response_format, p.is_active
		FROM providers p
		JOIN app_settings s ON CAST(s.value AS INTEGER)=p.id
		WHERE s.key='memory_provider_id'
	`).Scan(&provider.ID, &provider.Name, &provider.Base, &provider.Key, &provider.Model, &provider.RequestMode, &provider.ResponseFormat, &active)
	if err != nil {
		t.Fatalf("load memory provider: %v", err)
	}
	if strings.TrimSpace(provider.Key) == "" || strings.TrimSpace(provider.Base) == "" || strings.TrimSpace(provider.Model) == "" || active != 1 {
		t.Fatalf("memory provider is incomplete or inactive")
	}

	store, err := db.Open(filepath.Join(t.TempDir(), "chatxxx.sqlite"))
	if err != nil {
		t.Fatalf("open temp db: %v", err)
	}
	defer store.Close()

	now := db.Now()
	if _, err := store.DB.Exec(`
		INSERT INTO users (id, email, name, password_hash, role, status, created_at, updated_at)
		VALUES (1, 'live-title@example.com', 'title', 'hash', 'user', 'active', ?, ?)
	`, now, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := store.DB.Exec(`
		INSERT INTO providers (id, name, base_url, api_key, model, request_mode, response_format, is_active, created_at, updated_at)
		VALUES (3, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`, provider.Name, provider.Base, provider.Key, provider.Model, provider.RequestMode, provider.ResponseFormat, now, now); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := store.DB.Exec(`UPDATE app_settings SET value='3' WHERE key='memory_provider_id'`); err != nil {
		t.Fatalf("seed memory setting: %v", err)
	}
	if _, err := store.DB.Exec(`
		INSERT INTO conversations (id, session_id, user_id, title, created_at, updated_at)
		VALUES (1, 'abcdefabcdefabcdefabcdefabcdefab', 1, '新对话', ?, ?)
	`, now, now); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}

	server := NewServer(config.Config{}, store)
	title := server.maybeGenerateConversationTitle(context.Background(), 1, 1, "请帮我展示 Markdown 表格和二次方程求根公式")
	if title == "" || title == "新对话" || strings.Contains(title, "请帮我展示 Markdown 表格和二次方程求根公式") {
		t.Fatalf("unexpected generated title: %q", title)
	}
	t.Logf("generated title: %s", title)
}

func liveTitleTestDBPath(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv("CHATXXX_LIVE_DB_PATH"),
		filepath.Join("..", "..", "..", "data", "chatxxx.sqlite"),
		filepath.Join("..", "data", "chatxxx.sqlite"),
		filepath.Join("data", "chatxxx.sqlite"),
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Fatalf("chatxxx sqlite database not found")
	return ""
}
