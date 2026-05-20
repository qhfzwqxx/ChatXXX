package httpapi

import (
	"chatxxx/backend/internal/db"
	"net/http"
	"strconv"
	"strings"
)

const defaultWebSearchCardResultCount = 4

const (
	searchToolModeUniFuncs  = "unifuncs"
	searchToolModeSearching = "searching"
)

var adminSettingKeys = []string{
	"search_tool_mode",
	"unifuncs_api_key",
	"unifuncs_base_url",
	"web_search_card_result_count",
	"searching_base_url",
	"searching_api_key",
	"searching_model",
	"searching_api_id",
}

type appSetting struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func (s *Server) handleAdminGetSettings(w http.ResponseWriter, r *http.Request) {
	values := make(map[string]appSetting, len(adminSettingKeys))
	for _, key := range adminSettingKeys {
		values[key] = appSetting{Key: key}
	}
	rows, err := s.store.DB.Query(`SELECT key, value, created_at, updated_at FROM app_settings`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var item appSetting
			if err := rows.Scan(&item.Key, &item.Value, &item.CreatedAt, &item.UpdatedAt); err != nil {
				continue
			}
			if _, ok := values[item.Key]; ok {
				values[item.Key] = item
			}
		}
	}
	if strings.TrimSpace(values["search_tool_mode"].Value) == "" {
		values["search_tool_mode"] = appSetting{Key: "search_tool_mode", Value: searchToolModeUniFuncs}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"settings": values})
}

func (s *Server) handleGetClientSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"settings": map[string]interface{}{
			"web_search_card_result_count": s.webSearchCardResultCount(),
		},
	})
}

func (s *Server) handleAdminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UniFuncsAPIKey           string `json:"unifuncs_api_key"`
		UniFuncsBaseURL          string `json:"unifuncs_base_url"`
		WebSearchCardResultCount string `json:"web_search_card_result_count"`
		SearchToolMode           string `json:"search_tool_mode"`
		SearchingBaseURL         string `json:"searching_base_url"`
		SearchingAPIKey          string `json:"searching_api_key"`
		SearchingModel           string `json:"searching_model"`
		SearchingAPIID           string `json:"searching_api_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "请求体必须是 JSON")
		return
	}
	now := db.Now()
	items := map[string]string{
		"search_tool_mode":             normalizeSearchToolMode(req.SearchToolMode),
		"unifuncs_api_key":             strings.TrimSpace(req.UniFuncsAPIKey),
		"unifuncs_base_url":            strings.TrimSpace(req.UniFuncsBaseURL),
		"web_search_card_result_count": strconv.Itoa(clampWebSearchCardResultCount(req.WebSearchCardResultCount)),
		"searching_base_url":           strings.TrimSpace(req.SearchingBaseURL),
		"searching_api_key":            strings.TrimSpace(req.SearchingAPIKey),
		"searching_model":              strings.TrimSpace(req.SearchingModel),
		"searching_api_id":             strings.TrimSpace(req.SearchingAPIID),
	}
	for key, value := range items {
		if key != "search_tool_mode" && strings.TrimSpace(value) == "" {
			_, _ = s.store.DB.Exec(`DELETE FROM app_settings WHERE key=?`, key)
			continue
		}
		_, _ = s.store.DB.Exec(`
			INSERT INTO app_settings (key, value, created_at, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at
		`, key, value, now, now)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) webSearchCardResultCount() int {
	return clampWebSearchCardResultCount(s.settingValue("web_search_card_result_count"))
}

func (s *Server) searchToolMode() string {
	return normalizeSearchToolMode(s.settingValue("search_tool_mode"))
}

func normalizeSearchToolMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case searchToolModeSearching:
		return searchToolModeSearching
	default:
		return searchToolModeUniFuncs
	}
}

func clampWebSearchCardResultCount(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return defaultWebSearchCardResultCount
	}
	switch {
	case parsed <= 2:
		return 2
	case parsed <= 4:
		return 4
	case parsed <= 6:
		return 6
	default:
		return 10
	}
}
