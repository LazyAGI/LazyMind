package connector_test

import (
	"testing"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector/feishu"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector/localfs"
)

func TestConnectorsCanRegisterWithoutAppWiring(t *testing.T) {
	t.Parallel()

	registry, err := connector.NewDefaultConnectorRegistry(
		localfs.NewLocalFSConnector(nil),
		feishu.NewFeishuConnector(nil, nil),
	)
	if err != nil {
		t.Fatalf("register connectors: %v", err)
	}

	specs := registry.Specs()
	if len(specs) != 2 {
		t.Fatalf("expected two registered connectors, got %+v", specs)
	}
	if specs[0].ConnectorType != feishu.ConnectorType || specs[1].ConnectorType != localfs.ConnectorType {
		t.Fatalf("unexpected registered connectors: %+v", specs)
	}
}
