package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"lazymind/core/log"
	"lazymind/core/state"
	"lazymind/core/store"
)

// draftBufferFlushInterval is how often the background worker flushes dirty
// draft fields from the state store to the DB.
const draftBufferFlushInterval = 5 * time.Second

// draftBufferTTL is the TTL for draft buffer keys. Long enough to survive a
// brief service restart, short enough to not leak stale data forever.
const draftBufferTTL = 24 * time.Hour

// Draft buffer field names stored in the state hash.
const (
	bufFieldPluginYAML  = "plugin_yaml_content"
	bufFieldStateYAML   = "state_yaml_content"
	bufFieldStateLayout = "state_layout_content"
	bufFieldScenario    = "scenario_content"
	bufFieldScripts     = "scripts_content"
	bufFieldVersion     = "version"
	bufFieldDirtyTS     = "dirty_ts"
)

// draftBufferKey returns the state-store hash key for a given draft ID.
func draftBufferKey(draftID string) string {
	return "plugin_draft:" + draftID
}

// draftBufferWrite writes the given fields into the state-store buffer and
// marks the entry as dirty. versioned=true means the caller has already
// validated the optimistic-lock version and wants the version incremented on
// the next DB flush.
//
// This is a best-effort write: if the state store is unavailable, the caller
// must fall back to a direct DB write.
func draftBufferWrite(ctx context.Context, st state.Store, draftID string, fields map[string]any, newVersion int) error {
	if st == nil {
		return fmt.Errorf("state store not available")
	}
	key := draftBufferKey(draftID)

	all := make(map[string]any, len(fields)+2)
	for k, v := range fields {
		all[k] = v
	}
	all[bufFieldDirtyTS] = strconv.FormatInt(time.Now().UnixMilli(), 10)
	if newVersion > 0 {
		all[bufFieldVersion] = strconv.Itoa(newVersion)
	}
	return st.HSet(ctx, key, all, draftBufferTTL)
}

// draftBufferReadAndDelete reads all buffered fields for a draft and removes
// the dirty marker. Returns nil if the key does not exist or is already clean.
func draftBufferReadAndDelete(ctx context.Context, st state.Store, draftID string) (map[string]string, error) {
	if st == nil {
		return nil, fmt.Errorf("state store not available")
	}
	key := draftBufferKey(draftID)
	fields, err := st.HGetAll(ctx, key)
	if err != nil || len(fields) == 0 {
		return nil, err
	}
	if _, dirty := fields[bufFieldDirtyTS]; !dirty {
		return nil, nil
	}
	// Remove the dirty marker. If this fails, the next flush will re-process
	// the same data idempotently.
	_ = st.HDel(ctx, key, bufFieldDirtyTS)
	return fields, nil
}

// flushDraftBuffer flushes a single draft's buffered fields to the DB.
// It applies the same optimistic-lock logic as SavePluginDraft: if the
// buffered version does not match the DB version, the flush is skipped and
// the buffer is cleared (the write was already superseded by a direct DB write).
func flushDraftBuffer(ctx context.Context, draftID string) {
	st := store.State()
	if st == nil {
		return
	}
	fields, err := draftBufferReadAndDelete(ctx, st, draftID)
	if err != nil || len(fields) == 0 {
		return
	}

	db := store.DB()
	if db == nil {
		return
	}

	// Re-check version before writing.
	bufVersionStr, hasVersion := fields[bufFieldVersion]
	updates := map[string]any{"updated_at": time.Now().UTC()}

	if v, ok := fields[bufFieldPluginYAML]; ok {
		updates["plugin_yaml_content"] = v
	}
	if v, ok := fields[bufFieldStateYAML]; ok {
		updates["state_yaml_content"] = v
	}
	if v, ok := fields[bufFieldStateLayout]; ok {
		updates["state_layout_content"] = v
	}
	if v, ok := fields[bufFieldScenario]; ok {
		updates["scenario_content"] = v
	}
	if v, ok := fields[bufFieldScripts]; ok {
		updates["scripts_content"] = v
	}

	if hasVersion {
		bufVersion, convErr := strconv.Atoi(bufVersionStr)
		if convErr != nil {
			log.Logger.Warn().Str("draft_id", draftID).Str("version", bufVersionStr).Msg("[draft_buffer] invalid version in buffer; skipping flush")
			return
		}
		// Flush only if the DB version still matches what was in the buffer.
		// bufVersion is already the *new* version (old+1), so we check that
		// the DB version is bufVersion-1 (pre-increment state).
		res := db.WithContext(ctx).
			Exec("UPDATE plugin_drafts SET "+buildSetClause(updates)+", version = ? WHERE id = ? AND version = ?",
				append(mapValues(updates), bufVersion, draftID, bufVersion-1)...,
			)
		if res.Error != nil {
			log.Logger.Error().Err(res.Error).Str("draft_id", draftID).Msg("[draft_buffer] flush failed")
		} else if res.RowsAffected == 0 {
			log.Logger.Debug().Str("draft_id", draftID).Int("buf_version", bufVersion).Msg("[draft_buffer] version mismatch on flush; skipped (DB was ahead)")
		}
		return
	}

	// No version in buffer (layout-only or scenario/scripts save): plain update.
	if err := db.WithContext(ctx).Exec("UPDATE plugin_drafts SET "+buildSetClause(updates)+" WHERE id = ?",
		append(mapValues(updates), draftID)...,
	).Error; err != nil {
		log.Logger.Error().Err(err).Str("draft_id", draftID).Msg("[draft_buffer] flush (no-version) failed")
	}
}

