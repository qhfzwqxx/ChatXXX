package httpapi

import (
	"bytes"
	"chatxxx/backend/internal/config"
	"chatxxx/backend/internal/db"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "chatxxx.sqlite"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func insertSettingsForTest(t *testing.T, store *db.Store, values map[string]string) {
	t.Helper()
	now := db.Now()
	for key, value := range values {
		if _, err := store.DB.Exec(`
			INSERT INTO app_settings (key, value, created_at, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at
		`, key, value, now, now); err != nil {
			t.Fatalf("insert setting %s: %v", key, err)
		}
	}
}

func testPNGData(t *testing.T, side int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			img.Set(x, y, color.RGBA{R: 32, G: 120, B: 220, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func base64ForTest(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func TestExecuteGetCurrentTimeToolInvalidTimezone(t *testing.T) {
	raw := executeGetCurrentTimeTool(`{"timezone":"Mars/Phobos"}`)
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal tool output: %v", err)
	}
	if ok, _ := got["ok"].(bool); ok {
		t.Fatalf("expected ok=false, got %v", got["ok"])
	}
	if got["timezone"] != "Mars/Phobos" {
		t.Fatalf("expected timezone to echo invalid input, got %v", got["timezone"])
	}
}

func TestAssistantMetadataIncludesMemoryHits(t *testing.T) {
	metadata := assistantMetadataWithToolStepsAndMemoryHits(nil, memoryHitPayload{
		Method: "vector_rerank",
		Model:  "embedding-test",
		Dim:    3,
		Memories: []memorySearchHit{
			{
				Memory:      Memory{ID: 7, UserID: 1, Content: "用户叫张三", Category: "identity"},
				Score:       0.52,
				VectorScore: 0.52,
				RerankScore: 0.98,
				Reason:      "用户询问身份，这条记忆直接包含姓名",
			},
		},
	})
	var got struct {
		MemoryHits memoryHitPayload `json:"memory_hits"`
	}
	if err := json.Unmarshal([]byte(metadata), &got); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if got.MemoryHits.Method != "vector_rerank" || len(got.MemoryHits.Memories) != 1 {
		t.Fatalf("expected persisted memory hits, got %s", metadata)
	}
	if got.MemoryHits.Memories[0].RerankScore != 0.98 || got.MemoryHits.Memories[0].Reason == "" {
		t.Fatalf("expected rerank fields in metadata, got %#v", got.MemoryHits.Memories[0])
	}
}

func TestGenerateConversationTitleFallsBackToMemoryProvider(t *testing.T) {
	var gotAuth string
	var gotBody map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": "数学公式排版",
					},
				},
			},
		})
	}))
	defer upstream.Close()

	store := newTestStore(t)
	server := NewServer(config.Config{}, store)
	now := db.Now()
	if _, err := store.DB.Exec(`
		INSERT INTO users (id, email, name, password_hash, role, status, created_at, updated_at)
		VALUES (1, 'u@example.com', 'u', 'hash', 'user', 'active', ?, ?)
	`, now, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := store.DB.Exec(`
		INSERT INTO providers (id, name, base_url, api_key, model, request_mode, is_active, created_at, updated_at)
		VALUES (11, 'memory', ?, 'title-key', 'title-model', 'chat_completions', 1, ?, ?)
	`, upstream.URL+"/v1", now, now); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := store.DB.Exec(`UPDATE app_settings SET value='11' WHERE key='memory_provider_id'`); err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	if _, err := store.DB.Exec(`
		INSERT INTO conversations (id, session_id, user_id, title, created_at, updated_at)
		VALUES (21, 'abcdefabcdefabcdefabcdefabcdefab', 1, '新对话', ?, ?)
	`, now, now); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	if _, err := store.DB.Exec(`
		INSERT INTO messages (conversation_id, user_id, role, content, status, sort_order, created_at, updated_at)
		VALUES (21, 1, 'user', '展示一下数学公式和 Markdown', 'completed', 10, ?, ?)
	`, now, now); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	title := server.maybeGenerateConversationTitle(context.Background(), 21, 1, "fallback")
	if title != "数学公式排版" {
		t.Fatalf("unexpected title: %q", title)
	}
	if gotAuth != "Bearer title-key" {
		t.Fatalf("unexpected auth: %s", gotAuth)
	}
	if gotBody["model"] != "title-model" {
		t.Fatalf("unexpected model body: %#v", gotBody)
	}
	var stored string
	if err := store.DB.QueryRow(`SELECT title FROM conversations WHERE id=21`).Scan(&stored); err != nil || stored != "数学公式排版" {
		t.Fatalf("unexpected stored title: %q err=%v", stored, err)
	}
}

func TestExecuteWebSearchTool(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/web-search/search" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["query"] != "OpenAI Responses API" {
			t.Fatalf("unexpected query: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code":      0,
			"message":   "success",
			"requestId": "req_1",
			"data": map[string]interface{}{
				"webPages": []map[string]interface{}{
					{
						"name":       "Responses API guide",
						"url":        "https://example.com/responses",
						"displayUrl": "https://example.com/responses",
						"snippet":    "Use tools with Responses.",
						"siteName":   "example.com",
						"siteIcon":   "https://example.com/favicon.png",
					},
				},
			},
		})
	}))
	defer upstream.Close()

	server := &Server{cfg: config.Config{UniFuncsAPIKey: "test-key", UniFuncsBaseURL: upstream.URL}}
	raw := server.executeWebSearchTool(`{"query":"OpenAI Responses API","count":3}`)
	var got struct {
		OK      bool `json:"ok"`
		Results []struct {
			Title      string `json:"title"`
			URL        string `json:"url"`
			DisplayURL string `json:"display_url"`
			Snippet    string `json:"snippet"`
			SiteName   string `json:"site_name"`
			SiteIcon   string `json:"site_icon"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !got.OK || len(got.Results) != 1 {
		t.Fatalf("unexpected output: %s", raw)
	}
	if got.Results[0].Title != "Responses API guide" || got.Results[0].URL == "" {
		t.Fatalf("unexpected result: %#v", got.Results[0])
	}
	if got.Results[0].SiteIcon != "https://example.com/favicon.png" || got.Results[0].SiteName != "example.com" || got.Results[0].DisplayURL == "" {
		t.Fatalf("expected site metadata, got %#v", got.Results[0])
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("unexpected auth header: %s", gotAuth)
	}
}

func TestUniFuncsSearchEndpoint(t *testing.T) {
	server := &Server{cfg: config.Config{UniFuncsBaseURL: "https://api.unifuncs.com/api"}}
	endpoint, err := server.uniFuncsSearchEndpoint()
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	if endpoint != "https://api.unifuncs.com/api/web-search/search" {
		t.Fatalf("unexpected endpoint: %s", endpoint)
	}
	server.cfg.UniFuncsBaseURL = "https://api.unifuncs.com"
	endpoint, err = server.uniFuncsSearchEndpoint()
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	if endpoint != "https://api.unifuncs.com/api/web-search/search" {
		t.Fatalf("unexpected endpoint: %s", endpoint)
	}
	server.cfg.UniFuncsBaseURL = "https://api.unifuncs.com/api/web-search/search"
	endpoint, err = server.uniFuncsSearchEndpoint()
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	if endpoint != "https://api.unifuncs.com/api/web-search/search" {
		t.Fatalf("unexpected endpoint: %s", endpoint)
	}
}

func TestUniFuncsWebReaderEndpoint(t *testing.T) {
	server := &Server{cfg: config.Config{UniFuncsBaseURL: "https://api.unifuncs.com/api"}}
	endpoint, err := server.uniFuncsWebReaderEndpoint()
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	if endpoint != "https://api.unifuncs.com/api/web-reader/read" {
		t.Fatalf("unexpected endpoint: %s", endpoint)
	}
	server.cfg.UniFuncsBaseURL = "https://api.unifuncs.com"
	endpoint, err = server.uniFuncsWebReaderEndpoint()
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	if endpoint != "https://api.unifuncs.com/api/web-reader/read" {
		t.Fatalf("unexpected endpoint: %s", endpoint)
	}
	server.cfg.UniFuncsBaseURL = "https://api.unifuncs.com/api/web-search/search"
	endpoint, err = server.uniFuncsWebReaderEndpoint()
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	if endpoint != "https://api.unifuncs.com/api/web-reader/read" {
		t.Fatalf("unexpected endpoint: %s", endpoint)
	}
	server.cfg.UniFuncsBaseURL = "https://api.unifuncs.com/api/web-reader/read"
	endpoint, err = server.uniFuncsWebReaderEndpoint()
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	if endpoint != "https://api.unifuncs.com/api/web-reader/read" {
		t.Fatalf("unexpected endpoint: %s", endpoint)
	}
}

func TestExecuteWebSearchToolMissingKey(t *testing.T) {
	server := &Server{}
	raw := server.executeWebSearchTool(`{"query":"news"}`)
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if ok, _ := got["ok"].(bool); ok {
		t.Fatalf("expected missing key failure: %s", raw)
	}
	if !strings.Contains(fmt.Sprint(got["error"]), "UNIFUNCS_API_KEY") {
		t.Fatalf("expected key error, got %s", raw)
	}
}

func TestExecuteWebReaderTool(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/web-reader/read" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["url"] != "https://example.com/article" {
			t.Fatalf("unexpected url: %#v", body)
		}
		if body["format"] != "md" || body["maxWords"] != float64(1200) {
			t.Fatalf("unexpected reader options: %#v", body)
		}
		if body["liteMode"] != true || body["includeImages"] != true || body["topic"] != "Responses" {
			t.Fatalf("unexpected reader flags: %#v", body)
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("X-Unifuncs-Status", "0")
		w.Header().Set("X-Unifuncs-Request-Id", "reader_req_1")
		fmt.Fprint(w, "# Example Article\n\nReader content.")
	}))
	defer upstream.Close()

	server := &Server{cfg: config.Config{UniFuncsAPIKey: "test-key", UniFuncsBaseURL: upstream.URL}}
	raw := server.executeWebReaderTool(`{"url":"https://example.com/article","format":"md","lite_mode":true,"include_images":true,"max_words":1200,"topic":"Responses"}`)
	var got struct {
		OK        bool   `json:"ok"`
		Tool      string `json:"tool"`
		URL       string `json:"url"`
		Title     string `json:"title"`
		Content   string `json:"content"`
		RequestID string `json:"request_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !got.OK || got.Tool != "web_reader" || got.URL != "https://example.com/article" {
		t.Fatalf("unexpected output: %s", raw)
	}
	if got.Title != "Example Article" || !strings.Contains(got.Content, "Reader content.") {
		t.Fatalf("unexpected reader content: %#v", got)
	}
	if got.RequestID != "reader_req_1" || got.Status != "0" {
		t.Fatalf("expected response metadata, got %#v", got)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("unexpected auth header: %s", gotAuth)
	}
}

