package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// All scan admissions share this lock with the background loop. A failed or
// disconnected desktop controller loses its pause after the short lease.
type desktopUpdateGate struct {
	mu                                                       sync.Mutex
	bootstrapTx, bootstrapOwner, completedTx, completedOwner string
	epoch, highest, renewal                                  uint64
	active                                                   int64
	timer                                                    *time.Timer
	token                                                    string
	count                                                    func(context.Context) (int64, error)
}

func (g *desktopUpdateGate) enter() bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.epoch != 0 || g.bootstrapTx != "" {
		return false
	}
	g.active++
	return true
}
func (g *desktopUpdateGate) leave() {
	if g != nil {
		g.mu.Lock()
		g.active--
		g.mu.Unlock()
	}
}

func (g *desktopUpdateGate) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/desktop-update" {
			g.control(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if !g.enter() {
				w.Header().Set("Retry-After", "10")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"code":"desktop_update_preparing"}`)
				return
			}
			defer g.leave()
		}
		next.ServeHTTP(w, r)
	})
}
func (g *desktopUpdateGate) control(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if r.Method != http.MethodPost || err != nil || !net.ParseIP(host).IsLoopback() || r.Header.Get("Origin") != "" || g.token == "" || subtle.ConstantTimeCompare([]byte(g.token), []byte(r.Header.Get("X-LazyMind-Internal-Token"))) != 1 {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	var req struct {
		Action        string `json:"action"`
		TransactionID string `json:"transactionId,omitempty"`
		Owner         string `json:"owner,omitempty"`
		Epoch         uint64 `json:"epoch"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF || (req.Action != "status" && req.Action != "pause" && req.Action != "resume" && req.Action != "bootstrap-complete") || (req.Action != "status" && req.Action != "bootstrap-complete" && req.Epoch == 0) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if req.Action == "bootstrap-complete" {
		if g.CompleteUpdateBootstrap(req.TransactionID, req.Owner) != nil {
			w.WriteHeader(http.StatusConflict)
			return
		}
		req.Action = "status"
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if req.Action == "pause" {
		if (g.epoch != 0 && g.epoch != req.Epoch) || (g.epoch == 0 && req.Epoch <= g.highest) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		g.epoch = req.Epoch
		g.highest = req.Epoch
		if g.timer != nil {
			g.timer.Stop()
		}
		g.renewal++
		epoch, renewal := g.epoch, g.renewal
		g.timer = time.AfterFunc(2*time.Minute, func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.epoch == epoch && g.renewal == renewal {
				g.epoch = 0
			}
		})
	}
	if req.Action == "resume" {
		if g.epoch != 0 && g.epoch != req.Epoch {
			w.WriteHeader(http.StatusConflict)
			return
		}
		g.epoch = 0
		if g.timer != nil {
			g.timer.Stop()
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	n, err := g.count(ctx)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	wire := map[string]any{"active": n + g.active, "paused": g.epoch != 0, "epoch": g.epoch}
	if g.bootstrapTx != "" {
		wire["bootstrap"] = map[string]any{"transactionId": g.bootstrapTx, "owner": g.bootstrapOwner, "sealed": true}
	}
	_ = json.NewEncoder(w).Encode(wire)
}

func (g *desktopUpdateGate) BeginUpdateBootstrap(tx, owner string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(tx) != 32 || owner == "" {
		return errors.New("update_bootstrap_invalid")
	}
	if g.bootstrapTx != "" && (g.bootstrapTx != tx || g.bootstrapOwner != owner) {
		return errors.New("update_bootstrap_conflict")
	}
	g.bootstrapTx, g.bootstrapOwner = tx, owner
	return nil
}
func (g *desktopUpdateGate) CompleteUpdateBootstrap(tx, owner string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.bootstrapTx == "" {
		if tx != "" && tx == g.completedTx && owner == g.completedOwner {
			return nil
		}
		return errors.New("update_bootstrap_stale")
	}
	if tx != g.bootstrapTx || owner != g.bootstrapOwner || g.active != 0 {
		return errors.New("update_bootstrap_stale")
	}
	g.completedTx, g.completedOwner = tx, owner
	g.bootstrapTx, g.bootstrapOwner = "", ""
	return nil
}
