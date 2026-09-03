package common

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAppErrorWithDetailReturnsCopy(t *testing.T) {
	base := NewAppError(422, 2000228, "model not configured")
	detail := map[string]any{"reason": "model_not_configured"}

	detailed := base.WithDetail(detail)

	if detailed == base {
		t.Fatal("WithDetail returned the original AppError")
	}
	if base.Detail != nil {
		t.Fatalf("base Detail = %#v, want nil", base.Detail)
	}
	if detailed.Detail.(map[string]any)["reason"] != "model_not_configured" {
		t.Fatalf("Detail = %#v", detailed.Detail)
	}
}

func TestErrorCatalogCodeHasSingleMessage(t *testing.T) {
	messagesByCode := make(map[int]string)
	for key, appErr := range errorCatalog {
		assertCatalogCodeMessage(t, messagesByCode, key, appErr)
	}
	for _, pattern := range errorPatterns {
		assertCatalogCodeMessage(t, messagesByCode, pattern.template, pattern.appErr)
	}
}

func TestRequiredErrorAliasesUseSingleCode(t *testing.T) {
	codesBySemantic := make(map[string]int)
	for key, appErr := range errorCatalog {
		semantic := requiredErrorSemantic(key)
		if semantic == "" {
			continue
		}
		if previous, exists := codesBySemantic[semantic]; exists && previous != appErr.Code {
			t.Errorf("required-field semantic %q uses both codes %d and %d (catalog key %q)", semantic, previous, appErr.Code, key)
			continue
		}
		codesBySemantic[semantic] = appErr.Code
	}
}

func requiredErrorSemantic(message string) string {
	message = normalizeErrorKey(message)
	if strings.HasPrefix(message, "missing ") {
		return strings.TrimSpace(strings.TrimPrefix(message, "missing "))
	}
	if strings.HasSuffix(message, " missing") {
		return strings.TrimSpace(strings.TrimSuffix(message, " missing"))
	}
	for _, suffix := range []string{" is required", " are required", " required"} {
		if strings.HasSuffix(message, suffix) {
			return strings.TrimSpace(strings.TrimSuffix(message, suffix))
		}
	}
	return ""
}

func assertCatalogCodeMessage(t *testing.T, messagesByCode map[int]string, key string, appErr *AppError) {
	t.Helper()
	if previous, exists := messagesByCode[appErr.Code]; exists && previous != appErr.Message {
		t.Errorf("error code %d maps to both %q and %q (catalog key %q)", appErr.Code, previous, appErr.Message, key)
		return
	}
	messagesByCode[appErr.Code] = appErr.Message
}

func TestErrorCatalogCodesHaveTranslations(t *testing.T) {
	data, err := os.ReadFile("../../../i18n/errors/core.json")
	if err != nil {
		t.Fatal(err)
	}
	translations := map[string]map[string]string{}
	if err := json.Unmarshal(data, &translations); err != nil {
		t.Fatal(err)
	}
	for key, appErr := range errorCatalog {
		assertCatalogTranslations(t, translations, key, appErr)
	}
	for _, pattern := range errorPatterns {
		assertCatalogTranslations(t, translations, pattern.template, pattern.appErr)
	}
}

func assertCatalogTranslations(t *testing.T, translations map[string]map[string]string, key string, appErr *AppError) {
	t.Helper()
	code := strconv.Itoa(appErr.Code)
	messages, exists := translations[code]
	if !exists {
		t.Errorf("catalog key %q uses code %s without i18n", key, code)
		return
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		if strings.TrimSpace(messages[locale]) == "" {
			t.Errorf("catalog key %q code %s is missing %s", key, code, locale)
		}
	}
}

func TestResolveAppErrorUsesSpecificCodeAndKeepsDetail(t *testing.T) {
	appErr := ResolveAppError("cannot write file over directory: 测试", 400)
	if appErr.Code != 2001328 {
		t.Fatalf("Code = %d, want 2001328", appErr.Code)
	}
	if appErr.Message != "cannot write file over directory" {
		t.Fatalf("Message = %q", appErr.Message)
	}
	if appErr.Detail != "测试" {
		t.Fatalf("Detail = %#v, want 测试", appErr.Detail)
	}
}

func TestResolveAppErrorPreservesCallerHTTPStatus(t *testing.T) {
	appErr := ResolveAppError("rollback failed", 400)
	if appErr.HTTPStatus != 400 {
		t.Fatalf("HTTPStatus = %d, want 400", appErr.HTTPStatus)
	}
}

