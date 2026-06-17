package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func DefaultSQLitePath() string {
	dir := os.Getenv("LAZYMIND_STATE_SQLITE_DIR")
	if dir == "" {
		dir = "/data/sqlite"
	}
	return filepath.Join(dir, "core_state.db")
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		path = DefaultSQLitePath()
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

func MustSQLiteFromEnv() *SQLiteStore {
	s, err := NewSQLiteStore(os.Getenv("LAZYMIND_STATE_SQLITE_PATH"))
	if err != nil {
		panic(fmt.Errorf("sqlite state init failed: %w", err))
	}
	return s
}

func (s *SQLiteStore) init(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS state_kv (key TEXT PRIMARY KEY, value BLOB NOT NULL, expires_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS state_hash (key TEXT NOT NULL, field TEXT NOT NULL, value BLOB NOT NULL, expires_at INTEGER NOT NULL, PRIMARY KEY (key, field))`,
		`CREATE TABLE IF NOT EXISTS state_list (id INTEGER PRIMARY KEY AUTOINCREMENT, key TEXT NOT NULL, value BLOB NOT NULL, expires_at INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_state_list_key_id ON state_list(key, id)`,
		`CREATE TABLE IF NOT EXISTS state_zset (key TEXT NOT NULL, member TEXT NOT NULL, score REAL NOT NULL, expires_at INTEGER NOT NULL, PRIMARY KEY (key, member))`,
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

func nowMS() int64 { return time.Now().UnixMilli() }

func liveWhere() string { return "(expires_at = 0 OR expires_at > ?)" }

func (s *SQLiteStore) cleanupKey(ctx context.Context, key string) {
	now := nowMS()
	_, _ = s.db.ExecContext(ctx, `DELETE FROM state_kv WHERE key = ? AND expires_at > 0 AND expires_at <= ?`, key, now)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM state_hash WHERE key = ? AND expires_at > 0 AND expires_at <= ?`, key, now)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM state_list WHERE key = ? AND expires_at > 0 AND expires_at <= ?`, key, now)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM state_zset WHERE key = ? AND expires_at > 0 AND expires_at <= ?`, key, now)
}

func (s *SQLiteStore) HSet(ctx context.Context, key string, fields map[string]any, ttl time.Duration) error {
	exp := expiresAt(ttl)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for field, value := range fields {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO state_hash(key, field, value, expires_at) VALUES(?, ?, ?, ?)
			 ON CONFLICT(key, field) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at`,
			key, field, fmt.Sprint(value), exp); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT field, value FROM state_hash WHERE key = ? AND `+liveWhere(), key, nowMS())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var field, value string
		if err := rows.Scan(&field, &value); err != nil {
			return nil, err
		}
		out[field] = value
	}
	return out, rows.Err()
}

func (s *SQLiteStore) HGet(ctx context.Context, key, field string) ([]byte, error) {
	var value []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM state_hash WHERE key = ? AND field = ? AND `+liveWhere(), key, field, nowMS()).Scan(&value)
	return value, err
}

func (s *SQLiteStore) HDel(ctx context.Context, key string, fields ...string) error {
	if len(fields) == 0 {
		_, err := s.db.ExecContext(ctx, `DELETE FROM state_hash WHERE key = ?`, key)
		return err
	}
	for _, field := range fields {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM state_hash WHERE key = ? AND field = ?`, key, field); err != nil {
			return err
		}
	}
	return nil
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
	err := s.db.QueryRowContext(ctx, `SELECT value FROM state_kv WHERE key = ? AND `+liveWhere(), key, nowMS()).Scan(&value)
	return value, err
}

