package remotefs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"lazymind/core/common/orm"
)

func TestMemoryMountLazyInitializesDefaultsPerUser(t *testing.T) {
	db := newRemoteFSTestDB(t)
	handler := NewHandler(db.DB)

	listReq := httptest.NewRequest(http.MethodGet, "/remote-fs/list?path=memory&user_id=u1", nil)
	listRec := httptest.NewRecorder()
	handler.List(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var listBody struct {
		Items []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listBody.Items) != 2 ||
		listBody.Items[0].Path != memoryAgentsPath ||
		listBody.Items[1].Path != memoryUsersPath {
		t.Fatalf("unexpected memory root items: %+v", listBody.Items)
	}
	if strings.Contains(defaultSoulYAML, "schema_version") ||
		strings.Contains(defaultProfileYAML, "schema_version") {
		t.Fatal("fixed Memory defaults must not contain schema_version")
	}

	for entryPath, expected := range map[string]string{
		memorySoulPath:       defaultSoulYAML,
		memoryProfilePath:    defaultProfileYAML,
		memoryPreferencePath: defaultPreferenceYAML,
	} {
		req := httptest.NewRequest(http.MethodGet, "/remote-fs/content?path="+entryPath+"&user_id=u1", nil)
		rec := httptest.NewRecorder()
		handler.Content(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != expected {
			t.Fatalf("read %s status=%d body=%q", entryPath, rec.Code, rec.Body.String())
		}
	}

	referencesReq := httptest.NewRequest(
		http.MethodGet,
		"/remote-fs/info?path="+memoryReferencesPath+"&user_id=u1",
		nil,
	)
	referencesRec := httptest.NewRecorder()
	handler.Info(referencesRec, referencesReq)
	if referencesRec.Code != http.StatusOK ||
		!strings.Contains(referencesRec.Body.String(), `"type":"dir"`) {
		t.Fatalf(
			"references directory status=%d body=%s",
			referencesRec.Code,
			referencesRec.Body.String(),
		)
	}

	writeReq := httptest.NewRequest(http.MethodPut, "/remote-fs/content?path="+memoryProfilePath+"&user_id=u1", strings.NewReader("custom: true\n"))
	writeRec := httptest.NewRecorder()
	handler.Content(writeRec, writeReq)
	if writeRec.Code != http.StatusOK {
		t.Fatalf("write u1 profile status=%d body=%s", writeRec.Code, writeRec.Body.String())
	}

	otherReq := httptest.NewRequest(http.MethodGet, "/remote-fs/content?path="+memoryProfilePath+"&user_id=u2", nil)
	otherRec := httptest.NewRecorder()
	handler.Content(otherRec, otherReq)
	if otherRec.Code != http.StatusOK || otherRec.Body.String() != defaultProfileYAML {
		t.Fatalf("u2 default profile status=%d body=%q", otherRec.Code, otherRec.Body.String())
	}

	readUpdatedReq := httptest.NewRequest(
		http.MethodGet,
		"/remote-fs/content?path="+memoryProfilePath+"&user_id=u1",
		nil,
	)
	readUpdatedRec := httptest.NewRecorder()
	handler.Content(readUpdatedRec, readUpdatedReq)
	if readUpdatedRec.Code != http.StatusOK || readUpdatedRec.Body.String() != "custom: true\n" {
		t.Fatalf(
			"repeated initialization replaced u1 profile: status=%d body=%q",
			readUpdatedRec.Code,
			readUpdatedRec.Body.String(),
		)
	}
}

func TestMemoryMountDoesNotRecreateDeletedFixedFiles(t *testing.T) {
	db := newRemoteFSTestDB(t)
	handler := NewHandler(db.DB)

	initializeReq := httptest.NewRequest(
		http.MethodGet,
		"/remote-fs/content?path="+memoryProfilePath+"&user_id=u1",
		nil,
	)
	initializeRec := httptest.NewRecorder()
	handler.Content(initializeRec, initializeReq)
	if initializeRec.Code != http.StatusOK {
		t.Fatalf(
			"initialize profile status=%d body=%s",
			initializeRec.Code,
			initializeRec.Body.String(),
		)
	}

	deleteReq := httptest.NewRequest(
		http.MethodDelete,
		"/remote-fs/path?path="+memoryProfilePath+"&user_id=u1",
		nil,
	)
	deleteRec := httptest.NewRecorder()
	handler.Delete(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf(
			"delete fixed file status=%d body=%s",
			deleteRec.Code,
			deleteRec.Body.String(),
		)
	}

	readReq := httptest.NewRequest(
		http.MethodGet,
		"/remote-fs/content?path="+memoryProfilePath+"&user_id=u1",
		nil,
	)
	readRec := httptest.NewRecorder()
	handler.Content(readRec, readReq)
	if readRec.Code != http.StatusNotFound {
		t.Fatalf(
			"deleted fixed file was recreated: status=%d body=%s",
			readRec.Code,
			readRec.Body.String(),
		)
	}
}

func TestMemoryMountConcurrentFirstAccessIsIdempotent(t *testing.T) {
	db := newRemoteFSTestDB(t)
	service := newMemoryCurrentService(db.DB)

	const workers = 8
	start := make(chan struct{})
	errorsByWorker := make([]error, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(index int) {
			defer group.Done()
			<-start
			errorsByWorker[index] = service.ensureInitialized(
				t.Context(),
				"concurrent-user",
			)
		}(worker)
	}
	close(start)
	group.Wait()
	for worker, err := range errorsByWorker {
		if err != nil {
			t.Fatalf("worker %d initialize failed: %v", worker, err)
		}
	}

	var count int64
	if err := db.Model(&orm.MemoryCurrentEntry{}).
		Where("user_id = ?", "concurrent-user").
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != int64(len(defaultMemoryCurrentEntries("concurrent-user", service.clock()))) {
		t.Fatalf("initialized entry count=%d", count)
	}
}

func TestMemoryMountCurrentStateFileOperations(t *testing.T) {
	db := newRemoteFSTestDB(t)
	handler := NewHandler(db.DB)

	dirBody := `{"path":"memory/users/references","recursive":true}`
	dirReq := httptest.NewRequest(http.MethodPost, "/remote-fs/dir?user_id=u1", strings.NewReader(dirBody))
	dirRec := httptest.NewRecorder()
	handler.Dir(dirRec, dirReq)
	if dirRec.Code != http.StatusOK {
		t.Fatalf("mkdir status=%d body=%s", dirRec.Code, dirRec.Body.String())
	}

	content := []byte("# Coding\nPrefer tests.\n")
	writeReq := httptest.NewRequest(
		http.MethodPut,
		"/remote-fs/content?path=memory/users/references/coding.md&user_id=u1",
		bytes.NewReader(content),
	)
	writeReq.Header.Set("Content-Type", "text/markdown; charset=utf-8")
	writeRec := httptest.NewRecorder()
	handler.Content(writeRec, writeReq)
	if writeRec.Code != http.StatusOK {
		t.Fatalf("write status=%d body=%s", writeRec.Code, writeRec.Body.String())
	}

	existsReq := httptest.NewRequest(http.MethodGet, "/remote-fs/exists?path=memory/users/references/coding.md&user_id=u1", nil)
	existsRec := httptest.NewRecorder()
	handler.Exists(existsRec, existsReq)
	if existsRec.Code != http.StatusOK || !strings.Contains(existsRec.Body.String(), `"exists":true`) {
		t.Fatalf("exists status=%d body=%s", existsRec.Code, existsRec.Body.String())
	}

	copyBody := `{"from":"memory/users/references/coding.md","to":"memory/users/references/coding-copy.md"}`
	copyReq := httptest.NewRequest(http.MethodPost, "/remote-fs/copy?user_id=u1", strings.NewReader(copyBody))
	copyRec := httptest.NewRecorder()
	handler.Copy(copyRec, copyReq)
	if copyRec.Code != http.StatusOK {
		t.Fatalf("copy status=%d body=%s", copyRec.Code, copyRec.Body.String())
	}

	moveBody := `{"from":"memory/users/references/coding-copy.md","to":"memory/users/references/coding-moved.md"}`
	moveReq := httptest.NewRequest(http.MethodPost, "/remote-fs/move?user_id=u1", strings.NewReader(moveBody))
	moveRec := httptest.NewRecorder()
	handler.Move(moveRec, moveReq)
	if moveRec.Code != http.StatusOK {
		t.Fatalf("move status=%d body=%s", moveRec.Code, moveRec.Body.String())
	}

	readReq := httptest.NewRequest(http.MethodGet, "/remote-fs/content?path=memory/users/references/coding-moved.md&user_id=u1", nil)
	readRec := httptest.NewRecorder()
	handler.Content(readRec, readReq)
	if readRec.Code != http.StatusOK || !bytes.Equal(readRec.Body.Bytes(), content) {
		t.Fatalf("read moved status=%d body=%q", readRec.Code, readRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/remote-fs/path?path=memory/users/references/coding-moved.md&user_id=u1", nil)
	deleteRec := httptest.NewRecorder()
	handler.Delete(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	missingReq := httptest.NewRequest(http.MethodGet, "/remote-fs/content?path=memory/users/references/coding-moved.md&user_id=u1", nil)
	missingRec := httptest.NewRecorder()
	handler.Content(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("deleted path status=%d body=%s", missingRec.Code, missingRec.Body.String())
	}
}

func TestMemoryMountDirectoryCopyMoveAndRecursiveDelete(t *testing.T) {
	db := newRemoteFSTestDB(t)
	handler := NewHandler(db.DB)

	mkdirReq := httptest.NewRequest(
		http.MethodPost,
		"/remote-fs/dir?user_id=u1",
		strings.NewReader(`{"path":"memory/work/source/nested","recursive":true}`),
	)
	mkdirRec := httptest.NewRecorder()
	handler.Dir(mkdirRec, mkdirReq)
	if mkdirRec.Code != http.StatusOK {
		t.Fatalf("mkdir nested status=%d body=%s", mkdirRec.Code, mkdirRec.Body.String())
	}

	for entryPath, content := range map[string]string{
		"memory/work/source/root.txt":         "root",
		"memory/work/source/nested/child.txt": "child",
	} {
		req := httptest.NewRequest(
			http.MethodPut,
			"/remote-fs/content?path="+entryPath+"&user_id=u1",
			strings.NewReader(content),
		)
		rec := httptest.NewRecorder()
		handler.Content(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("write %s status=%d body=%s", entryPath, rec.Code, rec.Body.String())
		}
	}

	copyReq := httptest.NewRequest(
		http.MethodPost,
		"/remote-fs/copy?user_id=u1",
		strings.NewReader(`{"from":"memory/work/source","to":"memory/archive/copied"}`),
	)
	copyRec := httptest.NewRecorder()
	handler.Copy(copyRec, copyReq)
	if copyRec.Code != http.StatusOK {
		t.Fatalf("copy directory status=%d body=%s", copyRec.Code, copyRec.Body.String())
	}

	moveReq := httptest.NewRequest(
		http.MethodPost,
		"/remote-fs/move?user_id=u1",
		strings.NewReader(`{"from":"memory/archive/copied","to":"memory/archive/moved"}`),
	)
	moveRec := httptest.NewRecorder()
	handler.Move(moveRec, moveReq)
	if moveRec.Code != http.StatusOK {
		t.Fatalf("move directory status=%d body=%s", moveRec.Code, moveRec.Body.String())
	}

	oldExistsReq := httptest.NewRequest(
		http.MethodGet,
		"/remote-fs/exists?path=memory/archive/copied&user_id=u1",
		nil,
	)
	oldExistsRec := httptest.NewRecorder()
	handler.Exists(oldExistsRec, oldExistsReq)
	if oldExistsRec.Code != http.StatusOK ||
		!strings.Contains(oldExistsRec.Body.String(), `"exists":false`) {
		t.Fatalf(
			"moved source exists status=%d body=%s",
			oldExistsRec.Code,
			oldExistsRec.Body.String(),
		)
	}

	readReq := httptest.NewRequest(
		http.MethodGet,
		"/remote-fs/content?path=memory/archive/moved/nested/child.txt&user_id=u1",
		nil,
	)
	readRec := httptest.NewRecorder()
	handler.Content(readRec, readReq)
	if readRec.Code != http.StatusOK || readRec.Body.String() != "child" {
		t.Fatalf("read copied child status=%d body=%q", readRec.Code, readRec.Body.String())
	}

	nonRecursiveReq := httptest.NewRequest(
		http.MethodDelete,
		"/remote-fs/path?path=memory/archive/moved&user_id=u1",
		nil,
	)
	nonRecursiveRec := httptest.NewRecorder()
	handler.Delete(nonRecursiveRec, nonRecursiveReq)
	if nonRecursiveRec.Code != http.StatusConflict {
		t.Fatalf(
			"non-recursive delete status=%d body=%s",
			nonRecursiveRec.Code,
			nonRecursiveRec.Body.String(),
		)
	}

	recursiveReq := httptest.NewRequest(
		http.MethodDelete,
		"/remote-fs/path?path=memory/archive/moved&recursive=true&user_id=u1",
		nil,
	)
	recursiveRec := httptest.NewRecorder()
	handler.Delete(recursiveRec, recursiveReq)
	if recursiveRec.Code != http.StatusOK {
		t.Fatalf(
			"recursive delete status=%d body=%s",
			recursiveRec.Code,
			recursiveRec.Body.String(),
		)
	}
}

func TestMemoryMountRejectsTraversalWithoutCreatingEntries(t *testing.T) {
	db := newRemoteFSTestDB(t)
	handler := NewHandler(db.DB)

	tests := []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
		req  *http.Request
	}{
		{
			name: "content",
			call: handler.Content,
			req: httptest.NewRequest(
				http.MethodPut,
				"/remote-fs/content?path=memory/users/../agents/escaped.yaml&user_id=u1",
				strings.NewReader("escaped"),
			),
		},
		{
			name: "directory",
			call: handler.Dir,
			req: httptest.NewRequest(
				http.MethodPost,
				"/remote-fs/dir?user_id=u1",
				strings.NewReader(`{"path":"memory/users/../../skills/escaped","recursive":true}`),
			),
		},
		{
			name: "copy",
			call: handler.Copy,
			req: httptest.NewRequest(
				http.MethodPost,
				"/remote-fs/copy?user_id=u1",
				strings.NewReader(
					`{"from":"memory/users/../agents/soul.yaml","to":"memory/escaped.yaml"}`,
				),
			),
		},
		{
			name: "move",
			call: handler.Move,
			req: httptest.NewRequest(
				http.MethodPost,
				"/remote-fs/move?user_id=u1",
				strings.NewReader(
					`{"from":"memory/users/profile.yaml","to":"memory/../skills/escaped.yaml"}`,
				),
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			test.call(rec, test.req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	var count int64
	if err := db.Model(&orm.MemoryCurrentEntry{}).
		Where("path LIKE ? OR path LIKE ?", `%..%`, `%escaped%`).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("traversal created %d unexpected entries", count)
	}
}

func TestMemoryMountScopesCustomFilesAndMutationsByUser(t *testing.T) {
	db := newRemoteFSTestDB(t)
	handler := NewHandler(db.DB)

	writeReq := httptest.NewRequest(
		http.MethodPut,
		"/remote-fs/content?path=memory/users/references/private.md&user_id=u1",
		strings.NewReader("u1-only"),
	)
	writeRec := httptest.NewRecorder()
	handler.Content(writeRec, writeReq)
	if writeRec.Code != http.StatusOK {
		t.Fatalf("write u1 reference status=%d body=%s", writeRec.Code, writeRec.Body.String())
	}

	for _, operation := range []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
		req  *http.Request
	}{
		{
			name: "read",
			call: handler.Content,
			req: httptest.NewRequest(
				http.MethodGet,
				"/remote-fs/content?path=memory/users/references/private.md&user_id=u2",
				nil,
			),
		},
		{
			name: "delete",
			call: handler.Delete,
			req: httptest.NewRequest(
				http.MethodDelete,
				"/remote-fs/path?path=memory/users/references/private.md&user_id=u2",
				nil,
			),
		},
		{
			name: "copy",
			call: handler.Copy,
			req: httptest.NewRequest(
				http.MethodPost,
				"/remote-fs/copy?user_id=u2",
				strings.NewReader(
					`{"from":"memory/users/references/private.md","to":"memory/users/references/copied.md"}`,
				),
			),
		},
	} {
		t.Run(operation.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			operation.call(rec, operation.req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	readReq := httptest.NewRequest(
		http.MethodGet,
		"/remote-fs/content?path=memory/users/references/private.md&user_id=u1",
		nil,
	)
	readRec := httptest.NewRecorder()
	handler.Content(readRec, readReq)
	if readRec.Code != http.StatusOK || readRec.Body.String() != "u1-only" {
		t.Fatalf("u2 mutation affected u1 status=%d body=%q", readRec.Code, readRec.Body.String())
	}
}

func TestMemoryMountProtectsRootAndRejectsCrossMountMoves(t *testing.T) {
	db := newRemoteFSTestDB(t)
	handler := NewHandler(db.DB)

	initReq := httptest.NewRequest(http.MethodGet, "/remote-fs/list?path=memory&user_id=u1", nil)
	initRec := httptest.NewRecorder()
	handler.List(initRec, initReq)
	if initRec.Code != http.StatusOK {
		t.Fatalf("initialize memory status=%d body=%s", initRec.Code, initRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/remote-fs/path?path=memory&recursive=true&user_id=u1", nil)
	deleteRec := httptest.NewRecorder()
	handler.Delete(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusBadRequest {
		t.Fatalf("delete root status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	moveRootReq := httptest.NewRequest(
		http.MethodPost,
		"/remote-fs/move?user_id=u1",
		strings.NewReader(`{"from":"memory","to":"memory/backup"}`),
	)
	moveRootRec := httptest.NewRecorder()
	handler.Move(moveRootRec, moveRootReq)
	if moveRootRec.Code != http.StatusBadRequest {
		t.Fatalf("move root status=%d body=%s", moveRootRec.Code, moveRootRec.Body.String())
	}

	for _, direction := range []struct {
		name string
		from string
		to   string
	}{
		{
			name: "memory-to-skill",
			from: "memory/users/profile.yaml",
			to:   "skills/research/demo/profile.yaml",
		},
		{
			name: "skill-to-memory",
			from: "skills/research/demo/profile.yaml",
			to:   "memory/users/profile.yaml",
		},
	} {
		for _, operation := range []struct {
			name string
			call func(http.ResponseWriter, *http.Request)
		}{
			{name: "copy", call: handler.Copy},
			{name: "move", call: handler.Move},
		} {
			req := httptest.NewRequest(
				http.MethodPost,
				"/remote-fs/"+operation.name+"?user_id=u1",
				strings.NewReader(
					`{"from":"`+direction.from+`","to":"`+direction.to+`"}`,
				),
			)
			rec := httptest.NewRecorder()
			operation.call(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf(
					"%s %s status=%d body=%s",
					operation.name,
					direction.name,
					rec.Code,
					rec.Body.String(),
				)
			}
		}
	}

	var count int64
	if err := db.Model(&orm.MemoryCurrentEntry{}).Where("user_id = ?", "u1").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("expected protected memory root and defaults to remain")
	}
}