func TestExecuteWebReaderToolKeepsDefaultIncludeImages(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if _, ok := body["includeImages"]; ok {
			t.Fatalf("includeImages should be omitted when not specified: %#v", body)
		}
		w.Header().Set("X-Unifuncs-Status", "0")
		fmt.Fprint(w, "plain content")
	}))
	defer upstream.Close()

	server := &Server{cfg: config.Config{UniFuncsAPIKey: "test-key", UniFuncsBaseURL: upstream.URL}}
	raw := server.executeWebReaderTool(`{"url":"https://example.com/article","format":"markdown"}`)
	var got struct {
		OK      bool   `json:"ok"`
		Format  string `json:"format"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !got.OK || got.Format != "markdown" || got.Content != "plain content" {
		t.Fatalf("unexpected output: %s", raw)
	}
}

func TestExecuteWebReaderToolMissingKey(t *testing.T) {
	server := &Server{}
	raw := server.executeWebReaderTool(`{"url":"https://example.com"}`)
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if ok, _ := got["ok"].(bool); ok {
		t.Fatalf("expected missing key failure: %s", raw)
	}
	if !strings.Contains(fmt.Sprint(got["error"]), "UNIFUNCS_API_KEY") {
		t.Fatalf("expected key error, got %s", raw)
	}
}

func TestExecuteWebReaderToolRequiresURL(t *testing.T) {
	server := &Server{cfg: config.Config{UniFuncsAPIKey: "test-key"}}
	raw := server.executeWebReaderTool(`{}`)
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if ok, _ := got["ok"].(bool); ok {
		t.Fatalf("expected url failure: %s", raw)
	}
	if !strings.Contains(fmt.Sprint(got["error"]), "url is required") {
		t.Fatalf("expected url error, got %s", raw)
	}
}

func TestResponseToolDefinitionsRespectSearchMode(t *testing.T) {
	unifuncsTools := responseToolDefinitions(searchToolModeUniFuncs)
	if !hasToolForTest(unifuncsTools, "web_search") || !hasToolForTest(unifuncsTools, "web_reader") {
		t.Fatalf("unifuncs mode should expose web_search and web_reader: %#v", unifuncsTools)
	}
	if !hasToolForTest(unifuncsTools, "image_generate") || !hasToolForTest(unifuncsTools, "image_edit") {
		t.Fatalf("unifuncs mode should expose image tools: %#v", unifuncsTools)
	}
	if hasToolForTest(unifuncsTools, "searching") {
		t.Fatalf("unifuncs mode should not expose searching: %#v", unifuncsTools)
	}
	searchingTools := responseToolDefinitions(searchToolModeSearching)
	if !hasToolForTest(searchingTools, "searching") {
		t.Fatalf("searching mode should expose searching: %#v", searchingTools)
	}
	if !hasToolForTest(searchingTools, "image_generate") || !hasToolForTest(searchingTools, "image_edit") {
		t.Fatalf("searching mode should expose image tools: %#v", searchingTools)
	}
	if hasToolForTest(searchingTools, "web_search") || hasToolForTest(searchingTools, "web_reader") {
		t.Fatalf("searching mode should not expose UniFuncs tools: %#v", searchingTools)
	}
}

func TestExecuteImageGenerateToolUsesDocumentedJSONFormat(t *testing.T) {
	var gotAuth, gotContentType string
	var gotBody map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode image generate body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"created": 123,
			"data": []map[string]string{
				{"url": "https://example.com/image.png"},
			},
		})
	}))
	defer upstream.Close()

	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir()}}
	insertSettingsForTest(t, store, map[string]string{
		"image_tool_base_url":   upstream.URL,
		"image_tool_api_key":    "image-key",
		"image_generate_model":  "gpt-image-2",
		"image_default_size":    "1024x1024",
		"image_default_quality": "auto",
		"image_response_format": "url",
	})
	raw := server.executeImageGenerateTool(`{"prompt":"画一只猫","n":2,"size":"1536x1024","response_format":"url","quality":"high","style":"photorealistic","image":["https://example.com/ref.png"]}`)
	var got imageToolResult
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal image output: %v", err)
	}
	if !got.OK || got.Tool != "image_generate" || len(got.Images) != 1 || got.Images[0].URL == "" {
		t.Fatalf("unexpected output: %s", raw)
	}
	if gotAuth != "Bearer image-key" {
		t.Fatalf("unexpected auth: %s", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Fatalf("unexpected content-type: %s", gotContentType)
	}
	if gotBody["model"] != "gpt-image-2" || gotBody["prompt"] != "画一只猫" || gotBody["size"] != "1536x1024" || gotBody["response_format"] != "url" || gotBody["quality"] != "high" {
		t.Fatalf("unexpected request body: %#v", gotBody)
	}
	if gotBody["n"].(float64) != 2 {
		t.Fatalf("unexpected n: %#v", gotBody["n"])
	}
	if _, ok := gotBody["style"]; ok {
		t.Fatalf("gpt-image-2 should not send style to Tuzi upstream: %#v", gotBody)
	}
}

func TestExecuteImageGenerateToolRetriesTransientProviderError(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"error":{"message":"Upstream request failed","type":"upstream_error"}}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"created": 123,
			"data": []map[string]string{
				{"url": "https://example.com/retried-image.png"},
			},
		})
	}))
	defer upstream.Close()

	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir()}}
	insertSettingsForTest(t, store, map[string]string{
		"image_tool_base_url":   upstream.URL,
		"image_tool_api_key":    "image-key",
		"image_generate_model":  "gpt-image-2",
		"image_default_size":    "1024x1024",
		"image_default_quality": "auto",
		"image_response_format": "url",
	})
	raw := server.executeImageGenerateTool(`{"prompt":"画一只猫"}`)
	var got imageToolResult
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal image output: %v", err)
	}
	if !got.OK || len(got.Images) != 1 || got.Images[0].URL != "https://example.com/retried-image.png" {
		t.Fatalf("unexpected output: %s", raw)
	}
	if calls != 2 {
		t.Fatalf("expected one retry, got %d calls", calls)
	}
}

func TestExecuteImageGenerateToolShowsFriendlyTransientProviderError(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"error":{"message":"Upstream request failed","type":"upstream_error"}}`)
	}))
	defer upstream.Close()

	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir()}}
	insertSettingsForTest(t, store, map[string]string{
		"image_tool_base_url":   upstream.URL,
		"image_tool_api_key":    "image-key",
		"image_generate_model":  "gpt-image-2",
		"image_default_size":    "1024x1024",
		"image_default_quality": "auto",
		"image_response_format": "url",
	})
	raw := server.executeImageGenerateTool(`{"prompt":"画一只猫"}`)
	var got imageToolResult
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal image output: %v", err)
	}
	if got.OK || got.Error != "图片服务上游暂时失败，请稍后重试" {
		t.Fatalf("unexpected output: %s", raw)
	}
	if strings.Contains(got.Error, "Bad Gateway") || strings.Contains(got.Error, "upstream_error") {
		t.Fatalf("raw provider error leaked to user output: %s", raw)
	}
	if calls != 2 {
		t.Fatalf("expected one retry, got %d calls", calls)
	}
}

func TestExecuteImageGenerateToolUsesResponsesMode(t *testing.T) {
	var gotAuth, gotContentType string
	var gotBody map[string]interface{}
	imageData := base64.StdEncoding.EncodeToString(testPNGData(t, 8))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode responses image body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.image_generation_call.partial_image\n")
		fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
			"partial_image_b64": imageData,
		})+"\n\n")
		fmt.Fprint(w, "event: response.output_item.done\n")
		fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
			"item": map[string]interface{}{
				"type":   "image_generation_call",
				"status": "completed",
				"result": imageData,
			},
		})+"\n\n")
		fmt.Fprint(w, "event: response.completed\n")
		fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
			"response": map[string]interface{}{
				"output": []map[string]interface{}{
					{
						"type":   "image_generation_call",
						"status": "completed",
						"result": imageData,
					},
				},
			},
		})+"\n\n")
	}))
	defer upstream.Close()

	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir()}}
	insertSettingsForTest(t, store, map[string]string{
		"image_tool_mode":       imageToolModeResponses,
		"image_tool_base_url":   upstream.URL,
		"image_tool_api_key":    "image-key",
		"image_responses_model": "gpt-5.5",
		"image_default_size":    "1024x1024",
		"image_default_quality": "auto",
		"image_response_format": "url",
	})
	raw := server.executeImageGenerateTool(`{"prompt":"画一只猫","image":"https://example.com/ref.png","model":"gpt-image-2"}`)
	var got imageToolResult
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal image output: %v", err)
	}
	if !got.OK || got.Tool != "image_generate" || len(got.Images) != 1 || got.Images[0].B64JSON == "" {
		t.Fatalf("unexpected output: %s", raw)
	}
	if gotAuth != "Bearer image-key" {
		t.Fatalf("unexpected auth: %s", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Fatalf("unexpected content-type: %s", gotContentType)
	}
	if gotBody["model"] != "gpt-5.5" {
		t.Fatalf("expected configured responses model, got %#v", gotBody["model"])
	}
	if gotBody["stream"] != true {
		t.Fatalf("expected stream=true, got %#v", gotBody["stream"])
	}
	tools, _ := gotBody["tools"].([]interface{})
	if len(tools) != 1 || tools[0].(map[string]interface{})["type"] != "image_generation" {
		t.Fatalf("expected image_generation tool, got %#v", gotBody["tools"])
	}
	if tools[0].(map[string]interface{})["partial_images"] != float64(1) {
		t.Fatalf("expected responses image generation partial_images=1, got %#v", tools[0])
	}
	input, _ := gotBody["input"].([]interface{})
	if len(input) != 1 {
		t.Fatalf("expected one input item, got %#v", gotBody["input"])
	}
	content, _ := input[0].(map[string]interface{})["content"].([]interface{})
	var sawText, sawImage bool
	for _, item := range content {
		obj, _ := item.(map[string]interface{})
		if obj["type"] == "input_text" && obj["text"] == "画一只猫" {
			sawText = true
		}
		if obj["type"] == "input_image" && obj["image_url"] == "https://example.com/ref.png" {
			sawImage = true
		}
	}
	if !sawText || !sawImage {
		t.Fatalf("expected text and image input content, got %#v", content)
	}
}

