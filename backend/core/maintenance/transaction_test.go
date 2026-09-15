package maintenance

import (
	"net/http/httptest"
	"testing"
)

func TestRequestIdentityPrefersHeaderAndFallsBackOnlyForTaskID(t *testing.T) {
	for _, test := range []struct {
		name, taskHeader, runHeader, wantTask, wantRun string
	}{
		{"header wins", "header-task", "header-run", "header-task", "header-run"},
		{"query task fallback", "", "header-run", "query-task", "header-run"},
		{"run must come from header", "", "", "query-task", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("PUT", "/?task_id=query-task&run_id=query-run", nil)
			r.Header.Set("X-LazyMind-Task-Id", test.taskHeader)
			r.Header.Set("X-LazyMind-Run-Id", test.runHeader)
			identity := Execution(RequestContext(r))
			if identity.TaskID != test.wantTask || identity.RunID != test.wantRun {
				t.Fatalf("identity = %#v", identity)
			}
		})
	}
}
