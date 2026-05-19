package app

import (
	"chatxxx/backend/internal/config"
	"chatxxx/backend/internal/db"
	"chatxxx/backend/internal/httpapi"
	"fmt"
	"net/http"
)

func Run() error {
	cfg := config.Load()
	store, err := db.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	server := httpapi.NewServer(cfg, store)
	addr := fmt.Sprintf(":%s", cfg.Port)
	fmt.Printf("ChatXXX backend listening on http://127.0.0.1%s\n", addr)
	return http.ListenAndServe(addr, server.Routes())
}
