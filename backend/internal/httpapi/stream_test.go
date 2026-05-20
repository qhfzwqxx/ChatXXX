package httpapi

import (
	"chatxxx/backend/internal/config"
	"chatxxx/backend/internal/db"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	if hasToolForTest(unifuncsTools, "searching") {
		t.Fatalf("unifuncs mode should not expose searching: %#v", unifuncsTools)
	}
	searchingTools := responseToolDefinitions(searchToolModeSearching)
	if !hasToolForTest(searchingTools, "searching") {
		t.Fatalf("searching mode should expose searching: %#v", searchingTools)
	}
	if hasToolForTest(searchingTools, "web_search") || hasToolForTest(searchingTools, "web_reader") {
		t.Fatalf("searching mode should not expose UniFuncs tools: %#v", searchingTools)
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
	text, err := server.streamResponses(context.Background(), recorder, provider, &Conversation{}, 1, 1, "现在几点", &toolSteps, nil)
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

	result, err := readResponsesStream(context.Background(), httptest.NewRecorder(), strings.NewReader(stream), nil)
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
	result, err := readResponsesStream(context.Background(), httptest.NewRecorder(), strings.NewReader(stream), func() bool {
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
	text, err := server.streamResponses(context.Background(), recorder, provider, &Conversation{}, 1, 1, "你好", nil, nil)
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
