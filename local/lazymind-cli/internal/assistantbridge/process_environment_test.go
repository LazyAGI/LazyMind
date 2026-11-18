package assistantbridge

import (
	"strings"
	"testing"
)

func TestDetachedServiceDoesNotInheritForegroundLauncherJob(t *testing.T) {
	t.Setenv("LAZYMIND_CONNECTOR_LAUNCHER_JOB", "1")
	t.Setenv("LAZYMIND_CONNECTOR_CONFIG", "")
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	t.Setenv("LAZYMIND_KEEP_TEST_VALUE", "kept")
	kept := false
	for _, value := range detachedServiceEnvironment(t.TempDir()) {
		if strings.HasPrefix(value, "LAZYMIND_CONNECTOR_LAUNCHER_JOB=") {
			t.Fatal("background service inherited foreground job ownership")
		}
		kept = kept || value == "LAZYMIND_KEEP_TEST_VALUE=kept"
	}
	if !kept {
		t.Fatal("unrelated service environment was lost")
	}
}
