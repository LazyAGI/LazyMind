package tree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/filefilter"
)

const (
	targetSearchCacheTTL          = 10 * time.Minute
	targetSearchCacheLocalFSTTL   = 30 * time.Minute
	targetSearchCacheExpireTTL    = 24 * time.Hour
	targetSearchCacheBuildTimeout = 2 * time.Hour
	targetSearchCacheMaxObjects   = 5000
	targetSearchCachePageDelay    = time.Second

	targetSearchCacheStatusMissing  = "missing"
	targetSearchCacheStatusBuilding = "building"
	targetSearchCacheStatusComplete = "complete"
	targetSearchCacheStatusFailed   = "failed"
)

type targetSearchCache struct {
	mu        sync.Mutex
	entries   map[string]*targetSearchCacheEntry
	store     targetSearchCacheStore
	ttl       time.Duration
	expireTTL time.Duration
	lockTTL   time.Duration
	timeout   time.Duration
	maxItems  int
	delay     time.Duration
}

type targetSearchCacheEntry struct {
	nodes     []TreeNode
	building  bool
	complete  bool
	truncated bool
	lastError string
	staleAt   time.Time
	expiresAt time.Time
}

type targetSearchCacheSnapshot struct {
	nodes     []TreeNode
	status    string
	building  bool
	complete  bool
	truncated bool
	lastError string
	stale     bool
	staleAt   time.Time
}

