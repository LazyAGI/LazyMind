package server

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

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestErrorCatalogCodesHaveSingleMeaningAndTranslations(t *testing.T) {
	data, err := os.ReadFile("../../../../i18n/errors/scan-control-plane.json")
	if err != nil {
		t.Fatal(err)
	}
	translations := map[string]map[string]string{}
	if err := json.Unmarshal(data, &translations); err != nil {
		t.Fatal(err)
	}
	for code, entry := range errorCatalogByCode {
		localized, exists := translations[code]
		if !exists {
			t.Errorf("scan-control-plane error code %q is missing i18n", code)
			continue
		}
		if localized["en-US"] != entry.Message {
			t.Errorf("error code %q message is %q but en-US is %q", code, entry.Message, localized["en-US"])
		}
		if strings.TrimSpace(localized["zh-CN"]) == "" {
			t.Errorf("error code %q is missing zh-CN", code)
		}
	}
}

func TestResolveErrorPayloadUsesSpecificCodeAndPreservesDynamicDetail(t *testing.T) {
	code, message, details := resolveErrorPayload("INVALID_TARGET", "agent_id is required", nil)
	if code != "AGENT_ID_REQUIRED" || message != "Agent_id is required" {
		t.Fatalf("unexpected exact error payload: code=%q message=%q", code, message)
	}

	code, message, details = resolveErrorPayload("INVALID_REQUEST", "rules[2].time must use hh:mm:ss", nil)
	if code != "SCHEDULE_RULE_INVALID" || message != "A schedule rule is invalid" {
		t.Fatalf("unexpected patterned error payload: code=%q message=%q", code, message)
	}
	if details["detail"] != "rules[2].time must use hh:mm:ss" {
		t.Fatalf("dynamic detail was not preserved: %#v", details)
	}
}

func TestResolveErrorPayloadHidesUnknownInternalError(t *testing.T) {
	code, message, details := resolveErrorPayload("INTERNAL_ERROR", "database password leaked into driver error", nil)
	if code != "INTERNAL_ERROR" || message != "internal error" || len(details) != 0 {
		t.Fatalf("unexpected internal error payload: code=%q message=%q details=%#v", code, message, details)
	}
}

func TestErrorPayloadMapsStoreErrors(t *testing.T) {
	code, message, details := errorPayload(store.NewStoreError(store.ErrCodeAgentNotFound, "agent not found"))
	if code != "AGENT_NOT_FOUND" || message != "Agent not found" || len(details) != 0 {
		t.Fatalf("unexpected store error payload: code=%q message=%q details=%#v", code, message, details)
	}
}

func TestScanControlPlaneErrorSitesAreCatalogued(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.Walk("../..", func(file string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if file != "../.." && (strings.HasPrefix(info.Name(), ".") || info.Name() == "vendor") {
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
		constants := fileStringConstants(parsed)
		ast.Inspect(parsed, func(node ast.Node) bool {
			for _, message := range errorMessagesFromNode(node, constants) {
				message = normalizeSourceMessage(message)
				if message == "" || message == "%s" || message == "%w" {
					continue
				}
				if !catalogContainsSourceMessage(message) {
					position := fset.Position(node.Pos())
					t.Errorf("uncatalogued scan-control-plane error %q at %s:%d", message, file, position.Line)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func errorMessagesFromNode(node ast.Node, constants map[string]string) []string {
	switch value := node.(type) {
	case *ast.CallExpr:
		name := callName(value.Fun)
		switch name {
		case "errors.New":
			return callMessage(value, 0, constants, false)
		case "fmt.Errorf":
			return callMessage(value, 0, constants, true)
		case "NewError", "NewErrorWithDetails", "access.NewError", "connector.NewError", "sourceengine.NewError", "taskengine.NewError":
			return callMessage(value, 1, constants, false)
		case "NewStoreError", "store.NewStoreError":
			return callMessage(value, 1, constants, false)
		case "mapGORMError", "mapORMNotFound":
			return callMessage(value, 2, constants, false)
		case "unauthorized", "forbidden":
			return callMessage(value, 0, constants, false)
		case "FieldError", "sourceengine.FieldError":
			if len(value.Args) < 2 {
				return nil
			}
			field := leadingString(value.Args[0], constants)
			reason := leadingString(value.Args[1], constants)
			if field == "" {
				return nil
			}
			if reason == "" {
				reason = "%s"
			}
			return []string{field + ": " + reason}
		case "missingDependency":
			if len(value.Args) == 0 {
				return nil
			}
			name := leadingString(value.Args[0], constants)
			if name != "" {
				return []string{name + " is not configured"}
			}
		case "invalidJSON":
			return []string{"invalid JSON request body"}
		case "http.Error":
			return callMessage(value, 1, constants, false)
		}
	case *ast.CompositeLit:
		typeName := callName(value.Type)
		if !strings.HasSuffix(typeName, "Error") {
			return nil
		}
		for _, element := range value.Elts {
			field, ok := element.(*ast.KeyValueExpr)
			if !ok || callName(field.Key) != "Message" {
				continue
			}
			if message := leadingString(field.Value, constants); message != "" {
				return []string{message}
			}
		}
	}
	return nil
}

func callMessage(call *ast.CallExpr, index int, constants map[string]string, splitDetail bool) []string {
	if index >= len(call.Args) {
		return nil
	}
	message := leadingString(call.Args[index], constants)
	if splitDetail {
		message = stableErrorPrefix(message)
	}
	if message == "" {
		return nil
	}
	return []string{message}
}

func leadingString(expr ast.Expr, constants map[string]string) string {
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
	case *ast.Ident:
		return constants[value.Name]
	case *ast.BinaryExpr:
		return leadingString(value.X, constants)
	case *ast.CallExpr:
		if callName(value.Fun) == "fmt.Sprintf" && len(value.Args) > 0 {
			return leadingString(value.Args[0], constants)
		}
	}
	return ""
}

func fileStringConstants(file *ast.File) map[string]string {
	constants := map[string]string{}
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, spec := range general.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range values.Names {
				if index < len(values.Values) {
					if value := leadingString(values.Values[index], constants); value != "" {
						constants[name.Name] = value
					}
				}
			}
		}
	}
	return constants
}

func callName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return callName(value.X) + "." + value.Sel.Name
	case *ast.StarExpr:
		return callName(value.X)
	}
	return ""
}

func stableErrorPrefix(message string) string {
	message = strings.TrimSpace(message)
	if index := strings.Index(message, ": "); index > 0 {
		return strings.TrimSpace(message[:index])
	}
	return message
}

func normalizeSourceMessage(message string) string {
	return normalizeErrorMessage(strings.TrimSuffix(strings.TrimSpace(message), ":"))
}

func catalogContainsSourceMessage(message string) bool {
	if strings.Trim(message, "%s: ") == "" {
		return true
	}
	if strings.Contains(message, "%") {
		for _, pattern := range errorPatterns {
			if pattern.Template == message {
				return true
			}
		}
		return false
	}
	_, exists := errorCatalog[message]
	return exists
}
