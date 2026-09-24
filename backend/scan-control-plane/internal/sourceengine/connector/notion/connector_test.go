package notion

import (
	"context"
	"testing"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func TestSearchRejectsParentScopeBeforeLoadingCredentials(t *testing.T) {
	client := NewNotionConnector(nil, nil)
	for _, request := range []connector.SearchRequest{
		{Keyword: "plan", NodeRef: "parent"},
		{Keyword: "plan", TargetRef: "parent"},
		{Keyword: "plan", TargetType: TargetTypePage},
	} {
		if _, err := client.Search(context.Background(), request); err == nil {
			t.Fatal("scoped search silently widened to workspace")
		}
	}
}
