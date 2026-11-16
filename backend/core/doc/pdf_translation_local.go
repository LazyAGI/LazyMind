package doc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"lazymind/core/common"
	"lazymind/core/systemdeps"
)

var prepareLocalTranslationFont = systemdeps.EnsurePDFFont

// Local desktop bundles already contain this converter and its base Python
// dependencies. Run only the requested operation instead of requiring a daemon.
const localPDFTranslationScript = `
import json, os, pathlib, sys
sys.path.insert(0, sys.argv[1])
os.environ['OFFICE_CONVERT_TRANSLATION_FONT_PATH'] = sys.argv[2]
import main
request = json.load(sys.stdin)
# Core has authorized these server-owned paths. Do not inherit broader service
# roots or grant access to any other files in the user's home directory.
roots = {pathlib.Path(value).resolve().parent for key, value in request.items() if key.endswith('_path')}
main._allowed_roots = lambda: list(roots)
allowed_paths = {pathlib.Path(value).resolve() for key, value in request.items() if key.endswith('_path')}
validate_path = main._validate_shared_path
def validate_exact_path(raw_path, *, must_exist=True):
    path = pathlib.Path(raw_path).resolve()
    if path not in allowed_paths:
        raise ValueError('path is outside authorized operation')
    return validate_path(raw_path, must_exist=must_exist)
main._validate_shared_path = validate_exact_path
if sys.argv[3] == 'extract-translation-layout':
    result = main.extract_pdf_translation_layout(main.PDFTranslationLayoutRequest(**request))
elif sys.argv[3] == 'render-translation':
    result = main.render_pdf_translation(main.PDFTranslationRenderRequest(**request))
else:
    raise ValueError('unsupported PDF translation operation')
print(json.dumps(result, ensure_ascii=False))
`

func callPDFTranslationOperation(ctx context.Context, operation string, payload map[string]string, result any, timeout time.Duration) error {
	if operation != "extract-translation-layout" && operation != "render-translation" {
		return fmt.Errorf("unsupported PDF translation operation")
	}
	if !systemdeps.IsLocalRuntime() {
		serviceURL := strings.Replace(strings.TrimSpace(os.Getenv("LAZYMIND_OFFICE_CONVERT_URL")), "/v1/office/to-pdf", "/v1/pdf/"+operation, 1)
		if serviceURL == "" {
			return fmt.Errorf("PDF translation service is not configured")
		}
		return common.ApiPost(ctx, serviceURL, payload, nil, result, timeout)
	}
	python := strings.TrimSpace(os.Getenv("LAZYMIND_ALGORITHM_PYTHON"))
	source := strings.TrimSpace(os.Getenv("LAZYMIND_ALGORITHM_SOURCE"))
	if python == "" || source == "" {
		return fmt.Errorf("本地 PDF 翻译运行环境不可用，请修复或重新安装桌面应用后重试")
	}
	converter := filepath.Join(filepath.Dir(source), "backend", "office-convert-service")
	if info, err := os.Stat(filepath.Join(converter, "main.py")); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("本地 PDF 翻译组件缺失，请修复或重新安装桌面应用后重试")
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	font, err := prepareLocalTranslationFont(runCtx)
	if err != nil {
		return fmt.Errorf("PDF 翻译字体准备失败，请检查网络后重试: %w", err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(runCtx, python, "-I", "-B", "-c", localPDFTranslationScript, converter, font, operation)
	cmd.Stdin = bytes.NewReader(raw)
	var output bytes.Buffer
	cmd.Stdout = &output
	// Converter diagnostics can contain document text or private filesystem paths.
	// Return a bounded actionable error instead of exposing raw stderr.
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			return runCtx.Err()
		}
		return fmt.Errorf("本地 PDF 翻译排版失败，请重试；若仍失败，请修复桌面应用的 Python 运行环境")
	}
	if err := json.Unmarshal(output.Bytes(), result); err != nil {
		return fmt.Errorf("本地 PDF 翻译组件返回了无效结果")
	}
	return nil
}
