package httpapi

import (
	"chatxxx/backend/internal/db"
	"net/http"
	"strconv"
	"strings"
)

const defaultWebSearchCardResultCount = 4
const (
	defaultImageToolMode           = imageToolModeResponses
	defaultImageToolBaseURL        = "https://api.tu-zi.com"
	defaultImageGenerateModel      = "gpt-image-2"
	defaultImageEditModel          = "gpt-image-1.5"
	defaultImageResponsesModel     = "gpt-5.5"
	defaultImageChatModel          = "gpt-4o-image"
	defaultImageGenerateSize       = "1024x1024"
	defaultImageEditSize           = "1:1"
	defaultImageToolQuality        = "auto"
	defaultImageToolResponseFormat = "url"
)

const (
	imageToolModeImageAPI        = "image_api"
	imageToolModeResponses       = "responses"
	imageToolModeChatCompletions = "chat_completions"
)

const (
	searchToolModeUniFuncs  = "unifuncs"
	searchToolModeSearching = "searching"
)

var adminSettingKeys = []string{
	"search_tool_enabled",
	"search_tool_mode",
	"unifuncs_api_key",
	"unifuncs_base_url",
	"web_search_card_result_count",
	"searching_base_url",
	"searching_api_key",
	"searching_model",
	"searching_api_id",
	"image_tool_enabled",
	"image_tool_mode",
	"image_tool_base_url",
	"image_tool_api_key",
	"image_generate_model",
	"image_edit_model",
	"image_responses_model",
	"image_chat_model",
	"image_default_size",
	"image_edit_size",
	"image_default_quality",
	"image_response_format",
	"time_tool_enabled",
	"title_provider_id",
	"memory_provider_id",
	"embedding_provider_id",
	"memory_recent_message_limit",
	"memory_max_actions_per_run",
	"memory_inject_limit",
	"embedding_top_k",
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
		values["search_tool_mode"] = appSetting{Key: "search_tool_mode", Value: searchToolModeSearching}
	}
	defaults := map[string]string{
		"search_tool_enabled":   "1",
		"image_tool_enabled":    "1",
		"time_tool_enabled":     "1",
		"image_tool_mode":       defaultImageToolMode,
		"image_tool_base_url":   defaultImageToolBaseURL,
		"image_generate_model":  defaultImageGenerateModel,
		"image_edit_model":      defaultImageEditModel,
		"image_responses_model": defaultImageResponsesModel,
		"image_chat_model":      defaultImageChatModel,
		"image_default_size":    defaultImageGenerateSize,
		"image_edit_size":       defaultImageEditSize,
		"image_default_quality": defaultImageToolQuality,
		"image_response_format": defaultImageToolResponseFormat,
	}
	for key, value := range defaults {
		if strings.TrimSpace(values[key].Value) == "" {
			values[key] = appSetting{Key: key, Value: value}
		}
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
		SearchToolEnabled        string `json:"search_tool_enabled"`
		UniFuncsAPIKey           string `json:"unifuncs_api_key"`
		UniFuncsBaseURL          string `json:"unifuncs_base_url"`
		WebSearchCardResultCount string `json:"web_search_card_result_count"`
		SearchToolMode           string `json:"search_tool_mode"`
		SearchingBaseURL         string `json:"searching_base_url"`
		SearchingAPIKey          string `json:"searching_api_key"`
		SearchingModel           string `json:"searching_model"`
		SearchingAPIID           string `json:"searching_api_id"`
		ImageToolEnabled         string `json:"image_tool_enabled"`
		ImageToolMode            string `json:"image_tool_mode"`
		ImageToolBaseURL         string `json:"image_tool_base_url"`
		ImageToolAPIKey          string `json:"image_tool_api_key"`
		ImageGenerateModel       string `json:"image_generate_model"`
		ImageEditModel           string `json:"image_edit_model"`
		ImageResponsesModel      string `json:"image_responses_model"`
		ImageChatModel           string `json:"image_chat_model"`
		ImageDefaultSize         string `json:"image_default_size"`
		ImageEditSize            string `json:"image_edit_size"`
		ImageDefaultQuality      string `json:"image_default_quality"`
		ImageResponseFormat      string `json:"image_response_format"`
		TimeToolEnabled          string `json:"time_tool_enabled"`
		TitleProviderID          string `json:"title_provider_id"`
		MemoryProviderID         string `json:"memory_provider_id"`
		EmbeddingProviderID      string `json:"embedding_provider_id"`
		MemoryRecentMessageLimit string `json:"memory_recent_message_limit"`
		MemoryMaxActionsPerRun   string `json:"memory_max_actions_per_run"`
		MemoryInjectLimit        string `json:"memory_inject_limit"`
		EmbeddingTopK            string `json:"embedding_top_k"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "请求体必须是 JSON")
		return
	}
	now := db.Now()
	items := map[string]string{
		"search_tool_enabled":          normalizeEnabledSetting(req.SearchToolEnabled),
		"search_tool_mode":             normalizeSearchToolMode(req.SearchToolMode),
		"unifuncs_api_key":             strings.TrimSpace(req.UniFuncsAPIKey),
		"unifuncs_base_url":            strings.TrimSpace(req.UniFuncsBaseURL),
		"web_search_card_result_count": strconv.Itoa(clampWebSearchCardResultCount(req.WebSearchCardResultCount)),
		"searching_base_url":           strings.TrimSpace(req.SearchingBaseURL),
		"searching_api_key":            strings.TrimSpace(req.SearchingAPIKey),
		"searching_model":              strings.TrimSpace(req.SearchingModel),
		"searching_api_id":             strings.TrimSpace(req.SearchingAPIID),
		"image_tool_enabled":           normalizeEnabledSetting(req.ImageToolEnabled),
		"image_tool_mode":              normalizeImageToolMode(req.ImageToolMode),
		"image_tool_base_url":          normalizeImageToolBaseURL(req.ImageToolBaseURL),
		"image_tool_api_key":           strings.TrimSpace(req.ImageToolAPIKey),
		"image_generate_model":         normalizeImageModel(req.ImageGenerateModel, defaultImageGenerateModel),
		"image_edit_model":             normalizeImageModel(req.ImageEditModel, defaultImageEditModel),
		"image_responses_model":        normalizeImageMainlineModel(req.ImageResponsesModel, defaultImageResponsesModel),
		"image_chat_model":             normalizeImageMainlineModel(req.ImageChatModel, defaultImageChatModel),
		"image_default_size":           normalizeImageGenerateSize(req.ImageDefaultSize, defaultImageGenerateSize),
		"image_edit_size":              normalizeImageEditSize(req.ImageEditSize, defaultImageEditSize),
		"image_default_quality":        normalizeImageQuality(req.ImageDefaultQuality, defaultImageToolQuality),
		"image_response_format":        normalizeImageResponseFormat(req.ImageResponseFormat, defaultImageToolResponseFormat),
		"time_tool_enabled":            normalizeEnabledSetting(req.TimeToolEnabled),
		"title_provider_id":            strconv.Itoa(clampSettingInt(req.TitleProviderID, 0, 0, 1_000_000)),
		"memory_provider_id":           strconv.Itoa(clampSettingInt(req.MemoryProviderID, 0, 0, 1_000_000)),
		"embedding_provider_id":        strconv.Itoa(clampSettingInt(req.EmbeddingProviderID, 0, 0, 1_000_000)),
		"memory_recent_message_limit":  strconv.Itoa(clampSettingInt(req.MemoryRecentMessageLimit, 12, 2, 50)),
		"memory_max_actions_per_run":   strconv.Itoa(clampSettingInt(req.MemoryMaxActionsPerRun, 5, 1, 20)),
		"memory_inject_limit":          strconv.Itoa(clampSettingInt(req.MemoryInjectLimit, 20, 1, 50)),
		"embedding_top_k":              strconv.Itoa(clampSettingInt(req.EmbeddingTopK, 8, 1, 50)),
	}
	for key, value := range items {
		if key != "search_tool_mode" && !isPersistentSetting(key) && strings.TrimSpace(value) == "" {
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

func isPersistentSetting(key string) bool {
	switch key {
	case "search_tool_enabled", "image_tool_enabled", "time_tool_enabled", "title_provider_id", "memory_provider_id", "embedding_provider_id", "memory_recent_message_limit", "memory_max_actions_per_run", "memory_inject_limit", "embedding_top_k", "web_search_card_result_count", "image_tool_mode", "image_tool_base_url", "image_generate_model", "image_edit_model", "image_responses_model", "image_chat_model", "image_default_size", "image_edit_size", "image_default_quality", "image_response_format":
		return true
	default:
		return false
	}
}

func normalizeEnabledSetting(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "disabled", "off", "no":
		return "0"
	default:
		return "1"
	}
}

func (s *Server) settingEnabled(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(s.settingValue(key)))
	if value == "" {
		return fallback
	}
	return normalizeEnabledSetting(value) == "1"
}

func (s *Server) searchToolEnabled() bool {
	return s.settingEnabled("search_tool_enabled", true)
}

func (s *Server) imageToolEnabled() bool {
	return s.settingEnabled("image_tool_enabled", true)
}

func (s *Server) timeToolEnabled() bool {
	return s.settingEnabled("time_tool_enabled", true)
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
	case searchToolModeUniFuncs:
		return searchToolModeUniFuncs
	default:
		return searchToolModeSearching
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

func clampSettingInt(value string, fallback, minValue, maxValue int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		parsed = fallback
	}
	if parsed < minValue {
		return minValue
	}
	if parsed > maxValue {
		return maxValue
	}
	return parsed
}