func TestResolveAppErrorMatchesDynamicTemplate(t *testing.T) {
	appErr := ResolveAppError("review endpoint returned http 503", 502)
	if appErr.Code != 2001853 {
		t.Fatalf("Code = %d, want 2001853", appErr.Code)
	}
	if appErr.Message != "Review endpoint request failed" {
		t.Fatalf("Message = %q", appErr.Message)
	}
	if appErr.Detail != "review endpoint returned http 503" {
		t.Fatalf("Detail = %#v", appErr.Detail)
	}
}

func TestResolveSidechatErrorsUseSpecificCodes(t *testing.T) {
	for _, sample := range []struct {
		message string
		status  int
		code    int
	}{
		{"sidechat already has a request in progress", 409, 2002348},
		{"sidechat source attachment access denied", 403, 2002356},
		{"query chat source upload session: connection reset", 500, 2002359},
		{"at most 100 dataset_list are allowed", 400, 2002341},
		{"database_ids value is too long", 400, 2002342},
	} {
		t.Run(sample.message, func(t *testing.T) {
			appErr := ResolveAppError(sample.message, sample.status)
			if appErr.Code != sample.code || appErr.HTTPStatus != sample.status {
				t.Fatalf("resolved error = %#v, want code %d and HTTP status %d", appErr, sample.code, sample.status)
			}
		})
	}
}

func TestDirectAPIErrorMessagesAreCatalogued(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.Walk("..", func(file string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if file != ".." && (strings.HasPrefix(info.Name(), ".") || info.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") || strings.Contains(file, "error_catalog") {
			return nil
		}

		parsed, parseErr := parser.ParseFile(fset, file, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			messageArg := directAPIErrorMessageArg(call)
			if messageArg < 0 || messageArg >= len(call.Args) {
				return true
			}
			message := normalizeCatalogMessage(leadingString(call.Args[messageArg]))
			if message == "" {
				return true
			}
			if !catalogContainsSourceMessage(message) {
				pos := fset.Position(call.Pos())
				t.Errorf("uncatalogued API error %q at %s:%d", message, file, pos.Line)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCoreErrorConstructorsAreCatalogued(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.Walk("..", func(file string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if file != ".." && (strings.HasPrefix(info.Name(), ".") || info.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") || strings.Contains(file, "error_catalog") {
			return nil
		}
		parsed, parseErr := parser.ParseFile(fset, file, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			switch catalogCallName(call.Fun) {
			case "errors.New", "fmt.Errorf":
			default:
				return true
			}
			sourceMessage := leadingString(call.Args[0])
			if strings.TrimSpace(sourceMessage) == "" {
				pos := fset.Position(call.Pos())
				t.Errorf("dynamic Core error constructor without stable prefix at %s:%d", file, pos.Line)
				return true
			}
			message := normalizeCatalogMessage(sourceMessage)
			if message == "" {
				return true
			}
			if !catalogContainsSourceMessage(message) {
				pos := fset.Position(call.Pos())
				t.Errorf("uncatalogued Core error %q at %s:%d", message, file, pos.Line)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func catalogContainsSourceMessage(message string) bool {
	normalized := normalizeErrorKey(message)
	if normalized == "%s" || normalized == "%w" {
		return true
	}
	if strings.Contains(normalized, "%") {
		for _, pattern := range errorPatterns {
			if pattern.template == normalized {
				return true
			}
		}
		return false
	}
	_, exists := lookupErrorCatalog(normalized)
	return exists
}

func directAPIErrorMessageArg(call *ast.CallExpr) int {
	switch catalogCallName(call.Fun) {
	case "common.ReplyErr", "common.ReplyErrWithData", "skillhttperr.Reply", "skillhttperr.ReplyWithCode":
		return 1
	case "replyError":
		if len(call.Args) >= 3 {
			return 1
		}
	case "badRequest":
		return 0
	}
	return -1
}

func catalogCallName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return catalogCallName(value.X) + "." + value.Sel.Name
	default:
		return ""
	}
}

func leadingString(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return ""
		}
		text, err := strconv.Unquote(value.Value)
		if err != nil {
			return ""
		}
		return text
	case *ast.BinaryExpr:
		return leadingString(value.X)
	case *ast.CallExpr:
		if catalogCallName(value.Fun) != "fmt.Sprintf" || len(value.Args) == 0 {
			return ""
		}
		format := leadingString(value.Args[0])
		if strings.HasPrefix(format, "%s") && len(value.Args) > 1 {
			return leadingString(value.Args[1])
		}
		return format
	default:
		return ""
	}
}

func normalizeCatalogMessage(message string) string {
	message = strings.TrimSpace(message)
	if index := strings.Index(message, ": "); index >= 0 {
		message = message[:index]
	}
	message = strings.TrimSpace(strings.TrimSuffix(message, ":"))
	return strings.ToLower(message)
}
