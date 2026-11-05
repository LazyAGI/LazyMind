package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDesktopUpdateRuntimePlanSelection(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "fixture-token")
	peer := func(active int64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Action string
				Epoch  uint64
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			paused := request.Action == "pause"
			epoch := request.Epoch
			if !paused {
				epoch = 0
			}
			_ = json.NewEncoder(w).Encode(updateWorkState{Active: active, Paused: paused, Epoch: epoch})
		}))
	}
	core, scan := peer(1), peer(2)
	defer core.Close()
	defer scan.Close()
	for _, mode := range []string{"rag-disabled", "rag-enabled", "required-scan-unavailable"} {
		t.Run(mode, func(t *testing.T) {
			cfg := RuntimeConfig{LocalProxy: LocalProxyConfig{CoreHostPort: core.Listener.Addr().(*net.TCPAddr).Port}, Algorithm: AlgorithmConfig{RAGDisabled: mode == "rag-disabled"}}
			if mode == "rag-enabled" {
				cfg.LocalProxy.ScanHostPort = scan.Listener.Addr().(*net.TCPAddr).Port
			}
			for _, action := range []string{"status", "pause", "resume"} {
				state, err := controlDesktopUpdate(context.Background(), cfg, action, 7)
				if mode == "required-scan-unavailable" {
					if err == nil {
						t.Fatal("required service absence accepted", action)
					}
					continue
				}
				want := int64(1)
				if mode == "rag-enabled" {
					want = 3
				}
				if err != nil || state.Active != want || state.Paused != (action == "pause") {
					t.Fatalf("%s: %+v %v", action, state, err)
				}
			}
		})
	}
}
