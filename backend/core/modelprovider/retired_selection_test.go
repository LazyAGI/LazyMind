package modelprovider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestRetiredMetadataSelectionCannotBeSavedOrShared(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserSelectedModel{})
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, tc := range []struct {
		handler http.HandlerFunc
		body    string
	}{
		{SetSelectedModels, `{"selections":[{"model_key":"conversation_metadata","model_id":"old-model"}]}`},
		{SetSharedModel, `{"model_key":"conversation_metadata","share":true}`},
	} {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(tc.body))
		req.Header.Set("X-User-Id", "user-1")
		rec := httptest.NewRecorder()
		tc.handler(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid model_key") {
			t.Fatalf("retired selection accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
}
