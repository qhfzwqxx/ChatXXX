package httpapi

import (
	"chatxxx/backend/internal/db"
	"net/http"
	"strings"
)

type Provider struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	ProviderType    string `json:"provider_type"`
	BaseURL         string `json:"base_url"`
	APIKey          string `json:"api_key,omitempty"`
	Model           string `json:"model"`
	Capabilities    string `json:"capabilities"`
	ContextWindow   int64  `json:"context_window"`
	MaxOutputTokens int64  `json:"max_output_tokens"`
	IsDefault       bool   `json:"is_default"`
	IsVisible       bool   `json:"is_visible"`
	IsActive        bool   `json:"is_active"`
}

type providerRequest struct {
	Name            string `json:"name"`
	ProviderType    string `json:"provider_type"`
	BaseURL         string `json:"base_url"`
	APIKey          string `json:"api_key"`
	Model           string `json:"model"`
	Capabilities    string `json:"capabilities"`
	ContextWindow   int64  `json:"context_window"`
	MaxOutputTokens int64  `json:"max_output_tokens"`
	IsDefault       bool   `json:"is_default"`
	IsVisible       bool   `json:"is_visible"`
	IsActive        bool   `json:"is_active"`
}

func (s *Server) handleProviderCapabilities(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.DB.Query(`
		SELECT id, name, provider_type, base_url, model, capabilities, context_window, max_output_tokens, is_default, is_visible, is_active
		FROM providers WHERE is_active = 1 AND is_visible = 1 ORDER BY is_default DESC, id ASC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "读取模型失败")
		return
	}
	defer rows.Close()
	providers := make([]Provider, 0)
	var defaultID int64
	for rows.Next() {
		var p Provider
		var def, visible, active int
		if err := rows.Scan(&p.ID, &p.Name, &p.ProviderType, &p.BaseURL, &p.Model, &p.Capabilities, &p.ContextWindow, &p.MaxOutputTokens, &def, &visible, &active); err != nil {
			continue
		}
		p.IsDefault = def == 1
		p.IsVisible = visible == 1
		p.IsActive = active == 1
		if p.IsDefault || defaultID == 0 {
			defaultID = p.ID
		}
		providers = append(providers, p)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"capabilities": map[string]interface{}{
			"providers":           providers,
			"default_provider_id": defaultID,
		},
	})
}

func (s *Server) handleAdminListProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.DB.Query(`
		SELECT id, name, provider_type, base_url, model, capabilities, context_window, max_output_tokens, is_default, is_visible, is_active
		FROM providers ORDER BY is_default DESC, id ASC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "读取模型失败")
		return
	}
	defer rows.Close()
	items := make([]Provider, 0)
	for rows.Next() {
		var p Provider
		var def, visible, active int
		_ = rows.Scan(&p.ID, &p.Name, &p.ProviderType, &p.BaseURL, &p.Model, &p.Capabilities, &p.ContextWindow, &p.MaxOutputTokens, &def, &visible, &active)
		p.IsDefault = def == 1
		p.IsVisible = visible == 1
		p.IsActive = active == 1
		items = append(items, p)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"providers": items})
}

func (s *Server) handleAdminCreateProvider(w http.ResponseWriter, r *http.Request) {
	var req providerRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "请求体必须是 JSON")
		return
	}
	p := normalizeProvider(req)
	if p.Name == "" || p.BaseURL == "" || p.Model == "" {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "名称、Base URL、模型不能为空")
		return
	}
	if p.IsDefault {
		_, _ = s.store.DB.Exec(`UPDATE providers SET is_default = 0`)
	}
	now := db.Now()
	res, err := s.store.DB.Exec(`
		INSERT INTO providers (name, provider_type, base_url, api_key, model, capabilities, context_window, max_output_tokens, is_default, is_visible, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.Name, p.ProviderType, p.BaseURL, req.APIKey, p.Model, p.Capabilities, p.ContextWindow, p.MaxOutputTokens, boolInt(p.IsDefault), boolInt(p.IsVisible), boolInt(p.IsActive), now, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "保存模型失败")
		return
	}
	id, _ := res.LastInsertId()
	p.ID = id
	writeJSON(w, http.StatusCreated, map[string]interface{}{"provider": p})
}

func (s *Server) handleAdminUpdateProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "无效 ID")
		return
	}
	var req providerRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "请求体必须是 JSON")
		return
	}
	p := normalizeProvider(req)
	if p.IsDefault {
		_, _ = s.store.DB.Exec(`UPDATE providers SET is_default = 0 WHERE id <> ?`, id)
	}
	_, err := s.store.DB.Exec(`
		UPDATE providers SET name=?, provider_type=?, base_url=?, api_key=COALESCE(NULLIF(?, ''), api_key), model=?, capabilities=?, context_window=?, max_output_tokens=?, is_default=?, is_visible=?, is_active=?, updated_at=?
		WHERE id=?
	`, p.Name, p.ProviderType, p.BaseURL, req.APIKey, p.Model, p.Capabilities, p.ContextWindow, p.MaxOutputTokens, boolInt(p.IsDefault), boolInt(p.IsVisible), boolInt(p.IsActive), db.Now(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "更新模型失败")
		return
	}
	p.ID = id
	writeJSON(w, http.StatusOK, map[string]interface{}{"provider": p})
}

func (s *Server) handleAdminDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "无效 ID")
		return
	}
	_, _ = s.store.DB.Exec(`DELETE FROM providers WHERE id = ?`, id)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) handleAdminUsage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"events": []interface{}{}, "summary": map[string]int{"calls": 0}})
}

func normalizeProvider(req providerRequest) Provider {
	providerType := strings.TrimSpace(req.ProviderType)
	if providerType == "" {
		providerType = "openai_compatible"
	}
	caps := strings.TrimSpace(req.Capabilities)
	if caps == "" {
		caps = `{"input":{"text":true},"output":{"text":true},"features":{"stream":true}}`
	}
	return Provider{
		Name:            strings.TrimSpace(req.Name),
		ProviderType:    providerType,
		BaseURL:         strings.TrimRight(strings.TrimSpace(req.BaseURL), "/"),
		Model:           strings.TrimSpace(req.Model),
		Capabilities:    caps,
		ContextWindow:   req.ContextWindow,
		MaxOutputTokens: req.MaxOutputTokens,
		IsDefault:       req.IsDefault,
		IsVisible:       trueOrDefault(req.IsVisible),
		IsActive:        trueOrDefault(req.IsActive),
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func trueOrDefault(v bool) bool {
	return v
}
