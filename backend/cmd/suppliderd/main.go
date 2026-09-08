// Command suppliderd is the Supplider backend daemon. On the personal tier
// it runs as a Tauri sidecar: the desktop shell launches this binary on
// app start (the user never sees it), serving the local HTTP API on
// 127.0.0.1. The same binary code builds for small_business/enterprise
// tags with different adapters wired by storefactory.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/supplider/supplider/backend/internal/featureflag"
	"github.com/supplider/supplider/backend/internal/httpapi"
	"github.com/supplider/supplider/backend/internal/objectfactory"
	"github.com/supplider/supplider/backend/internal/storefactory"
	"github.com/supplider/supplider/backend/internal/supplier"
	"github.com/supplider/supplider/backend/internal/tier"
	"github.com/supplider/supplider/backend/internal/webui"
)

func main() {
	addr := flag.String("addr", envOr("SRM_HTTP_ADDR", "127.0.0.1:7612"), "HTTP listen address")
	dataDir := flag.String("data-dir", envOr("SRM_DATA_DIR", ""), "data directory (personal tier)")
	flag.Parse()

	log.Printf("suppliderd starting: tier=%s addr=%s data-dir=%q", tier.Current(), *addr, *dataDir)

	store, err := storefactory.Open(storefactory.Config{DataDir: *dataDir})
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	objects, err := objectfactory.Open(objectfactory.Config{DataDir: *dataDir})
	if err != nil {
		log.Fatalf("open object store: %v", err)
	}

	svc := supplier.NewService(store)

	// Feature matrix: AI flags stay off until a gateway provider is
	// configured (MVP ships without AI keys — all AI entries hidden).
	feats := featureflag.Default().WithAIState(false)

	// Single binary serves API + embedded UI. CORS lets the Tauri webview
	// (tauri://localhost / tauri.localhost) call the loopback sidecar.
	apiServer := httpapi.New(svc, feats).
		WithObjects(objects).
		MountWebUI(webui.Dist())
	handler := httpapi.CORS(apiServer.Mux)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Background maintenance: visibility disposition sweep at boot and
	// once per 24h (the 7-day buffer → auto-downgrade must progress even
	// though nobody runs the CLI on a desktop install).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	apiServer.StartMaintenanceLoops(ctx)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()
	log.Printf("suppliderd ready on http://%s", *addr)

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	log.Println("suppliderd stopped")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
