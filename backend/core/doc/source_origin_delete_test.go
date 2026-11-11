package doc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestSourceOriginDeletionCompletesAfterConcurrentMetadataUpdate(t *testing.T) {
	for _, market := range []bool{false, true} {
		t.Run(fmt.Sprintf("market=%t", market), func(t *testing.T) {
			db := originTestDB(t)
			originTestSeed(t, db, "dataset", `{"keep":true}`)
			stale := originTestRow(t, db, "dataset")
			const latest = `{"keep":true,"source_origin":{"version":1,"kind":"scan","basis":"created"}}`
			publish := func() {
				if err := db.Model(&orm.Dataset{}).Where("id = ?", "dataset").Updates(map[string]any{
					"dataset_info": json.RawMessage(latest), "resource_version": 11,
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
			var err error
			if market {
				deletedExternally := false
				previous := http.DefaultTransport
				http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.Method != http.MethodDelete || !strings.HasPrefix(r.URL.Path, "/v1/kbs/") {
						t.Fatalf("unexpected dependency: %s %s", r.Method, r.URL.Path)
					}
					deletedExternally = true
					publish()
					return testJSONResponse(http.StatusOK, `{}`), nil
				})
				t.Cleanup(func() { http.DefaultTransport = previous })
				err = DeleteMarketDataset(t.Context(), "user-1", "dataset")
				if !deletedExternally {
					t.Fatal("external deletion was not exercised")
				}
			} else {
				publish()
				err = db.Transaction(func(tx *gorm.DB) error {
					return softDeleteDataset(t.Context(), tx, &stale, "user-1", time.Now().UTC())
				})
			}
			row := originTestRow(t, db, "dataset")
			if err != nil || row.DeletedAt == nil || string(row.DatasetInfo) != latest || row.ResourceVersion != 12 {
				t.Fatalf("deletion did not converge: err=%v deleted=%v version=%d info=%s", err, row.DeletedAt, row.ResourceVersion, row.DatasetInfo)
			}
		})
	}
}
