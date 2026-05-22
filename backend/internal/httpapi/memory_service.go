package httpapi

import (
	"bytes"
	"chatxxx/backend/internal/db"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxMemoryContentRunes  = 4000
	embeddingBatchSize     = 32
	memoryFallbackEnabled  = false
	memoryVectorCandidateK = 100
	memoryVectorTopK       = 10
	memoryDefaultInjectK   = 20
	memoryMaxInjectLimit   = 50
)

type embeddingProvider struct {
	ID       int64
	Name     string
	Base     string
	Key      string
	Model    string
	Endpoint string
	Active   bool
}

type memoryAction struct {
	Action   string `json:"action"`
	ID       int64  `json:"id"`
	Content  string `json:"content"`
	Category string `json:"category"`
}

type memoryActionResult struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Ignored int `json:"ignored"`
	Skipped int `json:"skipped"`
}

type memoryEmbeddingResult struct {
	Success int      `json:"success"`
	Failed  int      `json:"failed"`
	Model   string   `json:"model"`
	Dim     int      `json:"dim"`
	Errors  []string `json:"errors,omitempty"`
}

type memorySearchHit struct {
	Memory
	Score       float64 `json:"score"`
	VectorScore float64 `json:"vector_score"`
	RerankScore float64 `json:"rerank_score,omitempty"`
	Reason      string  `json:"reason,omitempty"`
}

type memoryHitPayload struct {
	Method   string            `json:"method"`
	Model    string            `json:"model"`
	Dim      int               `json:"dim"`
	Memories []memorySearchHit `json:"memories"`
}

type scoredMemory struct {
	Memory Memory
	Score  float64
}

type memoryRerankItem struct {
	ID          int64   `json:"id"`
	RerankScore float64 `json:"rerank_score"`
	Reason      string  `json:"reason"`
}

type memoryRetrievalResult struct {
	Memories []Memory          `json:"memories"`
	Hits     []memorySearchHit `json:"hits"`
	Model    string            `json:"model"`
	Dim      int               `json:"dim"`
	Method   string            `json:"method"`
}

