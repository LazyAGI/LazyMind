package localworkspace

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/store"
)

func TestLocalOperationRealPythonRoundTrip(t *testing.T) {
	python := os.Getenv("WORKSPACE_TEST_PYTHON")
	if python == "" {
		t.Skip("set WORKSPACE_TEST_PYTHON and PYTHONPATH to run the actual Python/Core integration")
	}
	db, grant, states, conversation := operationFixture(t, PermissionAllowAll)
	store.Init(db.DB, nil, states)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "synthetic-local-operation-test-token")
	router := mux.NewRouter()
	base := "/internal/conversations/{conversation_id}/workspace-operations"
	router.HandleFunc(base+":prepare", InternalPrepareOperation).Methods("POST")
	router.HandleFunc(base+"/{operation_id}", InternalOperationStatus).Methods("GET")
	router.HandleFunc(base+"/{operation_id}:claim", InternalClaimLocalOperation).Methods("POST")
	router.HandleFunc(base+"/{operation_id}:complete", InternalCompleteLocalOperation).Methods("POST")
	router.HandleFunc("/conversations/{conversation_id}:workspace-approvals", ListOperationApprovals).Methods("GET")
	router.HandleFunc("/conversations/{conversation_id}/workspace-approvals/{operation_id}:decide", DecideOperationHandler).Methods("POST")
	server := httptest.NewServer(router)
	defer server.Close()
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside = filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(outside, []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]string{"base_url": server.URL, "root": grant.Path, "outside": outside,
		"workspace_id": grant.WorkspaceID, "conversation_id": conversation})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, filepath.Join(root, "tests/algorithm/chat/workspace_core_roundtrip.py"))
	command.Dir = root
	command.Env = append(os.Environ(), "WORKSPACE_TEST_CONTEXT="+string(data))
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "CORE_LOCAL_IO_ROUNDTRIP_OK") {
		t.Fatalf("real local IO integration failed: %v\n%s", err, output)
	}
}
