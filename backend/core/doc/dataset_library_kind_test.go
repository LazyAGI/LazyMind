package doc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

func libraryKindTestDB(t *testing.T) *orm.DB {
	t.Helper()
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.ACLModel{}, &orm.ACLGroupModel{}, &orm.UserGroupModel{}, &orm.KBModel{}, &orm.VisibilityModel{}, &orm.UserUIPreferences{}, &orm.PaperLibraryEntry{}, &orm.PaperLibraryAttachment{}); err != nil {
		t.Fatal(err)
	}
	acl.InitStore(db)
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/v1/algo/list":
			return testJSONResponse(200, `{"code":200,"data":{"items":[{"algo_id":"general_algo","display_name":"General"}]}}`), nil
		case r.URL.Path == "/v1/kbs":
			return testJSONResponse(200, `{"kb_id":"kb-kind"}`), nil
		case strings.HasPrefix(r.URL.Path, "/v1/kbs/"):
			return testJSONResponse(200, `{}`), nil
		case strings.HasSuffix(r.URL.Path, "/groups"):
			return testJSONResponse(200, `{"code":200,"data":[]}`), nil
		case strings.HasSuffix(r.URL.Path, "/source-access/by-dataset:batch"):
			var input struct {
				DatasetIDs []string `json:"dataset_ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				return nil, err
			}
			items := []map[string]any{}
			for _, id := range input.DatasetIDs {
				items = append(items, map[string]any{"dataset_id": id, "exists": false, "allowed": true})
			}
			b, _ := json.Marshal(map[string]any{"items": items})
			return testJSONResponse(200, string(b)), nil
		case strings.HasSuffix(r.URL.Path, "/sources/by-datasets"):
			return testJSONResponse(200, `{"source_map":{}}`), nil
		default:
			return testJSONResponse(404, `{"message":"not found"}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
	return db
}
func libraryKindRequest(t *testing.T, handler http.HandlerFunc, method, path, body, user string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-User-Id", user)
	req.Header.Set("X-User-Name", user)
	if strings.HasPrefix(path, "/api/core/datasets/ds-kind") {
		req = mux.SetURLVars(req, map[string]string{"dataset": "ds-kind"})
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}
func libraryKindResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDatasetSceneCreationPersistsAcrossListAndDetail(t *testing.T) {
	for _, test := range []struct{ input, want string }{{`"scene":"academic_papers",`, "academic_papers"}, {`"scene":"general",`, "general"}, {"", "general"}} {
		t.Run(test.want+test.input, func(t *testing.T) {
			db := libraryKindTestDB(t)
			level := "stored"
			if test.want == "academic_papers" {
				level = "parsed"
			}
			rec := libraryKindRequest(t, CreateDataset, "POST", "/api/core/datasets?dataset_id=ds-kind", `{`+test.input+`"display_name":"论文","processing_level":"`+level+`","tags":["research"],"algo":{"algo_id":"general_algo"}}`, "owner")
			if rec.Code != 200 {
				t.Fatalf("create status=%d %s", rec.Code, rec.Body.String())
			}
			if got := libraryKindResponse(t, rec)["scene"]; got != test.want {
				t.Fatalf("creation lost kind: %v want %s", got, test.want)
			}
			var row orm.Dataset
			if err := db.Take(&row, "id = ?", "ds-kind").Error; err != nil {
				t.Fatal(err)
			}
			if row.Scene != test.want {
				t.Fatalf("kind not persisted: %s", row.Ext)
			}
			rec = libraryKindRequest(t, GetDataset, "GET", "/api/core/datasets/ds-kind", "", "owner")
			if rec.Code != 200 || libraryKindResponse(t, rec)["scene"] != test.want {
				t.Fatalf("get lost kind: %d %s", rec.Code, rec.Body.String())
			}
			rec = libraryKindRequest(t, ListDatasets, "GET", "/api/core/datasets?page_size=10", "", "owner")
			body := libraryKindResponse(t, rec)
			datasets, ok := body["datasets"].([]any)
			if !ok || len(datasets) != 1 || datasets[0].(map[string]any)["scene"] != test.want {
				t.Fatalf("list lost kind: %s", rec.Body.String())
			}
		})
	}
}

func TestDatasetSceneSurvivesRenameAndCannotBeConverted(t *testing.T) {
	db := libraryKindTestDB(t)
	row := orm.Dataset{ID: "ds-kind", KbID: "kb-kind", DisplayName: "Papers", Scene: "academic_papers", ProcessingLevel: "stored", Ext: json.RawMessage(`{"tags":["tag"]}`), BaseModel: orm.BaseModel{CreateUserID: "owner"}}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	rec := libraryKindRequest(t, UpdateDataset, "PATCH", "/api/core/datasets/ds-kind", `{"display_name":"Renamed"}`, "owner")
	if rec.Code != 200 || libraryKindResponse(t, rec)["scene"] != "academic_papers" {
		t.Fatalf("rename lost kind: %d %s", rec.Code, rec.Body.String())
	}
	rec = libraryKindRequest(t, UpdateDataset, "PATCH", "/api/core/datasets/ds-kind", `{"scene":"general"}`, "owner")
	if rec.Code != http.StatusConflict {
		t.Fatalf("paper/document conversion accepted: %d %s", rec.Code, rec.Body.String())
	}
	if err := db.Take(&row, "id = ?", "ds-kind").Error; err != nil {
		t.Fatal(err)
	}
	if row.Scene != "academic_papers" {
		t.Fatalf("persisted kind changed: %s", row.Ext)
	}
}

func TestDatasetSceneReaderCannotChangeConfiguration(t *testing.T) {
	db := libraryKindTestDB(t)
	row := orm.Dataset{ID: "ds-kind", DisplayName: "Papers", Scene: "academic_papers", ProcessingLevel: "stored", Ext: json.RawMessage(`{"tags":["research"]}`), BaseModel: orm.BaseModel{CreateUserID: "owner"}}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	acl.GetStore().AddACL(acl.ResourceTypeDB, "ds-kind", acl.GranteeUser, "reader", acl.PermissionDatasetRead, "owner", nil)
	rec := libraryKindRequest(t, UpdateDataset, "PATCH", "/api/core/datasets/ds-kind", `{"scene":"general","display_name":"Changed"}`, "reader")
	if rec.Code != 403 {
		t.Fatalf("reader changed configuration: %d %s", rec.Code, rec.Body.String())
	}
	db.Take(&row, "id=?", "ds-kind")
	if row.Scene != "academic_papers" || row.DisplayName != "Papers" {
		t.Fatal("forbidden update changed library")
	}
}

func TestDatasetSceneRejectsUnknownCreationKind(t *testing.T) {
	libraryKindTestDB(t)
	rec := libraryKindRequest(t, CreateDataset, "POST", "/api/core/datasets?dataset_id=ds-kind", `{"display_name":"Unknown","scene":"guess","tags":["research"],"processing_level":"stored"}`, "owner")
	if rec.Code != 400 {
		t.Fatalf("unknown kind accepted: %d %s", rec.Code, rec.Body.String())
	}
}
