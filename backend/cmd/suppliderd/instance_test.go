package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mux builds a holder server; nil handler funcs yield 404, letting a test
// emulate a foreign process squatting on the sidecar port.
func holder(t *testing.T, ready http.HandlerFunc, features http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	if ready != nil {
		mux.HandleFunc("/readyz", ready)
	}
	if features != nil {
		mux.HandleFunc("/api/v1/features", features)
	}
	return httptest.NewServer(mux)
}

func okReady(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func TestAwaitExistingInstanceRecognizesSupplider(t *testing.T) {
	srv := holder(t, okReady, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tier":"personal","storage":"sqlite"}`))
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !awaitExistingInstance(ctx, srv.URL, time.Second) {
		t.Fatal("healthy personal sidecar must be recognized and trigger handoff")
	}
}

func TestAwaitExistingInstanceEachTier(t *testing.T) {
	for _, tier := range []string{"personal", "small_business", "enterprise"} {
		srv := holder(t, okReady, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tier":"` + tier + `"}`))
		})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		ok := awaitExistingInstance(ctx, srv.URL, 200*time.Millisecond)
		cancel()
		srv.Close()
		if !ok {
			t.Errorf("tier %q must be recognized", tier)
		}
	}
}

func TestAwaitExistingInstanceRejectsForeignProcess(t *testing.T) {
	// Nothing on the sidecar routes — some unrelated local server holds it.
	foreign := holder(t, nil, nil)
	defer foreign.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if awaitExistingInstance(ctx, foreign.URL, 300*time.Millisecond) {
		t.Fatal("a foreign process on the port must NOT be deferred to")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("foreign probe should return within the wait window, took %v", d)
	}
}

func TestAwaitExistingInstanceRejectsWrongReadyOrTier(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		features string
	}{
		{"readyz not ok", `{"status":"starting"}`, `{"tier":"personal"}`},
		{"unknown tier", `{"status":"ok"}`, `{"tier":"something-else"}`},
		{"no tier", `{"status":"ok"}`, `{"storage":"sqlite"}`},
		{"not json", `{"status":"ok"}`, `not-json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := holder(t,
				func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) },
				func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.features)) },
			)
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if awaitExistingInstance(ctx, srv.URL, 200*time.Millisecond) {
				t.Fatalf("case %s must not be treated as a healthy sidecar", tc.name)
			}
		})
	}
}

func TestAwaitExistingInstanceNothingListening(t *testing.T) {
	// A closed test server leaves a dead URL — fail fast, never hang startup.
	srv := holder(t, nil, nil)
	dead := srv.URL
	srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	if awaitExistingInstance(ctx, dead, 250*time.Millisecond) {
		t.Fatal("nothing listening must not be reported as an existing instance")
	}
	if d := time.Since(start); d > 900*time.Millisecond {
		t.Fatalf("dead-port probe must respect the wait window, took %v", d)
	}
}

func TestProbeOncePeerStillStartingBecomesHealthy(t *testing.T) {
	// Emulate a peer that binds immediately but answers a few probes later.
	var hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		hits++
		if hits < 3 {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/v1/features", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tier":"personal"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if !awaitExistingInstance(ctx, srv.URL, 2*time.Second) {
		t.Fatal("a sidecar still starting must be recognized once it becomes healthy")
	}
}
