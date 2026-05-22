package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var safeFileNamePattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

const maxWorkspaceFilesForPrompt = 30

func (s *Server) prepareWorkspaceAttachments(userID int64, items []attachment) ([]attachment, error) {
	if len(items) == 0 {
		return nil, nil
	}
	prepared := make([]attachment, 0, len(items))
	for index, item := range items {
		next := item
		if strings.TrimSpace(item.Content) == "" || strings.TrimSpace(item.Error) != "" {
			next.Content = ""
			next.Preview = ""
			prepared = append(prepared, next)
			continue
		}
		data, err := attachmentContentBytes(item)
		if err != nil {
			next.Content = ""
			next.Preview = ""
			next.Error = "文件内容保存失败：" + err.Error()
			prepared = append(prepared, next)
			continue
		}
		name := safeWorkspaceFileName(item.Name, index)
		relPath := filepath.ToSlash(filepath.Join("users", fmt.Sprintf("%d", userID), "uploads", time.Now().UTC().Format("20060102"), fmt.Sprintf("%d-%s", time.Now().UnixNano(), name)))
		fullPath := filepath.Join(s.workspaceRoot(), filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0750); err != nil {
			return nil, err
		}
		if err := os.WriteFile(fullPath, data, 0640); err != nil {
			return nil, err
		}
		next.Content = ""
		next.Preview = ""
		next.WorkspacePath = relPath
		next.URL = "/api/workspace/files/" + relPath
		next.Size = int64(len(data))
		prepared = append(prepared, next)
	}
	return prepared, nil
}

func attachmentContentBytes(item attachment) ([]byte, error) {
	content := strings.TrimSpace(item.Content)
	if content == "" {
		return nil, fmt.Errorf("empty content")
	}
	if strings.HasPrefix(strings.ToLower(content), "data:") || strings.HasPrefix(strings.ToLower(strings.TrimSpace(item.Type)), "image/") {
		return decodeDataURLOrBase64(content)
	}
	return []byte(item.Content), nil
}

func (s *Server) handleWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	relPath := strings.TrimPrefix(r.URL.Path, "/api/workspace/files/")
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	if relPath == "." || relPath == "" || strings.HasPrefix(relPath, "../") || strings.HasPrefix(relPath, "/") {
		writeError(w, http.StatusBadRequest, "INVALID_PATH", "文件路径无效")
		return
	}
	userPrefix := fmt.Sprintf("users/%d/", user.ID)
	if !strings.HasPrefix(relPath, userPrefix) && user.Role != "admin" {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "无权访问该文件")
		return
	}
	fullPath := filepath.Join(s.workspaceRoot(), filepath.FromSlash(relPath))
	if !strings.HasPrefix(filepath.Clean(fullPath), filepath.Clean(s.workspaceRoot())+string(os.PathSeparator)) {
		writeError(w, http.StatusBadRequest, "INVALID_PATH", "文件路径无效")
		return
	}
	http.ServeFile(w, r, fullPath)
}

func (s *Server) handlePublicWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	relPath := strings.TrimPrefix(r.URL.Path, "/api/workspace/public/")
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	expiresRaw := strings.TrimSpace(r.URL.Query().Get("expires"))
	signature := strings.TrimSpace(r.URL.Query().Get("sig"))
	expires, err := strconv.ParseInt(expiresRaw, 10, 64)
	if relPath == "." || relPath == "" || strings.HasPrefix(relPath, "../") || strings.HasPrefix(relPath, "/") || err != nil || signature == "" {
		writeError(w, http.StatusBadRequest, "INVALID_SIGNATURE", "图片链接无效")
		return
	}
	if time.Now().UTC().Unix() > expires {
		writeError(w, http.StatusForbidden, "EXPIRED_SIGNATURE", "图片链接已过期")
		return
	}
	if !hmac.Equal([]byte(signature), []byte(s.workspaceFileSignature(relPath, expires))) {
		writeError(w, http.StatusForbidden, "INVALID_SIGNATURE", "图片链接无效")
		return
	}
	fullPath := filepath.Join(s.workspaceRoot(), filepath.FromSlash(relPath))
	if !strings.HasPrefix(filepath.Clean(fullPath), filepath.Clean(s.workspaceRoot())+string(os.PathSeparator)) {
		writeError(w, http.StatusBadRequest, "INVALID_PATH", "文件路径无效")
		return
	}
	http.ServeFile(w, r, fullPath)
}

