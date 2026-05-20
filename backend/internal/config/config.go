package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Port            string
	DBPath          string
	SessionSecret   string
	CORSOrigin      string
	AppEnv          string
	UniFuncsAPIKey  string
	UniFuncsBaseURL string
}

func Load() Config {
	loadDotEnv(".env")
	if backendEnv := backendEnvPath(); backendEnv != "" {
		loadDotEnv(backendEnv)
	}
	baseDir := backendBaseDir()
	return Config{
		Port:            env("APP_PORT", "8007"),
		DBPath:          resolvePath(env("DB_PATH", "../data/chatxxx.sqlite"), baseDir),
		SessionSecret:   env("SESSION_SECRET", "dev-secret-change-me"),
		CORSOrigin:      env("CORS_ORIGIN", "http://127.0.0.1:5178"),
		AppEnv:          env("APP_ENV", "development"),
		UniFuncsAPIKey:  env("UNIFUNCS_API_KEY", ""),
		UniFuncsBaseURL: env("UNIFUNCS_BASE_URL", "https://api.unifuncs.com"),
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}

func backendEnvPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Clean(filepath.Join(filepath.Dir(exe), "..", ".env"))
}

func backendBaseDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Clean(filepath.Join(filepath.Dir(exe), ".."))
}

func resolvePath(path, baseDir string) string {
	if filepath.IsAbs(path) || strings.TrimSpace(baseDir) == "" {
		return path
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func EnsureDirForFile(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}