func TestExecuteImageGenerateToolUsesChatCompletionsCompatibilityMode(t *testing.T) {
	var gotAuth, gotContentType string
	var gotBody map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode chat image body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"delta": map[string]string{"content": "![generated]("}},
			},
		})+"\n\n")
		fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"delta": map[string]string{"content": "https://example.com/generated-cat.png)"}},
			},
		})+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir()}}
	insertSettingsForTest(t, store, map[string]string{
		"image_tool_mode":     imageToolModeChatCompletions,
		"image_tool_base_url": upstream.URL,
		"image_tool_api_key":  "image-key",
		"image_chat_model":    "gpt-4o-image",
	})
	raw := server.executeImageGenerateTool(`{"prompt":"画一只猫","image":["https://example.com/ref.png"]}`)
	var got imageToolResult
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal image output: %v", err)
	}
	if !got.OK || got.Tool != "image_generate" || len(got.Images) != 1 || got.Images[0].URL != "https://example.com/generated-cat.png" {
		t.Fatalf("unexpected output: %s", raw)
	}
	if gotAuth != "Bearer image-key" {
		t.Fatalf("unexpected auth: %s", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Fatalf("unexpected content-type: %s", gotContentType)
	}
	if gotBody["model"] != "gpt-4o-image" {
		t.Fatalf("expected configured chat model, got %#v", gotBody["model"])
	}
	if gotBody["stream"] != true {
		t.Fatalf("expected stream=true, got %#v", gotBody["stream"])
	}
	messages, _ := gotBody["messages"].([]interface{})
	if len(messages) != 1 {
		t.Fatalf("expected one message, got %#v", gotBody["messages"])
	}
	content, _ := messages[0].(map[string]interface{})["content"].([]interface{})
	var sawText, sawImage bool
	for _, item := range content {
		obj, _ := item.(map[string]interface{})
		if obj["type"] == "text" && strings.Contains(fmt.Sprint(obj["text"]), "画一只猫") {
			sawText = true
		}
		if obj["type"] == "image_url" {
			imageURL, _ := obj["image_url"].(map[string]interface{})
			if imageURL["url"] == "https://example.com/ref.png" {
				sawImage = true
			}
		}
	}
	if !sawText || !sawImage {
		t.Fatalf("expected text and image_url content, got %#v", content)
	}
}

func TestResponsesImageToolResultReturnsToModel(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir()}}
	now := db.Now()
	if _, err := store.DB.Exec(`INSERT INTO users (id, email, name, password_hash, role, status, created_at, updated_at) VALUES (1, 'image-loop@test.local', 'image', 'hash', 'user', 'active', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := store.DB.Exec(`INSERT INTO conversations (id, session_id, user_id, title, created_at, updated_at) VALUES (1, 'abcdefabcdefabcdefabcdefabcdefab', 1, 'image', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}

	var responseCalls int
	var secondRequest map[string]interface{}
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected responses path: %s", r.URL.Path)
		}
		responseCalls++
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode responses body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch responseCalls {
		case 1:
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, `data: {"response":{"output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"image_generate","arguments":"{\"prompt\":\"画一只猫\"}","status":"completed"}]}}`)
			fmt.Fprint(w, "\n\n")
		case 2:
			secondRequest = body
			fmt.Fprint(w, "event: response.output_text.delta\n")
			fmt.Fprint(w, `data: {"delta":"自由补充文本"}`)
			fmt.Fprint(w, "\n\n")
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, `data: {"response":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"自由补充文本"}]}]}}`)
			fmt.Fprint(w, "\n\n")
		default:
			t.Fatalf("unexpected extra responses call")
		}
	}))
	defer llm.Close()

	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("unexpected image path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"created": 123,
			"data": []map[string]string{
				{"url": "https://example.com/generated-cat.png"},
			},
		})
	}))
	defer image.Close()

	insertSettingsForTest(t, store, map[string]string{
		"image_tool_base_url":   image.URL,
		"image_tool_api_key":    "image-key",
		"image_generate_model":  "gpt-image-2",
		"image_default_size":    "1024x1024",
		"image_default_quality": "auto",
		"image_response_format": "url",
	})
	provider := runtimeProvider{Base: llm.URL, Key: "llm-key", Model: "chat-model", RequestMode: "responses"}
	var toolSteps []responseToolStep
	text, err := server.streamResponses(context.Background(), httptest.NewRecorder(), provider, nil, 1, 1, "画一只猫", "", "", "", &toolSteps, nil, nil, nil)
	if err != nil {
		t.Fatalf("streamResponses: %v", err)
	}
	if responseCalls != 2 {
		t.Fatalf("expected image result to be returned to model, got %d responses calls", responseCalls)
	}
	if text != "自由补充文本" {
		t.Fatalf("unexpected final text: %q", text)
	}
	rawInput, ok := secondRequest["input"].([]interface{})
	if !ok {
		t.Fatalf("expected second request input, got %#v", secondRequest)
	}
	var sawOutput bool
	for _, item := range rawInput {
		obj, _ := item.(map[string]interface{})
		if obj["type"] == "function_call_output" && obj["call_id"] == "call_1" {
			sawOutput = true
		}
	}
	if !sawOutput {
		t.Fatalf("expected second request to include image function_call_output: %#v", secondRequest["input"])
	}
}

func TestStreamResponsesImageGenerateUsesResponsesImageMode(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir()}}
	seedUserAndConversationForImageFlowTest(t, store)

	var imageRequest map[string]interface{}
	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected image responses path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&imageRequest); err != nil {
			t.Fatalf("decode image responses body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.image_generation_call.partial_image\n")
		fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
			"partial_image_b64": base64.StdEncoding.EncodeToString(testPNGData(t, 8)),
		})+"\n\n")
		fmt.Fprint(w, "event: response.completed\n")
		fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
			"response": map[string]interface{}{
				"output": []map[string]interface{}{
					{
						"type":   "image_generation_call",
						"status": "completed",
						"result": base64.StdEncoding.EncodeToString(testPNGData(t, 8)),
					},
				},
			},
		})+"\n\n")
	}))
	defer image.Close()

	insertSettingsForTest(t, store, map[string]string{
		"image_tool_mode":       imageToolModeResponses,
		"image_tool_base_url":   image.URL,
		"image_tool_api_key":    "image-key",
		"image_responses_model": "gpt-5.5",
		"image_default_size":    "1024x1024",
		"image_default_quality": "auto",
		"image_response_format": "url",
	})

	var responseCalls int
	var secondRequest map[string]interface{}
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected main LLM responses path: %s", r.URL.Path)
		}
		responseCalls++
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode main responses body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch responseCalls {
		case 1:
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, `data: {"response":{"output":[{"type":"function_call","id":"fc_responses_image","call_id":"call_responses_image","name":"image_generate","arguments":"{\"prompt\":\"画一只猫\",\"image\":\"https://example.com/ref.png\"}","status":"completed"}]}}`)
			fmt.Fprint(w, "\n\n")
		case 2:
			secondRequest = body
			fmt.Fprint(w, "event: response.output_text.delta\n")
			fmt.Fprint(w, `data: {"delta":"Responses 模式已生成"}`)
			fmt.Fprint(w, "\n\n")
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, `data: {"response":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Responses 模式已生成"}]}]}}`)
			fmt.Fprint(w, "\n\n")
		default:
			t.Fatalf("unexpected extra responses call")
		}
	}))
	defer llm.Close()

	provider := runtimeProvider{Base: llm.URL, Key: "llm-key", Model: "main-model", RequestMode: "responses"}
	var toolSteps []responseToolStep
	text, err := server.streamResponses(context.Background(), httptest.NewRecorder(), provider, nil, 1, 1, "请画一只猫", "", "", "", &toolSteps, nil, nil, nil)
	if err != nil {
		t.Fatalf("streamResponses: %v", err)
	}
	if text != "Responses 模式已生成" || responseCalls != 2 {
		t.Fatalf("unexpected stream result text=%q responseCalls=%d", text, responseCalls)
	}
	assertResponsesImageModeRequestForTest(t, imageRequest)
	assertFunctionCallOutputImageForTest(t, secondRequest, "call_responses_image", func(output imageToolResult) {
		if len(output.Images) != 1 || output.Images[0].B64JSON != "" || output.Images[0].URL != "" || !strings.HasPrefix(output.Images[0].WorkspacePath, "users/1/generated/") {
			t.Fatalf("expected model-facing output to omit inline image data, got %#v", output.Images)
		}
	})
	if !hasSuccessfulImageToolStep(toolSteps) {
		t.Fatalf("expected successful image tool step, got %#v", toolSteps)
	}
}

func TestResponsesImageModeCanReuseGeneratedBase64Image(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir(), PublicBaseURL: "https://chat.example.com"}}
	seedUserAndConversationForImageFlowTest(t, store)
	imageData := base64.StdEncoding.EncodeToString(testPNGData(t, 8))
	var imageRequests []map[string]interface{}
	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected image responses path: %s", r.URL.Path)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode image responses body: %v", err)
		}
		imageRequests = append(imageRequests, body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.completed\n")
		fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
			"response": map[string]interface{}{
				"output": []map[string]interface{}{
					{"type": "image_generation_call", "status": "completed", "result": imageData},
				},
			},
		})+"\n\n")
	}))
	defer image.Close()

	insertSettingsForTest(t, store, map[string]string{
		"image_tool_mode":       imageToolModeResponses,
		"image_tool_base_url":   image.URL,
		"image_tool_api_key":    "image-key",
		"image_responses_model": "gpt-5.5",
	})
	firstLLM := llmToolServerForTest(t, `{"prompt":"画一只猫"}`, "第一张好了")
	metadata := runSingleImageToolConversationForTest(t, server, runtimeProvider{Base: firstLLM.URL, Key: "llm-key", Model: "main-model", RequestMode: "responses"}, "请画一只猫")
	if len(imageRequests) != 1 {
		t.Fatalf("expected first image request, got %d", len(imageRequests))
	}
	var firstOutput struct {
		ToolSteps []responseToolStep `json:"tool_steps"`
	}
	if err := json.Unmarshal([]byte(metadata), &firstOutput); err != nil || len(firstOutput.ToolSteps) == 0 {
		t.Fatalf("expected tool metadata: %s err=%v", metadata, err)
	}
	assistant, err := server.insertMessage(1, 1, "assistant", "第一张好了", "completed", metadata)
	if err != nil || assistant.ID == 0 {
		t.Fatalf("insert assistant: %v", err)
	}
	if _, err := server.insertMessage(1, 1, "user", "基于刚才那张图加一点赛博朋克霓虹", "completed", "{}"); err != nil {
		t.Fatalf("insert second user: %v", err)
	}
	reuseLLM := llmToolServerForTest(t, `{"prompt":"基于刚才那张图加一点赛博朋克霓虹","image":"`+firstGeneratedWorkspacePathForTest(t, metadata)+`"}`, "第二张好了")
	defer reuseLLM.Close()
	_, err = server.streamResponses(context.Background(), httptest.NewRecorder(), runtimeProvider{Base: reuseLLM.URL, Key: "llm-key", Model: "main-model", RequestMode: "responses"}, nil, 1, 1, "", "", "", "https://chat.example.com", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("stream reuse responses: %v", err)
	}
	if len(imageRequests) != 2 {
		t.Fatalf("expected second image request, got %d", len(imageRequests))
	}
	assertResponsesImageRequestUsesSignedWorkspaceURLForTest(t, imageRequests[1], firstGeneratedWorkspacePathForTest(t, metadata))
}