func (s *Server) settingInt(key string, fallback int) int {
	value := strings.TrimSpace(s.settingValue(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func (s *Server) getProviderByID(id int64) (*runtimeProvider, error) {
	if id <= 0 {
		return nil, sql.ErrNoRows
	}
	row := s.store.DB.QueryRow(`SELECT id, name, base_url, api_key, model, request_mode, response_format, is_active FROM providers WHERE id=? AND is_active=1`, id)
	var p runtimeProvider
	var active int
	if err := row.Scan(&p.ID, &p.Name, &p.Base, &p.Key, &p.Model, &p.RequestMode, &p.ResponseFormat, &active); err != nil {
		return nil, err
	}
	p.Active = active == 1
	return &p, nil
}

func (s *Server) getMemoryProvider() (*runtimeProvider, error) {
	id := int64(s.settingInt("memory_provider_id", 0))
	if id <= 0 {
		return nil, sql.ErrNoRows
	}
	return s.getProviderByID(id)
}

func (s *Server) memoryInjectLimit() int {
	return max(1, min(s.settingInt("memory_inject_limit", memoryDefaultInjectK), memoryMaxInjectLimit))
}

func (s *Server) memoryVectorLimit(limit int) int {
	defaultLimit := max(1, limit)
	return max(1, min(s.settingInt("embedding_top_k", defaultLimit), memoryVectorCandidateK))
}

func (s *Server) getEmbeddingProvider() (*embeddingProvider, error) {
	id := int64(s.settingInt("embedding_provider_id", 0))
	if id <= 0 {
		return nil, sql.ErrNoRows
	}
	row := s.store.DB.QueryRow(`SELECT id, name, base_url, api_key, model, is_active FROM providers WHERE id=? AND is_active=1`, id)
	var p embeddingProvider
	var active int
	if err := row.Scan(&p.ID, &p.Name, &p.Base, &p.Key, &p.Model, &active); err != nil {
		return nil, err
	}
	p.Active = active == 1
	p.Endpoint = resolveEmbeddingEndpoint(p.Base)
	if strings.TrimSpace(p.Key) == "" || strings.TrimSpace(p.Model) == "" || strings.TrimSpace(p.Endpoint) == "" {
		return nil, errors.New("embedding provider is incomplete")
	}
	return &p, nil
}

func resolveEmbeddingEndpoint(baseURL string) string {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return strings.TrimRight(raw, "/") + "/embeddings"
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/embeddings"):
		parsed.Path = path
	case strings.HasSuffix(path, "/chat/completions"):
		parsed.Path = strings.TrimSuffix(path, "/chat/completions") + "/embeddings"
	case strings.HasSuffix(path, "/chat"):
		parsed.Path = strings.TrimSuffix(path, "/chat") + "/embeddings"
	case strings.HasSuffix(path, "/v1"):
		parsed.Path = path + "/embeddings"
	default:
		parsed.Path = path + "/embeddings"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func (s *Server) embedTexts(ctx context.Context, provider *embeddingProvider, texts []string) ([][]float32, int, error) {
	cleaned := make([]string, 0, len(texts))
	for _, text := range texts {
		text = strings.TrimSpace(text)
		if text != "" {
			cleaned = append(cleaned, text)
		}
	}
	if len(cleaned) == 0 {
		return nil, 0, errors.New("no text to embed")
	}
	input := interface{}(cleaned)
	if len(cleaned) == 1 {
		input = cleaned[0]
	}
	raw, _ := json.Marshal(map[string]interface{}{
		"model": provider.Model,
		"input": input,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+provider.Key)
	client := &http.Client{Timeout: time.Duration(max(15, len(cleaned)*2+10)) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("embedding returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, 0, err
	}
	if strings.TrimSpace(payload.Error.Message) != "" {
		return nil, 0, errors.New(strings.TrimSpace(payload.Error.Message))
	}
	if len(payload.Data) != len(cleaned) {
		return nil, 0, fmt.Errorf("embedding count mismatch")
	}
	vectors := make([][]float32, 0, len(payload.Data))
	dim := 0
	for _, item := range payload.Data {
		if len(item.Embedding) == 0 {
			return nil, 0, fmt.Errorf("empty embedding")
		}
		vec := make([]float32, len(item.Embedding))
		for i, value := range item.Embedding {
			vec[i] = float32(value)
		}
		if dim == 0 {
			dim = len(vec)
		} else if dim != len(vec) {
			return nil, 0, fmt.Errorf("embedding dimensions differ")
		}
		vectors = append(vectors, vec)
	}
	return vectors, dim, nil
}

func packVector(vector []float32) []byte {
	if len(vector) == 0 {
		return nil
	}
	var b bytes.Buffer
	for _, value := range vector {
		bits := math.Float32bits(value)
		b.WriteByte(byte(bits))
		b.WriteByte(byte(bits >> 8))
		b.WriteByte(byte(bits >> 16))
		b.WriteByte(byte(bits >> 24))
	}
	return b.Bytes()
}

func unpackVector(raw []byte) []float32 {
	if len(raw) == 0 || len(raw)%4 != 0 {
		return nil
	}
	out := make([]float32, 0, len(raw)/4)
	for i := 0; i+3 < len(raw); i += 4 {
		bits := uint32(raw[i]) | uint32(raw[i+1])<<8 | uint32(raw[i+2])<<16 | uint32(raw[i+3])<<24
		out = append(out, math.Float32frombits(bits))
	}
	return out
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return -1
	}
	var dot, na, nb float64
	for i := range a {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		na += av * av
		nb += bv * bv
	}
	if na <= 0 || nb <= 0 {
		return -1
	}
	return dot / math.Sqrt(na*nb)
}

func normalizeMemoryContent(content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errors.New("memory content is empty")
	}
	runes := []rune(content)
	if len(runes) > maxMemoryContentRunes {
		return "", fmt.Errorf("memory content is too long")
	}
	return content, nil
}

func normalizeMemoryCategory(category string) string {
	category = strings.TrimSpace(category)
	runes := []rune(category)
	if len(runes) > 32 {
		category = string(runes[:32])
	}
	return category
}

func estimateMemoryTokens(content string) int {
	count := len([]rune(strings.TrimSpace(content)))
	if count <= 0 {
		return 1
	}
	return max(1, (count+1)/2)
}

func (s *Server) findMemoryByContent(userID int64, content string) (Memory, bool) {
	row := s.store.DB.QueryRow(`
		SELECT id, user_id, content, source, category, weight, origin, tokens, enabled, embedding_model, embedding_dim, embedding_updated_at, (embedding IS NOT NULL), created_at, updated_at
		FROM memories
		WHERE user_id=? AND content=? AND deleted_at IS NULL
		LIMIT 1
	`, userID, content)
	m, err := scanMemory(row, "")
	return m, err == nil
}

func (s *Server) createSystemMemory(userID int64, content, category, origin string) (Memory, bool, error) {
	content, err := normalizeMemoryContent(content)
	if err != nil {
		return Memory{}, false, err
	}
	if existing, ok := s.findMemoryByContent(userID, content); ok {
		return existing, false, nil
	}
	now := db.Now()
	category = normalizeMemoryCategory(category)
	origin = strings.TrimSpace(origin)
	if origin == "" {
		origin = "auto"
	}
	res, err := s.store.DB.Exec(`
		INSERT INTO memories (user_id, content, source, category, weight, origin, tokens, enabled, created_at, updated_at)
		VALUES (?, ?, 'auto', ?, ?, ?, ?, 1, ?, ?)
	`, userID, content, category, 0, origin, estimateMemoryTokens(content), now, now)
	if err != nil {
		return Memory{}, false, err
	}
	id, _ := res.LastInsertId()
	m, err := s.memoryForUser(id, userID, "")
	return m, true, err
}

func (s *Server) updateSystemMemory(userID, id int64, content, category string) (Memory, bool, error) {
	content, err := normalizeMemoryContent(content)
	if err != nil {
		return Memory{}, false, err
	}
	category = normalizeMemoryCategory(category)
	now := db.Now()
	res, err := s.store.DB.Exec(`
		UPDATE memories
		SET content=?, category=?, weight=0, tokens=?, embedding=NULL, embedding_model='', embedding_dim=0, embedding_updated_at=NULL, updated_at=?
		WHERE id=? AND user_id=? AND deleted_at IS NULL
	`, content, category, estimateMemoryTokens(content), now, id, userID)
	if err != nil {
		return Memory{}, false, err
	}
	if rows, _ := res.RowsAffected(); rows <= 0 {
		return Memory{}, false, nil
	}
	m, err := s.memoryForUser(id, userID, "")
	return m, true, err
}

func (s *Server) indexMemories(ctx context.Context, userID int64, memories []Memory) memoryEmbeddingResult {
	provider, err := s.getEmbeddingProvider()
	if err != nil {
		return memoryEmbeddingResult{Model: "", Errors: []string{err.Error()}}
	}
	valid := make([]Memory, 0, len(memories))
	for _, memory := range memories {
		if memory.ID > 0 && strings.TrimSpace(memory.Content) != "" && memory.Enabled {
			valid = append(valid, memory)
		}
	}
	result := memoryEmbeddingResult{Model: provider.Model}
	for start := 0; start < len(valid); start += embeddingBatchSize {
		end := start + embeddingBatchSize
		if end > len(valid) {
			end = len(valid)
		}
		chunk := valid[start:end]
		texts := make([]string, 0, len(chunk))
		for _, item := range chunk {
			texts = append(texts, item.Content)
		}
		vectors, dim, err := s.embedTexts(ctx, provider, texts)
		if err != nil {
			result.Failed += len(chunk)
			if len(result.Errors) < 5 {
				result.Errors = append(result.Errors, err.Error())
			}
			continue
		}
		result.Dim = dim
		for i, item := range chunk {
			if i >= len(vectors) {
				result.Failed++
				continue
			}
			raw := packVector(vectors[i])
			res, err := s.store.DB.Exec(`
				UPDATE memories
				SET embedding=?, embedding_model=?, embedding_dim=?, embedding_updated_at=?, updated_at=?
				WHERE id=? AND user_id=? AND deleted_at IS NULL
			`, raw, provider.Model, dim, db.Now(), db.Now(), item.ID, userID)
			if err != nil {
				result.Failed++
				continue
			}
			if rows, _ := res.RowsAffected(); rows > 0 {
				result.Success++
			} else {
				result.Failed++
			}
		}
	}
	return result
}

func (s *Server) memoriesForInjection(ctx context.Context, userID int64, query string, limit int) memoryRetrievalResult {
	limit = max(1, min(limit, memoryMaxInjectLimit))
	vectorLimit := s.memoryVectorLimit(limit)
	fallback := func() []Memory {
		if !memoryFallbackEnabled {
			return nil
		}
		rows, err := s.store.DB.Query(`
			SELECT id, user_id, content, source, category, weight, origin, tokens, enabled, embedding_model, embedding_dim, embedding_updated_at, (embedding IS NOT NULL), created_at, updated_at
			FROM memories
			WHERE user_id=? AND enabled=1 AND deleted_at IS NULL
			ORDER BY updated_at DESC, id DESC
			LIMIT ?
		`, userID, limit)
		if err != nil {
			return nil
		}
		defer rows.Close()
		memories := make([]Memory, 0)
		for rows.Next() {
			if m, err := scanMemory(rows, ""); err == nil {
				memories = append(memories, m)
			}
		}
		return memories
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return memoryRetrievalResult{Memories: fallback(), Method: "fallback"}
	}
	provider, err := s.getEmbeddingProvider()
	if err != nil {
		return memoryRetrievalResult{Memories: fallback(), Method: "fallback"}
	}
	queryVectors, dim, err := s.embedTexts(ctx, provider, []string{query})
	if err != nil || len(queryVectors) == 0 {
		return memoryRetrievalResult{Memories: fallback(), Method: "fallback"}
	}
	rows, err := s.store.DB.Query(`
		SELECT id, user_id, content, source, category, weight, origin, tokens, enabled, embedding_model, embedding_dim, embedding_updated_at, embedding, created_at, updated_at
		FROM memories
		WHERE user_id=? AND enabled=1 AND deleted_at IS NULL AND embedding IS NOT NULL AND embedding_model=?
		ORDER BY updated_at DESC, id DESC
		LIMIT 200
	`, userID, provider.Model)
	if err != nil {
		return memoryRetrievalResult{Memories: fallback(), Model: provider.Model, Dim: dim, Method: "fallback"}
	}
	defer rows.Close()
	scored := make([]scoredMemory, 0)
	for rows.Next() {
		var m Memory
		var enabled int
		var embedding []byte
		var embeddingUpdatedAt sql.NullString
		if err := rows.Scan(&m.ID, &m.UserID, &m.Content, &m.Source, &m.Category, &m.Weight, &m.Origin, &m.Tokens, &enabled, &m.EmbeddingModel, &m.EmbeddingDim, &embeddingUpdatedAt, &embedding, &m.CreatedAt, &m.UpdatedAt); err != nil {
			continue
		}
		m.Enabled = enabled == 1
		if embeddingUpdatedAt.Valid {
			m.EmbeddingUpdatedAt = embeddingUpdatedAt.String
		}
		m.EmbeddingStatus = "ready"
		if int(m.EmbeddingDim) != dim {
			continue
		}
		vec := unpackVector(embedding)
		if len(vec) != dim {
			continue
		}
		score := cosine(queryVectors[0], vec)
		if score <= -1 {
			continue
		}
		scored = append(scored, scoredMemory{Memory: m, Score: score})
	}
	if len(scored) == 0 {
		return memoryRetrievalResult{Model: provider.Model, Dim: dim, Method: "vector"}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > vectorLimit {
		scored = scored[:vectorLimit]
	}
	if reranked, ok := s.rerankMemoryCandidates(ctx, query, scored, limit); ok {
		return memoryRetrievalResult{Memories: memoriesFromHits(reranked), Hits: reranked, Model: provider.Model, Dim: dim, Method: "vector_rerank"}
	}
	picked := make([]Memory, 0, limit)
	hits := make([]memorySearchHit, 0, limit)
	seen := map[int64]bool{}
	for _, item := range scored {
		picked = append(picked, item.Memory)
		hits = append(hits, memorySearchHit{Memory: item.Memory, Score: item.Score, VectorScore: item.Score})
		seen[item.Memory.ID] = true
		if len(picked) >= limit {
			break
		}
	}
	if memoryFallbackEnabled && len(picked) < limit {
		for _, item := range fallback() {
			if seen[item.ID] {
				continue
			}
			picked = append(picked, item)
			if len(picked) >= limit {
				break
			}
		}
	}
	return memoryRetrievalResult{Memories: picked, Hits: hits, Model: provider.Model, Dim: dim, Method: "vector"}
}

func memoriesFromHits(hits []memorySearchHit) []Memory {
	memories := make([]Memory, 0, len(hits))
	for _, hit := range hits {
		memories = append(memories, hit.Memory)
	}
	return memories
}

func (s *Server) rerankMemoryCandidates(ctx context.Context, query string, candidates []scoredMemory, limit int) ([]memorySearchHit, bool) {
	provider, err := s.getMemoryProvider()
	if err != nil || provider == nil || strings.TrimSpace(provider.Key) == "" || strings.TrimSpace(provider.Base) == "" || strings.TrimSpace(provider.Model) == "" {
		return nil, false
	}
	limit = max(1, min(limit, memoryMaxInjectLimit))
	prompt := buildMemoryRerankPrompt(query, candidates, limit)
	raw, err := s.completeMemoryRerankLLM(ctx, *provider, prompt)
	if err != nil {
		return nil, false
	}
	items := parseMemoryRerankItems(raw, candidates)
	if len(items) == 0 {
		return nil, false
	}
	byID := make(map[int64]scoredMemory, len(candidates))
	for _, candidate := range candidates {
		byID[candidate.Memory.ID] = candidate
	}
	hits := make([]memorySearchHit, 0, limit)
	seen := make(map[int64]bool, limit)
	for _, item := range items {
		candidate, ok := byID[item.ID]
		if !ok || seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		hits = append(hits, memorySearchHit{
			Memory:      candidate.Memory,
			Score:       candidate.Score,
			VectorScore: candidate.Score,
			RerankScore: item.RerankScore,
			Reason:      item.Reason,
		})
		if len(hits) >= limit {
			break
		}
	}
	if len(hits) == 0 {
		return nil, false
	}
	return hits, true
}

func buildMemoryRerankPrompt(query string, candidates []scoredMemory, limit int) string {
	var b strings.Builder
	b.WriteString("【当前时间】\n")
	b.WriteString(db.Now())
	b.WriteString("\n\n")
	b.WriteString("【当前用户问题】\n")
	b.WriteString(trimRunes(query, 3000))
	b.WriteString("\n\n【任务】\n")
	b.WriteString("请只对候选记忆按“对回答当前用户问题的帮助程度”进行重排。不要回答用户问题，不要新增、更新或删除记忆。\n")
	b.WriteString("候选记忆包含 created_at 和 updated_at；当记忆内容存在新旧、冲突或时效性差异时，请结合时间判断优先级。\n")
	b.WriteString("最多返回 ")
	b.WriteString(strconv.Itoa(limit))
	b.WriteString(" 条最相关记忆。rerank_score 必须是 0 到 1 的数字，reason 用一句中文说明命中原因。\n")
	b.WriteString(`输出格式：{"items":[{"id":123,"rerank_score":0.92,"reason":"用户询问姓名，这条记忆直接包含用户姓名"}]}`)
	b.WriteString("\n\n【候选记忆】\n")
	for _, candidate := range candidates {
		b.WriteString(fmt.Sprintf("- id=%d category=%s vector_score=%.6f created_at=%s updated_at=%s content=%s\n", candidate.Memory.ID, candidate.Memory.Category, candidate.Score, candidate.Memory.CreatedAt, candidate.Memory.UpdatedAt, trimRunes(candidate.Memory.Content, 700)))
	}
	return b.String()
}

func (s *Server) completeMemoryRerankLLM(ctx context.Context, provider runtimeProvider, prompt string) (string, error) {
	instructions := "你是 ChatXXX 的记忆重排模型。你只输出 JSON，不要 markdown，不要解释。你只能对给定候选记忆排序，不能回答用户问题。"
	switch strings.TrimSpace(provider.RequestMode) {
	case "responses":
		body := map[string]interface{}{
			"model":  provider.Model,
			"stream": false,
			"input": []interface{}{
				responseInputMessage{
					Type: "message",
					Role: "user",
					Content: []responseInputContent{
						{Type: "input_text", Text: prompt},
					},
				},
			},
			"instructions": instructions,
		}
		if rf, ok, err := parseJSONObject(provider.ResponseFormat); err != nil {
			return "", err
		} else if ok {
			body["text"] = map[string]interface{}{"format": rf}
		}
		raw, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(provider.Base, "/")+"/responses", bytes.NewReader(raw))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+provider.Key)
		resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("memory rerank provider returned %s: %s", resp.Status, strings.TrimSpace(string(bodyBytes)))
		}
		return extractResponsesText(bodyBytes)
	default:
		requestBody := map[string]interface{}{
			"model":  provider.Model,
			"stream": false,
			"messages": []map[string]string{
				{"role": "system", "content": instructions},
				{"role": "user", "content": prompt},
			},
		}
		if rf, ok, err := parseJSONObject(provider.ResponseFormat); err != nil {
			return "", err
		} else if ok {
			requestBody["response_format"] = rf
		}
		raw, _ := json.Marshal(requestBody)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(provider.Base, "/")+"/chat/completions", bytes.NewReader(raw))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+provider.Key)
		resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("memory rerank provider returned %s: %s", resp.Status, strings.TrimSpace(string(bodyBytes)))
		}
		return extractSearchingContent(bodyBytes)
	}
}