func (s *Server) publicWorkspaceFileURL(relPath string, userID int64, publicBaseURL string, ttl time.Duration) (string, error) {
	relPath = filepath.ToSlash(filepath.Clean(strings.TrimSpace(relPath)))
	if !isWorkspacePathForUser(relPath, userID) {
		return "", fmt.Errorf("workspace image path is not available to this user")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
	if baseURL == "" {
		return "", fmt.Errorf("public base URL is not configured; image-to-image needs a URL-accessible workspace image")
	}
	expires := time.Now().UTC().Add(ttl).Unix()
	signature := s.workspaceFileSignature(relPath, expires)
	escapedPath := strings.ReplaceAll(filepath.ToSlash(relPath), " ", "%20")
	return fmt.Sprintf("%s/api/workspace/public/%s?expires=%d&sig=%s", baseURL, escapedPath, expires, signature), nil
}

func (s *Server) workspaceFileSignature(relPath string, expires int64) string {
	secret := strings.TrimSpace(s.cfg.SessionSecret)
	if secret == "" {
		secret = "dev-secret-change-me"
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(filepath.ToSlash(filepath.Clean(relPath))))
	_, _ = mac.Write([]byte("|"))
	_, _ = mac.Write([]byte(strconv.FormatInt(expires, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) publicBaseURLForRequest(r *http.Request) string {
	if configured := strings.TrimRight(strings.TrimSpace(s.cfg.PublicBaseURL), "/"); configured != "" {
		return configured
	}
	if r == nil {
		return ""
	}
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		proto = "http"
		if r.TLS != nil {
			proto = "https"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return ""
	}
	return strings.TrimRight(proto+"://"+host, "/")
}

func (s *Server) workspaceRoot() string {
	root := strings.TrimSpace(s.cfg.WorkspacePath)
	if root == "" {
		root = filepath.Clean(filepath.Join("..", "workspaces"))
	}
	return root
}

type workspacePromptFile struct {
	Name      string
	Path      string
	Type      string
	Size      int64
	Width     int
	Height    int
	UpdatedAt string
	ModTime   time.Time
}

func (s *Server) workspaceSystemPrompt(userID int64) string {
	if userID <= 0 {
		return ""
	}
	files := s.workspacePromptFilesFromMessages(userID)
	if len(files) == 0 {
		files = s.workspacePromptFilesFromDisk(userID)
	}
	if len(files) == 0 {
		return ""
	}
	sort.Slice(files, func(i, j int) bool {
		return workspacePromptFileTime(files[i]).After(workspacePromptFileTime(files[j]))
	})
	if len(files) > maxWorkspaceFilesForPrompt {
		files = files[:maxWorkspaceFilesForPrompt]
	}
	var b strings.Builder
	b.WriteString("User workspace files are stored per user. Recent files available to this user are listed below. Treat these as file references, not inline content; when a tool accepts a workspace path, use the path exactly as shown.")
	for _, file := range files {
		b.WriteString("\n- ")
		b.WriteString(firstNonEmpty(file.Name, filepath.Base(file.Path), "file"))
		b.WriteString("；path=")
		b.WriteString(file.Path)
		if strings.TrimSpace(file.Type) != "" {
			b.WriteString("；type=")
			b.WriteString(strings.TrimSpace(file.Type))
		}
		if file.Width > 0 && file.Height > 0 {
			b.WriteString(fmt.Sprintf("；dimensions=%dx%d", file.Width, file.Height))
		}
		if file.Size > 0 {
			b.WriteString(fmt.Sprintf("；size=%d bytes", file.Size))
		}
	}
	return b.String()
}

func (s *Server) workspacePromptFilesFromMessages(userID int64) []workspacePromptFile {
	if s.store == nil || s.store.DB == nil {
		return nil
	}
	rows, err := s.store.DB.Query(`
		SELECT attachments, updated_at
		FROM messages
		WHERE user_id=? AND deleted_at IS NULL AND attachments IS NOT NULL AND attachments <> '' AND attachments <> '[]'
		ORDER BY updated_at DESC, id DESC
		LIMIT 80
	`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	files := make([]workspacePromptFile, 0, maxWorkspaceFilesForPrompt)
	seen := map[string]bool{}
	for rows.Next() {
		var raw, updatedAt string
		if err := rows.Scan(&raw, &updatedAt); err != nil {
			continue
		}
		var items []attachment
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			continue
		}
		for _, item := range items {
			path := strings.TrimSpace(item.WorkspacePath)
			if !isWorkspacePathForUser(path, userID) || seen[path] {
				continue
			}
			seen[path] = true
			files = append(files, workspacePromptFile{
				Name:      strings.TrimSpace(firstNonEmpty(item.OriginalName, item.Name)),
				Path:      path,
				Type:      strings.TrimSpace(item.Type),
				Size:      item.Size,
				Width:     item.Width,
				Height:    item.Height,
				UpdatedAt: updatedAt,
			})
			if len(files) >= maxWorkspaceFilesForPrompt {
				return files
			}
		}
	}
	return files
}

func (s *Server) workspacePromptFilesFromDisk(userID int64) []workspacePromptFile {
	root := filepath.Clean(s.workspaceRoot())
	userRoot := filepath.Join(root, "users", fmt.Sprintf("%d", userID))
	if _, err := os.Stat(userRoot); err != nil {
		return nil
	}
	files := make([]workspacePromptFile, 0, maxWorkspaceFilesForPrompt)
	_ = filepath.WalkDir(userRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		files = append(files, workspacePromptFile{
			Name:    entry.Name(),
			Path:    filepath.ToSlash(rel),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC(),
		})
		return nil
	})
	return files
}

func workspacePromptFileTime(file workspacePromptFile) time.Time {
	if strings.TrimSpace(file.UpdatedAt) != "" {
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(file.UpdatedAt)); err == nil {
			return parsed
		}
	}
	if !file.ModTime.IsZero() {
		return file.ModTime
	}
	return time.Time{}
}

func safeWorkspaceFileName(name string, index int) string {
	name = strings.TrimSpace(filepath.Base(name))
	if name == "" || name == "." {
		name = fmt.Sprintf("file-%d", index+1)
	}
	name = safeFileNamePattern.ReplaceAllString(name, "_")
	name = strings.Trim(name, "._")
	if name == "" {
		name = fmt.Sprintf("file-%d", index+1)
	}
	if len(name) > 120 {
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		if len(ext) > 20 {
			ext = ""
		}
		if len(base) > 100 {
			base = base[:100]
		}
		name = base + ext
	}
	return name
}

func attachmentWorkspaceSummary(raw string) string {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "[]" {
		return ""
	}
	var items []attachment
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return ""
	}
	var b strings.Builder
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = "未命名文件"
		}
		if b.Len() == 0 {
			b.WriteString("工作空间文件：")
		}
		b.WriteString("\n- 文件名：")
		b.WriteString(name)
		if strings.TrimSpace(item.WorkspacePath) != "" {
			b.WriteString("；路径：")
			b.WriteString(strings.TrimSpace(item.WorkspacePath))
		}
		if item.Width > 0 && item.Height > 0 {
			b.WriteString(fmt.Sprintf("；尺寸：%dx%d", item.Width, item.Height))
		}
		if item.Size > 0 {
			b.WriteString(fmt.Sprintf("；大小：%d bytes", item.Size))
		}
		if strings.TrimSpace(item.Error) != "" {
			b.WriteString("；状态：")
			b.WriteString(strings.TrimSpace(item.Error))
		}
	}
	return b.String()
}
