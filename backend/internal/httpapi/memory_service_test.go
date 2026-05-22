package httpapi

import (
	"chatxxx/backend/internal/config"
	"chatxxx/backend/internal/db"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMemoryActionsObjectAndSanitize(t *testing.T) {
	raw := `Here: {"actions":[{"action":"add","content":"用户偏好简洁回答","category":"偏好","weight":12},{"action":"delete","content":"bad"},{"action":"update","id":9,"content":"用户正在做 ChatXXX","category":"项目"}]}`
	actions := parseMemoryActions(raw, 5)
	if len(actions) != 2 {
		t.Fatalf("expected 2 actions, got %#v", actions)
	}
	if actions[0].Action != "add" || actions[0].Category != "偏好" {
		t.Fatalf("unexpected first action: %#v", actions[0])
	}
	if actions[1].Action != "update" || actions[1].ID != 9 {
		t.Fatalf("unexpected second action: %#v", actions[1])
	}
}

func TestVectorPackUnpackCosine(t *testing.T) {
	raw := packVector([]float32{1, 0, 0})
	got := unpackVector(raw)
	if len(got) != 3 || got[0] != 1 || got[1] != 0 || got[2] != 0 {
		t.Fatalf("unexpected vector: %#v", got)
	}
	if score := cosine(got, []float32{1, 0, 0}); score < 0.999 {
		t.Fatalf("expected cosine near 1, got %f", score)
	}
	if score := cosine(got, []float32{0, 1, 0}); score > 0.001 {
		t.Fatalf("expected cosine near 0, got %f", score)
	}
}

func TestParseMemoryRerankItemsSanitizesAndSorts(t *testing.T) {
	candidates := []scoredMemory{
		{Memory: Memory{ID: 1, Content: "用户叫张三"}, Score: 0.30},
		{Memory: Memory{ID: 2, Content: "用户今年17岁"}, Score: 0.40},
		{Memory: Memory{ID: 3, Content: "用户喜欢简洁回答"}, Score: 0.20},
	}
	raw := `{"items":[
		{"id":1,"rerank_score":1.2,"reason":"直接包含姓名"},
		{"id":2,"rerank_score":0.8,"reason":"年龄相关"},
		{"id":999,"rerank_score":1,"reason":"非法"},
		{"id":1,"rerank_score":0.1,"reason":"重复"},
		{"id":3,"rerank_score":0.8,"reason":"同分用向量分排序"}
	]}`
	items := parseMemoryRerankItems(raw, candidates)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %#v", items)
	}
	if items[0].ID != 1 || items[0].RerankScore != 1 {
		t.Fatalf("expected id 1 clamped to score 1 first, got %#v", items[0])
	}
	if items[1].ID != 2 || items[2].ID != 3 {
		t.Fatalf("expected vector score tie-break between id 2 and 3, got %#v", items)
	}
}

func TestParseMemoryRerankItemsInvalidJSON(t *testing.T) {
	items := parseMemoryRerankItems(`not json`, []scoredMemory{{Memory: Memory{ID: 1}, Score: 0.1}})
	if len(items) != 0 {
		t.Fatalf("expected no items for invalid JSON, got %#v", items)
	}
}

func TestBuildMemoryRerankPromptIncludesTimeFields(t *testing.T) {
	prompt := buildMemoryRerankPrompt("我是谁", []scoredMemory{{
		Memory: Memory{
			ID:        1,
			Content:   "用户叫张三",
			Category:  "人物",
			CreatedAt: "2026-05-20T01:02:03Z",
			UpdatedAt: "2026-05-20T04:05:06Z",
		},
		Score: 0.42,
	}}, 10)
	for _, want := range []string{"【当前时间】", "created_at=2026-05-20T01:02:03Z", "updated_at=2026-05-20T04:05:06Z", "结合时间判断优先级"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %s", want, prompt)
		}
	}
}