func parseMemoryRerankItems(raw string, candidates []scoredMemory) []memoryRerankItem {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	allowed := make(map[int64]bool, len(candidates))
	vectorScores := make(map[int64]float64, len(candidates))
	for _, candidate := range candidates {
		allowed[candidate.Memory.ID] = true
		vectorScores[candidate.Memory.ID] = candidate.Score
	}
	candidate := extractJSONArrayOrObject(raw)
	var object struct {
		Items []memoryRerankItem `json:"items"`
	}
	var items []memoryRerankItem
	if err := json.Unmarshal([]byte(candidate), &object); err == nil && object.Items != nil {
		items = object.Items
	} else if err := json.Unmarshal([]byte(candidate), &items); err != nil {
		return nil
	}
	out := make([]memoryRerankItem, 0, len(items))
	seen := map[int64]bool{}
	for _, item := range items {
		if !allowed[item.ID] || seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		if math.IsNaN(item.RerankScore) || math.IsInf(item.RerankScore, 0) {
			continue
		}
		if item.RerankScore < 0 {
			item.RerankScore = 0
		} else if item.RerankScore > 1 {
			item.RerankScore = 1
		}
		item.Reason = trimRunes(strings.TrimSpace(item.Reason), 160)
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RerankScore == out[j].RerankScore {
			return vectorScores[out[i].ID] > vectorScores[out[j].ID]
		}
		return out[i].RerankScore > out[j].RerankScore
	})
	return out
}

