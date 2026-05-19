package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Port          string
	DBPath        string
	SessionSecret string
	CORSOrigin    string
	AppEnv        string
}

func Load() Config {
	loadDotEnv(".env")
	return Config{
		Port:          env("APP_PORT", "8007"),
		DBPath:        env("DB_PATH", "../data/chatxxx.sqlite"),
		SessionSecret: env("SESSION_SECRET", "dev-secret-change-me"),
		CORSOrigin:    env("CORS_ORIGIN", "http://127.0.0.1:5178"),
		AppEnv:        env("APP_ENV", "development"),
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

func EnsureDirForFile(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}
