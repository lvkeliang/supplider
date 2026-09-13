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
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/supplider/supplider/backend/internal/aigateway"
	"github.com/supplider/supplider/backend/internal/backup"
	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/featureflag"
	"github.com/supplider/supplider/backend/internal/httpapi"
	"github.com/supplider/supplider/backend/internal/objectfactory"
	"github.com/supplider/supplider/backend/internal/storefactory"
	"github.com/supplider/supplider/backend/internal/supplier"
	"github.com/supplider/supplider/backend/internal/tier"
	"github.com/supplider/supplider/backend/internal/vectorfactory"
	"github.com/supplider/supplider/backend/internal/webui"
)

func main() {
	addr := flag.String("addr", envOr("SRM_HTTP_ADDR", "127.0.0.1:7612"), "HTTP listen address")
	dataDir := flag.String("data-dir", envOr("SRM_DATA_DIR", ""), "data directory (personal tier)")
	flag.Parse()

	log.Printf("suppliderd starting: tier=%s addr=%s data-dir=%q", tier.Current(), *addr, *dataDir)

	// Acquire the listen socket BEFORE touching the data directory. If the
	// port is already held by a healthy suppliderd (an orphaned sidecar
	// after a hard app crash), defer to it and exit cleanly so the desktop
	// UI connects to the running instance instead of showing a permanent
	// "后端未连接". This must precede restore/store open so a duplicate
	// process never swaps the live library or contends on the DB.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			probeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			if awaitExistingInstance(probeCtx, "http://"+*addr, 2*time.Second) {
				cancel()
				log.Printf("another suppliderd is already serving http://%s and is healthy; exiting so the UI uses the running instance", *addr)
				return
			}
			cancel()
		}
		log.Fatalf("listen %s: %v", *addr, err)
	}

	// Staged restore (应用内恢复/迁移) must complete BEFORE any DB handle
	// exists — it replaces supplider.db and attachments on disk.
	applyPendingRestore(*dataDir)

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

	// AI provider config: read the persisted selection and build the live
	// gateway. No key → disabled (all AI entries hidden). The /features
	// endpoint derives AI flags from this gateway at request time, so saving
	// a config lights the entry points without a restart.
	rawAI, err := store.GetSetting(context.Background(), aigateway.SettingKey)
	if err != nil && !errors.Is(err, datamodel.ErrNotFound) {
		log.Printf("load ai config: %v (AI disabled)", err)
	}
	aiCfg, _ := aigateway.DecodeConfig(rawAI)
	gateway := aigateway.New(aiCfg, nil)

	// Semantic search (TR-19-E) vector index. Personal tier persists it to
	// <DataDir>/vectors.db (SQLite) so the index survives restarts; it is
	// primed/refreshed via POST /ai/index.
	vectorIndex, err := vectorfactory.Open(vectorfactory.Config{DataDir: *dataDir})
	if err != nil {
		log.Fatalf("open vector store: %v", err)
	}
	defer vectorIndex.Close()

	feats := featureflag.Default().WithAIState(gateway.Enabled(), gateway.CanEmbed())

	// Single binary serves API + embedded UI. CORS lets the Tauri webview
	// (tauri://localhost / tauri.localhost) call the loopback sidecar.
	apiServer := httpapi.New(svc, feats).
		WithGateway(gateway).
		WithVectorStore(vectorIndex).
		WithObjects(objects).
		WithBackup(backupWriter(store, objects)).
		WithRestore(restoreFuncs(*dataDir)).
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

	// Serve on the listener acquired at startup (single-instance gate). A
	// Serve failure after bind must NOT call log.Fatalf: os.Exit skips the
	// deferred store.Close(), risking an un-checkpointed SQLite WAL. Relay
	// it to the main goroutine so the clean-shutdown path runs instead.
	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()
	log.Printf("suppliderd ready on http://%s", *addr)

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		log.Printf("http server stopped serving: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	log.Println("suppliderd stopped")
}

// dbSnapshotter is the backup capability implemented by the SQLite adapter
// (VACUUM INTO); the in-memory dev store lacks it.
type dbSnapshotter interface {
	SnapshotTo(ctx context.Context, destPath string) error
}

// rootReporter is implemented by the local-FS object store; it locates the
// attachments tree for inclusion in a backup.
type rootReporter interface {
	Root() string
}

// backupWriter wires GET /api/v1/backup when the wired adapters support it:
// a consistent DB snapshot plus every attachment, zipped. Returns nil
// (endpoint answers 501) on ephemeral/in-memory wiring.
func backupWriter(store interface{}, objects interface{}) httpapi.BackupFunc {
	sn, ok := store.(dbSnapshotter)
	if !ok {
		return nil
	}
	root := ""
	if rr, ok := objects.(rootReporter); ok {
		root = rr.Root()
	}
	return func(ctx context.Context, w io.Writer) error {
		_, err := backup.WriteArchive(ctx, w, sn.SnapshotTo, root, map[string]any{
			"tier": tier.Current(),
		})
		return err
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
