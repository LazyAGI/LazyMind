package datasource

import (
	"testing"

	"lazymind/core/common/orm"
)

// makeDBConn creates a minimal orm.ExternalDatabaseConnection for DSN tests.
func makeDBConn(host string, port int, dbName, username string) orm.ExternalDatabaseConnection {
	return orm.ExternalDatabaseConnection{
		Host:         host,
		Port:         port,
		DatabaseName: dbName,
		Username:     username,
	}
}

// TestNormalizeDatabaseConnectionFields validates db type, host, database, username, and port.
func TestNormalizeDatabaseConnectionFields(t *testing.T) {
	// Happy path: mysql.
	dbType, host, dbName, user, port, err := normalizeDatabaseConnectionFields("mysql", "localhost", "mydb", "root", 3306)
	if err != nil {
		t.Fatalf("mysql: %v", err)
	}
	if dbType != "mysql" || host != "localhost" || dbName != "mydb" || user != "root" || port != 3306 {
		t.Fatalf("mysql normalization mismatch: %q %q %q %q %d", dbType, host, dbName, user, port)
	}

	// Happy path: postgresql with auto port.
	dbType, _, _, _, port, err = normalizeDatabaseConnectionFields("postgresql", "pg-host", "pgdb", "admin", 0)
	if err != nil {
		t.Fatalf("postgresql: %v", err)
	}
	if port != 5432 {
		t.Fatalf("postgresql default port: got %d, want 5432", port)
	}

	// postgres alias maps to postgresql.
	dbType, _, _, _, _, err = normalizeDatabaseConnectionFields("postgres", "h", "d", "u", 0)
	if err != nil {
		t.Fatalf("postgres alias: %v", err)
	}
	if dbType != "postgresql" {
		t.Fatalf("postgres alias: got %q, want postgresql", dbType)
	}

	// Invalid db type.
	_, _, _, _, _, err = normalizeDatabaseConnectionFields("oracle", "h", "d", "u", 0)
	if err == nil {
		t.Fatal("expected error for oracle db type")
	}

	// Missing fields.
	_, _, _, _, _, err = normalizeDatabaseConnectionFields("mysql", "", "d", "u", 0)
	if err == nil {
		t.Fatal("expected error for empty host")
	}

	// Invalid port.
	_, _, _, _, _, err = normalizeDatabaseConnectionFields("mysql", "h", "d", "u", 99999)
	if err == nil {
		t.Fatal("expected error for invalid port")
	}

	// Negative port.
	_, _, _, _, _, err = normalizeDatabaseConnectionFields("mysql", "h", "d", "u", -1)
	if err == nil {
		t.Fatal("expected error for negative port")
	}
}

// TestDefaultDatabasePort returns 3306 for mysql and 5432 for everything else.
func TestDefaultDatabasePort(t *testing.T) {
	if got := defaultDatabasePort("mysql"); got != 3306 {
		t.Fatalf("mysql: got %d, want 3306", got)
	}
	if got := defaultDatabasePort("postgresql"); got != 5432 {
		t.Fatalf("postgresql: got %d, want 5432", got)
	}
	if got := defaultDatabasePort("unknown"); got != 5432 {
		t.Fatalf("unknown: got %d, want 5432", got)
	}
	if got := defaultDatabasePort("  MySQL  "); got != 3306 {
		t.Fatalf("MySQL with spaces: got %d, want 3306", got)
	}
}

// TestNormalizeOptions trims whitespace from option keys and values.
func TestNormalizeOptions(t *testing.T) {
	input := map[string]string{" key1 ": " val1 ", "": "empty_key", "key2": "val2"}
	got := normalizeOptions(input)
	if got["key1"] != "val1" {
		t.Fatalf("key1: got %q, want val1", got["key1"])
	}
	if _, exists := got[""]; exists {
		t.Fatal("empty key should be removed")
	}
	if got["key2"] != "val2" {
		t.Fatalf("key2: got %q, want val2", got["key2"])
	}
}

// TestDatasourceFirstNonEmpty returns the first non-whitespace value.
func TestDatasourceFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "hello"); got != "hello" {
		t.Fatalf("firstNonEmpty: got %q, want hello", got)
	}
	if got := firstNonEmpty(""); got != "" {
		t.Fatalf("firstNonEmpty all empty: got %q, want empty", got)
	}
}

// TestMysqlDSN produces a valid MySQL DSN string with options.
func TestMysqlDSN(t *testing.T) {
	conn := makeDBConn("mysql-host", 3306, "mydb", "myuser")
	dsn := mysqlDSN(conn, "secret", map[string]string{"charset": "utf8mb4"})
	if dsn == "" {
		t.Fatal("expected non-empty DSN string")
	}
}

// TestPostgresDSN produces a valid PostgreSQL DSN string.
func TestPostgresDSN(t *testing.T) {
	conn := makeDBConn("pg-host", 5432, "pgdb", "pguser")
	dsn := postgresDSN(conn, "secret", nil)
	if dsn == "" {
		t.Fatal("expected non-empty DSN string")
	}
}
