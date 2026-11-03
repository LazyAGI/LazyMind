package academic

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"lazymind/core/common"
)

var errArxivImportLockLost = errors.New("arXiv import database lock was lost")

type arxivImportGuard struct {
	ctx     context.Context
	check   func() error
	release func()
}

// The database holds this lock for the entire check/download/store/link sequence.
// Serializing by canonical base ID also protects the shared parent across datasets
// and revisions. Closing the owning connection, including process termination,
// releases the lock. The guard cancels PostgreSQL imports on detected session loss.
func lockArxivImport(ctx context.Context, db *gorm.DB, baseID string) (*arxivImportGuard, error) {
	switch db.Dialector.Name() {
	case "postgres":
		return lockPostgresArxivImport(ctx, db, "academic-arxiv-import:"+baseID)
	case "sqlite":
		return lockSQLiteArxivImport(ctx, db, baseID)
	default:
		return nil, fmt.Errorf("arXiv import locking does not support database %q", db.Dialector.Name())
	}
}

func lockPostgresArxivImport(ctx context.Context, db *gorm.DB, key string) (*arxivImportGuard, error) {
	dialect, ok := db.Dialector.(*postgres.Dialector)
	if !ok || dialect.DSN == "" {
		return nil, errors.New("arXiv import locking requires a PostgreSQL connection DSN")
	}
	// Use a private physical session rather than consuming the application's
	// connection pool: a pool full of lock waiters would starve the active import.
	config, err := pgx.ParseConfig(dialect.DSN)
	if err != nil {
		return nil, fmt.Errorf("configure arXiv import lock: %w", err)
	}
	// Downloads intentionally leave this session idle. Keep the application's
	// idle-session policy intact, but do not let it expire an active import lock.
	config.RuntimeParams["idle_session_timeout"] = "0"
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connect arXiv import lock: %w", err)
	}
	closeConn := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// This session never re-enters a pool with an unreleased advisory lock.
		_ = conn.Close(cleanup)
	}
	for {
		var locked bool
		err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1, 0))", key).Scan(&locked)
		if err != nil {
			closeConn()
			return nil, err
		}
		if locked {
			return guardPostgresArxivImport(ctx, conn, closeConn), nil
		}
		if err = waitArxivImportLock(ctx); err != nil {
			closeConn()
			return nil, err
		}
	}
}

func guardPostgresArxivImport(ctx context.Context, conn *pgx.Conn, closeConn func()) *arxivImportGuard {
	lockCtx, cancel := context.WithCancelCause(ctx)
	var session sync.Mutex // pgx sessions cannot execute concurrent operations.
	check := func() error {
		session.Lock()
		defer session.Unlock()
		if cause := context.Cause(lockCtx); cause != nil {
			return cause
		}
		probe, stop := context.WithTimeout(lockCtx, time.Second)
		defer stop()
		if err := conn.Ping(probe); err != nil {
			// Retain an external request cancellation; otherwise identify lock
			// loss explicitly rather than reporting it as user cancellation.
			if lockCtx.Err() == nil {
				cancel(fmt.Errorf("%w: %v", errArxivImportLockLost, err))
			}
			return context.Cause(lockCtx)
		}
		return nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-lockCtx.Done():
				return
			case <-ticker.C:
				if check() != nil {
					return
				}
			}
		}
	}()
	var once sync.Once
	return &arxivImportGuard{ctx: lockCtx, check: check, release: func() {
		once.Do(func() {
			cancel(nil)
			<-done
			session.Lock()
			defer session.Unlock()
			closeConn()
		})
	}}
}

func lockSQLiteArxivImport(ctx context.Context, db *gorm.DB, key string) (*arxivImportGuard, error) {
	if proxy, ok := db.Dialector.(interface{ IsSQLiteProxy() bool }); ok && proxy.IsSQLiteProxy() {
		endpoint, err := url.Parse(os.Getenv("LAZYMIND_SQLITE_SERVER_URL"))
		if err != nil || endpoint == nil {
			return nil, errors.New("arXiv import locking requires a local SQLite proxy")
		}
		host := endpoint.Hostname()
		if host != "localhost" && !net.ParseIP(host).IsLoopback() {
			return nil, errors.New("arXiv import locking requires a local SQLite proxy and shared database filesystem")
		}
	}
	var databases []struct{ Name, File string }
	if err := db.WithContext(ctx).Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
		return nil, err
	}
	var mainPath string
	for _, database := range databases {
		if database.Name == "main" {
			mainPath = database.File
			break
		}
	}
	if mainPath == "" {
		return nil, errors.New("arXiv import locking requires a file-backed SQLite database")
	}
	// The desktop SQLite proxy exposes its local main database path here too.
	// All Core instances must see that same filesystem, including these sidecars.
	// Fail closed for a remote proxy or an inaccessible path.
	mainPath, err := filepath.EvalSymlinks(mainPath)
	if err != nil {
		return nil, fmt.Errorf("arXiv import locking requires access to the SQLite database filesystem: %w", err)
	}
	mainPath, err = filepath.Abs(mainPath)
	if err != nil {
		return nil, err
	}
	lockDir := mainPath + ".academic-import-locks"
	if err = os.MkdirAll(lockDir, 0700); err != nil {
		return nil, err
	}
	// The main SQLite writer cannot be held while the document bridge writes to
	// it. Separate coordination databases supply SQLite's cross-process writer
	// locks without blocking those writes. 128 lazily-created shards bound disk
	// usage. Never unlink a live shard: a new inode could admit another holder.
	hash := sha256.Sum256([]byte(key))
	lockPath := filepath.Join(lockDir, fmt.Sprintf("arxiv-%02x.sqlite", hash[0]%128))
	lockURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(lockPath)}).String()
	pool, err := sql.Open("sqlite", lockURL)
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(0)
	conn, err := pool.Conn(ctx)
	if err != nil {
		_ = pool.Close()
		return nil, err
	}
	closeConn := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(cleanup, "ROLLBACK")
		_ = conn.Close()
		_ = pool.Close()
	}
	if _, err = conn.ExecContext(ctx, "PRAGMA busy_timeout=0"); err != nil {
		closeConn()
		return nil, err
	}
	for {
		if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err == nil {
			var once sync.Once
			return &arxivImportGuard{ctx: ctx, check: ctx.Err, release: func() { once.Do(closeConn) }}, nil
		}
		if !common.IsSQLiteBusy(err) {
			closeConn()
			return nil, err
		}
		if err = waitArxivImportLock(ctx); err != nil {
			closeConn()
			return nil, err
		}
	}
}

func waitArxivImportLock(ctx context.Context) error {
	timer := time.NewTimer(25 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