func memorySystemPrompt(memories []Memory) string {
	if len(memories) == 0 {
		return ""
	}
	lines := make([]string, 0, len(memories)+1)
	lines = append(lines, "以下是用户自己的长期记忆，可作为回答依据；当用户直接询问相关内容时，可以根据这些记忆正常回答：")
	for _, memory := range memories {
		content := strings.TrimSpace(memory.Content)
		if content != "" {
			lines = append(lines, "- "+content)
		}
	}
	return strings.Join(lines, "\n")
}

func (s *Server) runAutoMemoryAfterReply(conversationID, userID int64, userMessage, assistantMessage Message) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = s.extractAndApplyMemories(ctx, conversationID, userID, userMessage, assistantMessage)
	}()
}

func (s *Server) extractAndApplyMemories(ctx context.Context, conversationID, userID int64, userMessage, assistantMessage Message) (*memoryActionResult, error) {
	provider, err := s.getMemoryProvider()
	if err != nil || provider == nil || provider.Key == "" || provider.Base == "" || provider.Model == "" {
		return nil, err
	}
	conversation, err := s.getConversationForUser(conversationID, userID)
	if err != nil || conversation == nil || !conversation.MemoryEnabled {
		return nil, err
	}
	limit := max(2, min(s.settingInt("memory_recent_message_limit", 12), 50))
	recent, _ := s.recentMessagesForMemory(conversationID, userID, limit)
	existing := s.memoriesForAutoPrompt(userID, 120)
	maxActions := max(1, min(s.settingInt("memory_max_actions_per_run", 5), 20))
	prompt := buildMemoryExtractionPrompt(userMessage, assistantMessage, recent, existing, maxActions)
	raw, err := s.completeMemoryLLM(ctx, *provider, prompt)
	if err != nil {
		return nil, err
	}
	actions := parseMemoryActions(raw, maxActions)
	result := &memoryActionResult{}
	changed := make([]Memory, 0)
	origin := fmt.Sprintf("auto:conv-%d", conversationID)
	for _, action := range actions {
		switch strings.ToLower(strings.TrimSpace(action.Action)) {
		case "add":
			m, created, err := s.createSystemMemory(userID, action.Content, action.Category, origin)
			if err != nil {
				result.Skipped++
				continue
			}
			if created {
				result.Added++
				changed = append(changed, m)
			} else {
				result.Ignored++
			}
		case "update":
			if action.ID <= 0 {
				result.Skipped++
				continue
			}
			m, updated, err := s.updateSystemMemory(userID, action.ID, action.Content, action.Category)
			if err != nil || !updated {
				result.Skipped++
				continue
			}
			result.Updated++
			changed = append(changed, m)
		default:
			result.Ignored++
		}
	}
	if len(changed) > 0 {
		s.indexMemories(ctx, userID, changed)
	}
	_, _ = s.store.DB.Exec(`UPDATE conversations SET last_auto_memory_message_id=?, updated_at=? WHERE id=? AND user_id=?`, assistantMessage.ID, db.Now(), conversationID, userID)
	return result, nil
}