// buildSetClauseAndValues turns a map into a "k1 = ?, k2 = ?" SQL fragment
// and the corresponding ordered values slice. Both are produced in the same
// deterministic iteration order to avoid field/value mismatch.
// Keys are trusted (internal only, not user-provided).
func buildSetClauseAndValues(m map[string]any) (string, []any) {
	// Sort keys for deterministic order.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion-sort on a small map (typically ≤8 fields) is fine.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	parts := make([]string, len(keys))
	vals := make([]any, len(keys))
	for i, k := range keys {
		parts[i] = k + " = ?"
		vals[i] = m[k]
	}
	return strings.Join(parts, ", "), vals
}

// buildSetClause is kept for callers that don't need the values.
func buildSetClause(m map[string]any) string {
	clause, _ := buildSetClauseAndValues(m)
	return clause
}

// mapValues returns the values of m in the same order as buildSetClauseAndValues.
func mapValues(m map[string]any) []any {
	_, vals := buildSetClauseAndValues(m)
	return vals
}

// StartDraftBufferFlusher starts a goroutine that periodically scans the
// state store for dirty draft buffers and flushes them to the DB.
// Call once at startup (after store.Init). The goroutine exits when ctx is cancelled.
func StartDraftBufferFlusher(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(draftBufferFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				// Graceful shutdown: one final flush pass.
				flushAllDirtyDrafts(context.Background())
				return
			case <-ticker.C:
				flushAllDirtyDrafts(ctx)
			}
		}
	}()
}

// flushAllDirtyDrafts scans for all dirty draft keys in the state store and
// flushes them to the DB. Uses a naming convention scan: keys prefixed with
// "plugin_draft:" that contain a dirty_ts field.
//
// Because state.Store does not expose SCAN, we rely on the DB to enumerate
// recently-modified draft IDs and check each one in the state store.
// This avoids needing SCAN and works for both Redis and SQLite backends.
func flushAllDirtyDrafts(ctx context.Context) {
	db := store.DB()
	st := store.State()
	if db == nil || st == nil {
		return
	}

	// Fetch draft IDs updated in the last 2 minutes; these are the only ones
	// that could have a live buffer entry.
	type row struct{ ID string }
	var rows []row
	if err := db.WithContext(ctx).
		Raw("SELECT id FROM plugin_drafts WHERE updated_at >= ? ORDER BY updated_at DESC LIMIT 200",
			time.Now().Add(-2*time.Minute)).
		Scan(&rows).Error; err != nil {
		log.Logger.Error().Err(err).Msg("[draft_buffer] scan recent drafts failed")
		return
	}

	for _, r := range rows {
		flushDraftBuffer(ctx, r.ID)
	}
}

// draftBufferStats returns a JSON-serialisable summary for debugging.
func draftBufferStats(ctx context.Context, draftID string) map[string]string {
	st := store.State()
	if st == nil {
		return map[string]string{"error": "state store unavailable"}
	}
	fields, err := st.HGetAll(ctx, draftBufferKey(draftID))
	if err != nil {
		return map[string]string{"error": err.Error()}
	}
	// Omit large content fields for readability.
	summary := make(map[string]string)
	for k, v := range fields {
		switch k {
		case bufFieldPluginYAML, bufFieldStateYAML, bufFieldStateLayout,
			bufFieldScenario, bufFieldScripts:
			summary[k] = fmt.Sprintf("<len=%d>", len(v))
		default:
			summary[k] = v
		}
	}
	return summary
}

// marshalDraftBufferStats is a helper used in debug endpoints.
func marshalDraftBufferStats(ctx context.Context, draftID string) string {
	b, _ := json.Marshal(draftBufferStats(ctx, draftID))
	return string(b)
}