type TargetSearchCacheStore interface {
	Get(ctx context.Context, key string) (targetSearchCacheSnapshot, bool, error)
	Set(ctx context.Context, key string, snapshot targetSearchCacheSnapshot, staleTTL, expireTTL time.Duration) error
	TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

type targetSearchCacheStore = TargetSearchCacheStore

func newTargetSearchCache() *targetSearchCache {
	return &targetSearchCache{
		entries:   map[string]*targetSearchCacheEntry{},
		ttl:       targetSearchCacheTTL,
		expireTTL: targetSearchCacheExpireTTL,
		lockTTL:   targetSearchCacheBuildTimeout,
		timeout:   targetSearchCacheBuildTimeout,
		maxItems:  targetSearchCacheMaxObjects,
		delay:     targetSearchCachePageDelay,
	}
}

func (c *targetSearchCache) snapshot(ctx context.Context, req TargetTreeSearchRequest) targetSearchCacheSnapshot {
	if c == nil {
		return targetSearchCacheSnapshot{}
	}
	key := targetSearchCacheKey(req)
	if c.store != nil {
		if snapshot, ok, err := c.store.Get(ctx, key); err == nil && ok {
			return snapshot
		}
	}
	c.mu.Lock()
	entry := c.entries[key]
	if entry != nil && time.Now().Before(entry.expiresAt) {
		snapshot := entry.snapshot()
		c.mu.Unlock()
		return snapshot
	}
	c.mu.Unlock()
	return targetSearchCacheSnapshot{status: targetSearchCacheStatusMissing}
}

func (c *targetSearchCache) build(ctx context.Context, key string, conn connector.SourceConnector, req TargetTreeSearchRequest, previous targetSearchCacheSnapshot, build func(context.Context, connector.SourceConnector, TargetTreeSearchRequest) ([]TreeNode, bool, error)) targetSearchCacheSnapshot {
	if ctx == nil {
		ctx = context.Background()
	}
	buildCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	nodes, truncated, err := build(buildCtx, conn, req)
	snapshot := targetSearchCacheSnapshot{
		nodes:     append([]TreeNode(nil), nodes...),
		status:    targetSearchCacheStatusComplete,
		complete:  true,
		truncated: truncated,
	}
	if err != nil {
		if previous.complete {
			snapshot = previous
			snapshot.status = targetSearchCacheStatusComplete
			snapshot.complete = true
			snapshot.stale = true
			snapshot.lastError = err.Error()
		} else {
			snapshot.nodes = nil
			snapshot.status = targetSearchCacheStatusFailed
			snapshot.complete = false
			snapshot.truncated = false
			snapshot.lastError = err.Error()
		}
	}
	now := time.Now()
	staleTTL := c.staleTTL(req)
	if snapshot.staleAt.IsZero() || err == nil {
		snapshot.staleAt = now.Add(staleTTL)
	}
	snapshot.stale = now.After(snapshot.staleAt)
	c.mu.Lock()
	entry := c.entries[key]
	if entry == nil {
		entry = &targetSearchCacheEntry{}
		c.entries[key] = entry
	}
	entry.nodes = snapshot.nodes
	entry.truncated = snapshot.truncated
	entry.complete = snapshot.complete
	entry.building = false
	entry.lastError = snapshot.lastError
	entry.staleAt = snapshot.staleAt
	entry.expiresAt = now.Add(c.expireTTL)
	c.mu.Unlock()
	persistErr := ""
	if c.store != nil {
		setCtx, setCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer setCancel()
		if err := c.store.Set(setCtx, key, snapshot, staleTTL, c.expireTTL); err != nil {
			persistErr = err.Error()
		}
	}
	fmt.Fprintf(os.Stdout, "target search cache build status=%s connector=%s auth_connection_id=%s nodes=%d truncated=%t stale=%t error=%q persist_error=%q\n", snapshot.status, req.ConnectorType, req.AuthConnectionID, len(snapshot.nodes), snapshot.truncated, snapshot.stale, snapshot.lastError, persistErr)
	return snapshot
}

func (c *targetSearchCache) buildIfUnlocked(ctx context.Context, conn connector.SourceConnector, req TargetTreeSearchRequest, build func(context.Context, connector.SourceConnector, TargetTreeSearchRequest) ([]TreeNode, bool, error)) targetSearchCacheSnapshot {
	if c == nil {
		return targetSearchCacheSnapshot{status: targetSearchCacheStatusMissing}
	}
	key := targetSearchCacheKey(req)
	if c.store != nil {
		previous, hasPrevious, err := c.store.Get(ctx, key)
		if err == nil && hasPrevious && previous.complete && !previous.stale {
			fmt.Fprintf(os.Stdout, "target search cache prewarm skip connector=%s auth_connection_id=%s status=%s nodes=%d truncated=%t stale=%t\n", req.ConnectorType, req.AuthConnectionID, previous.status, len(previous.nodes), previous.truncated, previous.stale)
			return previous
		}
		locked, err := c.store.TryLock(ctx, key, c.lockTTL)
		if err != nil {
			return targetSearchCacheSnapshot{status: targetSearchCacheStatusFailed, lastError: err.Error()}
		}
		if !locked {
			if snapshot, ok, err := c.store.Get(ctx, key); err == nil && ok {
				fmt.Fprintf(os.Stdout, "target search cache prewarm locked connector=%s auth_connection_id=%s status=%s nodes=%d truncated=%t stale=%t error=%q\n", req.ConnectorType, req.AuthConnectionID, snapshot.status, len(snapshot.nodes), snapshot.truncated, snapshot.stale, snapshot.lastError)
				return snapshot
			}
			fmt.Fprintf(os.Stdout, "target search cache prewarm locked connector=%s auth_connection_id=%s status=%s\n", req.ConnectorType, req.AuthConnectionID, targetSearchCacheStatusBuilding)
			return targetSearchCacheSnapshot{status: targetSearchCacheStatusBuilding, building: true}
		}
		return c.build(ctx, key, conn, req, previous, build)
	}
	c.mu.Lock()
	entry := c.entries[key]
	if entry != nil && time.Now().Before(entry.expiresAt) && entry.complete && time.Now().Before(entry.staleAt) {
		snapshot := entry.snapshot()
		c.mu.Unlock()
		return snapshot
	}
	if entry != nil && entry.building {
		snapshot := entry.snapshot()
		c.mu.Unlock()
		return snapshot
	}
	previous := targetSearchCacheSnapshot{}
	if entry != nil && time.Now().Before(entry.expiresAt) {
		previous = entry.snapshot()
		entry.building = true
	} else {
		c.entries[key] = &targetSearchCacheEntry{building: true, expiresAt: time.Now().Add(c.lockTTL)}
	}
	c.mu.Unlock()
	return c.build(ctx, key, conn, req, previous, build)
}

func (c *targetSearchCache) staleTTL(req TargetTreeSearchRequest) time.Duration {
	if isLocalFSTargetSearch(req) {
		return targetSearchCacheLocalFSTTL
	}
	return c.ttl
}

func (e *targetSearchCacheEntry) snapshot() targetSearchCacheSnapshot {
	if e == nil {
		return targetSearchCacheSnapshot{}
	}
	nodes := append([]TreeNode(nil), e.nodes...)
	return targetSearchCacheSnapshot{
		nodes:     nodes,
		status:    entryStatus(e),
		building:  e.building,
		complete:  e.complete,
		truncated: e.truncated,
		lastError: e.lastError,
		stale:     !e.staleAt.IsZero() && time.Now().After(e.staleAt),
		staleAt:   e.staleAt,
	}
}

func entryStatus(e *targetSearchCacheEntry) string {
	if e == nil {
		return targetSearchCacheStatusMissing
	}
	if e.building {
		return targetSearchCacheStatusBuilding
	}
	if e.complete {
		return targetSearchCacheStatusComplete
	}
	if strings.TrimSpace(e.lastError) != "" {
		return targetSearchCacheStatusFailed
	}
	return targetSearchCacheStatusMissing
}

func targetSearchCacheKey(req TargetTreeSearchRequest) string {
	parts := []string{
		string(req.ConnectorType),
		req.AgentID,
		req.AuthConnectionID,
		stableTargetSearchCacheProviderOptions(req.ConnectorType, req.ProviderOptions),
	}
	if targetSearchHasCurrentLevel(req) {
		parts = append(parts, string(req.TargetType), req.TargetRef, req.NodeRef)
	}
	return strings.Join(parts, "\x00")
}

func stableTargetSearchCacheProviderOptions(connectorType connector.ConnectorType, options map[string]any) string {
	if connectorType != "feishu" {
		return stableProviderOptions(options)
	}
	if len(options) == 0 {
		return ""
	}
	filtered := make(map[string]any, len(options))
	for key, value := range options {
		switch key {
		case "tenant_key", "tenant_id", "user_id":
			continue
		default:
			filtered[key] = value
		}
	}
	return stableProviderOptions(filtered)
}

func targetSearchCacheStorageKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func stableProviderOptions(options map[string]any) string {
	if len(options) == 0 {
		return ""
	}
	keys := make([]string, 0, len(options))
	for key := range options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]any, len(options))
	for _, key := range keys {
		out[key] = options[key]
	}
	data, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(data)
}

