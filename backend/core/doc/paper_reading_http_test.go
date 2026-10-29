package doc

import (
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPaperReadingHTTPACLCacheAndSourceFreshness(t *testing.T) {
	for _, kind := range []string{"guide", "visual", "translation"} {
		t.Run(kind, func(t *testing.T) {
			installOriginalSourceTransport(t)
			db := newDocumentTestDB(t)
			db.AutoMigrate(&orm.AsyncJob{})
			seedDocumentListDataset(t, db, "ds", "owner")
			seedDocumentListDoc(t, db, "ds", "doc", "paper.pdf", time.Now(), "owner", nil)
			source := filepath.Join(t.TempDir(), "paper.pdf")
			os.WriteFile(source, []byte("%PDF-original"), 0600)
			db.Model(&orm.Document{}).Where("id = ?", "doc").Update("ext", mustJSON(documentExt{StoredPath: source, OriginalFilename: "paper.pdf"}))
			hash, _ := pdfir.SourceHash(source)
			d := pdfir.Document{Version: 1, ProjectionVersion: pdfir.ProjectionVersion, SourceSHA256: hash, CoordinateSpace: "normalized_top_left", Blocks: []pdfir.Block{{ID: "p", Type: "text", Text: "An original paragraph.", Page: 0}, {ID: "p2", Type: "text", Text: "Another paragraph.", Page: 1}, {ID: "v", Type: "image", Page: 0, Image: &pdfir.Image{Captions: []string{"Figure 1: Architecture"}}}, {ID: "v2", Type: "table", Page: 1, Table: &pdfir.Table{Captions: []string{"Table 1: Results"}}}}}
			irpath := filepath.Join(t.TempDir(), "ir.json")
			os.WriteFile(irpath, mustJSON(d), 0600)
			db.Create(&orm.AsyncJob{ID: "job", JobType: pdfIRJobType, ResourceType: "document_pdf_ir", ResourceID: pdfIRResource("ds", "doc"), Status: "succeeded", PayloadJSON: mustJSON(pdfIRPayload{Revision: pdfir.Revision, SourceSHA256: hash}), ResultJSON: mustJSON(pdfIRResult{IRPath: irpath}), CreatedAt: time.Now()})
			calls := 0
			modelLoads := 0
			change := false
			revoke := false
			model := "one"
			h := paperReadingHandler{kind: kind, model: formulaExplanationHandler{loadModel: func(context.Context, string) (map[string]any, string, string, error) {
				modelLoads++
				return map[string]any{"llm": "selected"}, model, "selected", nil
			}, generate: func(ctx context.Context, content, instruction string, cfg map[string]any) (string, error) {
				calls++
				if revoke {
					db.Model(&orm.Dataset{}).Where("id = ?", "ds").Update("create_user_id", "other")
				}
				if change {
					os.WriteFile(source, []byte("%PDF-changed"), 0600)
				}
				if !strings.Contains(content, "original paragraph") && !strings.Contains(content, "Another paragraph") && !strings.Contains(content, "Table 1: Results") {
					t.Error("authoritative context missing")
				}
				switch kind {
				case "guide":
					return `{"stages":[{"key":"problem","title":"Problem","summary":"Original","evidence_ids":["p"]}]}`, nil
				case "translation":
					if strings.Contains(content, "source_blocks") {
						t.Error("translation includes unrelated paper context")
					}
					return `{"translated_text":"原始段落。"}`, nil
				default:
					if !strings.Contains(instruction, "no image pixels") {
						t.Error("visual input limitation missing")
					}
					return `{"summary":"Contextual explanation","observations":[{"text":"Architecture","evidence_ids":["v"]}]}`, nil
				}
			}}}
			block := "v"
			if kind == "translation" {
				block = "p"
			}
			call := func(user, body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/", strings.NewReader(body))
				r.Header.Set("X-User-Id", user)
				r = mux.SetURLVars(r, map[string]string{"dataset": "ds", "document": "doc", "block": block})
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			if w := call("stranger", `{}`); w.Code != 404 || calls != 0 {
				t.Fatal("unauthorized generation")
			}
			if w := call("owner", `{"language":"fr"}`); w.Code != 400 || calls != 0 {
				t.Fatal("invalid language accepted")
			}
			if w := call("owner", `{} {}`); w.Code != 400 || calls != 0 {
				t.Fatal("extra JSON accepted")
			}
			for _, bad := range []any{"", strings.Repeat("a", 63), strings.Repeat("g", 64), 42, nil} {
				body := string(mustJSON(map[string]any{"source_sha256": bad}))
				if w := call("owner", body); w.Code != 400 || modelLoads != 0 {
					t.Fatalf("invalid source hash accepted: %v %d", bad, w.Code)
				}
			}
			mismatch := string(mustJSON(map[string]any{"source_sha256": strings.Repeat("a", 64)}))
			if w := call("owner", mismatch); w.Code != 409 || modelLoads != 0 || calls != 0 {
				t.Fatalf("stale reader reached model/cache: status=%d loads=%d", w.Code, modelLoads)
			}
			if kind != "guide" {
				original := block
				if kind == "translation" {
					block = "v"
				} else {
					block = "p"
				}
				if w := call("owner", string(mustJSON(map[string]any{"source_sha256": strings.ToUpper(hash)}))); w.Code != 404 || calls != 0 {
					t.Fatal("wrong block kind generated")
				}
				block = original
			}
			w := call("owner", `{}`)
			if w.Code != 200 || calls != 1 {
				t.Fatalf("generation failed: %d %s", w.Code, w.Body)
			}
			var authoritative struct {
				Data struct {
					SourceSHA256 string `json:"source_sha256"`
				} `json:"data"`
			}
			json.Unmarshal(w.Body.Bytes(), &authoritative)
			if authoritative.Data.SourceSHA256 != hash {
				t.Fatal("response hash not authoritative")
			}
			filepath.WalkDir(filepath.Join(filepath.Dir(source), ".lazymind-artifacts", "paper-reading"), func(path string, entry os.DirEntry, err error) error {
				if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".json") {
					raw, _ := os.ReadFile(path)
					var cached map[string]any
					json.Unmarshal(raw, &cached)
					cached["source_sha256"] = "forged cached hash"
					os.WriteFile(path, mustJSON(cached), 0600)
				}
				return nil
			})
			if kind == "translation" {
				var e struct {
					Data paperTranslation `json:"data"`
				}
				json.Unmarshal(w.Body.Bytes(), &e)
				if e.Data.SourceSHA256 != hash || e.Data.BlockID != "p" {
					t.Fatal("translation source scope missing")
				}
			}
			if w := call("owner", string(mustJSON(map[string]any{"source_sha256": hash}))); w.Code != 200 || calls != 1 {
				t.Fatal("cache miss")
			} else {
				json.Unmarshal(w.Body.Bytes(), &authoritative)
				if authoritative.Data.SourceSHA256 != hash {
					t.Fatal("cached source hash trusted")
				}
			}
			beforeLoads := modelLoads
			if w := call("owner", mismatch); w.Code != 409 || calls != 1 || modelLoads != beforeLoads {
				t.Fatal("stale source reached existing cache")
			}

			if w := call("owner", `{"force":true}`); w.Code != 200 || calls != 2 {
				t.Fatal("force ignored")
			}
			if w := call("owner", `{"language":"en"}`); w.Code != 200 || calls != 3 {
				t.Fatal("language cache reused")
			}
			model = "two"
			if w := call("owner", `{}`); w.Code != 200 || calls != 4 {
				t.Fatal("model cache reused")
			}
			if kind != "guide" {
				original := block
				if kind == "translation" {
					block = "p2"
				} else {
					block = "v2"
				}
				if w := call("owner", `{}`); w.Code != 200 || calls != 5 {
					t.Fatalf("block-specific cache reused: %d %s", w.Code, w.Body)
				}
				block = original
			}
			revoke = true
			if w := call("owner", `{"force":true}`); w.Code != 404 {
				t.Fatalf("revoked ACL returned generated result: %d", w.Code)
			}
			revoke = false
			db.Model(&orm.Dataset{}).Where("id = ?", "ds").Update("create_user_id", "owner")
			change = true
			if w := call("owner", `{"force":true}`); w.Code != 409 {
				t.Fatalf("stale generated result returned: %d %s", w.Code, w.Body)
			}
			if w := call("owner", `{}`); w.Code != 409 {
				t.Fatal("stale source cache returned")
			}
		})
	}
}
