package access

import (
	"context"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestDefaultCheckerCanUseAgentAllowsSameTenantOnlineAgent(t *testing.T) {
	t.Parallel()

	checker := NewDefaultChecker(&checkerStore{
		agent: store.Agent{AgentID: "agent-1", TenantID: "tenant-1", Status: "ONLINE"},
	})

	if err := checker.CanUseAgent(context.Background(), Actor{UserID: "user-1", TenantID: "tenant-1"}, "agent-1"); err != nil {
		t.Fatalf("expected online same-tenant agent to be allowed, got %v", err)
	}
}

func TestDefaultCheckerCanUseAgentRejectsCrossTenantOrUnavailableAgent(t *testing.T) {
	t.Parallel()

	actor := Actor{UserID: "user-1", TenantID: "tenant-1"}
	crossTenant := NewDefaultChecker(&checkerStore{
		agent: store.Agent{AgentID: "agent-1", TenantID: "tenant-2", Status: "ONLINE"},
	})
	if err := crossTenant.CanUseAgent(context.Background(), actor, "agent-1"); ErrorCodeOf(err) != ErrCodeForbidden {
		t.Fatalf("expected cross-tenant agent forbidden, got %v", err)
	}

	offline := NewDefaultChecker(&checkerStore{
		agent: store.Agent{AgentID: "agent-1", TenantID: "tenant-1", Status: "OFFLINE"},
	})
	if err := offline.CanUseAgent(context.Background(), actor, "agent-1"); ErrorCodeOf(err) != ErrCodeForbidden {
		t.Fatalf("expected offline agent forbidden, got %v", err)
	}
}

func TestDefaultCheckerCanUseAuthConnectionUsesVerifier(t *testing.T) {
	t.Parallel()

	verifier := &authVerifierStub{}
	checker := NewDefaultChecker(&checkerStore{}, WithAuthConnectionVerifier(verifier))

	if err := checker.CanUseAuthConnection(context.Background(), Actor{UserID: "user-1", TenantID: "tenant-1"}, "auth-1"); err != nil {
		t.Fatalf("expected verifier success to allow auth connection, got %v", err)
	}
	if verifier.actor.UserID != "user-1" || verifier.actor.TenantID != "tenant-1" || verifier.authConnectionID != "auth-1" {
		t.Fatalf("verifier did not receive caller and auth connection: %+v auth=%q", verifier.actor, verifier.authConnectionID)
	}

	denied := connector.NewError("AUTH_CONNECTION_INVALID", "invalid")
	checker = NewDefaultChecker(&checkerStore{}, WithAuthConnectionVerifier(&authVerifierStub{err: denied}))
	if err := checker.CanUseAuthConnection(context.Background(), Actor{UserID: "user-1", TenantID: "tenant-1"}, "auth-1"); err == nil {
		t.Fatalf("expected verifier error to deny auth connection")
	}
}

func TestDefaultCheckerSourceActionsUsePermissionVerifier(t *testing.T) {
	t.Parallel()

	actor := Actor{UserID: "collab-1", TenantID: "tenant-1"}
	source := store.Source{SourceID: "source-1", TenantID: "tenant-1", CreatedBy: "owner-1"}
	verifier := &sourcePermissionVerifierStub{
		allowed: map[SourceAction]bool{
			SourceActionRead:  true,
			SourceActionWrite: true,
		},
	}
	checker := NewDefaultChecker(&checkerStore{source: source}, WithSourcePermissionVerifier(verifier))

	if err := checker.CanReadSource(context.Background(), actor, "source-1"); err != nil {
		t.Fatalf("expected read collaborator to be allowed, got %v", err)
	}
	if err := checker.CanWriteSource(context.Background(), actor, "source-1"); err != nil {
		t.Fatalf("expected write collaborator to be allowed, got %v", err)
	}
	if err := checker.CanDeleteSource(context.Background(), actor, "source-1"); ErrorCodeOf(err) != ErrCodeForbidden {
		t.Fatalf("expected delete without permission to be forbidden, got %v", err)
	}
	if got := verifier.actions; len(got) != 3 || got[0] != SourceActionRead || got[1] != SourceActionWrite || got[2] != SourceActionDelete {
		t.Fatalf("source actions were not checked separately: %+v", got)
	}
}

func TestDefaultCheckerDefaultOwnerPolicyRejectsNonOwner(t *testing.T) {
	t.Parallel()

	checker := NewDefaultChecker(&checkerStore{
		source: store.Source{SourceID: "source-1", TenantID: "tenant-1", CreatedBy: "owner-1"},
	})
	err := checker.CanReadSource(context.Background(), Actor{UserID: "user-2", TenantID: "tenant-1"}, "source-1")
	if ErrorCodeOf(err) != ErrCodeForbidden {
		t.Fatalf("expected non-owner forbidden by fallback owner policy, got %v", err)
	}
}

type checkerStore struct {
	source  store.Source
	binding store.Binding
	task    store.ParseTaskWithRefs
	agent   store.Agent
}

func (s *checkerStore) GetSource(context.Context, string) (store.Source, error) {
	if s.source.SourceID == "" {
		return store.Source{}, store.NewStoreError(store.ErrCodeSourceNotFound, "source not found")
	}
	return s.source, nil
}

func (s *checkerStore) GetBinding(context.Context, string, string) (store.Binding, error) {
	if s.binding.BindingID == "" {
		return store.Binding{}, store.NewStoreError(store.ErrCodeBindingNotFound, "binding not found")
	}
	return s.binding, nil
}

func (s *checkerStore) GetParseTask(context.Context, string) (store.ParseTaskWithRefs, error) {
	if s.task.Task.TaskID == "" {
		return store.ParseTaskWithRefs{}, store.NewStoreError(store.ErrCodeTaskNotFound, "task not found")
	}
	return s.task, nil
}

func (s *checkerStore) ListSourceAccess(context.Context, string) ([]store.Source, error) {
	if s.source.SourceID == "" {
		return nil, nil
	}
	return []store.Source{s.source}, nil
}

func (s *checkerStore) GetAgent(context.Context, string) (store.Agent, error) {
	if s.agent.AgentID == "" {
		return store.Agent{}, store.NewStoreError(store.ErrCodeAgentNotFound, "agent not found")
	}
	if s.agent.UpdatedAt.IsZero() {
		s.agent.UpdatedAt = time.Now()
	}
	return s.agent, nil
}

type authVerifierStub struct {
	actor            Actor
	authConnectionID string
	err              error
}

func (v *authVerifierStub) VerifyAuthConnection(_ context.Context, actor Actor, authConnectionID string) error {
	v.actor = actor
	v.authConnectionID = authConnectionID
	return v.err
}

type sourcePermissionVerifierStub struct {
	allowed map[SourceAction]bool
	actions []SourceAction
}

func (v *sourcePermissionVerifierStub) CanCreateSource(context.Context, Actor) error {
	return nil
}

func (v *sourcePermissionVerifierStub) CanAccessSource(_ context.Context, _ Actor, _ store.Source, action SourceAction) error {
	v.actions = append(v.actions, action)
	if v.allowed[action] {
		return nil
	}
	return forbidden("access denied")
}
