// Package workbuddyhost owns the local CDP connector lifecycle and invocation
// identity rendezvous. It never decides Workflow execution eligibility.
package workbuddyhost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sync"
	"time"
)

type Identity struct {
	SessionID  string `json:"sessionId"`
	RequestID  string `json:"requestId"`
	ToolCallID string `json:"toolCallId"`
}
type IdentityRequest struct {
	ID        string          `json:"id"`
	Arguments json.RawMessage `json:"arguments"`
	connector string
	done      chan Identity
}
type Broker struct {
	mu      sync.Mutex
	pending map[string]*IdentityRequest
}

var uuid = regexp.MustCompile(`(?i)^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func (b *Broker) Resolve(ctx context.Context, connector string, args json.RawMessage) (Identity, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(args, &fields) != nil || len(fields) == 0 {
		return Identity{}, errors.New("workflow.start arguments required")
	}
	var key string
	if json.Unmarshal(fields["idempotency_key"], &key) != nil || len(key) < 16 || len(key) > 256 {
		return Identity{}, errors.New("WorkBuddy workflow.start requires a stable idempotency_key of 16–256 characters")
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return Identity{}, err
	}
	r := &IdentityRequest{ID: hex.EncodeToString(secret), Arguments: append(json.RawMessage(nil), args...), connector: connector, done: make(chan Identity, 1)}
	b.mu.Lock()
	if b.pending == nil {
		b.pending = make(map[string]*IdentityRequest)
	}
	if len(b.pending) >= 16 {
		b.mu.Unlock()
		return Identity{}, errors.New("too many pending identity requests")
	}
	b.pending[r.ID] = r
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.pending, r.ID); b.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	select {
	case identity := <-r.done:
		return identity, nil
	case <-ctx.Done():
		return Identity{}, errors.New("WorkBuddy native start identity unavailable; check CDP connector and retry with the same idempotency_key")
	}
}
func (b *Broker) Pending(connector string) []IdentityRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := []IdentityRequest{}
	for _, r := range b.pending {
		if r.connector == connector {
			result = append(result, *r)
		}
	}
	return result
}
func (b *Broker) Accept(connector, id string, identity Identity) error {
	if !uuid.MatchString(identity.SessionID) || identity.RequestID == "" || identity.ToolCallID == "" {
		return errors.New("incomplete native invocation evidence")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.pending[id]
	if r == nil || r.connector != connector {
		return errors.New("identity request expired or belongs to another connector")
	}
	select {
	case r.done <- identity:
	default:
		return errors.New("identity already resolved")
	}
	return nil
}
