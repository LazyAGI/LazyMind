package academic

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"
)

func TestSQLiteFileURLWindowsAndEscaping(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{`C:/Users/Alice/Zotero/zotero.sqlite`, "file:///C:/Users/Alice/Zotero/zotero.sqlite"},
		{`D:\Users\Alice Smith\论文#1?.sqlite`, "file:///D:/Users/Alice%20Smith/%E8%AE%BA%E6%96%87%231%3F.sqlite"},
		{`/tmp/论文 #1?.sqlite`, "file:///tmp/%E8%AE%BA%E6%96%87%20%231%3F.sqlite"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got := sqliteFileURL(tc.path)
			if got.String() != tc.want {
				t.Fatalf("got %s; want %s", got.String(), tc.want)
			}
			parsed, err := url.Parse(got.String())
			if err != nil || parsed.Host != "" {
				t.Fatalf("file path became authority: %v, %v", parsed, err)
			}
		})
	}
}

func TestSQLiteFileURLRealDatabase(t *testing.T) {
	file := filepath.Join(t.TempDir(), "论文 # %.sqlite")
	u := sqliteFileURL(file)
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE marker (value TEXT); INSERT INTO marker VALUES ('original')"); err != nil {
		t.Fatal(err)
	}
	tx, closeTx, err := openZoteroReadTransaction(t.Context(), &zoteroDatabaseLocation{Path: file})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTx()
	var value string
	if err := tx.QueryRow("SELECT value FROM marker").Scan(&value); err != nil || value != "original" {
		t.Fatalf("wrong database: %q, %v", value, err)
	}
	if _, err := tx.Exec("INSERT INTO marker VALUES ('forbidden')"); err == nil {
		t.Fatal("Zotero database was writable")
	}
}
