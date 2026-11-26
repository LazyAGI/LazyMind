package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/internal/updateformattest"
)

// Launch the real Core with disposable SQLite files. Replace only its OS
// account path and outbound transport; the production entry registers jobs.
func TestInPlaceComponentRetryCoreChild(t *testing.T) {
	root := os.Getenv("LAZYMIND_UNIT_RETRY_DIR")
	if root == "" {
		return
	}
	resolveComponentMaintenanceRoot = func() (string, error) { return filepath.Join(root, "updater"), nil }
	http.DefaultTransport = componentEntryTransport(func(req *http.Request) (*http.Response, error) {
		var name string
		switch req.URL.String() {
		case os.Getenv("LAZYMIND_UNIT_RAG_URL"):
			name = "payload.zip"
		case os.Getenv("LAZYMIND_UNIT_CLUSTER_URL"):
			name = "cluster.zip"
		case os.Getenv("LAZYMIND_UNIT_CLUSTER_CATALOG_URL"):
			name = "cluster-catalog.json"
		case os.Getenv("LAZYMIND_UNIT_CATALOG_URL"):
			name = "catalog.json"
		default:
			return nil, errors.New("offline fixture")
		}
		if err := os.WriteFile(filepath.Join(root, "requested."+name), []byte(req.URL.String()), 0600); err != nil {
			return nil, err
		}
		file, err := os.Open(filepath.Join(root, name))
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: req, Body: file}, nil
	})
	if os.Getenv("LAZYMIND_UNIT_CORE_BOOTSTRAP_ENTRY") == "1" {
		core := os.Getenv("ACL_DB_DSN")
		if _, err := os.Stat(core); os.IsNotExist(err) {
			if err := updateformattest.CreateCoreDatabase(core); err != nil {
				t.Fatal(err)
			}
			db, err := gorm.Open(sqlite.Open(os.Getenv("LAZYMIND_READONLY_DB_DSN")), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&readonlyorm.LazyLLMKBDocRow{}, &readonlyorm.LazyLLMDocRow{}); err != nil {
				t.Fatal(err)
			}
			raw, err := db.DB()
			if err != nil || raw.Close() != nil {
				t.Fatal("could not close fixture database")
			}
		}
	}
	TestComponentMaintenanceEntrypointChild(t)
}
