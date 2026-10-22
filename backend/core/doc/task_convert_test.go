package doc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNeedsOfficeConvertBeforeParse(t *testing.T) {
	pptExt := documentExt{
		StoredPath:       "/data/demo.pptx",
		OriginalFilename: "demo.pptx",
		ContentType:      "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		ConvertRequired:  true,
	}
	pptmExt := documentExt{
		StoredPath:       "/data/demo.pptm",
		OriginalFilename: "demo.pptm",
		ContentType:      "application/vnd.ms-powerpoint.presentation.macroEnabled.12",
		ConvertRequired:  true,
	}
	docExt := documentExt{
		StoredPath:       "/data/demo.docx",
		OriginalFilename: "demo.docx",
		ContentType:      "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		ConvertRequired:  true,
	}
	xlsxExt := documentExt{
		StoredPath:       "/data/demo.xlsx",
		OriginalFilename: "demo.xlsx",
		ContentType:      "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		ConvertRequired:  true,
	}
	officialMineruCfg := map[string]any{"ocr_type": "mineru", "ocr_url": "https://mineru.net/api/v4/"}
	officialMineruEmptyURLCfg := map[string]any{"ocr_type": "mineru"}
	selfHostedMineruCfg := map[string]any{"ocr_type": "mineru", "ocr_url": "http://172.24.176.1:20234/api/v1/pdf_parse"}
	paddleCfg := map[string]any{"ocr_type": "paddleocr", "ocr_url": "http://paddle:8000"}

	tests := []struct {
		name      string
		doc       documentExt
		ocrConfig map[string]any
		want      bool
	}{
		{"ppt with official mineru skips convert", pptExt, officialMineruCfg, false},
		{"ppt with official mineru empty url skips convert", pptExt, officialMineruEmptyURLCfg, false},
		{"ppt with self-hosted mineru converts", pptExt, selfHostedMineruCfg, true},
		{"ppt with paddle converts", pptExt, paddleCfg, true},
		{"ppt without ocr config converts", pptExt, nil, true},
		{"pptm with official mineru converts", pptmExt, officialMineruCfg, true},
		{"pptm with self-hosted mineru converts", pptmExt, selfHostedMineruCfg, true},
		{"pptm with paddle converts", pptmExt, paddleCfg, true},
		{"pptm without ocr config converts", pptmExt, nil, true},
		{"docx with official mineru still converts", docExt, officialMineruCfg, true},
		{"xlsx with paddle parses directly", xlsxExt, paddleCfg, false},
		{"xlsx with official mineru parses directly", xlsxExt, officialMineruCfg, false},
		{"non-office never converts", documentExt{StoredPath: "/data/demo.pdf", ConvertRequired: false}, officialMineruCfg, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := needsOfficeConvertBeforeParse(tt.doc, tt.ocrConfig, documentParseProfileCloud); got != tt.want {
				t.Fatalf("needsOfficeConvertBeforeParse() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParsePathForIngestionPresentationMineru(t *testing.T) {
	d := documentExt{
		StoredPath:       "/data/demo.pptx",
		ParseStoredPath:  "/data/demo.pdf",
		OriginalFilename: "demo.pptx",
		ConvertRequired:  true,
	}
	cfg := map[string]any{"ocr_type": "mineru"}
	if got := parsePathForIngestion(d, cfg, documentParseProfileCloud); got != "/data/demo.pptx" {
		t.Fatalf("parsePathForIngestion() = %q, want original pptx path", got)
	}
}

func TestParsePathForIngestionMacroPresentationMineru(t *testing.T) {
	d := documentExt{
		StoredPath:       "/data/demo.pptm",
		ParseStoredPath:  "/data/demo.pdf",
		OriginalFilename: "demo.pptm",
		ConvertRequired:  true,
	}
	cfg := map[string]any{"ocr_type": "mineru"}
	if got := parsePathForIngestion(d, cfg, documentParseProfileCloud); got != "/data/demo.pdf" {
		t.Fatalf("parsePathForIngestion() = %q, want converted pdf path", got)
	}
}

func TestNewDocumentExtSpreadsheetParsesDirectly(t *testing.T) {
	d := newDocumentExt("/data/demo.xlsx", "demo.xlsx", "demo.xlsx", 100, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "", nil)
	if d.ConvertRequired {
		t.Fatal("xlsx upload should not require office conversion")
	}
	if d.ConvertStatus != ConvertStatusNone {
		t.Fatalf("xlsx ConvertStatus = %q, want %q", d.ConvertStatus, ConvertStatusNone)
	}
}

func TestParsePathForIngestionSpreadsheet(t *testing.T) {
	d := documentExt{
		StoredPath:       "/data/demo.xlsx",
		ParseStoredPath:  "/data/demo.pdf",
		OriginalFilename: "demo.xlsx",
		ConvertRequired:  true,
	}
	if got := parsePathForIngestion(d, nil, documentParseProfileCloud); got != "/data/demo.xlsx" {
		t.Fatalf("parsePathForIngestion() = %q, want original spreadsheet path", got)
	}
}

func TestParsePathForIngestionPresentationPaddle(t *testing.T) {
	d := documentExt{
		StoredPath:       "/data/demo.pptx",
		ParseStoredPath:  "/data/demo.pdf",
		OriginalFilename: "demo.pptx",
		ConvertRequired:  true,
	}
	cfg := map[string]any{"ocr_type": "paddleocr"}
	if got := parsePathForIngestion(d, cfg, documentParseProfileCloud); got != "/data/demo.pdf" {
		t.Fatalf("parsePathForIngestion() = %q, want converted pdf path", got)
	}
}

func TestParsePathForIngestionPresentationSelfHostedMineru(t *testing.T) {
	d := documentExt{
		StoredPath:       "/data/demo.pptx",
		ParseStoredPath:  "/data/demo.pdf",
		OriginalFilename: "demo.pptx",
		ConvertRequired:  true,
	}
	cfg := map[string]any{"ocr_type": "mineru", "ocr_url": "http://local-mineru:8000/api/v1/pdf_parse"}
	if got := parsePathForIngestion(d, cfg, documentParseProfileCloud); got != "/data/demo.pdf" {
		t.Fatalf("parsePathForIngestion() = %q, want converted pdf path", got)
	}
}

func TestParsePathForIngestionMacroPresentationSelfHostedMineru(t *testing.T) {
	d := documentExt{
		StoredPath:       "/data/demo.pptm",
		ParseStoredPath:  "/data/demo.pdf",
		OriginalFilename: "demo.pptm",
		ConvertRequired:  true,
	}
	cfg := map[string]any{"ocr_type": "mineru", "ocr_url": "http://local-mineru:8000/api/v1/pdf_parse"}
	if got := parsePathForIngestion(d, cfg, documentParseProfileCloud); got != "/data/demo.pdf" {
		t.Fatalf("parsePathForIngestion() = %q, want converted pdf path", got)
	}
}

func TestLocalParseProfileOfficeWithOCRUsesNativeReader(t *testing.T) {
	d := documentExt{
		StoredPath:       "/data/demo.docx",
		ParseStoredPath:  "/data/demo.pdf",
		OriginalFilename: "demo.docx",
		ContentType:      "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		ConvertRequired:  true,
	}
	for _, cfg := range []map[string]any{
		{"ocr_type": "mineru", "ocr_url": "http://local-mineru:8000/api/v1/pdf_parse"},
		{"ocr_type": "paddleocr", "ocr_url": "https://paddleocr.aistudio-app.com/api/v2/ocr/jobs"},
	} {
		if got := needsOfficeConvertBeforeParse(d, cfg, documentParseProfileLocal); got {
			t.Fatalf("needsOfficeConvertBeforeParse() = true for cfg %#v, want false", cfg)
		}
		if got := parsePathForIngestion(d, cfg, documentParseProfileLocal); got != "/data/demo.docx" {
			t.Fatalf("parsePathForIngestion() = %q, want original docx path", got)
		}
	}
}

func TestDesktopOfficeDirectParseClearsLegacyConversionState(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv(documentParseProfileEnv, "cloud")
	if resolveDocumentParseProfile() != documentParseProfileLocal {
		t.Fatal("desktop must use native Office readers")
	}
	for _, suffix := range []string{".docx", ".pptx"} {
		fresh := newDocumentExt("/data/new"+suffix, "new"+suffix, "new"+suffix, 100, "", "", nil)
		if fresh.ConvertRequired || fresh.ConvertStatus != ConvertStatusNone {
			t.Fatalf("desktop upload must not queue conversion: %#v", fresh)
		}
		d := documentExt{
			StoredPath: "/data/demo" + suffix, ConvertRequired: true,
			ConvertStatus: ConvertStatusFailed, ConvertError: "missing office",
			ParseStoredPath: "/data/demo.pdf", ParseStoredName: "demo.pdf",
			ParseContentType: "application/pdf", ParseFileSize: 100,
		}
		if !normalizeLocalOfficeDirectParse(&d, documentParseProfileLocal) {
			t.Fatal("expected legacy conversion state to change")
		}
		if d.ConvertRequired || d.ConvertStatus != ConvertStatusNone || d.ConvertError != "" ||
			d.ParseStoredPath != "" || d.ParseStoredName != "" || d.ParseContentType != "" || d.ParseFileSize != 0 {
			t.Fatalf("stale conversion state: %#v", d)
		}
		if normalizeLocalOfficeDirectParse(&d, documentParseProfileLocal) {
			t.Fatal("normalization must be idempotent")
		}
	}
}

func TestLocalParseProfileOfficialMinerUPresentationSkipsConvert(t *testing.T) {
	d := documentExt{
		StoredPath:       "/data/demo.pptx",
		ParseStoredPath:  "/data/demo.pdf",
		OriginalFilename: "demo.pptx",
		ContentType:      "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		ConvertRequired:  true,
	}
	cfg := map[string]any{"ocr_type": "mineru", "ocr_url": "https://mineru.net/api/v4/"}
	if got := needsOfficeConvertBeforeParse(d, cfg, documentParseProfileLocal); got {
		t.Fatal("needsOfficeConvertBeforeParse() = true, want false")
	}
	if got := parsePathForIngestion(d, cfg, documentParseProfileLocal); got != "/data/demo.pptx" {
		t.Fatalf("parsePathForIngestion() = %q, want original pptx path", got)
	}
}

func TestLocalParseProfileSpreadsheetWithoutOCRParsesDirectly(t *testing.T) {
	d := documentExt{
		StoredPath:       "/data/demo.xlsx",
		ParseStoredPath:  "/data/demo.pdf",
		OriginalFilename: "demo.xlsx",
		ConvertRequired:  true,
	}
	if got := needsOfficeConvertBeforeParse(d, nil, documentParseProfileLocal); got {
		t.Fatal("needsOfficeConvertBeforeParse() = true, want direct spreadsheet parse")
	}
	if got := parsePathForIngestion(d, nil, documentParseProfileLocal); got != "/data/demo.xlsx" {
		t.Fatalf("parsePathForIngestion() = %q, want original spreadsheet path", got)
	}
}

func TestOfficeConversionGuardResetsLegacySpreadsheetState(t *testing.T) {
	d := documentExt{
		StoredPath:       "/data/demo.xlsx",
		OriginalFilename: "demo.xlsx",
		ParseStoredPath:  "/data/demo.pdf",
		ParseStoredName:  "demo.pdf",
		ParseContentType: "application/pdf",
		ParseFileSize:    123,
		ConvertRequired:  true,
		ConvertStatus:    ConvertStatusPending,
		ConvertError:     "stale error",
		ConvertProvider:  convertProviderHTTP,
	}

	callOfficeConvertWithRetry(context.Background(), &d, documentParseProfileCloud)

	if d.ConvertRequired || d.ConvertStatus != ConvertStatusNone {
		t.Fatalf("spreadsheet conversion state was not reset: %#v", d)
	}
	if d.ConvertError != "" || d.ConvertProvider != "" {
		t.Fatalf("spreadsheet conversion diagnostics were not cleared: %#v", d)
	}
	if d.ParseStoredPath != "" || d.ParseStoredName != "" || d.ParseContentType != "" || d.ParseFileSize != 0 {
		t.Fatalf("legacy spreadsheet PDF fields were not cleared: %#v", d)
	}
}

func TestLocalLibreOfficeFallbackUserErrorRecommendsOnlineParsing(t *testing.T) {
	msg := officeConvertUserError(documentParseProfileLocal, "convert failed")
	if !testStringContainsAll(msg, "convert failed", "MinerU/PaddleOCR", "LibreOffice") {
		t.Fatalf("unexpected message: %q", msg)
	}
}

func TestLocalConvertMissingLibreOfficeDoesNotRequireHTTPURL(t *testing.T) {
	t.Setenv("LAZYMIND_LIBREOFFICE_PATH", filepath.Join(t.TempDir(), "missing-soffice"))
	t.Setenv("LAZYMIND_OFFICE_CONVERT_URL", "")

	_, provider, err := convertOfficeToPDF(context.Background(), documentParseProfileLocal, "/data/demo.docx", "/data/demo.pdf")
	if err == nil {
		t.Fatal("expected missing LibreOffice error")
	}
	if provider != convertProviderLibreOffice {
		t.Fatalf("provider = %q, want %q", provider, convertProviderLibreOffice)
	}
	if !strings.Contains(err.Error(), "LAZYMIND_LIBREOFFICE_PATH") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCloudConvertStillRequiresHTTPURL(t *testing.T) {
	t.Setenv("LAZYMIND_OFFICE_CONVERT_URL", "")

	_, provider, err := convertOfficeToPDF(context.Background(), documentParseProfileCloud, "/data/demo.docx", "/data/demo.pdf")
	if err == nil {
		t.Fatal("expected missing office convert URL error")
	}
	if provider != convertProviderHTTP {
		t.Fatalf("provider = %q, want %q", provider, convertProviderHTTP)
	}
	if !strings.Contains(err.Error(), "LAZYMIND_OFFICE_CONVERT_URL") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDetectLibreOfficeUsesEnvOverride(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "soffice")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake soffice: %v", err)
	}
	t.Setenv("LAZYMIND_LIBREOFFICE_PATH", bin)

	detected := detectLibreOffice()
	if !detected.Found || detected.Path != bin || detected.Source != "LAZYMIND_LIBREOFFICE_PATH" {
		t.Fatalf("unexpected detection: %#v", detected)
	}
}

func TestLibreOfficePathCandidatesCoverMacOSAndWindows(t *testing.T) {
	mac := libreOfficePathCandidates("darwin", func(string) string { return "" })
	if len(mac) != 1 || mac[0] != "/Applications/LibreOffice.app/Contents/MacOS/soffice" {
		t.Fatalf("unexpected mac candidates: %v", mac)
	}
	win := libreOfficePathCandidates("windows", func(key string) string {
		switch key {
		case "ProgramFiles":
			return `C:\Program Files`
		case "ProgramFiles(x86)":
			return `C:\Program Files (x86)`
		default:
			return ""
		}
	})
	joined := strings.Join(win, "\n")
	if !strings.Contains(joined, `C:\Program Files`) || !strings.Contains(joined, "soffice.exe") {
		t.Fatalf("unexpected windows candidates: %v", win)
	}
}

func TestLibreOfficeProfileURI(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "unix absolute", path: "/tmp/lo-profile-123", want: "file:///tmp/lo-profile-123"},
		{name: "windows drive", path: `C:\Users\me\AppData\Local\Temp\lo-profile-123`, want: "file:///C:/Users/me/AppData/Local/Temp/lo-profile-123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := libreOfficeProfileURI(tt.path); got != tt.want {
				t.Fatalf("libreOfficeProfileURI() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUnknownDocumentParseProfileFallsBackToCloud(t *testing.T) {
	profile, ok := normalizeDocumentParseProfile("surprise")
	if ok {
		t.Fatal("unknown profile should not be accepted")
	}
	if profile != documentParseProfileCloud {
		t.Fatalf("profile = %q, want cloud", profile)
	}
}

func testStringContainsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
