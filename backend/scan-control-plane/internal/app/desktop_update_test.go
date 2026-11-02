package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func scanUpdateRequest(g *desktopUpdateGate, action string, epoch int) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/internal/desktop-update", strings.NewReader(fmt.Sprintf(`{"action":%q,"epoch":%d}`, action, epoch)))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-LazyMind-Internal-Token", "fixture-token")
	w := httptest.NewRecorder()
	g.control(w, r)
	return w
}
func TestDesktopUpdateScanPausesTheRealRuntimeLoopAndResumes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := &desktopUpdateGate{token: "fixture-token", count: func(context.Context) (int64, error) { return 0, nil }}
		r := &Runtime{updateGate: g}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var wg sync.WaitGroup
		entered, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int64
		r.startLoop(ctx, &wg, time.Second, func(context.Context) {
			if calls.Add(1) == 1 {
				close(entered)
				<-release
			}
		})
		<-entered
		w := scanUpdateRequest(g, "pause", 1)
		var state struct {
			Active int64
			Paused bool
		}
		_ = json.Unmarshal(w.Body.Bytes(), &state)
		if w.Code != 200 || state.Active != 1 || !state.Paused {
			t.Fatal(w.Code, w.Body.String())
		}
		close(release)
		time.Sleep(3 * time.Second)
		if calls.Load() != 1 {
			t.Fatal("pause admitted another iteration")
		}
		if scanUpdateRequest(g, "resume", 2).Code != 409 {
			t.Fatal("stale resume accepted")
		}
		if scanUpdateRequest(g, "resume", 1).Code != 200 {
			t.Fatal("resume failed")
		}
		time.Sleep(2 * time.Second)
		if calls.Load() < 2 {
			t.Fatal("scan did not resume")
		}
		cancel()
		wg.Wait()
	})
}
func TestDesktopUpdateScanLeaseAndHTTPAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := &desktopUpdateGate{token: "fixture-token", count: func(context.Context) (int64, error) { return 0, nil }}
		h := g.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
		scanUpdateRequest(g, "pause", 1)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/sources/tasks", nil))
		if w.Code != 503 {
			t.Fatal("new parse admitted")
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
		if w.Code != 204 {
			t.Fatal("health blocked")
		}
		time.Sleep(121 * time.Second)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/sources/tasks", nil))
		if w.Code != 204 {
			t.Fatal("crashed controller left scan paused")
		}
	})
}
func TestDesktopUpdateScanRejectsUnauthorizedAndUnreadableState(t *testing.T) {
	g := &desktopUpdateGate{token: "fixture-token", count: func(context.Context) (int64, error) { return 0, errors.New("private details") }}
	r := httptest.NewRequest("POST", "/internal/desktop-update", strings.NewReader(`{"action":"pause","epoch":1}`))
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	g.control(w, r)
	if w.Code != 403 || g.epoch != 0 {
		t.Fatal("unauthorized control")
	}
	w = scanUpdateRequest(g, "status", 0)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body.String())
	}
}