func (s *Server) completeMemoryLLM(ctx context.Context, provider runtimeProvider, prompt string) (string, error) {
	switch strings.TrimSpace(provider.RequestMode) {
	case "responses":
		body := map[string]interface{}{
			"model":  provider.Model,
			"stream": false,
			"input": []interface{}{
				responseInputMessage{
					Type: "message",
					Role: "user",
					Content: []responseInputContent{
						{Type: "input_text", Text: prompt},
					},
				},
			},
			"instructions": memoryLLMInstructions(),
		}
		if rf, ok, err := parseJSONObject(provider.ResponseFormat); err != nil {
			return "", err
		} else if ok {
			body["text"] = map[string]interface{}{"format": rf}
		}
		raw, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(provider.Base, "/")+"/responses", bytes.NewReader(raw))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+provider.Key)
		resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("memory provider returned %s: %s", resp.Status, strings.TrimSpace(string(bodyBytes)))
		}
		return extractResponsesText(bodyBytes)
	default:
		requestBody := map[string]interface{}{
			"model":  provider.Model,
			"stream": false,
			"messages": []map[string]string{
				{"role": "system", "content": memoryLLMInstructions()},
				{"role": "user", "content": prompt},
			},
		}
		if rf, ok, err := parseJSONObject(provider.ResponseFormat); err != nil {
			return "", err
		} else if ok {
			requestBody["response_format"] = rf
		}
		raw, _ := json.Marshal(requestBody)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(provider.Base, "/")+"/chat/completions", bytes.NewReader(raw))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+provider.Key)
		resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("memory provider returned %s: %s", resp.Status, strings.TrimSpace(string(bodyBytes)))
		}
		return extractSearchingContent(bodyBytes)
	}
}