func TestDBMigrationAddsMemoryColumnsAndSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chatxxx.sqlite")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	_, err = conn.Exec(`
		CREATE TABLE memories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			content TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'manual',
			category TEXT NOT NULL DEFAULT '',
			weight INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			deleted_at TEXT DEFAULT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
	`)
	if err != nil {
		t.Fatalf("seed old schema: %v", err)
	}
	_ = conn.Close()

	store, err := db.Open(path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer store.Close()

	for _, column := range []string{"origin", "tokens", "embedding", "embedding_model", "embedding_dim", "embedding_updated_at"} {
		var found int
		rows, err := store.DB.Query(`PRAGMA table_info(memories)`)
		if err != nil {
			t.Fatalf("pragma: %v", err)
		}
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt sql.NullString
			var pk int
			_ = rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
			if name == column {
				found = 1
			}
		}
		_ = rows.Close()
		if found == 0 {
			t.Fatalf("missing column %s", column)
		}
	}
	var value string
	if err := store.DB.QueryRow(`SELECT value FROM app_settings WHERE key='memory_provider_id'`).Scan(&value); err != nil || value != "0" {
		t.Fatalf("missing default memory_provider_id: value=%q err=%v", value, err)
	}
	if err := store.DB.QueryRow(`SELECT value FROM app_settings WHERE key='title_provider_id'`).Scan(&value); err != nil || value != "0" {
		t.Fatalf("missing default title_provider_id: value=%q err=%v", value, err)
	}
}

func TestMemoriesForInjectionDoesNotFallbackWithoutEmbeddings(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "chatxxx.sqlite"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	now := db.Now()
	if _, err := store.DB.Exec(`
		INSERT INTO users (id, email, name, password_hash, role, status, created_at, updated_at)
		VALUES (1, 'u@example.com', 'u', 'hash', 'user', 'active', ?, ?);
		INSERT INTO memories (user_id, content, source, category, weight, enabled, created_at, updated_at)
		VALUES (1, '用户叫张三', 'auto', '人物', 10, 1, ?, ?);
	`, now, now, now, now); err != nil {
		t.Fatalf("seed db: %v", err)
	}
	server := NewServer(config.Config{}, store)
	got := server.memoriesForInjection(nil, 1, "我是谁", 5)
	if len(got.Memories) != 0 {
		t.Fatalf("expected no fallback memories, got %#v", got)
	}
}

func TestMemoriesForInjectionRunsForSingleRuneQuery(t *testing.T) {
	var embeddingCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("unexpected embedding path: %s", r.URL.Path)
		}
		embeddingCalls++
		var body struct {
			Input interface{} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode embedding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": []float64{1, 0, 0}},
			},
		})
	}))
	defer upstream.Close()

	store, err := db.Open(filepath.Join(t.TempDir(), "chatxxx.sqlite"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	now := db.Now()
	vector := packVector([]float32{1, 0, 0})
	if _, err := store.DB.Exec(`
		INSERT INTO users (id, email, name, password_hash, role, status, created_at, updated_at)
		VALUES (1, 'u@example.com', 'u', 'hash', 'user', 'active', ?, ?)
	`, now, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := store.DB.Exec(`
		INSERT INTO providers (id, name, base_url, api_key, model, is_active, created_at, updated_at)
		VALUES (1, 'embed', ?, 'key', 'mock-embedding', 1, ?, ?)
	`, upstream.URL+"/v1", now, now); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := store.DB.Exec(`UPDATE app_settings SET value='1' WHERE key='embedding_provider_id'`); err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	if _, err := store.DB.Exec(`
		INSERT INTO memories (user_id, content, source, category, weight, enabled, embedding, embedding_model, embedding_dim, embedding_updated_at, created_at, updated_at)
		VALUES (1, '单字测试记忆', 'auto', '其他', 0, 1, ?, 'mock-embedding', 3, ?, ?, ?)
	`, vector, now, now, now); err != nil {
		t.Fatalf("seed db: %v", err)
	}

	server := NewServer(config.Config{}, store)
	got := server.memoriesForInjection(context.Background(), 1, "你", 10)
	if embeddingCalls == 0 {
		t.Fatalf("expected embedding call for single-rune query")
	}
	if len(got.Memories) != 1 || got.Memories[0].Content != "单字测试记忆" {
		t.Fatalf("expected memory hit for single-rune query, got %#v", got)
	}
}
