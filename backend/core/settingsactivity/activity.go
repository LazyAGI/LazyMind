// Package settingsactivity records active capability use without tool arguments or results.
package settingsactivity

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
)

type Activity struct {
	RunID          string `json:"run_id"`
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	Capability     string `json:"capability"`
	ResourceID     string `json:"resource_id"`
	UpdatedAt      int64  `json:"updated_at"`
}

type ReportRequest struct {
	RunID          string `json:"run_id"`
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	Capability     string `json:"capability"`
	ResourceID     string `json:"resource_id"`
	Active         *bool  `json:"active"`
}

func key(user string) string { return "settings:activity:" + user }

// Serialize reporting and applying a change across Core replicas.
func WithLock(ctx context.Context, cache state.Store, user string, apply func() error) error {
	if cache == nil {
		return errors.New("activity state unavailable")
	}
	lockKey, token := "settings:change:"+user, []byte(uuid.NewString())
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		acquired, err := cache.SetNX(ctx, lockKey, token, 30*time.Second)
		if err != nil {
			return err
		}
		if acquired {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("settings change busy")
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if atomic, ok := cache.(state.CompareAndDeleteStore); ok {
			_, _ = atomic.CompareAndDelete(releaseCtx, lockKey, token)
		}
	}()
	return apply()
}

func Read(ctx context.Context, cache state.Store, user string) ([]Activity, error) {
	if cache == nil {
		return nil, errors.New("activity state unavailable")
	}
	entries, err := cache.HGetAll(ctx, key(user))
	if err != nil {
		return nil, err
	}
	result := make([]Activity, 0, len(entries))
	for _, raw := range entries {
		var entry Activity
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, nil
}

func InternalReport(w http.ResponseWriter, r *http.Request) {
	expected := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	supplied := r.Header.Get("X-LazyMind-Internal-Token")
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(supplied)) != 1 {
		common.ReplyErr(w, "internal token required", http.StatusUnauthorized)
		return
	}
	var input ReportRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || input.Active == nil || input.ID == "" || len(input.ID) > 128 ||
		input.ConversationID == "" || len(input.ConversationID) > 64 || len(input.ResourceID) > 128 || len(input.RunID) > 128 ||
		(input.Capability != "skills_enabled" && input.Capability != "mcp_enabled") || decoder.Decode(new(any)) != io.EOF {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	user := strings.TrimSpace(store.UserID(r))
	db := store.DB()
	if db == nil || user == "" {
		common.ReplyErr(w, "unable to query conversation status", http.StatusServiceUnavailable)
		return
	}
	var count int64
	if err := db.WithContext(r.Context()).Model(&orm.Conversation{}).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", input.ConversationID, user).Count(&count).Error; err != nil {
		common.ReplyErr(w, "unable to query conversation status", http.StatusServiceUnavailable)
		return
	}
	if count != 1 {
		common.ReplyErr(w, "forbidden", http.StatusForbidden)
		return
	}
	// Built-in MCP servers are independent of the user's MCP master control.
	if input.Capability == "mcp_enabled" && *input.Active {
		if err := db.WithContext(r.Context()).Model(&orm.MCPServer{}).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", input.ResourceID, user).Count(&count).Error; err != nil {
			common.ReplyErr(w, "unable to query conversation status", http.StatusServiceUnavailable)
			return
		}
		if count == 0 {
			common.ReplyOK(w, map[string]bool{"tracked": false})
			return
		}
	}
	cache := store.State()
	err := WithLock(r.Context(), cache, user, func() error {
		if !*input.Active {
			return cache.HDel(r.Context(), key(user), input.ID)
		}
		value, err := json.Marshal(Activity{ID: input.ID, RunID: input.RunID, ConversationID: input.ConversationID, Capability: input.Capability, ResourceID: input.ResourceID, UpdatedAt: time.Now().Unix()})
		if err != nil {
			return err
		}
		return cache.HSet(r.Context(), key(user), map[string]any{input.ID: string(value)}, 24*time.Hour)
	})
	if err != nil {
		common.ReplyErr(w, "unable to query conversation status", http.StatusServiceUnavailable)
		return
	}
	common.ReplyOK(w, map[string]bool{"tracked": true})
}