func memoryLLMInstructions() string {
	return strings.Join([]string{
		"你是 ChatXXX 的长期记忆管理模型。",
		"你只判断哪些稳定信息应该写入用户长期记忆，不回答用户问题。",
		"是否记忆、记忆哪些内容由你根据上下文自主决定。",
		"可以保存任何你认为未来对理解用户、延续对话或完成任务有帮助的信息。",
		"你只能输出 JSON，不要 markdown，不要解释。",
		`输出格式：{"actions":[{"action":"add","content":"...","category":"偏好|人物|项目|技术|目标|其他"},{"action":"update","id":123,"content":"...","category":"..."},{"action":"ignore"}]}`,
		"update 只能使用我给你的已有记忆 id；不能删除记忆。",
	}, "\n")
}

func buildMemoryExtractionPrompt(userMessage, assistantMessage Message, recent []Message, existing []Memory, maxActions int) string {
	var b strings.Builder
	b.WriteString("【动作上限】最多 ")
	b.WriteString(strconv.Itoa(maxActions))
	b.WriteString(" 个 action。\n\n【已有长期记忆】\n")
	if len(existing) == 0 {
		b.WriteString("无\n")
	} else {
		for _, memory := range existing {
			b.WriteString(fmt.Sprintf("- id=%d category=%s content=%s\n", memory.ID, memory.Category, memory.Content))
		}
	}
	b.WriteString("\n【最近上下文】\n")
	for _, msg := range recent {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(messagePromptContent(msg))
		if content == "" {
			continue
		}
		b.WriteString("[")
		b.WriteString(msg.Role)
		b.WriteString("] ")
		b.WriteString(trimRunes(content, 1600))
		b.WriteString("\n\n")
	}
	b.WriteString("【本轮用户消息】\n")
	b.WriteString(trimRunes(messagePromptContent(userMessage), 3000))
	b.WriteString("\n\n【本轮助手回复】\n")
	b.WriteString(trimRunes(assistantMessage.Content, 3000))
	return b.String()
}