func TestStreamResponsesImageGenerateUsesChatCompletionsImageMode(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir()}}
	seedUserAndConversationForImageFlowTest(t, store)

	var imageRequest map[string]interface{}
	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected image chat completions path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&imageRequest); err != nil {
			t.Fatalf("decode image chat body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"delta": map[string]string{"content": "![generated](https://example.com/generated-dog.png)"}},
			},
		})+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer image.Close()

	insertSettingsForTest(t, store, map[string]string{
		"image_tool_mode":     imageToolModeChatCompletions,
		"image_tool_base_url": image.URL,
		"image_tool_api_key":  "image-key",
		"image_chat_model":    "gpt-4o-image",
	})

	var responseCalls int
	var secondRequest map[string]interface{}
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected main LLM responses path: %s", r.URL.Path)
		}
		responseCalls++
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode main responses body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch responseCalls {
		case 1:
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, `data: {"response":{"output":[{"type":"function_call","id":"fc_chat_image","call_id":"call_chat_image","name":"image_generate","arguments":"{\"prompt\":\"画一只狗\",\"image\":\"https://example.com/ref.png\"}","status":"completed"}]}}`)
			fmt.Fprint(w, "\n\n")
		case 2:
			secondRequest = body
			fmt.Fprint(w, "event: response.output_text.delta\n")
			fmt.Fprint(w, `data: {"delta":"Chat Completions 模式已生成"}`)
			fmt.Fprint(w, "\n\n")
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, `data: {"response":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Chat Completions 模式已生成"}]}]}}`)
			fmt.Fprint(w, "\n\n")
		default:
			t.Fatalf("unexpected extra responses call")
		}
	}))
	defer llm.Close()

	provider := runtimeProvider{Base: llm.URL, Key: "llm-key", Model: "main-model", RequestMode: "responses"}
	var toolSteps []responseToolStep
	text, err := server.streamResponses(context.Background(), httptest.NewRecorder(), provider, nil, 1, 1, "请画一只狗", "", "", "", &toolSteps, nil, nil, nil)
	if err != nil {
		t.Fatalf("streamResponses: %v", err)
	}
	if text != "Chat Completions 模式已生成" || responseCalls != 2 {
		t.Fatalf("unexpected stream result text=%q responseCalls=%d", text, responseCalls)
	}
	assertChatCompletionsImageModeRequestForTest(t, imageRequest)
	assertFunctionCallOutputImageForTest(t, secondRequest, "call_chat_image", func(output imageToolResult) {
		if len(output.Images) != 1 || output.Images[0].URL != "https://example.com/generated-dog.png" {
			t.Fatalf("expected generated dog URL, got %#v", output.Images)
		}
	})
	if !hasSuccessfulImageToolStep(toolSteps) {
		t.Fatalf("expected successful image tool step, got %#v", toolSteps)
	}
}

func TestChatCompletionsImageModeCanReuseGeneratedBase64Image(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir(), PublicBaseURL: "https://chat.example.com"}}
	seedUserAndConversationForImageFlowTest(t, store)
	imageDataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNGData(t, 8))
	var imageRequests []map[string]interface{}
	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected image chat completions path: %s", r.URL.Path)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode image chat body: %v", err)
		}
		imageRequests = append(imageRequests, body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"delta": map[string]string{"content": imageDataURL}},
			},
		})+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer image.Close()

	insertSettingsForTest(t, store, map[string]string{
		"image_tool_mode":     imageToolModeChatCompletions,
		"image_tool_base_url": image.URL,
		"image_tool_api_key":  "image-key",
		"image_chat_model":    "gpt-4o-image",
	})
	firstLLM := llmToolServerForTest(t, `{"prompt":"画一只狗"}`, "第一张好了")
	metadata := runSingleImageToolConversationForTest(t, server, runtimeProvider{Base: firstLLM.URL, Key: "llm-key", Model: "main-model", RequestMode: "responses"}, "请画一只狗")
	if len(imageRequests) != 1 {
		t.Fatalf("expected first image request, got %d", len(imageRequests))
	}
	if _, err := server.insertMessage(1, 1, "assistant", "第一张好了", "completed", metadata); err != nil {
		t.Fatalf("insert assistant: %v", err)
	}
	if _, err := server.insertMessage(1, 1, "user", "基于刚才那张图加一副墨镜", "completed", "{}"); err != nil {
		t.Fatalf("insert second user: %v", err)
	}
	path := firstGeneratedWorkspacePathForTest(t, metadata)
	reuseLLM := llmToolServerForTest(t, `{"prompt":"基于刚才那张图加一副墨镜","image":"`+path+`"}`, "第二张好了")
	defer reuseLLM.Close()
	_, err := server.streamResponses(context.Background(), httptest.NewRecorder(), runtimeProvider{Base: reuseLLM.URL, Key: "llm-key", Model: "main-model", RequestMode: "responses"}, nil, 1, 1, "", "", "", "https://chat.example.com", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("stream reuse chat: %v", err)
	}
	if len(imageRequests) != 2 {
		t.Fatalf("expected second image request, got %d", len(imageRequests))
	}
	assertChatImageRequestUsesSignedWorkspaceURLForTest(t, imageRequests[1], path)
}

func TestStreamResponsesKeepsSuccessfulImageWhenAssistantSummaryFails(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir(), PublicBaseURL: "https://chat.example.com"}}
	seedUserAndConversationForImageFlowTest(t, store)

	var imageRequest map[string]interface{}
	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected image responses path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&imageRequest); err != nil {
			t.Fatalf("decode image responses body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.completed\n")
		fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
			"response": map[string]interface{}{
				"output": []map[string]interface{}{
					{"type": "image_generation_call", "status": "completed", "result": base64.StdEncoding.EncodeToString(testPNGData(t, 8))},
				},
			},
		})+"\n\n")
	}))
	defer image.Close()

	insertSettingsForTest(t, store, map[string]string{
		"image_tool_mode":       imageToolModeResponses,
		"image_tool_base_url":   image.URL,
		"image_tool_api_key":    "image-key",
		"image_responses_model": "gpt-5.5",
	})

	var calls int
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected main LLM responses path: %s", r.URL.Path)
		}
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"type":      "function_call",
							"id":        "fc_1",
							"call_id":   "call_1",
							"name":      "image_generate",
							"arguments": `{"prompt":"画一只猫"}`,
							"status":    "completed",
						},
					},
				},
			})+"\n\n")
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":{"message":"Upstream request failed","type":"upstream_error"}}`)
	}))
	defer llm.Close()

	var toolSteps []responseToolStep
	text, err := server.streamResponses(context.Background(), httptest.NewRecorder(), runtimeProvider{Base: llm.URL, Key: "llm-key", Model: "main-model", RequestMode: "responses"}, nil, 1, 1, "画一只猫", "", "", "https://chat.example.com", &toolSteps, nil, nil, nil)
	if err != nil {
		t.Fatalf("streamResponses: %v", err)
	}
	if text != "图片已生成。" {
		t.Fatalf("expected preserved success text, got %q", text)
	}
	if !hasSuccessfulImageToolStep(toolSteps) {
		t.Fatalf("expected successful image tool step, got %#v", toolSteps)
	}
	if imageRequest == nil {
		t.Fatalf("expected image request to run")
	}
}

func TestExecuteImageGenerateToolResolvesWorkspaceReference(t *testing.T) {
	var gotBody map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode image generate body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"created": 789,
			"data": []map[string]string{
				{"url": "https://example.com/generated.png"},
			},
		})
	}))
	defer upstream.Close()

	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir()}}
	insertSettingsForTest(t, store, map[string]string{
		"image_tool_base_url": upstream.URL,
		"image_tool_api_key":  "image-key",
	})
	workspaceRel := "users/1/uploads/test/reference.png"
	workspaceFull := filepath.Join(server.workspaceRoot(), filepath.FromSlash(workspaceRel))
	if err := os.MkdirAll(filepath.Dir(workspaceFull), 0750); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	pngData := testPNGData(t, 32)
	if err := os.WriteFile(workspaceFull, pngData, 0640); err != nil {
		t.Fatalf("write workspace png: %v", err)
	}
	raw := server.executeImageGenerateToolWithContext(`{"prompt":"以这张图生成","image":"users/1/uploads/test/reference.png"}`, responseToolContext{UserID: 1, PublicBaseURL: "https://chat.example.com"})
	var got imageToolResult
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal image output: %v", err)
	}
	if !got.OK {
		t.Fatalf("unexpected output: %s", raw)
	}
	imageValue, _ := gotBody["image"].(string)
	if !strings.HasPrefix(imageValue, "https://chat.example.com/api/workspace/public/"+workspaceRel+"?") {
		t.Fatalf("expected workspace path to be resolved to a signed URL, got %#v", gotBody["image"])
	}
	if strings.Contains(imageValue, "base64") || !strings.Contains(imageValue, "expires=") || !strings.Contains(imageValue, "sig=") {
		t.Fatalf("expected signed URL image reference, got %#v", gotBody["image"])
	}
}

