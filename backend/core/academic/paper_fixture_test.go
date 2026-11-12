package academic

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func migratePaperFixtureDB(t testing.TB) *orm.DB {
	t.Helper()
	db := orm.OpenTestDB(t)
	// OpenTestDB provides an empty schema/file: skip repeated existence probes and
	// commit all DDL before callers start their own business transactions.
	if err := db.Transaction(func(tx *gorm.DB) error {
		return tx.Migrator().CreateTable(orm.AllModelsForDDL()...)
	}); err != nil {
		t.Fatalf("migrate paper fixture: %v", err)
	}
	return db
}

func TestPaperFixtureSchemaIsolationAndTransactions(t *testing.T) {
	start := time.Now()
	legacy := orm.MigrateAllModelsForTest(t)
	legacyElapsed := time.Since(start)
	start = time.Now()
	db := migratePaperFixtureDB(t)
	t.Logf("full-model migration: legacy=%s transaction=%s", legacyElapsed, time.Since(start))
	want, got := paperFixtureSchema(t, legacy), paperFixtureSchema(t, db)
	if len(want) != len(got) {
		t.Fatalf("schema definition count changed: legacy=%d transaction=%d", len(want), len(got))
	}
	for index := range want {
		if want[index] != got[index] {
			t.Fatalf("schema differs at %d: legacy=%s transaction=%s", index, want[index], got[index])
		}
	}

	work := orm.AcademicWork{ID: "fixture-committed", CanonicalTitle: "Committed", NormalizedTitle: "committed", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)}
	if err := db.Transaction(func(tx *gorm.DB) error { return tx.Create(&work).Error }); err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var count int
	if err := pool.QueryRowContext(ctx, "SELECT COUNT(*) FROM academic_works WHERE id = 'fixture-committed'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("business commit must be visible outside the migration transaction: count=%d err=%v", count, err)
	}
	var peerCount int64
	if err := legacy.Model(&orm.AcademicWork{}).Count(&peerCount).Error; err != nil || peerCount != 0 {
		t.Fatalf("fixture data leaked to another database: count=%d err=%v", peerCount, err)
	}
	rollback := errors.New("roll back business transaction")
	work.ID = "fixture-rolled-back"
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&work).Error; err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("business rollback failed: %v", err)
	}
	if err := db.Model(&orm.AcademicWork{}).Where("id=?", work.ID).Count(&peerCount).Error; err != nil || peerCount != 0 {
		t.Fatalf("rolled-back data remained: count=%d err=%v", peerCount, err)
	}
}

func paperFixtureSchema(t *testing.T, db *orm.DB) []string {
	t.Helper()
	queries := []string{`SELECT type || ':' || name || ':' || tbl_name || ':' || COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`}
	schema := ""
	if db.Dialector.Name() == "postgres" {
		if err := db.Raw("SELECT current_schema()").Scan(&schema).Error; err != nil {
			t.Fatal(err)
		}
		queries = []string{
			`SELECT json_build_array(table_name, column_name, data_type, udt_name, is_nullable, column_default, character_maximum_length, numeric_precision, numeric_scale)::text FROM information_schema.columns WHERE table_schema = current_schema()`,
			`SELECT json_build_array(tablename, indexname, indexdef)::text FROM pg_indexes WHERE schemaname = current_schema()`,
			`SELECT json_build_array(rel.relname, con.conname, con.contype, pg_get_constraintdef(con.oid))::text FROM pg_constraint con JOIN pg_class rel ON rel.oid = con.conrelid JOIN pg_namespace ns ON ns.oid = rel.relnamespace WHERE ns.nspname = current_schema()`,
		}
	}
	var definitions []string
	for index, query := range queries {
		var records []string
		if err := db.Raw(query).Scan(&records).Error; err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if schema != "" {
				record = strings.ReplaceAll(record, schema, "fixture_schema")
			} else {
				// GORM emits table constraints from a map; declaration order is immaterial.
				end := strings.LastIndex(record, ")")
				if end >= 0 {
					if columns, constraints, ok := strings.Cut(record[:end], ",CONSTRAINT "); ok {
						parts := strings.Split(constraints, ",CONSTRAINT ")
						sort.Strings(parts)
						record = columns + ",CONSTRAINT " + strings.Join(parts, ",CONSTRAINT ") + record[end:]
					}
				}
			}
			definitions = append(definitions, strconv.Itoa(index)+":"+record)
		}
	}
	sort.Strings(definitions)
	return definitions
}