func parseMemoryActions(raw string, maxActions int) []memoryAction {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	candidate := extractJSONArrayOrObject(raw)
	var object struct {
		Actions []memoryAction `json:"actions"`
	}
	if err := json.Unmarshal([]byte(candidate), &object); err == nil && object.Actions != nil {
		return sanitizeMemoryActions(object.Actions, maxActions)
	}
	var array []memoryAction
	if err := json.Unmarshal([]byte(candidate), &array); err == nil {
		return sanitizeMemoryActions(array, maxActions)
	}
	return nil
}

func sanitizeMemoryActions(actions []memoryAction, maxActions int) []memoryAction {
	maxActions = max(1, maxActions)
	out := make([]memoryAction, 0, min(len(actions), maxActions))
	for _, action := range actions {
		action.Action = strings.ToLower(strings.TrimSpace(action.Action))
		if action.Action != "add" && action.Action != "update" && action.Action != "ignore" {
			continue
		}
		action.Content = strings.TrimSpace(action.Content)
		action.Category = normalizeMemoryCategory(action.Category)
		if action.Action == "ignore" {
			out = append(out, memoryAction{Action: "ignore"})
		} else if action.Content != "" {
			out = append(out, action)
		}
		if len(out) >= maxActions {
			break
		}
	}
	return out
}

