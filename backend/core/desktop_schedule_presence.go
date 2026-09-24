package main

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"lazymind/core/schedulepresence"
	"lazymind/core/scheduler"
	"lazymind/core/store"
)

var desktopSchedulePresenceMu sync.Mutex

// setDesktopSchedulePresence is called only by the managed Electron process.
// A short lease also pauses scheduled work if the window process disappears.
func setDesktopSchedulePresence(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("LAZYMIND_RUNTIME_PROFILE")), "desktop") {
		http.NotFound(w, r)
		return
	}
	expected := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	provided := strings.TrimSpace(r.Header.Get("X-LazyMind-Internal-Token"))
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var request struct {
		Active *bool `json:"active"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Active == nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	desktopSchedulePresenceMu.Lock()
	defer desktopSchedulePresenceMu.Unlock()
	if !*request.Active {
		schedulepresence.SetActive(false)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !schedulepresence.Active() {
		if db := store.DB(); db != nil {
			if err := scheduler.SkipMissedSchedules(r.Context(), db, time.Now().UTC()); err != nil {
				http.Error(w, "schedule unavailable", http.StatusServiceUnavailable)
				return
			}
		} else {
			http.Error(w, "schedule unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	schedulepresence.SetActive(true)
	w.WriteHeader(http.StatusNoContent)
}
