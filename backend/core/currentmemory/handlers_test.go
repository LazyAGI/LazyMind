package currentmemory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
)

func TestSoulPublicHandlersAreUserScopedAndPatchFields(t *testing.T) {
	db := newCurrentMemoryTestDB(t)
	handler := NewHandler(db.DB)

	unauthenticated := httptest.NewRequest(http.MethodGet, "/memory/soul", nil)
	unauthenticatedRecorder := httptest.NewRecorder()
	handler.GetSoul(unauthenticatedRecorder, unauthenticated)
	if unauthenticatedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf(
			"unauthenticated status=%d body=%s",
			unauthenticatedRecorder.Code,
			unauthenticatedRecorder.Body.String(),
		)
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/memory/soul", nil)
	getRequest.Header.Set("X-User-Id", "user-1")
	getRecorder := httptest.NewRecorder()
	handler.GetSoul(getRecorder, getRequest)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
	var initial struct {
		Code int `json:"code"`
		Data struct {
			Document  SoulDocument `json:"document"`
			UpdatedAt int64        `json:"updated_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if initial.Code != 0 ||
		initial.Data.Document.Identity.Name != "LazyMind" ||
		initial.Data.UpdatedAt == 0 {
		t.Fatalf("unexpected get response: %#v", initial)
	}
	if bytes.Contains(getRecorder.Body.Bytes(), []byte(`"etag"`)) {
		t.Fatalf("Soul response must not expose etag: %s", getRecorder.Body.String())
	}

	patchRequest := httptest.NewRequest(
		http.MethodPatch,
		"/memory/soul",
		bytes.NewBufferString(`{"identity":{"name":"小懒"}}`),
	)
	patchRequest.Header.Set("X-User-Id", "user-1")
	patchRecorder := httptest.NewRecorder()
	handler.PatchSoul(patchRecorder, patchRequest)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", patchRecorder.Code, patchRecorder.Body.String())
	}
	var patched struct {
		Data struct {
			Document SoulDocument `json:"document"`
		} `json:"data"`
	}
	if err := json.Unmarshal(patchRecorder.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if patched.Data.Document.Identity.Name != "小懒" ||
		patched.Data.Document.Mission.PrimaryGoal == "" {
		t.Fatalf("patch did not preserve full Soul document: %#v", patched.Data.Document)
	}
	for _, body := range []string{
		`{"identity":{"name":null}}`,
		`{"identity":{"name":123}}`,
		`{"identity":{"name":true}}`,
		`{"identity":null}`,
		`{"identity":{"unknown":"value"}}`,
		`{}`,
	} {
		request := httptest.NewRequest(
			http.MethodPatch,
			"/memory/soul",
			bytes.NewBufferString(body),
		)
		request.Header.Set("X-User-Id", "user-1")
		recorder := httptest.NewRecorder()
		handler.PatchSoul(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf(
				"invalid Soul patch %s status=%d body=%s",
				body,
				recorder.Code,
				recorder.Body.String(),
			)
		}
	}

	otherUserRequest := httptest.NewRequest(http.MethodGet, "/memory/soul", nil)
	otherUserRequest.Header.Set("X-User-Id", "user-2")
	otherUserRecorder := httptest.NewRecorder()
	handler.GetSoul(otherUserRecorder, otherUserRequest)
	if otherUserRecorder.Code != http.StatusOK ||
		bytes.Contains(otherUserRecorder.Body.Bytes(), []byte(`"小懒"`)) {
		t.Fatalf(
			"cross-user Soul leak status=%d body=%s",
			otherUserRecorder.Code,
			otherUserRecorder.Body.String(),
		)
	}
}

func TestProfilePatchDistinguishesOmittedNullAndEmptyList(t *testing.T) {
	db := newCurrentMemoryTestDB(t)
	handler := NewHandler(db.DB)

	firstPatch := httptest.NewRequest(
		http.MethodPatch,
		"/memory/profile",
		bytes.NewBufferString(
			`{"identity":{"preferred_name":"Alice","aliases":["A"]},"locale":{"timezone":"Asia/Shanghai"}}`,
		),
	)
	firstPatch.Header.Set("X-User-Id", "user-1")
	firstRecorder := httptest.NewRecorder()
	handler.PatchProfile(firstRecorder, firstPatch)
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("first patch status=%d body=%s", firstRecorder.Code, firstRecorder.Body.String())
	}

	clearPatch := httptest.NewRequest(
		http.MethodPatch,
		"/memory/profile",
		bytes.NewBufferString(
			`{"identity":{"preferred_name":null,"aliases":[]}}`,
		),
	)
	clearPatch.Header.Set("X-User-Id", "user-1")
	clearRecorder := httptest.NewRecorder()
	handler.PatchProfile(clearRecorder, clearPatch)
	if clearRecorder.Code != http.StatusOK {
		t.Fatalf("clear patch status=%d body=%s", clearRecorder.Code, clearRecorder.Body.String())
	}
	var cleared struct {
		Data CurrentMemoryProfileData `json:"data"`
	}
	if err := json.Unmarshal(clearRecorder.Body.Bytes(), &cleared); err != nil {
		t.Fatalf("decode clear response: %v", err)
	}
	if cleared.Data.Document.Identity.PreferredName != nil ||
		len(cleared.Data.Document.Identity.Aliases) != 0 {
		t.Fatalf("profile clear semantics failed: %#v", cleared.Data.Document.Identity)
	}
	if cleared.Data.Document.Locale.Timezone == nil ||
		*cleared.Data.Document.Locale.Timezone != "Asia/Shanghai" {
		t.Fatalf("omitted timezone changed: %#v", cleared.Data.Document.Locale)
	}

	for _, body := range []string{
		`{"identity":{"aliases":null}}`,
		`{"identity":{"preferred_name":123}}`,
		`{"identity":{"aliases":[123]}}`,
		`{"identity":{"aliases":[true]}}`,
		`{"identity":{"unknown":"value"}}`,
		`{}`,
	} {
		request := httptest.NewRequest(
			http.MethodPatch,
			"/memory/profile",
			bytes.NewBufferString(body),
		)
		request.Header.Set("X-User-Id", "user-1")
		recorder := httptest.NewRecorder()
		handler.PatchProfile(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf(
				"invalid patch %s status=%d body=%s",
				body,
				recorder.Code,
				recorder.Body.String(),
			)
		}
	}
}

func TestPreferencePublicHandlersListDetailReorderAndDelete(t *testing.T) {
	db := newCurrentMemoryTestDB(t)
	handler := NewHandler(db.DB)
	repository := NewRepository(db.DB)
	if err := repository.EnsureInitialized(t.Context(), "user-1"); err != nil {
		t.Fatalf("initialize memory: %v", err)
	}
	const timestamp = "2026-07-20T09:30:00+08:00"
	preferences := PreferenceDocument{Preferences: []PreferenceItem{
		{
			Name:      "pref.first",
			Summary:   "First preference",
			Ref:       "references/first.md",
			CreatedAt: timestamp,
			UpdatedAt: timestamp,
		},
		{
			Name:      "pref.second",
			Summary:   "Second preference",
			Ref:       "references/missing.md",
			CreatedAt: timestamp,
			UpdatedAt: timestamp,
		},
	}}
	preferenceContent, err := RenderPreferences(preferences)
	if err != nil {
		t.Fatalf("render preferences: %v", err)
	}
	now := time.Date(2026, 7, 24, 9, 30, 0, 0, time.UTC)
	if err := repository.UpdateFileContent(
		t.Context(),
		"user-1",
		PreferencePath,
		preferenceContent,
		now,
	); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}
	referenceContent := []byte(validReferenceDocument)
	if err := repository.UpsertEntry(t.Context(), orm.MemoryCurrentEntry{
		UserID:    "user-1",
		Path:      ReferencesPath + "/first.md",
		EntryType: EntryFile,
		Content:   referenceContent,
		Size:      int64(len(referenceContent)),
		Mime:      "text/markdown; charset=utf-8",
		FileType:  "markdown",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed reference: %v", err)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/memory/preferences", nil)
	listRequest.Header.Set("X-User-Id", "user-1")
	listRecorder := httptest.NewRecorder()
	handler.ListPreferences(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var listed struct {
		Data CurrentMemoryPreferenceListData `json:"data"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listed.Data.TotalSize != 2 ||
		len(listed.Data.Items) != 2 ||
		listed.Data.ETag == "" ||
		listed.Data.UpdatedAt == 0 {
		t.Fatalf("unexpected list response: %#v", listed.Data)
	}
	if bytes.Contains(listRecorder.Body.Bytes(), []byte(`"ref"`)) {
		t.Fatalf("public preference list leaked ref: %s", listRecorder.Body.String())
	}

	availableRequest := httptest.NewRequest(
		http.MethodGet,
		"/memory/preferences/pref.first",
		nil,
	)
	availableRequest.Header.Set("X-User-Id", "user-1")
	availableRequest = mux.SetURLVars(availableRequest, map[string]string{"name": "pref.first"})
	availableRecorder := httptest.NewRecorder()
	handler.GetPreference(availableRecorder, availableRequest)
	if availableRecorder.Code != http.StatusOK {
		t.Fatalf(
			"available detail status=%d body=%s",
			availableRecorder.Code,
			availableRecorder.Body.String(),
		)
	}
	var available struct {
		Data CurrentMemoryPreferenceDetailData `json:"data"`
	}
	if err := json.Unmarshal(availableRecorder.Body.Bytes(), &available); err != nil {
		t.Fatalf("decode available detail: %v", err)
	}
	if available.Data.ReferenceStatus != "available" ||
		available.Data.Reference == nil ||
		available.Data.Reference.PreferenceDetails == "" {
		t.Fatalf("unexpected available detail: %#v", available.Data)
	}
	if bytes.Contains(availableRecorder.Body.Bytes(), []byte(`"ref"`)) {
		t.Fatalf("public preference detail leaked ref: %s", availableRecorder.Body.String())
	}

	missingRequest := httptest.NewRequest(
		http.MethodGet,
		"/memory/preferences/pref.second",
		nil,
	)
	missingRequest.Header.Set("X-User-Id", "user-1")
	missingRequest = mux.SetURLVars(missingRequest, map[string]string{"name": "pref.second"})
	missingRecorder := httptest.NewRecorder()
	handler.GetPreference(missingRecorder, missingRequest)
	if missingRecorder.Code != http.StatusOK {
		t.Fatalf(
			"missing reference detail status=%d body=%s",
			missingRecorder.Code,
			missingRecorder.Body.String(),
		)
	}
	var missing struct {
		Data CurrentMemoryPreferenceDetailData `json:"data"`
	}
	if err := json.Unmarshal(missingRecorder.Body.Bytes(), &missing); err != nil {
		t.Fatalf("decode missing detail: %v", err)
	}
	if missing.Data.ReferenceStatus != "missing" || missing.Data.Reference != nil {
		t.Fatalf("unexpected missing detail: %#v", missing.Data)
	}

	conflictRequest := httptest.NewRequest(
		http.MethodPut,
		"/memory/preferences:order",
		bytes.NewBufferString(
			`{"ordered_names":["pref.second","pref.first"],"expected_etag":"stale"}`,
		),
	)
	conflictRequest.Header.Set("X-User-Id", "user-1")
	conflictRecorder := httptest.NewRecorder()
	handler.ReorderPreferences(conflictRecorder, conflictRequest)
	if conflictRecorder.Code != http.StatusConflict ||
		!bytes.Contains(conflictRecorder.Body.Bytes(), []byte(`"code":2000107`)) ||
		!bytes.Contains(conflictRecorder.Body.Bytes(), []byte(`"message":"preference etag conflict"`)) ||
		!bytes.Contains(conflictRecorder.Body.Bytes(), []byte(`"current_etag"`)) {
		t.Fatalf(
			"stale reorder status=%d body=%s",
			conflictRecorder.Code,
			conflictRecorder.Body.String(),
		)
	}

	nonExactRequest := httptest.NewRequest(
		http.MethodPut,
		"/memory/preferences:order",
		bytes.NewBufferString(
			`{"ordered_names":[" pref.second","pref.first"],"expected_etag":"`+
				listed.Data.ETag+`"}`,
		),
	)
	nonExactRequest.Header.Set("X-User-Id", "user-1")
	nonExactRecorder := httptest.NewRecorder()
	handler.ReorderPreferences(nonExactRecorder, nonExactRequest)
	if nonExactRecorder.Code != http.StatusBadRequest {
		t.Fatalf(
			"non-exact reorder status=%d body=%s",
			nonExactRecorder.Code,
			nonExactRecorder.Body.String(),
		)
	}

	orderBody, err := json.Marshal(CurrentMemoryPreferenceOrderRequest{
		OrderedNames: []string{"pref.second", "pref.first"},
		ExpectedETag: listed.Data.ETag,
	})
	if err != nil {
		t.Fatal(err)
	}
	orderRequest := httptest.NewRequest(
		http.MethodPut,
		"/memory/preferences:order",
		bytes.NewReader(orderBody),
	)
	orderRequest.Header.Set("X-User-Id", "user-1")
	orderRecorder := httptest.NewRecorder()
	handler.ReorderPreferences(orderRecorder, orderRequest)
	if orderRecorder.Code != http.StatusOK {
		t.Fatalf("reorder status=%d body=%s", orderRecorder.Code, orderRecorder.Body.String())
	}
	var reordered struct {
		Data CurrentMemoryPreferenceListData `json:"data"`
	}
	if err := json.Unmarshal(orderRecorder.Body.Bytes(), &reordered); err != nil {
		t.Fatalf("decode reorder response: %v", err)
	}
	if reordered.Data.Items[0].Name != "pref.second" ||
		reordered.Data.ETag == listed.Data.ETag {
		t.Fatalf("unexpected reorder response: %#v", reordered.Data)
	}

	deleteRequest := httptest.NewRequest(
		http.MethodDelete,
		"/memory/preferences/pref.first",
		nil,
	)
	deleteRequest.Header.Set("X-User-Id", "user-1")
	deleteRequest = mux.SetURLVars(deleteRequest, map[string]string{"name": "pref.first"})
	deleteRecorder := httptest.NewRecorder()
	handler.DeletePreference(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf(
			"delete status=%d body=%s",
			deleteRecorder.Code,
			deleteRecorder.Body.String(),
		)
	}
	secondDeleteRequest := httptest.NewRequest(
		http.MethodDelete,
		"/memory/preferences/pref.first",
		nil,
	)
	secondDeleteRequest.Header.Set("X-User-Id", "user-1")
	secondDeleteRequest = mux.SetURLVars(
		secondDeleteRequest,
		map[string]string{"name": "pref.first"},
	)
	secondDeleteRecorder := httptest.NewRecorder()
	handler.DeletePreference(secondDeleteRecorder, secondDeleteRequest)
	if secondDeleteRecorder.Code != http.StatusNoContent {
		t.Fatalf(
			"idempotent delete status=%d body=%s",
			secondDeleteRecorder.Code,
			secondDeleteRecorder.Body.String(),
		)
	}
	crossUserDelete := httptest.NewRequest(
		http.MethodDelete,
		"/memory/preferences/pref.second",
		nil,
	)
	crossUserDelete.Header.Set("X-User-Id", "user-2")
	crossUserDelete = mux.SetURLVars(
		crossUserDelete,
		map[string]string{"name": "pref.second"},
	)
	crossUserDeleteRecorder := httptest.NewRecorder()
	handler.DeletePreference(crossUserDeleteRecorder, crossUserDelete)
	if crossUserDeleteRecorder.Code != http.StatusNoContent {
		t.Fatalf(
			"cross-user idempotent delete status=%d body=%s",
			crossUserDeleteRecorder.Code,
			crossUserDeleteRecorder.Body.String(),
		)
	}
	if err := repository.DeletePath(
		t.Context(),
		"user-2",
		PreferencePath,
	); err != nil {
		t.Fatalf("remove preference index: %v", err)
	}
	missingIndexDelete := httptest.NewRequest(
		http.MethodDelete,
		"/memory/preferences/pref.second",
		nil,
	)
	missingIndexDelete.Header.Set("X-User-Id", "user-2")
	missingIndexDelete = mux.SetURLVars(
		missingIndexDelete,
		map[string]string{"name": "pref.second"},
	)
	missingIndexDeleteRecorder := httptest.NewRecorder()
	handler.DeletePreference(missingIndexDeleteRecorder, missingIndexDelete)
	if missingIndexDeleteRecorder.Code != http.StatusNoContent {
		t.Fatalf(
			"missing-index idempotent delete status=%d body=%s",
			missingIndexDeleteRecorder.Code,
			missingIndexDeleteRecorder.Body.String(),
		)
	}
	if _, err := repository.GetEntry(
		t.Context(),
		"user-1",
		ReferencesPath+"/first.md",
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted preference reference still exists: %v", err)
	}

	afterDeleteRequest := httptest.NewRequest(http.MethodGet, "/memory/preferences", nil)
	afterDeleteRequest.Header.Set("X-User-Id", "user-1")
	afterDeleteRecorder := httptest.NewRecorder()
	handler.ListPreferences(afterDeleteRecorder, afterDeleteRequest)
	var afterDelete struct {
		Data CurrentMemoryPreferenceListData `json:"data"`
	}
	if err := json.Unmarshal(afterDeleteRecorder.Body.Bytes(), &afterDelete); err != nil {
		t.Fatalf("decode list after delete: %v", err)
	}
	if afterDelete.Data.TotalSize != 1 ||
		afterDelete.Data.Items[0].Name != "pref.second" {
		t.Fatalf("unexpected list after delete: %#v", afterDelete.Data)
	}
}

func TestPreferenceDetailRejectsCorruptStoredReference(t *testing.T) {
	db := newCurrentMemoryTestDB(t)
	handler := NewHandler(db.DB)
	repository := NewRepository(db.DB)
	if err := repository.EnsureInitialized(t.Context(), "user-1"); err != nil {
		t.Fatal(err)
	}
	const timestamp = "2026-07-20T09:30:00+08:00"
	content, err := RenderPreferences(PreferenceDocument{Preferences: []PreferenceItem{
		{
			Name:      "pref.corrupt",
			Summary:   "Corrupt reference",
			Ref:       "references/corrupt.md",
			CreatedAt: timestamp,
			UpdatedAt: timestamp,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := repository.UpdateFileContent(
		t.Context(),
		"user-1",
		PreferencePath,
		content,
		now,
	); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("# no frontmatter\n")
	if err := repository.UpsertEntry(t.Context(), orm.MemoryCurrentEntry{
		UserID:    "user-1",
		Path:      ReferencesPath + "/corrupt.md",
		EntryType: EntryFile,
		Content:   corrupt,
		Size:      int64(len(corrupt)),
		Mime:      "text/markdown",
		FileType:  "markdown",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodGet,
		"/memory/preferences/pref.corrupt",
		nil,
	)
	request.Header.Set("X-User-Id", "user-1")
	request = mux.SetURLVars(request, map[string]string{"name": "pref.corrupt"})
	recorder := httptest.NewRecorder()
	handler.GetPreference(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	if err := repository.DeletePath(
		t.Context(),
		"user-1",
		ReferencesPath+"/corrupt.md",
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpsertEntry(t.Context(), orm.MemoryCurrentEntry{
		UserID:    "user-1",
		Path:      ReferencesPath + "/corrupt.md",
		EntryType: EntryDir,
		FileType:  "directory",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	handler.GetPreference(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf(
			"directory reference status=%d body=%s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
}

func TestSoulPatchRetriesContentCASAndStopsAfterThreeConflicts(t *testing.T) {
	db := newCurrentMemoryTestDB(t)
	handler := NewHandler(db.DB)
	externalRepository := NewRepository(db.DB)
	if err := externalRepository.EnsureInitialized(t.Context(), "user-1"); err != nil {
		t.Fatal(err)
	}

	hookCalls := 0
	var hookErr error
	handler.module.repository.beforeCompareAndSwap = func() {
		hookCalls++
		if hookCalls != 1 {
			return
		}
		entry, err := externalRepository.GetEntry(
			t.Context(),
			"user-1",
			SoulPath,
		)
		if err != nil {
			hookErr = err
			return
		}
		document, err := ParseSoul(entry.Content)
		if err != nil {
			hookErr = err
			return
		}
		document.Mission.PrimaryGoal = "Preserve a concurrent RemoteFS update"
		content, err := RenderSoul(document)
		if err != nil {
			hookErr = err
			return
		}
		hookErr = externalRepository.UpdateFileContent(
			t.Context(),
			"user-1",
			SoulPath,
			content,
			time.Now().UTC(),
		)
	}
	request := httptest.NewRequest(
		http.MethodPatch,
		"/memory/soul",
		bytes.NewBufferString(`{"identity":{"name":"CAS merged"}}`),
	)
	request.Header.Set("X-User-Id", "user-1")
	recorder := httptest.NewRecorder()
	handler.PatchSoul(recorder, request)
	if hookErr != nil {
		t.Fatalf("CAS hook failed: %v", hookErr)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("retrying patch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	merged, err := handler.module.GetSoul(t.Context(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if merged.Document.Identity.Name != "CAS merged" ||
		merged.Document.Mission.PrimaryGoal != "Preserve a concurrent RemoteFS update" {
		t.Fatalf("CAS retry lost a concurrent field: %#v", merged.Document)
	}

	hookCalls = 0
	hookErr = nil
	handler.module.repository.beforeCompareAndSwap = func() {
		hookCalls++
		entry, err := externalRepository.GetEntry(
			t.Context(),
			"user-1",
			SoulPath,
		)
		if err != nil {
			hookErr = err
			return
		}
		document, err := ParseSoul(entry.Content)
		if err != nil {
			hookErr = err
			return
		}
		document.Epistemic.VerificationMode = fmt.Sprintf("concurrent-%d", hookCalls)
		content, err := RenderSoul(document)
		if err != nil {
			hookErr = err
			return
		}
		hookErr = externalRepository.UpdateFileContent(
			t.Context(),
			"user-1",
			SoulPath,
			content,
			time.Now().UTC(),
		)
	}
	exhaustedRequest := httptest.NewRequest(
		http.MethodPatch,
		"/memory/soul",
		bytes.NewBufferString(`{"identity":{"role":"never-committed"}}`),
	)
	exhaustedRequest.Header.Set("X-User-Id", "user-1")
	exhaustedRecorder := httptest.NewRecorder()
	handler.PatchSoul(exhaustedRecorder, exhaustedRequest)
	if hookErr != nil {
		t.Fatalf("CAS exhaustion hook failed: %v", hookErr)
	}
	if exhaustedRecorder.Code != http.StatusConflict || hookCalls != 3 {
		t.Fatalf(
			"CAS exhaustion status=%d hook_calls=%d body=%s",
			exhaustedRecorder.Code,
			hookCalls,
			exhaustedRecorder.Body.String(),
		)
	}
}

func newCurrentMemoryTestDB(t *testing.T) *orm.DB {
	t.Helper()
	db, err := orm.Connect(
		orm.DriverSQLite,
		filepath.Join(t.TempDir(), "current-memory.db"),
	)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.MemoryCurrentEntry{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}