func extractJSONArrayOrObject(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[") {
		return raw
	}
	objectStart := strings.Index(raw, "{")
	objectEnd := strings.LastIndex(raw, "}")
	arrayStart := strings.Index(raw, "[")
	arrayEnd := strings.LastIndex(raw, "]")
	if objectStart >= 0 && objectEnd > objectStart {
		return raw[objectStart : objectEnd+1]
	}
	if arrayStart >= 0 && arrayEnd > arrayStart {
		return raw[arrayStart : arrayEnd+1]
	}
	return raw
}

func extractResponsesText(body []byte) (string, error) {
	var payload struct {
		Output []responseOutputItem `json:"output"`
		Text   json.RawMessage      `json:"text"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.Error.Message) != "" {
		return "", errors.New(strings.TrimSpace(payload.Error.Message))
	}
	parsed := parseResponseOutputItems(payload.Output)
	if strings.TrimSpace(parsed.Text) != "" {
		return strings.TrimSpace(parsed.Text), nil
	}
	if len(payload.Text) > 0 {
		var text string
		if err := json.Unmarshal(payload.Text, &text); err == nil && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text), nil
		}
	}
	return strings.TrimSpace(string(body)), nil
}

func (s *Server) recentMessagesForMemory(conversationID, userID int64, limit int) ([]Message, error) {
	rows, err := s.store.DB.Query(`
		SELECT id, conversation_id, user_id, role, content, reasoning_content, status, attachments, metadata, version_group_id, version_index, is_active_version, parent_user_message_id, sort_order, created_at, updated_at
		FROM messages
		WHERE conversation_id=? AND user_id=? AND deleted_at IS NULL AND is_active_version=1 AND status IN ('completed','stopped')
		ORDER BY sort_order DESC, id DESC
		LIMIT ?
	`, conversationID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Message, 0)
	for rows.Next() {
		if msg, err := scanMessage(rows); err == nil {
			items = append(items, msg)
		}
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items, nil
}

func (s *Server) memoriesForAutoPrompt(userID int64, limit int) []Memory {
	rows, err := s.store.DB.Query(`
		SELECT id, user_id, content, source, category, weight, origin, tokens, enabled, embedding_model, embedding_dim, embedding_updated_at, (embedding IS NOT NULL), created_at, updated_at
		FROM memories
		WHERE user_id=? AND deleted_at IS NULL AND enabled=1
		ORDER BY updated_at DESC, id DESC
		LIMIT ?
	`, userID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	items := make([]Memory, 0)
	for rows.Next() {
		if m, err := scanMemory(rows, ""); err == nil {
			items = append(items, m)
		}
	}
	return items
}

func trimRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}