func TestExecuteImageEditToolUsesDocumentedMultipartFormat(t *testing.T) {
	var gotAuth, gotContentType string
	var gotFields map[string]string
	var gotFileNames []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("multipart reader: %v", err)
		}
		gotFields = map[string]string{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("next part: %v", err)
			}
			data, _ := io.ReadAll(part)
			if part.FileName() != "" {
				gotFileNames = append(gotFileNames, part.FormName()+":"+part.FileName()+":"+part.Header.Get("Content-Type"))
				if _, err := png.Decode(bytes.NewReader(data)); err != nil {
					t.Fatalf("uploaded file is not png: %v", err)
				}
				continue
			}
			gotFields[part.FormName()] = string(data)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"created": 456,
			"data": []map[string]string{
				{"b64_json": "ZmFrZQ=="},
			},
		})
	}))
	defer upstream.Close()

	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir()}}
	insertSettingsForTest(t, store, map[string]string{
		"image_tool_base_url":   upstream.URL,
		"image_tool_api_key":    "image-key",
		"image_edit_model":      "gpt-image-1.5",
		"image_edit_size":       "1:1",
		"image_response_format": "b64_json",
	})
	if _, err := store.DB.Exec(`INSERT INTO users (id, email, name, password_hash, role, status, created_at, updated_at) VALUES (1, 'u@test.local', 'u', 'x', 'user', 'active', ?, ?)`, db.Now(), db.Now()); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := store.DB.Exec(`INSERT INTO conversations (id, user_id, title, created_at, updated_at) VALUES (1, 1, 't', ?, ?)`, db.Now(), db.Now()); err != nil {
		t.Fatalf("insert conversation: %v", err)
	}
	workspaceRel := "users/1/uploads/test/source.png"
	workspaceFull := filepath.Join(server.workspaceRoot(), filepath.FromSlash(workspaceRel))
	if err := os.MkdirAll(filepath.Dir(workspaceFull), 0750); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	pngData := testPNGData(t, 64)
	if err := os.WriteFile(workspaceFull, pngData, 0640); err != nil {
		t.Fatalf("write workspace png: %v", err)
	}
	attachments := attachmentsMetadata([]attachment{
		{Name: "source.png", Type: "image/png", Size: int64(len(pngData)), WorkspacePath: workspaceRel, URL: "/api/workspace/files/" + workspaceRel, Width: 64, Height: 64},
	})
	if _, err := store.DB.Exec(`INSERT INTO messages (conversation_id, user_id, role, content, status, attachments, metadata, version_group_id, version_index, is_active_version, sort_order, created_at, updated_at) VALUES (1, 1, 'user', '编辑这张图', 'completed', ?, '{}', 1, 1, 1, 10, ?, ?)`, attachments, db.Now(), db.Now()); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	raw := server.executeImageEditTool(responseToolCall{Name: "image_edit", Arguments: `{"prompt":"改成水彩","size":"1:1","response_format":"b64_json","image_path":"users/1/uploads/test/source.png"}`}, responseToolContext{ConversationID: 1, UserID: 1})
	var got imageToolResult
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal image edit output: %v", err)
	}
	if !got.OK || got.Tool != "image_edit" || len(got.Images) != 1 || got.Images[0].B64JSON != "" || got.Images[0].WorkspacePath == "" || got.Images[0].URL == "" {
		t.Fatalf("unexpected output: %s", raw)
	}
	if gotAuth != "Bearer image-key" || !strings.HasPrefix(gotContentType, "multipart/form-data; boundary=") {
		t.Fatalf("unexpected headers auth=%s content-type=%s", gotAuth, gotContentType)
	}
	if gotFields["model"] != "gpt-image-1.5" || gotFields["prompt"] != "改成水彩" || gotFields["size"] != "1:1" || gotFields["response_format"] != "b64_json" || gotFields["n"] != "1" {
		t.Fatalf("unexpected fields: %#v", gotFields)
	}
	if len(gotFileNames) != 1 || !strings.Contains(gotFileNames[0], "image:source.png:image/png") {
		t.Fatalf("unexpected files: %#v", gotFileNames)
	}
}

func TestPrepareWorkspaceAttachmentsStoresFilesAndPromptUsesSummary(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir()}}
	pngData := testPNGData(t, 32)
	items, err := server.prepareWorkspaceAttachments(7, []attachment{
		{Name: "big.png", Type: "image/png", Size: int64(len(pngData)), Content: "data:image/png;base64," + base64ForTest(pngData), Width: 32, Height: 32},
	})
	if err != nil {
		t.Fatalf("prepare workspace attachments: %v", err)
	}
	if len(items) != 1 || items[0].WorkspacePath == "" || items[0].URL == "" {
		t.Fatalf("expected workspace metadata: %#v", items)
	}
	if items[0].Content != "" || items[0].Preview != "" {
		t.Fatalf("base64 content should not be persisted in message metadata: %#v", items[0])
	}
	if _, err := os.Stat(filepath.Join(server.workspaceRoot(), filepath.FromSlash(items[0].WorkspacePath))); err != nil {
		t.Fatalf("expected workspace file: %v", err)
	}
	prompt := attachmentPromptText(attachmentsMetadata(items))
	if strings.Contains(prompt, "data:image") || strings.Contains(prompt, base64ForTest(pngData)) {
		t.Fatalf("prompt should not include base64: %s", prompt)
	}
	if !strings.Contains(prompt, "big.png") || !strings.Contains(prompt, items[0].WorkspacePath) {
		t.Fatalf("prompt should include workspace summary, got %s", prompt)
	}
}

func TestWorkspaceSystemPromptListsMessageAttachmentMetadata(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir()}}
	if _, err := store.DB.Exec(`INSERT INTO users (id, email, name, password_hash, role, status, created_at, updated_at) VALUES (9, 'workspace@test.local', 'workspace', 'x', 'user', 'active', ?, ?)`, db.Now(), db.Now()); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := store.DB.Exec(`INSERT INTO conversations (id, user_id, title, created_at, updated_at) VALUES (1, 9, 'workspace', ?, ?)`, db.Now(), db.Now()); err != nil {
		t.Fatalf("insert conversation: %v", err)
	}
	items := []attachment{
		{
			Name:          "processed.png",
			Type:          "image/png",
			Size:          321,
			Width:         64,
			Height:        64,
			OriginalName:  "my cat.jpg",
			WorkspacePath: "users/9/uploads/20260521/123-processed.png",
			URL:           "/api/workspace/files/users/9/uploads/20260521/123-processed.png",
		},
	}
	if _, err := store.DB.Exec(`INSERT INTO messages (conversation_id, user_id, role, content, status, attachments, metadata, version_group_id, version_index, is_active_version, sort_order, created_at, updated_at) VALUES (1, 9, 'user', '图在这里', 'completed', ?, '{}', 1, 1, 1, 10, ?, ?)`, attachmentsMetadata(items), db.Now(), db.Now()); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	prompt := server.workspaceSystemPrompt(9)
	if !strings.Contains(prompt, "my cat.jpg") || !strings.Contains(prompt, items[0].WorkspacePath) || !strings.Contains(prompt, "dimensions=64x64") {
		t.Fatalf("workspace prompt should include attachment metadata, got %s", prompt)
	}
	if strings.Contains(prompt, "data:image") {
		t.Fatalf("workspace prompt should not include inline image data: %s", prompt)
	}
}

func TestMessagePromptContentIncludesGeneratedImageURLs(t *testing.T) {
	metadata := assistantMetadataWithToolSteps([]responseToolStep{
		{
			Name:   "image_generate",
			Status: "completed",
			Output: mustJSONString(imageToolResult{
				OK:   true,
				Tool: "image_generate",
				Images: []imageToolResultImage{
					{URL: "https://example.com/generated-cat.png"},
					{B64JSON: "ZmFrZQ=="},
				},
			}),
		},
	})
	content := messagePromptContent(Message{
		Role:     "assistant",
		Content:  "",
		Metadata: metadata,
	})
	if !strings.Contains(content, "https://example.com/generated-cat.png") || !strings.Contains(content, "image_generate") {
		t.Fatalf("expected generated image URL in prompt content, got %s", content)
	}
	if strings.Contains(content, "ZmFrZQ==") || strings.Contains(content, "data:image") {
		t.Fatalf("generated image prompt should not include base64: %s", content)
	}
}

func TestSanitizeMessageMetadataForClientOmitsImageBase64WhenURLExists(t *testing.T) {
	output := mustJSONString(imageToolResult{
		OK:   true,
		Tool: "image_generate",
		Images: []imageToolResultImage{
			{URL: "/api/workspace/files/users/1/generated/cat.png", B64JSON: "ZmFrZQ==", WorkspacePath: "users/1/generated/cat.png"},
		},
	})
	metadata := mustJSONString(map[string]interface{}{
		"tool_steps": []map[string]interface{}{
			{
				"name":   "image_generate",
				"status": "completed",
				"output": output,
			},
		},
	})
	sanitized := sanitizeMessageMetadataForClient(metadata)
	if strings.Contains(sanitized, "ZmFrZQ==") || strings.Contains(sanitized, "b64_json") {
		t.Fatalf("sanitized metadata should omit image base64: %s", sanitized)
	}
	if !strings.Contains(sanitized, "/api/workspace/files/users/1/generated/cat.png") || !strings.Contains(sanitized, "users/1/generated/cat.png") {
		t.Fatalf("sanitized metadata should keep image references: %s", sanitized)
	}
}

func TestConversationResponseRewritesFailedSummaryForSuccessfulImageStep(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store, cfg: config.Config{WorkspacePath: t.TempDir()}}
	seedUserAndConversationForImageFlowTest(t, store)
	metadata := mustJSONString(map[string]interface{}{
		"tool_steps": []map[string]interface{}{
			{
				"name":   "image_generate",
				"status": "completed",
				"output": mustJSONString(imageToolResult{
					OK:   true,
					Tool: "image_generate",
					Images: []imageToolResultImage{
						{URL: "/api/workspace/files/users/1/generated/cat.png", WorkspacePath: "users/1/generated/cat.png"},
					},
				}),
			},
		},
	})
	if _, err := store.DB.Exec(`INSERT INTO messages (conversation_id, user_id, role, content, status, attachments, metadata, version_group_id, version_index, is_active_version, sort_order, created_at, updated_at) VALUES (1, 1, 'assistant', '模型调用失败：模型服务暂时不可用，请稍后重试', 'completed', '[]', ?, 1, 1, 1, 20, ?, ?)`, metadata, db.Now(), db.Now()); err != nil {
		t.Fatalf("insert assistant: %v", err)
	}
	conversation, err := server.getConversationForUser(1, 1)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	messages, err := server.listMessages(conversation.ID, 1)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) == 0 || messages[len(messages)-1].Content != "图片已生成。" {
		t.Fatalf("expected successful image fallback text, got %#v", messages)
	}
	if strings.Contains(messages[len(messages)-1].Metadata, "b64_json") {
		t.Fatalf("expected sanitized metadata, got %s", messages[len(messages)-1].Metadata)
	}
}

