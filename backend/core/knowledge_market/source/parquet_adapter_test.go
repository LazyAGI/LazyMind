package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"
)

type chinataxTestRow struct {
	Title          string `parquet:"title,optional"`
	Channel        string `parquet:"channel,optional"`
	Content        string `parquet:"content,optional"`
	DocumentNumber string `parquet:"document_number,optional"`
	EffectLevel    string `parquet:"effect_level,optional"`
	TaxType        string `parquet:"tax_type,optional"`
	Aging          string `parquet:"aging,optional"`
	Labels         string `parquet:"labels,optional"`
	IssuingDept    string `parquet:"issuing_department,optional"`
	WrittenDate    string `parquet:"written_date,optional"`
	URL            string `parquet:"url,optional"`
}

func writeTestChinataxParquet(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create parquet: %v", err)
	}
	defer f.Close()

	writer := parquet.NewWriter(f)
	for _, row := range []chinataxTestRow{
		{Title: "第一条政策", DocumentNumber: "财税〔2026〕1号", EffectLevel: "规范性文件", TaxType: "增值税", Content: "第一条政策正文"},
		{Title: "第二条政策", DocumentNumber: "国家税务总局公告2026年第2号", EffectLevel: "部门规章", TaxType: "企业所得税", Content: "第二条政策正文"},
	} {
		if err := writer.Write(row); err != nil {
			t.Fatalf("write parquet row: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close parquet writer: %v", err)
	}
}

func TestChinataxPolicyAdapterMaterializesRows(t *testing.T) {
	root := t.TempDir()
	parquetPath := filepath.Join(root, "train-00000-of-00001.parquet")
	writeTestChinataxParquet(t, parquetPath)

	adapter, err := New(AdapterChinataxPolicy, nil)
	if err != nil {
		t.Fatalf("new chinatax adapter: %v", err)
	}
	files := []FileEntry{{Path: "train-00000-of-00001.parquet", Size: 0}}
	units, err := adapter.Materialize(context.Background(), root, files)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("expected 2 units, got %d", len(units))
	}
	for _, unit := range units {
		if _, err := os.Stat(unit.LocalPath); err != nil {
			t.Fatalf("generated file %s: %v", unit.LocalPath, err)
		}
		if unit.DisplayName == "" || len(unit.Tags) == 0 {
			t.Fatalf("unexpected unit %+v", unit)
		}
	}
}

func TestChinataxPolicyMatch(t *testing.T) {
	adapter, err := New(AdapterChinataxPolicy, nil)
	if err != nil {
		t.Fatalf("new chinatax adapter: %v", err)
	}
	if !adapter.Match("data/train.parquet") || !adapter.Match("train.parquet") {
		t.Fatal("expected parquet files to match")
	}
	if adapter.Match("data/train.jsonl") || adapter.Match(".hidden.parquet") {
		t.Fatal("expected non-parquet and hidden files to not match")
	}
}
