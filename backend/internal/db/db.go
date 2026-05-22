package db

import (
	"chatxxx/backend/internal/config"
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB *sql.DB
}

func Open(path string) (*Store, error) {
	if err := config.EnsureDirForFile(path); err != nil {
		return nil, err
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	store := &Store{DB: conn}
	if err := store.migrate(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.DB.Close()
}

func Now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func (s *Store) migrate() error {
	stmts := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			status TEXT NOT NULL DEFAULT 'active',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS providers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			provider_type TEXT NOT NULL DEFAULT 'openai_compatible',
			base_url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			model TEXT NOT NULL,
			capabilities TEXT NOT NULL DEFAULT '{}',
			request_mode TEXT NOT NULL DEFAULT 'chat_completions',
			response_format TEXT NOT NULL DEFAULT '',
			context_window INTEGER NOT NULL DEFAULT 0,
			max_output_tokens INTEGER NOT NULL DEFAULT 0,
			is_default INTEGER NOT NULL DEFAULT 0,
			is_visible INTEGER NOT NULL DEFAULT 1,
			is_active INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS conversations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL DEFAULT '',
			user_id INTEGER NOT NULL,
			title TEXT NOT NULL DEFAULT '新对话',
			system_prompt TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			memory_enabled INTEGER NOT NULL DEFAULT 1,
			last_auto_memory_message_id INTEGER NOT NULL DEFAULT 0,
			pinned INTEGER NOT NULL DEFAULT 0,
			archived INTEGER NOT NULL DEFAULT 0,
			archive_category_id INTEGER NOT NULL DEFAULT 0,
			deleted_at TEXT DEFAULT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS archive_categories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(user_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			reasoning_content TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'completed',
			attachments TEXT NOT NULL DEFAULT '[]',
			metadata TEXT NOT NULL DEFAULT '{}',
			version_group_id INTEGER NOT NULL DEFAULT 0,
			version_index INTEGER NOT NULL DEFAULT 1,
			is_active_version INTEGER NOT NULL DEFAULT 1,
			parent_user_message_id INTEGER NOT NULL DEFAULT 0,
			sort_order REAL NOT NULL DEFAULT 0,
			deleted_at TEXT DEFAULT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS memories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			content TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'manual',
			category TEXT NOT NULL DEFAULT '',
			weight INTEGER NOT NULL DEFAULT 0,
			origin TEXT NOT NULL DEFAULT '',
			tokens INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			embedding BLOB DEFAULT NULL,
			embedding_model TEXT NOT NULL DEFAULT '',
			embedding_dim INTEGER NOT NULL DEFAULT 0,
			embedding_updated_at TEXT DEFAULT NULL,
			deleted_at TEXT DEFAULT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS generation_runs (
			run_id TEXT PRIMARY KEY,
			conversation_id INTEGER NOT NULL,
			assistant_message_id INTEGER DEFAULT NULL,
			user_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'running',
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id, expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_user ON conversations(user_id, deleted_at, archived, pinned, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id, deleted_at, sort_order, id)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_user ON memories(user_id, enabled, deleted_at, updated_at)`,
	}
	for _, stmt := range stmts {
		if _, err := s.DB.Exec(stmt); err != nil {
			return err
		}
	}
	if err := s.ensureColumn("providers", "request_mode", "TEXT NOT NULL DEFAULT 'chat_completions'"); err != nil {
		return err
	}
	if err := s.ensureColumn("providers", "response_format", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("conversations", "last_auto_memory_message_id", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn("conversations", "session_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	_, _ = s.DB.Exec(`UPDATE conversations SET session_id = lower(hex(randomblob(16))) WHERE session_id = '' OR session_id IS NULL`)
	if _, err := s.DB.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_conversations_session ON conversations(session_id)`); err != nil {
		return err
	}
	memoryColumns := map[string]string{
		"origin":               "TEXT NOT NULL DEFAULT ''",
		"tokens":               "INTEGER NOT NULL DEFAULT 0",
		"embedding":            "BLOB DEFAULT NULL",
		"embedding_model":      "TEXT NOT NULL DEFAULT ''",
		"embedding_dim":        "INTEGER NOT NULL DEFAULT 0",
		"embedding_updated_at": "TEXT DEFAULT NULL",
	}
	for column, definition := range memoryColumns {
		if err := s.ensureColumn("memories", column, definition); err != nil {
			return err
		}
	}
	_, _ = s.DB.Exec(`UPDATE memories SET origin = COALESCE(NULLIF(origin, ''), source, 'manual') WHERE origin = ''`)
	_, _ = s.DB.Exec(`UPDATE memories SET tokens = MAX(1, CAST(LENGTH(content) / 2 AS INTEGER)) WHERE tokens = 0`)
	if _, err := s.DB.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_embedding ON memories(user_id, enabled, deleted_at, embedding_model)`); err != nil {
		return err
	}
	for _, item := range []struct {
		key   string
		value string
	}{
		{"title_provider_id", "0"},
		{"memory_provider_id", "0"},
		{"embedding_provider_id", "0"},
		{"memory_recent_message_limit", "12"},
		{"memory_max_actions_per_run", "5"},
		{"memory_inject_limit", "20"},
		{"embedding_top_k", "8"},
		{"image_tool_mode", "image_api"},
		{"image_tool_base_url", "https://api.tu-zi.com"},
		{"image_generate_model", "gpt-image-2"},
		{"image_edit_model", "gpt-image-1.5"},
		{"image_responses_model", "gpt-5.5"},
		{"image_chat_model", "gpt-4o-image"},
		{"image_default_size", "1024x1024"},
		{"image_edit_size", "1:1"},
		{"image_default_quality", "auto"},
		{"image_response_format", "url"},
	} {
		if err := s.ensureSetting(item.key, item.value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureColumn(table, column, definition string) error {
	rows, err := s.DB.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notnull, &dflt, &pk); err != nil {
			continue
		}
		if name == column {
			return nil
		}
	}
	_, err = s.DB.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition)
	return err
}

func (s *Store) ensureSetting(key, value string) error {
	now := Now()
	_, err := s.DB.Exec(`
		INSERT INTO app_settings (key, value, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(key) DO NOTHING
	`, key, value, now, now)
	return err
}
