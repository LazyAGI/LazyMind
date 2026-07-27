package runtime

import (
	"context"
	"reflect"
	"testing"

	"lazymind/core/compat/contract"
	"lazymind/core/compat/skill"
)

type stubSkillPort struct{}

func (stubSkillPort) List(context.Context, contract.CallContext, skill.ListInput) (skill.ListResult, error) {
	return skill.ListResult{}, nil
}

func (stubSkillPort) GetMetadata(context.Context, contract.CallContext, string) (skill.Summary, error) {
	return skill.Summary{}, nil
}

func (stubSkillPort) ReadContent(context.Context, contract.CallContext, string, string) (skill.Content, error) {
	return skill.Content{}, nil
}

func TestNewCreatesSkillFacadeWhenPortProvided(t *testing.T) {
	rt, err := New(Dependencies{SkillPort: stubSkillPort{}})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if rt.Skill == nil {
		t.Fatalf("Skill facade is nil")
	}
}

func TestNewAllowsNilSkillPort(t *testing.T) {
	rt, err := New(Dependencies{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if rt == nil {
		t.Fatalf("Runtime is nil")
	}
	if rt.Skill != nil {
		t.Fatalf("Skill facade = %#v, want nil", rt.Skill)
	}
}

func TestRuntimeDoesNotContainRequestState(t *testing.T) {
	typ := reflect.TypeOf(Runtime{})
	for _, name := range []string{"UserID", "UserName", "RequestID", "PageRequest"} {
		if _, ok := typ.FieldByName(name); ok {
			t.Fatalf("Runtime contains request field %s", name)
		}
	}
	if typ.NumField() != 1 {
		t.Fatalf("Runtime field count = %d, want 1", typ.NumField())
	}
}