func TestExecuteResponseToolDisablesInactiveSearchModeTools(t *testing.T) {
	server := &Server{}
	searchDisabled := server.executeResponseTool(responseToolCall{Name: "web_search", Arguments: `{"query":"news"}`}, searchToolModeSearching)
	if !strings.Contains(searchDisabled, "disabled") {
		t.Fatalf("expected web_search disabled in searching mode: %s", searchDisabled)
	}
	searchingDisabled := server.executeResponseTool(responseToolCall{Name: "searching", Arguments: `{"query":"news"}`}, searchToolModeUniFuncs)
	if !strings.Contains(searchingDisabled, "disabled") {
		t.Fatalf("expected searching disabled in unifuncs mode: %s", searchingDisabled)
	}
}

func TestExecuteSearchingTool(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["model"] != "search-model" || body["id"] != "api-123" || body["api_id"] != "api-123" {
			t.Fatalf("unexpected searching request body: %#v", body)
		}
		messages, _ := body["messages"].([]interface{})
		if len(messages) != 2 || !strings.Contains(fmt.Sprint(messages[1]), "OpenAI news") {
			t.Fatalf("unexpected messages: %#v", body["messages"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": "联网搜索 LLM 的完整输出",
					},
				},
			},
		})
	}))
	defer upstream.Close()

	store := newTestStore(t)
	server := &Server{store: store}
	now := db.Now()
	for key, value := range map[string]string{
		"searching_base_url": upstream.URL,
		"searching_api_key":  "search-key",
		"searching_model":    "search-model",
		"searching_api_id":   "api-123",
	} {
		_, err := store.DB.Exec(`INSERT INTO app_settings (key, value, created_at, updated_at) VALUES (?, ?, ?, ?)`, key, value, now, now)
		if err != nil {
			t.Fatalf("insert setting: %v", err)
		}
	}
	raw := server.executeSearchingTool(`{"query":"OpenAI news"}`)
	var got struct {
		OK      bool   `json:"ok"`
		Tool    string `json:"tool"`
		Query   string `json:"query"`
		Model   string `json:"model"`
		APIID   string `json:"api_id"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !got.OK || got.Tool != "searching" || got.Query != "OpenAI news" || got.Model != "search-model" || got.APIID != "api-123" {
		t.Fatalf("unexpected output: %s", raw)
	}
	if got.Content != "联网搜索 LLM 的完整输出" {
		t.Fatalf("unexpected content: %#v", got)
	}
	if gotAuth != "Bearer search-key" {
		t.Fatalf("unexpected auth header: %s", gotAuth)
	}
}

func TestStreamResponsesToolLoop(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store}

	var (
		mu    sync.Mutex
		calls []string
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		calls = append(calls, string(body))
		callIndex := len(calls)
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		switch callIndex {
		case 1:
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"id":        "fc_1",
							"call_id":   "call_1",
							"type":      "function_call",
							"name":      "get_current_time",
							"arguments": `{"timezone":"Mars/Phobos"}`,
						},
					},
				},
			})+"\n\n")
		case 2:
			fmt.Fprint(w, "event: response.output_text.delta\n")
			fmt.Fprint(w, "data: {\"delta\":\"时间工具已返回。\"}\n\n")
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"type": "message",
							"role": "assistant",
							"content": []map[string]interface{}{
								{"type": "output_text", "text": "时间工具已返回。"},
							},
						},
					},
				},
			})+"\n\n")
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer upstream.Close()

	provider := runtimeProvider{
		Base:        upstream.URL,
		Key:         "test-key",
		Model:       "gpt-test",
		RequestMode: "responses",
	}
	recorder := httptest.NewRecorder()
	toolSteps := make([]responseToolStep, 0)
	text, err := server.streamResponses(context.Background(), recorder, provider, &Conversation{}, 1, 1, "现在几点", "", "", "", &toolSteps, nil, nil, nil)
	if err != nil {
		t.Fatalf("stream responses: %v", err)
	}
	if text != "时间工具已返回。" {
		t.Fatalf("unexpected text: %q", text)
	}
	if len(toolSteps) != 2 {
		t.Fatalf("expected 2 tool steps, got %d", len(toolSteps))
	}
	if toolSteps[0].Status != "running" || toolSteps[1].Status != "completed" {
		t.Fatalf("unexpected tool step statuses: %#v", toolSteps)
	}
	if toolSteps[0].ContentOffset != 0 || toolSteps[1].ContentOffset != 0 {
		t.Fatalf("expected tool steps at content offset 0, got %#v", toolSteps)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("expected 2 upstream calls, got %d", len(calls))
	}
	secondRequest := parseJSONForTest(t, calls[1])
	inputItems, ok := secondRequest["input"].([]interface{})
	if !ok {
		t.Fatalf("second request missing input array: %v", secondRequest)
	}
	var foundOutput bool
	for _, item := range inputItems {
		obj, _ := item.(map[string]interface{})
		if obj["type"] != "function_call_output" {
			continue
		}
		foundOutput = true
		if obj["call_id"] != "call_1" {
			t.Fatalf("unexpected call_id: %v", obj["call_id"])
		}
		if !strings.Contains(fmt.Sprint(obj["output"]), `"ok":false`) {
			t.Fatalf("tool output should contain invalid timezone error: %v", obj["output"])
		}
	}
	if !foundOutput {
		t.Fatalf("second request missing function_call_output: %s", calls[1])
	}
	if !strings.Contains(recorder.Body.String(), "event: tool_steps") {
		t.Fatalf("expected tool_steps event in stream: %s", recorder.Body.String())
	}
	if !strings.Contains(calls[0], `"tool_choice":"auto"`) {
		t.Fatalf("first request missing tool_choice: %s", calls[0])
	}
}

func TestStreamChatCompletionsIncludesRuntimeSystemPrompt(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store}

	var requestBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		requestBody = string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"好\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	provider := runtimeProvider{
		Base:  upstream.URL,
		Key:   "test-key",
		Model: "gpt-test",
	}
	text, err := server.streamChatCompletions(context.Background(), httptest.NewRecorder(), provider, &Conversation{}, 1, 1, "你好", "", "", nil, nil)
	if err != nil {
		t.Fatalf("stream chat completions: %v", err)
	}
	if text != "好" {
		t.Fatalf("unexpected text: %q", text)
	}
	body := parseJSONForTest(t, requestBody)
	messages, ok := body["messages"].([]interface{})
	if !ok || len(messages) == 0 {
		t.Fatalf("request missing messages: %s", requestBody)
	}
	first, _ := messages[0].(map[string]interface{})
	if first["role"] != "system" || !strings.Contains(fmt.Sprint(first["content"]), runtimeSystemToolPrinciple) {
		t.Fatalf("first message should be runtime system prompt, got %#v", first)
	}
}

func TestStreamResponsesExecutesOnlyOneToolCallPerRound(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store}

	var (
		mu    sync.Mutex
		calls []string
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		calls = append(calls, string(body))
		callIndex := len(calls)
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		switch callIndex {
		case 1:
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"id":        "fc_1",
							"call_id":   "call_1",
							"type":      "function_call",
							"name":      "get_current_time",
							"arguments": `{"timezone":"UTC"}`,
						},
						{
							"id":        "fc_ignored",
							"call_id":   "call_ignored",
							"type":      "function_call",
							"name":      "get_current_time",
							"arguments": `{"timezone":"Asia/Shanghai"}`,
						},
					},
				},
			})+"\n\n")
		case 2:
			fmt.Fprint(w, "event: response.output_text.delta\n")
			fmt.Fprint(w, "data: {\"delta\":\"第一个工具完成。\"}\n\n")
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"type": "message",
							"role": "assistant",
							"content": []map[string]interface{}{
								{"type": "output_text", "text": "第一个工具完成。"},
							},
						},
						{
							"id":        "fc_2",
							"call_id":   "call_2",
							"type":      "function_call",
							"name":      "get_current_time",
							"arguments": `{"timezone":"Asia/Shanghai"}`,
						},
					},
				},
			})+"\n\n")
		case 3:
			fmt.Fprint(w, "event: response.output_text.delta\n")
			fmt.Fprint(w, "data: {\"delta\":\"两个工具都完成。\"}\n\n")
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"type": "message",
							"role": "assistant",
							"content": []map[string]interface{}{
								{"type": "output_text", "text": "两个工具都完成。"},
							},
						},
					},
				},
			})+"\n\n")
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer upstream.Close()

	provider := runtimeProvider{
		Base:        upstream.URL,
		Key:         "test-key",
		Model:       "gpt-test",
		RequestMode: "responses",
	}
	recorder := httptest.NewRecorder()
	toolSteps := make([]responseToolStep, 0)
	text, err := server.streamResponses(context.Background(), recorder, provider, &Conversation{}, 1, 1, "查两个时间", "", "", "", &toolSteps, nil, nil, nil)
	if err != nil {
		t.Fatalf("stream responses: %v", err)
	}
	if text != "第一个工具完成。两个工具都完成。" {
		t.Fatalf("unexpected text: %q", text)
	}
	if len(toolSteps) != 4 {
		t.Fatalf("expected two executed tools, got %#v", toolSteps)
	}
	if toolSteps[0].CallID != "call_1" || toolSteps[2].CallID != "call_2" {
		t.Fatalf("unexpected executed tool order: %#v", toolSteps)
	}
	if toolSteps[2].ContentOffset <= 0 {
		t.Fatalf("second tool should appear after visible text, got offset %d", toolSteps[2].ContentOffset)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("expected 3 upstream calls, got %d", len(calls))
	}
	if strings.Contains(calls[1], "call_ignored") {
		t.Fatalf("ignored second tool call from first round should not be sent back: %s", calls[1])
	}
	if !strings.Contains(calls[1], "call_1") || !strings.Contains(calls[2], "call_2") {
		t.Fatalf("expected executed tool calls in follow-up requests: %#v", calls)
	}
}

func TestStreamResponsesPromptsForTextBetweenTools(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store}

	var (
		mu    sync.Mutex
		calls []string
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		calls = append(calls, string(body))
		callIndex := len(calls)
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		switch callIndex {
		case 1:
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"id":        "fc_1",
							"call_id":   "call_1",
							"type":      "function_call",
							"name":      "get_current_time",
							"arguments": `{"timezone":"UTC"}`,
						},
					},
				},
			})+"\n\n")
		case 2:
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"id":        "fc_blocked",
							"call_id":   "call_blocked",
							"type":      "function_call",
							"name":      "get_current_time",
							"arguments": `{"timezone":"Asia/Shanghai"}`,
						},
					},
				},
			})+"\n\n")
		case 3:
			fmt.Fprint(w, "event: response.output_text.delta\n")
			fmt.Fprint(w, "data: {\"delta\":\"我先说明一下，再查第二个时间。\"}\n\n")
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"type": "message",
							"role": "assistant",
							"content": []map[string]interface{}{
								{"type": "output_text", "text": "我先说明一下，再查第二个时间。"},
							},
						},
						{
							"id":        "fc_2",
							"call_id":   "call_2",
							"type":      "function_call",
							"name":      "get_current_time",
							"arguments": `{"timezone":"Asia/Shanghai"}`,
						},
					},
				},
			})+"\n\n")
		case 4:
			fmt.Fprint(w, "event: response.output_text.delta\n")
			fmt.Fprint(w, "data: {\"delta\":\"完成。\"}\n\n")
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"type": "message",
							"role": "assistant",
							"content": []map[string]interface{}{
								{"type": "output_text", "text": "完成。"},
							},
						},
					},
				},
			})+"\n\n")
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer upstream.Close()

	provider := runtimeProvider{
		Base:        upstream.URL,
		Key:         "test-key",
		Model:       "gpt-test",
		RequestMode: "responses",
	}
	recorder := httptest.NewRecorder()
	toolSteps := make([]responseToolStep, 0)
	text, err := server.streamResponses(context.Background(), recorder, provider, &Conversation{}, 1, 1, "查两个时间", "", "", "", &toolSteps, nil, nil, nil)
	if err != nil {
		t.Fatalf("stream responses: %v", err)
	}
	if text != "我先说明一下，再查第二个时间。完成。" {
		t.Fatalf("unexpected text: %q", text)
	}
	if len(toolSteps) != 4 {
		t.Fatalf("expected exactly two executed tools, got %#v", toolSteps)
	}
	if toolSteps[0].CallID != "call_1" || toolSteps[2].CallID != "call_2" {
		t.Fatalf("blocked tool should not be executed, got %#v", toolSteps)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 4 {
		t.Fatalf("expected 4 upstream calls, got %d", len(calls))
	}
	if !strings.Contains(calls[2], responsesToolPacingRetryInstruction) {
		t.Fatalf("third request should include tool pacing retry instruction: %s", calls[2])
	}
	if strings.Contains(calls[2], "call_blocked") {
		t.Fatalf("blocked tool call should not be sent back as executed context: %s", calls[2])
	}
	if !strings.Contains(calls[3], "call_2") {
		t.Fatalf("fourth request should include the allowed second tool output: %s", calls[3])
	}
}

func TestReadResponsesStreamFindsToolCallFromOutputItemDone(t *testing.T) {
	stream := strings.Join([]string{
		"event: response.output_item.added",
		`data: {"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","status":"in_progress","arguments":"","call_id":"call_1","name":"get_current_time"}}`,
		"",
		"event: response.output_item.done",
		`data: {"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","status":"completed","arguments":"{\"timezone\":\"Asia/Shanghai\"}","call_id":"call_1","name":"get_current_time"}}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"output":[]}}`,
		"",
	}, "\n")

	result, err := readResponsesStream(context.Background(), httptest.NewRecorder(), strings.NewReader(stream), nil, nil)
	if err != nil {
		t.Fatalf("read responses stream: %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", result.ToolCalls)
	}
	call := result.ToolCalls[0]
	if call.CallID != "call_1" || call.Name != "get_current_time" || call.Arguments != `{"timezone":"Asia/Shanghai"}` {
		t.Fatalf("unexpected tool call: %#v", call)
	}
	if len(result.Output) != 1 || result.Output[0].Status != "completed" {
		t.Fatalf("expected merged completed output item, got %#v", result.Output)
	}
}

func TestReadResponsesStreamStopsAfterSentText(t *testing.T) {
	stream := strings.Join([]string{
		"event: response.output_text.delta",
		`data: {"delta":"已发送内容"}`,
		"",
		"event: response.output_text.delta",
		`data: {"delta":"不应保存"}`,
		"",
	}, "\n")
	calls := 0
	result, err := readResponsesStream(context.Background(), httptest.NewRecorder(), strings.NewReader(stream), nil, func() bool {
		calls++
		return calls > 2
	})
	if !errors.Is(err, errGenerationStopped) {
		t.Fatalf("expected stopped error, got %v", err)
	}
	if result.Text != "已发送内" {
		t.Fatalf("expected only sent text to be kept, got %q", result.Text)
	}
}

func TestStreamResponsesRetriesUnexpectedEOFBeforeToolCall(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store}
	var calls []map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected responses path: %s", r.URL.Path)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode responses body: %v", err)
		}
		calls = append(calls, body)
		w.Header().Set("Content-Type", "text/event-stream")
		switch len(calls) {
		case 1:
			w.Header().Set("Content-Length", "9999")
			fmt.Fprint(w, "event: response.output_text.delta\n")
			fmt.Fprint(w, `data: {"delta":"我会查一下。"}`)
			fmt.Fprint(w, "\n\n")
		case 2:
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"type":      "function_call",
							"id":        "fc_time",
							"call_id":   "call_time",
							"name":      "get_current_time",
							"arguments": `{"timezone":"Asia/Shanghai"}`,
							"status":    "completed",
						},
					},
				},
			})+"\n\n")
		case 3:
			fmt.Fprint(w, "event: response.output_text.delta\n")
			fmt.Fprint(w, `data: {"delta":"时间已返回"}`)
			fmt.Fprint(w, "\n\n")
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"type": "message",
							"role": "assistant",
							"content": []map[string]string{
								{"type": "output_text", "text": "时间已返回"},
							},
						},
					},
				},
			})+"\n\n")
		default:
			t.Fatalf("unexpected extra responses call")
		}
	}))
	defer upstream.Close()

	var persisted []string
	provider := runtimeProvider{Base: upstream.URL, Key: "llm-key", Model: "main-model", RequestMode: "responses"}
	var toolSteps []responseToolStep
	text, err := server.streamResponses(context.Background(), httptest.NewRecorder(), provider, &Conversation{}, 1, 1, "现在几点", "", "", "", &toolSteps, func(text string, force bool) {
		persisted = append(persisted, text)
	}, nil, nil)
	if err != nil {
		t.Fatalf("stream responses: %v", err)
	}
	if text != "我会查一下。时间已返回" {
		t.Fatalf("unexpected retried text: %q", text)
	}
	if len(calls) != 3 {
		t.Fatalf("expected retry plus tool-result round, got %d calls", len(calls))
	}
	secondRequest, _ := json.Marshal(calls[1])
	if !strings.Contains(string(secondRequest), responsesStreamReconnectRetryInstruction) || !strings.Contains(string(secondRequest), "我会查一下。") {
		t.Fatalf("expected retry request to include reconnect instruction and partial text: %s", secondRequest)
	}
	if len(toolSteps) != 2 || toolSteps[0].Status != "running" || toolSteps[1].Status != "completed" {
		t.Fatalf("expected retried tool step to complete, got %#v", toolSteps)
	}
	if len(persisted) == 0 || !strings.Contains(persisted[len(persisted)-1], "时间已返回") {
		t.Fatalf("expected persisted final text, got %#v", persisted)
	}
}

func TestStreamResponsesPreservesReasoningSummaryForToolContinuation(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store}
	var secondRequest map[string]interface{}
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected responses path: %s", r.URL.Path)
		}
		calls++
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode responses body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls {
		case 1:
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"id":      "rs_1",
							"type":    "reasoning",
							"summary": []interface{}{},
						},
						{
							"type":      "function_call",
							"id":        "fc_time",
							"call_id":   "call_time",
							"name":      "get_current_time",
							"arguments": `{"timezone":"Asia/Shanghai"}`,
							"status":    "completed",
						},
					},
				},
			})+"\n\n")
		case 2:
			secondRequest = body
			fmt.Fprint(w, "event: response.output_text.delta\n")
			fmt.Fprint(w, `data: {"delta":"完成"}`)
			fmt.Fprint(w, "\n\n")
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"type": "message",
							"role": "assistant",
							"content": []map[string]string{
								{"type": "output_text", "text": "完成"},
							},
						},
					},
				},
			})+"\n\n")
		default:
			t.Fatalf("unexpected extra responses call")
		}
	}))
	defer upstream.Close()

	provider := runtimeProvider{Base: upstream.URL, Key: "llm-key", Model: "main-model", RequestMode: "responses"}
	text, err := server.streamResponses(context.Background(), httptest.NewRecorder(), provider, &Conversation{}, 1, 1, "现在几点", "", "", "", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("stream responses: %v", err)
	}
	if text != "完成" {
		t.Fatalf("unexpected text: %q", text)
	}
	input, _ := secondRequest["input"].([]interface{})
	if len(input) < 1 {
		t.Fatalf("expected second request input, got %#v", secondRequest)
	}
	var reasoning map[string]interface{}
	for _, item := range input {
		obj, _ := item.(map[string]interface{})
		if obj["type"] == "reasoning" {
			reasoning = obj
			break
		}
	}
	if reasoning == nil {
		t.Fatalf("expected reasoning output item to be preserved, got %#v", input)
	}
	if _, ok := reasoning["summary"]; !ok {
		t.Fatalf("expected reasoning summary to be preserved, got %#v", reasoning)
	}
}

func TestUserFacingModelStreamErrorHidesUnexpectedEOF(t *testing.T) {
	if got := userFacingModelStreamError(io.ErrUnexpectedEOF); strings.Contains(strings.ToLower(got), "eof") || got == "" {
		t.Fatalf("expected friendly EOF message, got %q", got)
	}
}

func TestStreamResponsesWithoutTools(t *testing.T) {
	store := newTestStore(t)
	server := &Server{store: store}

	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\n")
		fmt.Fprint(w, "data: {\"delta\":\"普通回答\"}\n\n")
		fmt.Fprint(w, "event: response.completed\n")
		fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
			"response": map[string]interface{}{
				"output": []map[string]interface{}{
					{
						"type": "message",
						"role": "assistant",
						"content": []map[string]interface{}{
							{"type": "output_text", "text": "普通回答"},
						},
					},
				},
			},
		})+"\n\n")
	}))
	defer upstream.Close()

	provider := runtimeProvider{
		Base:        upstream.URL,
		Key:         "test-key",
		Model:       "gpt-test",
		RequestMode: "responses",
	}
	recorder := httptest.NewRecorder()
	text, err := server.streamResponses(context.Background(), recorder, provider, &Conversation{}, 1, 1, "你好", "", "", "", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("stream responses: %v", err)
	}
	if text != "普通回答" {
		t.Fatalf("unexpected text: %q", text)
	}
	if calls != 1 {
		t.Fatalf("expected 1 upstream call, got %d", calls)
	}
	if strings.Contains(recorder.Body.String(), "event: tool_steps") {
		t.Fatalf("did not expect tool_steps for normal response: %s", recorder.Body.String())
	}
}

func hasToolForTest(tools []map[string]interface{}, name string) bool {
	for _, tool := range tools {
		if tool["name"] == name {
			return true
		}
	}
	return false
}

func mustJSONStringForTest(v interface{}) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func parseJSONForTest(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("parse json: %v", err)
	}
	return got
}

func seedUserAndConversationForImageFlowTest(t *testing.T, store *db.Store) {
	t.Helper()
	now := db.Now()
	if _, err := store.DB.Exec(`INSERT INTO users (id, email, name, password_hash, role, status, created_at, updated_at) VALUES (1, 'image-flow@test.local', 'image-flow', 'hash', 'user', 'active', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := store.DB.Exec(`INSERT INTO conversations (id, session_id, user_id, title, created_at, updated_at) VALUES (1, '11111111111111111111111111111111', 1, 'image flow', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
}

func assertResponsesImageModeRequestForTest(t *testing.T, request map[string]interface{}) {
	t.Helper()
	if request["model"] != "gpt-5.5" {
		t.Fatalf("expected responses image model, got %#v", request["model"])
	}
	if request["stream"] != true {
		t.Fatalf("expected responses image mode to stream, got %#v", request["stream"])
	}
	tools, _ := request["tools"].([]interface{})
	if len(tools) != 1 || tools[0].(map[string]interface{})["type"] != "image_generation" {
		t.Fatalf("expected image_generation tool, got %#v", request["tools"])
	}
	if tools[0].(map[string]interface{})["partial_images"] != float64(1) {
		t.Fatalf("expected responses image generation partial_images=1, got %#v", tools[0])
	}
	input, _ := request["input"].([]interface{})
	if len(input) != 1 {
		t.Fatalf("expected responses input, got %#v", request["input"])
	}
	content, _ := input[0].(map[string]interface{})["content"].([]interface{})
	var sawText, sawImage bool
	for _, item := range content {
		obj, _ := item.(map[string]interface{})
		if obj["type"] == "input_text" && strings.Contains(fmt.Sprint(obj["text"]), "猫") {
			sawText = true
		}
		if obj["type"] == "input_image" && obj["image_url"] == "https://example.com/ref.png" {
			sawImage = true
		}
	}
	if !sawText || !sawImage {
		t.Fatalf("expected responses image request text and image, got %#v", content)
	}
}

func assertChatCompletionsImageModeRequestForTest(t *testing.T, request map[string]interface{}) {
	t.Helper()
	if request["model"] != "gpt-4o-image" {
		t.Fatalf("expected chat image model, got %#v", request["model"])
	}
	if request["stream"] != true {
		t.Fatalf("expected chat image mode to stream, got %#v", request["stream"])
	}
	messages, _ := request["messages"].([]interface{})
	if len(messages) != 1 {
		t.Fatalf("expected one chat message, got %#v", request["messages"])
	}
	content, _ := messages[0].(map[string]interface{})["content"].([]interface{})
	var sawText, sawImage bool
	for _, item := range content {
		obj, _ := item.(map[string]interface{})
		if obj["type"] == "text" && strings.Contains(fmt.Sprint(obj["text"]), "狗") {
			sawText = true
		}
		if obj["type"] == "image_url" {
			imageURL, _ := obj["image_url"].(map[string]interface{})
			if imageURL["url"] == "https://example.com/ref.png" {
				sawImage = true
			}
		}
	}
	if !sawText || !sawImage {
		t.Fatalf("expected chat image request text and image_url, got %#v", content)
	}
}

func assertFunctionCallOutputImageForTest(t *testing.T, request map[string]interface{}, callID string, check func(imageToolResult)) {
	t.Helper()
	rawInput, ok := request["input"].([]interface{})
	if !ok {
		t.Fatalf("expected second request input, got %#v", request)
	}
	for _, item := range rawInput {
		obj, _ := item.(map[string]interface{})
		if obj["type"] != "function_call_output" || obj["call_id"] != callID {
			continue
		}
		outputRaw, _ := obj["output"].(string)
		var output imageToolResult
		if err := json.Unmarshal([]byte(outputRaw), &output); err != nil {
			t.Fatalf("unmarshal tool output: %v", err)
		}
		if !output.OK || output.Tool != "image_generate" {
			t.Fatalf("unexpected image tool output: %#v", output)
		}
		if strings.Contains(outputRaw, "data:image/") || strings.Contains(outputRaw, "base64") || strings.Contains(outputRaw, "b64_json") {
			t.Fatalf("model-facing image tool output should not contain inline image data: %.240s", outputRaw)
		}
		check(output)
		return
	}
	t.Fatalf("expected function_call_output for %s in %#v", callID, request["input"])
}

func llmToolServerForTest(t *testing.T, arguments, finalText string) *httptest.Server {
	t.Helper()
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected LLM path: %s", r.URL.Path)
		}
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls {
		case 1:
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"type":      "function_call",
							"id":        fmt.Sprintf("fc_%d", calls),
							"call_id":   fmt.Sprintf("call_%d", calls),
							"name":      "image_generate",
							"arguments": arguments,
							"status":    "completed",
						},
					},
				},
			})+"\n\n")
		case 2:
			fmt.Fprint(w, "event: response.output_text.delta\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]string{"delta": finalText})+"\n\n")
			fmt.Fprint(w, "event: response.completed\n")
			fmt.Fprint(w, "data: "+mustJSONStringForTest(map[string]interface{}{
				"response": map[string]interface{}{
					"output": []map[string]interface{}{
						{
							"type": "message",
							"role": "assistant",
							"content": []map[string]string{
								{"type": "output_text", "text": finalText},
							},
						},
					},
				},
			})+"\n\n")
		default:
			t.Fatalf("unexpected extra LLM call")
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func runSingleImageToolConversationForTest(t *testing.T, server *Server, provider runtimeProvider, latest string) string {
	t.Helper()
	var toolSteps []responseToolStep
	_, err := server.streamResponses(context.Background(), httptest.NewRecorder(), provider, nil, 1, 1, latest, "", "", "https://chat.example.com", &toolSteps, nil, nil, nil)
	if err != nil {
		t.Fatalf("stream image conversation: %v", err)
	}
	if !hasSuccessfulImageToolStep(toolSteps) {
		t.Fatalf("expected successful image tool step, got %#v", toolSteps)
	}
	return assistantMetadataWithToolSteps(toolSteps)
}

func firstGeneratedWorkspacePathForTest(t *testing.T, metadata string) string {
	t.Helper()
	var payload struct {
		ToolSteps []responseToolStep `json:"tool_steps"`
	}
	if err := json.Unmarshal([]byte(metadata), &payload); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	for _, step := range payload.ToolSteps {
		var output imageToolResult
		if err := json.Unmarshal([]byte(step.Output), &output); err != nil || !output.OK {
			continue
		}
		for _, image := range output.Images {
			if strings.TrimSpace(image.WorkspacePath) != "" {
				return strings.TrimSpace(image.WorkspacePath)
			}
		}
	}
	t.Fatalf("metadata did not contain generated workspace path: %s", metadata)
	return ""
}

func assertResponsesImageRequestUsesSignedWorkspaceURLForTest(t *testing.T, request map[string]interface{}, workspacePath string) {
	t.Helper()
	input, _ := request["input"].([]interface{})
	if len(input) != 1 {
		t.Fatalf("expected responses input, got %#v", request["input"])
	}
	content, _ := input[0].(map[string]interface{})["content"].([]interface{})
	for _, item := range content {
		obj, _ := item.(map[string]interface{})
		if obj["type"] != "input_image" {
			continue
		}
		urlValue, _ := obj["image_url"].(string)
		assertSignedWorkspaceURLForTest(t, urlValue, workspacePath)
		return
	}
	t.Fatalf("expected input_image with signed workspace URL, got %#v", content)
}

func assertChatImageRequestUsesSignedWorkspaceURLForTest(t *testing.T, request map[string]interface{}, workspacePath string) {
	t.Helper()
	messages, _ := request["messages"].([]interface{})
	if len(messages) != 1 {
		t.Fatalf("expected one chat message, got %#v", request["messages"])
	}
	content, _ := messages[0].(map[string]interface{})["content"].([]interface{})
	for _, item := range content {
		obj, _ := item.(map[string]interface{})
		if obj["type"] != "image_url" {
			continue
		}
		imageURL, _ := obj["image_url"].(map[string]interface{})
		urlValue, _ := imageURL["url"].(string)
		assertSignedWorkspaceURLForTest(t, urlValue, workspacePath)
		return
	}
	t.Fatalf("expected image_url with signed workspace URL, got %#v", content)
}

func assertSignedWorkspaceURLForTest(t *testing.T, urlValue, workspacePath string) {
	t.Helper()
	expectedPrefix := "https://chat.example.com/api/workspace/public/" + workspacePath + "?"
	if !strings.HasPrefix(urlValue, expectedPrefix) || !strings.Contains(urlValue, "expires=") || !strings.Contains(urlValue, "sig=") {
		t.Fatalf("expected signed URL for %s, got %s", workspacePath, urlValue)
	}
	if strings.Contains(urlValue, "base64") || strings.Contains(urlValue, "data:image") {
		t.Fatalf("signed URL should not include inline image data: %s", urlValue)
	}
}
