package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/glebarez/go-sqlite"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type updateBootstrapContext struct {
	TransactionID, Owner string
	Databases            map[string]string
}

func loadUpdateBootstrap(tx, owner, raw string) (*updateBootstrapContext, error) {
	if tx == "" && owner == "" && raw == "" {
		return nil, nil
	}
	decoded, err := hex.DecodeString(tx)
	if err != nil || len(decoded) != 16 || len(tx) != 32 || owner == "" || len(owner) > 256 || len(raw) > 256*1024 {
		return nil, errors.New("update_bootstrap_invalid")
	}
	var storage struct {
		Version   int
		Databases map[string]string
	}
	if json.Unmarshal([]byte(raw), &storage) != nil || storage.Version != 2 {
		return nil, errors.New("update_bootstrap_invalid")
	}
	return &updateBootstrapContext{tx, owner, storage.Databases}, nil
}

// Generated recipe hashes come from isolated actual schema owners.
// This inspection never creates a database or executes a migration.
func verifyBootstrapDatabase(ctx context.Context, filename, want string) error {
	fail := func() error { return errors.New("update_bootstrap_invalid") }
	if !filepath.IsAbs(filename) {
		return fail()
	}
	info, err := os.Stat(filename)
	if err != nil || !info.Mode().IsRegular() {
		return fail()
	}
	path := filepath.ToSlash(filename)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	address := url.URL{Scheme: "file", Path: path}
	query := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(500)"}}
	address.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", address.String())
	if err != nil {
		return fail()
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	hash := sha256.New()
	field := func(value string) { _, _ = fmt.Fprintf(hash, "%d:", len(value)); _, _ = io.WriteString(hash, value) }
	for _, pragma := range []string{"application_id", "user_version"} {
		var value int64
		if tx.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&value) != nil {
			return fail()
		}
		field(strconv.FormatInt(value, 10))
	}
	rows, err := tx.QueryContext(ctx, "SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE sql IS NOT NULL AND name NOT GLOB 'sqlite_*' ORDER BY type COLLATE BINARY,name COLLATE BINARY")
	if err != nil {
		return fail()
	}
	for rows.Next() {
		var kind, name, table, statement string
		if rows.Scan(&kind, &name, &table, &statement) != nil {
			rows.Close()
			return fail()
		}
		for _, v := range []string{kind, name, table, statement} {
			field(v)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fail()
	}
	if hex.EncodeToString(hash.Sum(nil)) != want {
		return fail()
	}
	return nil
}

func verifyOwnBootstrapStorage(ctx context.Context, b *updateBootstrapContext) error {
	if b == nil {
		return nil
	}
	if err := verifyBootstrapDatabase(ctx, b.Databases["scan"], expectedScanFormatSHA); err != nil {
		return err
	}
	if err := verifyBootstrapDatabase(ctx, b.Databases["scan-state"], expectedScanStateFormatSHA); err != nil {
		return err
	}
	return nil
}
