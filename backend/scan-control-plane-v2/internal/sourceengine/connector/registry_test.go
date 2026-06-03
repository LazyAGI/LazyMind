package connector

import (
	"context"
	"testing"
)

type testConnector struct {
	spec ConnectorSpec
}

func (c testConnector) Spec() ConnectorSpec {
	return c.spec
}

func (c testConnector) ValidateTarget(context.Context, ValidateTargetRequest) (NormalizedTarget, error) {
	return NormalizedTarget{}, nil
}

func (c testConnector) ListChildren(context.Context, ListChildrenRequest) (RawObjectPage, error) {
	return RawObjectPage{}, nil
}

func (c testConnector) Search(context.Context, SearchRequest) (RawObjectPage, error) {
	return RawObjectPage{}, nil
}

func (c testConnector) FetchPage(context.Context, FetchPageRequest) (RawObjectPage, error) {
	return RawObjectPage{}, nil
}

func (c testConnector) ExportObject(context.Context, ExportObjectRequest) (ExportedObject, error) {
	return ExportedObject{}, nil
}

func (c testConnector) MapObject(context.Context, RawObject) (NormalizedSourceObject, error) {
	return NormalizedSourceObject{}, nil
}

func TestDefaultConnectorRegistry(t *testing.T) {
	t.Parallel()

	registry, err := NewDefaultConnectorRegistry(&testConnector{spec: testConnectorSpec("test")})
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	if specs := registry.Specs(); len(specs) != 1 || specs[0].ConnectorType != "test" {
		t.Fatalf("unexpected specs: %+v", specs)
	}

	registered, err := registry.Get("test")
	if err != nil {
		t.Fatalf("get connector: %v", err)
	}
	if registered.Spec().ConnectorType != "test" {
		t.Fatalf("unexpected connector: %+v", registered.Spec())
	}

	err = registry.Register(&testConnector{spec: testConnectorSpec("test")})
	assertErrorCode(t, err, ErrorCodeAlreadyExists)
	_, err = registry.Get("missing")
	assertErrorCode(t, err, ErrorCodeNotFound)
}

func TestConfigSpecValidate(t *testing.T) {
	t.Parallel()

	spec := ConnectorSpec{
		ConnectorType:           "test_connector",
		TargetTypes:             []TargetType{"test_root"},
		MaxPageSize:             100,
		RequiresAgentID:         true,
		RequiredProviderOptions: []string{"fixture"},
	}
	req := ValidateTargetRequest{
		ConnectorType:   "test_connector",
		TargetType:      "test_root",
		TargetRef:       "test://root",
		AgentID:         "agent-1",
		ProviderOptions: ProviderOptions{"fixture": "default"},
		UserID:          "user-1",
	}
	if err := spec.ConfigSpec().Validate(req); err != nil {
		t.Fatalf("validate config: %v", err)
	}

	req.AgentID = ""
	assertErrorCode(t, spec.ConfigSpec().Validate(req), ErrorCodeInvalidArgument)

	req.AgentID = "agent-1"
	req.ProviderOptions = nil
	assertErrorCode(t, spec.ConfigSpec().Validate(req), ErrorCodeInvalidArgument)

	req.ProviderOptions = ProviderOptions{"fixture": "default"}
	req.TargetType = "other"
	assertErrorCode(t, spec.ConfigSpec().Validate(req), ErrorCodeInvalidTarget)
}

func testConnectorSpec(connectorType ConnectorType) ConnectorSpec {
	return ConnectorSpec{
		ConnectorType: connectorType,
		DisplayName:   string(connectorType),
		TargetTypes:   []TargetType{"test_root"},
		MaxPageSize:   100,
	}
}

func assertErrorCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s, got nil", code)
	}
	got, ok := ErrorCodeOf(err)
	if !ok || got != code {
		t.Fatalf("expected error code %s, got %v (ok=%v, err=%v)", code, got, ok, err)
	}
}
