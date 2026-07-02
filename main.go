package main

import (
	"context"
	_ "embed"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"z-api-proxy/internal/config"
	"z-api-proxy/internal/counter"
	"z-api-proxy/internal/proxy"
	"z-api-proxy/internal/tray"
)

// version is set at build time via ldflags (-X main.version=...).
// It defaults to "dev" for local, untagged builds.
var version = "dev"

//go:embed assets/icon.ico
var iconNormal []byte

//go:embed assets/icon-error.ico
var iconError []byte

func main() {
	logPath := filepath.Join(config.AppConfigDir(), "proxy.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		log.SetOutput(logFile)
	}
	log.Printf("=== z-api-proxy %s starting ===", version)

	configPath := config.DefaultConfigPath()
	manager, err := config.NewManager(configPath)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	ctr := counter.New()
	px := proxy.New(manager, ctr)

	cfg := manager.Get()
	server := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      px,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", cfg.Server.Listen)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	tray.Run(iconNormal, iconError, manager, ctr, px, configPath, version)

	log.Println("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
	log.Println("=== z-api-proxy stopped ===")
}