func paginateCachedTargetNodes(nodes []TreeNode, keyword string, includeFiles bool, policy filefilter.Policy, pageSize int, cursor string) (TreeNodePage, error) {
	offset, err := cursorOffset(cursor)
	if err != nil {
		return TreeNodePage{}, err
	}
	matches := make([]TreeNode, 0, pageSize)
	seen := 0
	for _, node := range nodes {
		if !includeFiles && !node.IsContainer {
			continue
		}
		if !targetAllowsTreeNode(policy, node) {
			continue
		}
		if !treeNodeSearchMatches(node, keyword) {
			continue
		}
		seen++
		if seen <= offset {
			continue
		}
		matches = append(matches, node)
		if len(matches) > pageSize {
			return TreeNodePage{
				Items:      buildSearchPathTree(nodes, matches[:pageSize]),
				HasMore:    true,
				NextCursor: strconv.Itoa(offset + pageSize),
				SearchMode: SearchModeCache,
			}, nil
		}
	}
	return TreeNodePage{Items: buildSearchPathTree(nodes, matches), ListComplete: true, SearchMode: SearchModeCache}, nil
}

func treeNodeSearchMatches(node TreeNode, keyword string) bool {
	needle := strings.ToLower(strings.TrimSpace(keyword))
	if needle == "" {
		return true
	}
	for _, value := range []string{node.SearchName, node.DisplayName} {
		if strings.Contains(strings.ToLower(value), needle) {
			return true
		}
	}
	return false
}
