package workflow

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/state"
)

func TestDriverActivityTracksConcurrentCallsAndIdempotentCleanup(t *testing.T) {
	cache, err := state.NewSQLiteStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	first := beginDriverActivity(cache, "conversation", "session")
	second := beginDriverActivity(cache, "conversation", "session")
	first()
	first()
	values, err := cache.HGetAll(context.Background(), DriverActivityKey("conversation"))
	if err != nil || len(values) != 1 {
		t.Fatalf("remaining=%v err=%v", values, err)
	}
	for _, raw := range values {
		var activity DriverActivity
		if json.Unmarshal([]byte(raw), &activity) != nil || activity.SessionID != "session" || activity.ExpiresAt <= time.Now().Unix() {
			t.Fatalf("invalid activity %s", raw)
		}
	}
	second()
	values, err = cache.HGetAll(context.Background(), DriverActivityKey("conversation"))
	if err != nil || len(values) != 0 {
		t.Fatalf("not cleaned up: %v %v", values, err)
	}
}