func (s *SQLiteStore) Del(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM state_kv WHERE key = ?`, key); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM state_hash WHERE key = ?`, key); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM state_list WHERE key = ?`, key); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM state_zset WHERE key = ?`, key); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) Exists(ctx context.Context, key string) (bool, error) {
	for _, query := range []string{
		`SELECT 1 FROM state_kv WHERE key = ? AND ` + liveWhere() + ` LIMIT 1`,
		`SELECT 1 FROM state_hash WHERE key = ? AND ` + liveWhere() + ` LIMIT 1`,
		`SELECT 1 FROM state_list WHERE key = ? AND ` + liveWhere() + ` LIMIT 1`,
		`SELECT 1 FROM state_zset WHERE key = ? AND ` + liveWhere() + ` LIMIT 1`,
	} {
		var one int
		err := s.db.QueryRowContext(ctx, query, key, nowMS()).Scan(&one)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	return false, nil
}

func (s *SQLiteStore) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	s.cleanupKey(ctx, key)
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO state_kv(key, value, expires_at) VALUES(?, ?, ?)`, key, value, expiresAt(ttl))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *SQLiteStore) RPush(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO state_list(key, value, expires_at) VALUES(?, ?, ?)`, key, value, expiresAt(ttl))
	return err
}

func (s *SQLiteStore) LPush(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	// Ordering is not important for the only LPush/BLPop path; insert is enough.
	return s.RPush(ctx, key, value, ttl)
}

func normalizeListRange(length int, start, stop int64) (int, int, bool) {
	if length == 0 {
		return 0, 0, false
	}
	n := int64(length)
	if start < 0 {
		start = n + start
	}
	if stop < 0 {
		stop = n + stop
	}
	if start < 0 {
		start = 0
	}
	if stop >= n {
		stop = n - 1
	}
	if start > stop || start >= n || stop < 0 {
		return 0, 0, false
	}
	return int(start), int(stop), true
}

func (s *SQLiteStore) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT value FROM state_list WHERE key = ? AND `+liveWhere()+` ORDER BY id ASC`, key, nowMS())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		all = append(all, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	startIdx, stopIdx, ok := normalizeListRange(len(all), start, stop)
	if !ok {
		return nil, nil
	}
	return all[startIdx : stopIdx+1], nil
}

func (s *SQLiteStore) LTrim(ctx context.Context, key string, start, stop int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := nowMS()
	if _, err := tx.ExecContext(ctx, `DELETE FROM state_list WHERE key = ? AND expires_at > 0 AND expires_at <= ?`, key, now); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM state_list WHERE key = ? AND `+liveWhere()+` ORDER BY id ASC`, key, now)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	startIdx, stopIdx, ok := normalizeListRange(len(ids), start, stop)
	if !ok {
		if _, err := tx.ExecContext(ctx, `DELETE FROM state_list WHERE key = ?`, key); err != nil {
			return err
		}
		return tx.Commit()
	}

	keep := ids[startIdx : stopIdx+1]
	placeholders := make([]string, len(keep))
	args := make([]any, 0, len(keep)+1)
	args = append(args, key)
	for i, id := range keep {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := `DELETE FROM state_list WHERE key = ? AND id NOT IN (` + strings.Join(placeholders, ",") + `)`
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) BLPop(ctx context.Context, key string, timeout time.Duration) error {
	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var id int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM state_list WHERE key = ? AND `+liveWhere()+` ORDER BY id ASC LIMIT 1`, key, nowMS()).Scan(&id)
		if err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM state_list WHERE id = ?`, id)
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			return err
		}
		_ = tx.Rollback()
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return sql.ErrNoRows
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (s *SQLiteStore) ZAdd(ctx context.Context, key, member string, score float64, ttl time.Duration) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO state_zset(key, member, score, expires_at) VALUES(?, ?, ?, ?)
		 ON CONFLICT(key, member) DO UPDATE SET score = excluded.score, expires_at = excluded.expires_at`,
		key, member, score, expiresAt(ttl))
	return err
}

func (s *SQLiteStore) ZRemRangeByScore(ctx context.Context, key string, min, max float64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM state_zset WHERE key = ? AND score >= ? AND score <= ?`, key, min, max)
	return err
}

func (s *SQLiteStore) ZCard(ctx context.Context, key string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_zset WHERE key = ? AND `+liveWhere(), key, nowMS()).Scan(&n)
	return n, err
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func UniqueMember(prefix string, score float64) string {
	return prefix + ":" + strconv.FormatFloat(score, 'f', 0, 64) + ":" + strconv.FormatInt(time.Now().UnixNano(), 10)
}
