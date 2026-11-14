package migrate

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func githubStarSQLiteSchemaFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT type,name,sql FROM sqlite_master WHERE sql IS NOT NULL
 AND name NOT LIKE 'sqlite_%' AND name NOT IN ('schema_migrations','schema_migration_history','schema_migration_lock')
 ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	var objects [][3]string
	for rows.Next() {
		var object [3]string
		if err := rows.Scan(&object[0], &object[1], &object[2]); err != nil {
			t.Fatal(err)
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var lines []string
	for _, object := range objects {
		kind, name, ddl := object[0], object[1], object[2]
		tokens := githubStarSQLTokens(t, ddl)
		if kind != "table" {
			lines = append(lines, kind+"|"+name+"|"+githubStarSQLSignature(kind, tokens))
			continue
		}
		lines = append(lines, "table|"+name+"|"+publicationSQLiteTableFingerprint(t, db, name))
		if name == "user_chat_settings" {
			// Its historical INSERT ... SELECT * consumer requires physical order.
			lines = append(lines, "ordered|"+name+"|"+ddl)
		}
		checkExpressions := false
		switch name {
		case "chat_histories", "conversation_tool_grants", "conversation_workspace_bindings", "conversations", "local_workspaces", "paper_libraries", "paper_library_entries":
			checkExpressions = true
		}
		for i, token := range tokens {
			if token == "constraint" && i+1 < len(tokens) {
				lines = append(lines, "constraint|"+name+"|"+githubStarSQLSignature("", tokens[i:i+2]))
			}
			if token == "check" && checkExpressions {
				if i+1 >= len(tokens) || tokens[i+1] != "(" {
					t.Fatal("unrecognized SQLite CHECK definition", name)
				}
				depth, end := 0, -1
				for j := i + 1; j < len(tokens); j++ {
					if tokens[j] == "(" {
						depth++
					}
					if tokens[j] == ")" {
						depth--
					}
					if depth == 0 {
						end = j
						break
					}
				}
				if end < 0 {
					t.Fatal("unbalanced SQLite CHECK definition", name)
				}
				start := i
				if i >= 2 && tokens[i-2] == "constraint" {
					start = i - 2
				}
				lines = append(lines, "check|"+name+"|"+githubStarSQLSignature("", tokens[start:end+1]))
			}
			// These properties are not all exposed by SQLite's column/index PRAGMAs.
			// Keep their complete definition, rather than silently accepting them.
			if (token == "check" && !checkExpressions) || token == "autoincrement" || token == "collate" || token == "generated" || token == "as" || token == "virtual" || token == "stored" || token == "without" || token == "strict" || token == "deferrable" || token == "conflict" {
				lines = append(lines, "definition|"+name+"|"+githubStarSQLSignature(kind, tokens))
				break
			}
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// Only SQL spacing/identifier spelling is normalized; quoted literals stay intact.
func githubStarSQLSignature(kind string, tokens []string) string {
	parts := append([]string(nil), tokens...)
	headerOn := -1
	if kind == "index" || kind == "trigger" {
		for i, token := range tokens {
			if token == "on" {
				headerOn = i
				break
			}
		}
	}
	for i, token := range parts {
		identifier := strings.HasPrefix(token, "`") || strings.HasPrefix(token, "[")
		if i > 0 && strings.HasPrefix(token, `"`) {
			previous := parts[i-1]
			identifier = previous == kind || previous == "constraint" || previous == "collate" || previous == "references" || previous == "from" || previous == "join" || previous == "into" || i-1 == headerOn
		}
		if identifier {
			quote := token[:1]
			token = strings.ReplaceAll(token[1:len(token)-1], quote+quote, quote)
			parts[i] = githubStarSQLFold(token)
		}
	}
	encoded, _ := json.Marshal(parts)
	return string(encoded)
}

func githubStarSQLFold(value string) string {
	bytes := []byte(value)
	for i, b := range bytes {
		if b >= 'A' && b <= 'Z' {
			bytes[i] += 'a' - 'A'
		}
	}
	return string(bytes)
}

func githubStarSQLTokens(t *testing.T, ddl string) []string {
	t.Helper()
	var tokens []string
	for i := 0; i < len(ddl); {
		if strings.ContainsRune(" \t\r\n\f\v", rune(ddl[i])) {
			i++
			continue
		}
		if strings.HasPrefix(ddl[i:], "--") {
			for i < len(ddl) && ddl[i] != '\n' {
				i++
			}
			continue
		}
		if strings.HasPrefix(ddl[i:], "/*") {
			end := strings.Index(ddl[i+2:], "*/")
			if end < 0 {
				t.Fatal("unclosed SQLite SQL comment")
			}
			i += end + 4
			continue
		}
		start := i
		if strings.ContainsRune("'\"`[", rune(ddl[i])) {
			endQuote := ddl[i]
			if endQuote == '[' {
				endQuote = ']'
			}
			i++
			closed := false
			for i < len(ddl) {
				if ddl[i] == endQuote {
					i++
					if endQuote != ']' && i < len(ddl) && ddl[i] == endQuote {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				t.Fatal("unclosed SQLite SQL quote")
			}
			tokens = append(tokens, ddl[start:i])
			continue
		}
		for i < len(ddl) && (ddl[i] >= 'a' && ddl[i] <= 'z' || ddl[i] >= 'A' && ddl[i] <= 'Z' || ddl[i] >= '0' && ddl[i] <= '9' || ddl[i] == '_' || ddl[i] == '$' || ddl[i] >= 128) {
			i++
		}
		if i == start {
			i++
		}
		tokens = append(tokens, githubStarSQLFold(ddl[start:i]))
	}
	return tokens
}

func githubStarSchemaFixture(t *testing.T, ddl string) *sql.DB {
	t.Helper()
	db := openRawSQLite(t, filepath.Join(t.TempDir(), "schema.db"))
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestGitHubStarSQLiteSchemaEquivalentRepresentations(t *testing.T) {
	for _, sample := range []struct{ name, left, right string }{
		{"whitespace", "CREATE TABLE sample(id TEXT PRIMARY KEY, scope TEXT DEFAULT 'Keep  Case');", "CREATE TABLE sample ( id TEXT PRIMARY KEY,\n scope TEXT DEFAULT 'Keep  Case' );"},
		// Publication schema and PostgreSQL compare columns by name; neither
		// construction path promises SELECT * column order for these tables.
		{"column order", "CREATE TABLE conversations(id TEXT PRIMARY KEY, scope TEXT NOT NULL DEFAULT '', CONSTRAINT allowed CHECK(length(scope)>0));", "CREATE TABLE conversations(scope TEXT NOT NULL DEFAULT '', id TEXT PRIMARY KEY, CONSTRAINT allowed CHECK(length(scope)>0));"},
		{"quoted identifiers", "CREATE TABLE sample(id TEXT); CREATE INDEX ix ON sample(id);", "CREATE TABLE `sample` (`id` TEXT); CREATE INDEX `ix` ON `sample` (`id`);"},
		{"trigger whitespace", "CREATE TABLE sample(id TEXT); CREATE TRIGGER guard BEFORE UPDATE ON sample BEGIN SELECT RAISE(ABORT,'Keep  Case'); END;", "CREATE TABLE sample(id TEXT); CREATE TRIGGER guard BEFORE UPDATE ON sample\n BEGIN SELECT RAISE ( ABORT, 'Keep  Case' ); END;"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			left, right := githubStarSchemaFixture(t, sample.left), githubStarSchemaFixture(t, sample.right)
			if sqliteSchemaFingerprint(t, left) == sqliteSchemaFingerprint(t, right) {
				t.Fatal("fixture must expose the original raw DDL comparison")
			}
			if githubStarSQLiteSchemaFingerprint(t, left) != githubStarSQLiteSchemaFingerprint(t, right) {
				t.Fatal("equivalent SQLite schemas differ")
			}
		})
	}
}

func TestGitHubStarSQLiteSchemaRetainsConstraints(t *testing.T) {
	const schema = `CREATE TABLE parent(a TEXT,b TEXT,PRIMARY KEY(a,b));
CREATE TABLE conversations(id TEXT,scope TEXT NOT NULL DEFAULT 'Keep  Case',value INTEGER DEFAULT 1,
 CONSTRAINT allowed CHECK(value>0 AND length(scope)>0),PRIMARY KEY(id,scope),
 FOREIGN KEY(id,scope) REFERENCES parent(a,b) ON DELETE CASCADE);
CREATE UNIQUE INDEX scoped ON conversations(scope COLLATE NOCASE DESC,id);
CREATE INDEX expression ON conversations(lower(scope)) WHERE value>0;
CREATE VIEW projection AS SELECT id,'View  Case' AS label FROM conversations;
CREATE TRIGGER guard BEFORE UPDATE ON conversations BEGIN SELECT RAISE(ABORT,'Trigger  Case'); END;`
	baseline := githubStarSchemaFixture(t, schema)
	want := githubStarSQLiteSchemaFingerprint(t, baseline)
	for _, sample := range []struct{ name, from, to string }{
		{"type", "value INTEGER", "value TEXT"},
		{"not null", "scope TEXT NOT NULL", "scope TEXT"},
		{"default", "DEFAULT 'Keep  Case'", "DEFAULT 'Keep Case'"},
		{"primary key order", "PRIMARY KEY(id,scope)", "PRIMARY KEY(scope,id)"},
		{"index uniqueness", "UNIQUE INDEX scoped", "INDEX scoped"},
		{"index column order", "scope COLLATE NOCASE DESC,id", "id,scope COLLATE NOCASE DESC"},
		{"index sort order", "NOCASE DESC", "NOCASE ASC"},
		{"index collation", "COLLATE NOCASE", "COLLATE BINARY"},
		{"partial index", "WHERE value>0", "WHERE value>1"},
		{"expression index", "lower(scope)", "upper(scope)"},
		{"foreign key order", "REFERENCES parent(a,b)", "REFERENCES parent(b,a)"},
		{"foreign key action", "ON DELETE CASCADE", "ON DELETE RESTRICT"},
		{"check", "CHECK(value>0", "CHECK(value>1"},
		{"check name", "CONSTRAINT allowed", "CONSTRAINT renamed"},
		{"check literal", "length(scope)>0", "scope<>'Forbidden  Case'"},
		{"column collation", "scope TEXT NOT NULL", "scope TEXT COLLATE NOCASE NOT NULL"},
		{"trigger literal", "'Trigger  Case'", "'Trigger Case'"},
		{"view literal case", "'View  Case'", "'view  Case'"},
		{"table missing", "CREATE TABLE parent(a TEXT,b TEXT,PRIMARY KEY(a,b));", ""},
	} {
		t.Run(sample.name, func(t *testing.T) {
			changed := githubStarSchemaFixture(t, strings.Replace(schema, sample.from, sample.to, 1))
			if sqliteSchemaFingerprint(t, baseline) == sqliteSchemaFingerprint(t, changed) {
				t.Fatal("original comparison did not detect the changed fixture")
			}
			if githubStarSQLiteSchemaFingerprint(t, changed) == want {
				t.Fatal("schema comparison lost a real constraint")
			}
		})
	}
	for _, sample := range []struct{ name, left, right string }{
		{"autoincrement", "CREATE TABLE sequence(id INTEGER PRIMARY KEY AUTOINCREMENT);", "CREATE TABLE sequence(id INTEGER PRIMARY KEY);"},
		{"strict", "CREATE TABLE sample(id TEXT PRIMARY KEY) STRICT;", "CREATE TABLE sample(id TEXT PRIMARY KEY);"},
		{"without rowid", "CREATE TABLE sample(id TEXT PRIMARY KEY) WITHOUT ROWID;", "CREATE TABLE sample(id TEXT PRIMARY KEY);"},
		{"generated expression", "CREATE TABLE sample(id TEXT, computed TEXT AS (id||'A') VIRTUAL);", "CREATE TABLE sample(id TEXT, computed TEXT AS (id||'a') VIRTUAL);"},
		{"hidden column", "CREATE TABLE sample(id TEXT, computed TEXT AS (id) VIRTUAL);", "CREATE TABLE sample(id TEXT, computed TEXT AS (id) STORED);"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			left, right := githubStarSchemaFixture(t, sample.left), githubStarSchemaFixture(t, sample.right)
			if githubStarSQLiteSchemaFingerprint(t, left) == githubStarSQLiteSchemaFingerprint(t, right) {
				t.Fatal("schema comparison lost a table property")
			}
		})
	}
}

func TestGitHubStarSQLiteSchemaConstraintIdentity(t *testing.T) {
	for _, sample := range []struct{ name, left, right string }{
		{"check name binding", "CREATE TABLE conversations(value INTEGER, CONSTRAINT lower_bound CHECK(value>0), CONSTRAINT upper_bound CHECK(value<10));", "CREATE TABLE conversations(value INTEGER, CONSTRAINT upper_bound CHECK(value>0), CONSTRAINT lower_bound CHECK(value<10));"},
		{"trigger double quoted literal", `CREATE TABLE sample(id TEXT); CREATE TRIGGER guard BEFORE UPDATE ON sample BEGIN SELECT 1 FROM sample AS a JOIN sample AS b ON "UPPER"='UPPER'; END;`, `CREATE TABLE sample(id TEXT); CREATE TRIGGER guard BEFORE UPDATE ON sample BEGIN SELECT 1 FROM sample AS a JOIN sample AS b ON "upper"='UPPER'; END;`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			left, right := githubStarSchemaFixture(t, sample.left), githubStarSchemaFixture(t, sample.right)
			if sqliteSchemaFingerprint(t, left) == sqliteSchemaFingerprint(t, right) {
				t.Fatal("original comparison did not detect changed constraint identity")
			}
			if githubStarSQLiteSchemaFingerprint(t, left) == githubStarSQLiteSchemaFingerprint(t, right) {
				t.Fatal("schema comparison lost constraint identity or literal case")
			}
		})
	}
}
