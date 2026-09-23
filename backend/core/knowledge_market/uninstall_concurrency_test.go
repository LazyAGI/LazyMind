package knowledge_market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

// Run on SQLite by default and on the isolated PostgreSQL schema via
// TEST_DB_DRIVER=postgres. The HTTP boundary is blocked, not simulated by sleep.
func TestMarketUninstallSerializesUpdateAndRetry(t *testing.T) {
	for _, action := range []string{"update", "retry"} {
		for _, deleteFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/delete-fails=%t", action, deleteFails), func(t *testing.T) {
				stubMarketWorkerHealth(t)
				db := orm.OpenTestDB(t).DB
				if err := db.AutoMigrate(&orm.KnowledgeMarketItem{}, &orm.KnowledgeMarketInstall{}, &orm.AsyncJob{}, &orm.Dataset{}, &orm.EvalSet{}, &orm.DefaultDataset{}); err != nil {
					t.Fatal(err)
				}
				store.Init(db, db, nil)
				if err := SeedCatalog(context.Background(), db, writeCatalog(t, handlerTestCatalog)); err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC()
				if err := db.Create(&orm.Dataset{ID: "ds-uninstall", KbID: "kb-uninstall", DisplayName: "Synthetic concurrency fixture", Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "user-a", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
					t.Fatal(err)
				}
				insertInstall(t, db, "law-cn", "user-a", "done", "ds-uninstall", now)
				insertInstallJob(t, db, "old-failure", "user-a", "law-cn", "failed", now, 0, 2, "")
				if err := db.Model(&orm.AsyncJob{}).Where("id = ?", "old-failure").Update("job_type", updateJobType).Error; err != nil {
					t.Fatal(err)
				}

				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				deleting, releaseDelete := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				defer releaseOnce.Do(func() { close(releaseDelete) })
				previous := http.DefaultTransport
				http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					switch r.URL.Path {
					case "/api/scan/internal/source-access/by-dataset:batch":
						return testJSONResponse(http.StatusOK, `{"items":[{"dataset_id":"ds-uninstall","exists":false,"allowed":true}]}`), nil
					case "/v1/kbs/kb-uninstall":
						close(deleting)
						select {
						case <-releaseDelete:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
						if deleteFails {
							return testJSONResponse(http.StatusBadGateway, `{}`), nil
						}
						return testJSONResponse(http.StatusOK, `{}`), nil
					default:
						return nil, fmt.Errorf("unexpected test request %s", r.URL.Path)
					}
				})
				t.Cleanup(func() { http.DefaultTransport = previous })
				t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo.test")
				t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
				router := mux.NewRouter()
				router.HandleFunc("/datasets/{dataset}", doc.DeleteDataset).Methods(http.MethodDelete)
				router.HandleFunc("/items/{market_item_id}:update", MarketUpdate).Methods(http.MethodPost)
				router.HandleFunc("/tasks/{job_id}:retry", MarketRetryTask).Methods(http.MethodPost)
				request := func(method, path string, requestCtx context.Context) int {
					r := httptest.NewRequest(method, path, nil).WithContext(requestCtx)
					r.Header.Set("X-User-Id", "user-a")
					w := httptest.NewRecorder()
					router.ServeHTTP(w, r)
					return w.Code
				}
				deleted := make(chan int, 1)
				go func() { deleted <- request(http.MethodDelete, "/datasets/ds-uninstall", ctx) }()
				select {
				case <-deleting:
				case status := <-deleted:
					t.Fatalf("uninstall did not reach external delete: %d", status)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}

				// Ensure the racing request has already read the pre-uninstall
				// install record before allowing the external deletion to finish.
				type racingKey struct{}
				readInstall := make(chan struct{})
				var readOnce sync.Once
				if err := db.Callback().Query().After("gorm:query").Register("test:install-preflight", func(q *gorm.DB) {
					if q.Statement.Table == "knowledge_market_installs" && q.Statement.Context.Value(racingKey{}) == true {
						readOnce.Do(func() { close(readInstall) })
					}
				}); err != nil {
					t.Fatal(err)
				}
				defer db.Callback().Query().Remove("test:install-preflight")
				path := "/items/law-cn:update"
				if action == "retry" {
					path = "/tasks/old-failure:retry"
				}
				requested := make(chan int, 1)
				go func() { requested <- request(http.MethodPost, path, context.WithValue(ctx, racingKey{}, true)) }()
				select {
				case <-readInstall:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				status, acceptedDuringDelete := 0, false
				select {
				case status = <-requested:
					acceptedDuringDelete = status == http.StatusOK
				case <-time.After(100 * time.Millisecond):
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				releaseOnce.Do(func() { close(releaseDelete) })
				var deleteStatus int
				select {
				case deleteStatus = <-deleted:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if status == 0 {
					select {
					case status = <-requested:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
				if acceptedDuringDelete {
					t.Error("accepted work while its dataset was being externally deleted")
				}
				if deleteFails {
					if deleteStatus != http.StatusBadGateway {
						t.Fatalf("failed external delete status=%d", deleteStatus)
					}
					// A contending SQLite writer may reject with 409; either way the
					// failed delete must leave the installation available for recovery.
					if status != http.StatusOK && status != http.StatusConflict {
						t.Fatalf("request after failed delete status=%d", status)
					}
					var retained orm.Dataset
					if err := db.Take(&retained, "id = ? AND deleted_at IS NULL", "ds-uninstall").Error; err != nil {
						t.Fatalf("failed delete lost dataset: %v", err)
					}
					if status == http.StatusConflict && request(http.MethodPost, path, ctx) != http.StatusOK {
						t.Fatal("failed delete did not release the lock for recovery")
					}
				} else {
					if deleteStatus != http.StatusOK {
						t.Fatalf("delete status=%d", deleteStatus)
					}
					if status != http.StatusNotFound && status != http.StatusConflict {
						t.Fatalf("accepted stale action after uninstall: status=%d", status)
					}
					var pending int64
					if err := db.Model(&orm.AsyncJob{}).Where("status IN ?", []string{"pending", "running"}).Count(&pending).Error; err != nil {
						t.Fatal(err)
					}
					if pending != 0 {
						t.Fatalf("uninstall left %d active jobs targeting the removed dataset", pending)
					}
				}
			})
		}
	}
}
