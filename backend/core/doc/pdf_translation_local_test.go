package doc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalPDFTranslationExtractsAndRendersChinese(t *testing.T) {
	python := os.Getenv("LAZYMIND_TEST_PDF_PYTHON")
	if python == "" {
		python, _ = exec.LookPath("python3")
	}
	if python == "" {
		t.Skip("Python unavailable; set LAZYMIND_TEST_PDF_PYTHON")
	}
	if err := exec.Command(python, "-I", "-B", "-c", "import fitz, fastapi, fontTools").Run(); err != nil {
		if os.Getenv("LAZYMIND_TEST_PDF_PYTHON") != "" {
			t.Fatalf("requested Python missing PDF dependencies: %v", err)
		}
		t.Skip("PDF Python dependencies unavailable")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	font := filepath.Join(root, "frontend/public/fonts/NotoSansSC-wght.ttf")
	if _, err := os.Stat(font); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv("LAZYMIND_ALGORITHM_PYTHON", python)
	t.Setenv("LAZYMIND_ALGORITHM_SOURCE", filepath.Join(root, "algorithm"))
	old := prepareLocalTranslationFont
	prepareLocalTranslationFont = func(context.Context) (string, error) { return font, nil }
	t.Cleanup(func() { prepareLocalTranslationFont = old })
	dir := t.TempDir()
	source := filepath.Join(dir, "source.pdf")
	layout := filepath.Join(dir, "layout.json")
	translations := filepath.Join(dir, "translations.json")
	output := filepath.Join(dir, "translated.pdf")
	script := `import fitz,sys
pdf=fitz.open(); p=pdf.new_page(); p.insert_text((72,100), 'Reasoning segmentation evaluates difficult visual understanding tasks.'); pdf.save(sys.argv[1])`
	if out, err := exec.Command(python, "-I", "-B", "-c", script, source).CombinedOutput(); err != nil {
		t.Fatalf("create fixture: %v %s", err, out)
	}
	before, _ := os.ReadFile(source)
	var extracted struct {
		BlockCount int `json:"block_count"`
	}
	if err := callPDFTranslationOperation(context.Background(), "extract-translation-layout", map[string]string{"source_path": source, "output_path": layout}, &extracted, time.Minute); err != nil {
		t.Fatal(err)
	}
	if extracted.BlockCount < 1 {
		t.Fatal("no extracted blocks")
	}
	raw, err := os.ReadFile(layout)
	if err != nil {
		t.Fatal(err)
	}
	var manifest translationLayoutManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	translated := map[string]string{}
	for _, b := range manifest.Blocks {
		translated[b.ID] = "推理分割用于评估复杂的视觉理解任务。"
	}
	raw, _ = json.Marshal(translated)
	if err := os.WriteFile(translations, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var rendered struct {
		Rendered  int  `json:"rendered_block_count"`
		Requested int  `json:"requested_block_count"`
		Preserved bool `json:"layout_preserved"`
	}
	if err := callPDFTranslationOperation(context.Background(), "render-translation", map[string]string{"source_path": source, "layout_path": layout, "translations_path": translations, "output_path": output, "target_language": "zh"}, &rendered, time.Minute); err != nil {
		t.Fatal(err)
	}
	if rendered.Rendered != len(translated) || rendered.Requested != len(translated) || !rendered.Preserved {
		t.Fatalf("render result: %+v", rendered)
	}
	out, err := exec.Command(python, "-I", "-B", "-c", `import fitz,sys,unicodedata; d=fitz.open(sys.argv[1]); assert len(d)==1; print(unicodedata.normalize("NFKC",d[0].get_text()))`, output).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "推理分割") {
		t.Fatalf("Chinese output missing: %v %s", err, out)
	}
	after, _ := os.ReadFile(source)
	if string(before) != string(after) {
		t.Fatal("source PDF changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := callPDFTranslationOperation(ctx, "extract-translation-layout", map[string]string{"source_path": source, "output_path": filepath.Join(dir, "cancelled.json")}, &extracted, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cancelled.json")); !os.IsNotExist(err) {
		t.Fatal("cancelled operation wrote output")
	}

	t.Run("killsRunningConverterOnDeadline", func(t *testing.T) {
		runtimeSource := t.TempDir()
		converter := filepath.Join(runtimeSource, "backend", "office-convert-service")
		if err := os.MkdirAll(converter, 0700); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(runtimeSource, "started")
		markerJSON, _ := json.Marshal(marker)
		code := "import pathlib,time\npathlib.Path(" + string(markerJSON) + ").write_text('started')\ntime.sleep(60)\n"
		if err := os.WriteFile(filepath.Join(converter, "main.py"), []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LAZYMIND_ALGORITHM_SOURCE", filepath.Join(runtimeSource, "algorithm"))
		started := time.Now()
		err := callPDFTranslationOperation(context.Background(), "extract-translation-layout", map[string]string{}, &extracted, time.Second)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline: %v", err)
		}
		if time.Since(started) > 3*time.Second {
			t.Fatal("converter did not terminate promptly")
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatal("converter never started", err)
		}
	})
}

func TestCloudPDFTranslationKeepsHTTPService(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "cloud")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/pdf/extract-translation-layout" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"block_count":2}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_OFFICE_CONVERT_URL", server.URL+"/v1/office/to-pdf")
	var result struct {
		Count int `json:"block_count"`
	}
	if err := callPDFTranslationOperation(context.Background(), "extract-translation-layout", map[string]string{}, &result, time.Second); err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 {
		t.Fatal(result)
	}
}
