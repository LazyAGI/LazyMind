package state

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func defaultSQLitePath() string {
	dir := os.Getenv("LAZYMIND_STATE_SQLITE_DIR")
	if dir == "" {
		dir = "/data/sqlite"
	}
	return filepath.Join(dir, "scan_state.db")
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		path = defaultSQLitePath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	s := &SQLiteStore{db: db}
	if err := s.init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) init(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS state_kv (key TEXT PRIMARY KEY, value BLOB NOT NULL, expires_at INTEGER NOT NULL)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func expiresAt(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	return time.Now().Add(ttl).UnixMilli()
}

func nowMS() int64 {
	return time.Now().UnixMilli()
}

func (s *SQLiteStore) cleanupKey(ctx context.Context, key string) {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM state_kv WHERE key = ? AND expires_at > 0 AND expires_at <= ?`, key, nowMS())
}

func (s *SQLiteStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO state_kv(key, value, expires_at) VALUES(?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at`,
		key, value, expiresAt(ttl))
	return err
}

func (s *SQLiteStore) Get(ctx context.Context, key string) ([]byte, error) {
	var value []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM state_kv WHERE key = ? AND (expires_at = 0 OR expires_at > ?)`,
		key, nowMS()).Scan(&value)
	return value, err
}

func (s *SQLiteStore) Del(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM state_kv WHERE key = ?`, key); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) Exists(ctx context.Context, key string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM state_kv WHERE key = ? AND (expires_at = 0 OR expires_at > ?) LIMIT 1`,
		key, nowMS()).Scan(&one)
	if err == nil {
		return true, nil
	}
	if IsMissing(err) {
		return false, nil
	}
	return false, err
}

func (s *SQLiteStore) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	s.cleanupKey(ctx, key)
	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO state_kv(key, value, expires_at) VALUES(?, ?, ?)`,
		key, value, expiresAt(ttl))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
