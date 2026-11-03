package chat

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// Exercise the production HTTP server and driver, not a direct SQLite connection
// marked as a proxy. The server owns only temporary databases.
func checkpointProxyDB(t *testing.T) *gorm.DB {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	root := t.TempDir()
	binary := filepath.Join(root, "runtime-manager")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binary, ".")
	build.Dir = filepath.Join(repo, "local/local-runtime-manager")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build SQLite server: %v\n%s", err, output)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.Split(listener.Addr().String(), ":")[1]
	listener.Close()
	tokenFile := filepath.Join(root, "run", "pc-token")
	if err := os.MkdirAll(filepath.Dir(tokenFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, []byte("checkpoint-test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	server := exec.Command(binary, "internal", "sqlite-server-run", "--repo-root", repo,
		"--runtime-root", root, "--build-root", filepath.Join(root, "build"))
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "LAZYMIND_") {
			server.Env = append(server.Env, entry)
		}
	}
	server.Env = append(server.Env, "LAZYMIND_LOCAL_SQLITE_SERVER_PORT="+port, "LAZYMIND_HOST_HOME="+filepath.Join(root, "host-home"))
	logFile, err := os.Create(filepath.Join(root, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	server.Stdout, server.Stderr = logFile, logFile
	if err := server.Start(); err != nil {
		logFile.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = server.Process.Kill()
		_ = server.Wait()
		_ = logFile.Close()
	})
	url := "http://127.0.0.1:" + port
	client := &http.Client{Timeout: time.Second}
	ready := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		response, err := client.Get(url + "/healthz")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		output, _ := os.ReadFile(logFile.Name())
		t.Fatalf("SQLite server did not start: %s", output)
	}
	t.Setenv("LAZYMIND_SQLITE_SERVER_URL", url)
	t.Setenv("LAZYMIND_SQLITE_SERVER_TOKEN_FILE", tokenFile)
	db, err := orm.Connect(orm.DriverSQLite, "sqliteproxy://core")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec("CREATE TABLE checkpoint_probe (id TEXT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.Document{}, &orm.ResourceOperationReceipt{}); err != nil {
		t.Fatal(err)
	}
	return db.DB
}

func TestConversationCheckpointSQLiteProxy(t *testing.T) {
	db := checkpointProxyDB(t)
	abort := errors.New("abort checkpoint")
	for _, rollback := range []bool{false, true} {
		name := "commit"
		if rollback {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			done := make(chan error, 1)
			go func() {
				done <- conversationCheckpoint(ctx, db, "", func(tx *gorm.DB) error {
					if err := tx.Exec("INSERT INTO checkpoint_probe VALUES (?)", name+"-checkpoint").Error; err != nil {
						return err
					}
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
					if rollback {
						return abort
					}
					return nil
				})
			}()
			select {
			case <-entered:
			case err := <-done:
				t.Fatalf("checkpoint never entered: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			background, ordinary := make(chan error, 1), make(chan error, 1)
			go func() {
				background <- db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					if err := tx.Exec("INSERT INTO checkpoint_probe VALUES (?)", name+"-background").Error; err != nil {
						return err
					}
					if err := tx.Create(&orm.Document{ID: name + "-resource", DatasetID: "isolated", DisplayName: "resource"}).Error; err != nil {
						return err
					}
					return tx.Create(&orm.ResourceOperationReceipt{TenantID: "default", UserID: "owner", OperationKey: name, Action: "knowledge.document.upload", ParameterHash: "fixture", Result: []byte(`{"status":"succeeded"}`), CreatedAt: time.Now()}).Error
				})
			}()
			go func() {
				ordinary <- db.WithContext(ctx).Exec("INSERT INTO checkpoint_probe VALUES (?)", name+"-ordinary").Error
			}()
			select {
			case err := <-background:
				t.Errorf("background transaction escaped checkpoint: %v", err)
				background <- err
			case err := <-ordinary:
				t.Errorf("ordinary write escaped checkpoint: %v", err)
				ordinary <- err
			case <-time.After(100 * time.Millisecond):
			}
			// A canceled waiter must leave the active transaction intact.
			waitCtx, stopWait := context.WithTimeout(ctx, 100*time.Millisecond)
			err := db.WithContext(waitCtx).Transaction(func(tx *gorm.DB) error {
				return tx.Exec("INSERT INTO checkpoint_probe VALUES (?)", name+"-canceled").Error
			})
			stopWait()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("waiting transaction cancellation: %v", err)
			}
			unblock()
			if err := <-done; rollback && !errors.Is(err, abort) || !rollback && err != nil {
				t.Fatalf("checkpoint result: %v", err)
			}
			for _, result := range []<-chan error{background, ordinary} {
				if err := <-result; err != nil {
					t.Errorf("peer failed after release: %v", err)
				}
			}
			for _, model := range []any{&orm.Document{}, &orm.ResourceOperationReceipt{}} {
				var count int64
				query := db.Model(model)
				if _, ok := model.(*orm.Document); ok {
					query = query.Where("id = ?", name+"-resource")
				} else {
					query = query.Where("operation_key = ?", name)
				}
				if err := query.Count(&count).Error; err != nil || count != 1 {
					t.Errorf("resource transaction not atomically retained after checkpoint: count=%d err=%v", count, err)
				}
			}
			for suffix, want := range map[string]int64{"checkpoint": 1, "background": 1, "ordinary": 1, "canceled": 0} {
				if rollback && suffix == "checkpoint" {
					want = 0
				}
				var count int64
				if err := db.Table("checkpoint_probe").Where("id = ?", name+"-"+suffix).Count(&count).Error; err != nil || count != want {
					t.Errorf("%s count=%d want=%d err=%v", suffix, count, want, err)
				}
			}
		})
	}
	for _, rollback := range []bool{false, true} {
		name := "outer-commit"
		if rollback {
			name = "outer-rollback"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := conversationCheckpoint(ctx, tx, "", func(inner *gorm.DB) error {
					return inner.Exec("INSERT INTO checkpoint_probe VALUES (?)", name).Error
				}); err != nil {
					return err
				}
				if rollback {
					return abort
				}
				return nil
			})
			if rollback && !errors.Is(err, abort) || !rollback && err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := db.Table("checkpoint_probe").Where("id = ?", name).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if rollback && count != 0 || !rollback && count != 1 {
				t.Fatalf("outer transaction ownership lost: count=%d", count)
			}
		})
	}
}
