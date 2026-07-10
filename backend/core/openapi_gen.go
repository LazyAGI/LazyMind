package main

import (
	"encoding/json"
	"strings"

	"github.com/gorilla/mux"
)

const (
	openAPITitle       = "Backend Core API"
	openAPIVersion     = "0.1.0"
	openAPIDescription = "LazyMind Go backend core API - proxies to algorithm services. text Kong text /api/core。"
	apiPrefix          = "/api/core"
)

// buildOpenAPISpecFromRouter text swagger text OpenAPI 3.0 spec。
// text/Requesttext，text /docs、/openapi.json、/openapi.yaml
// text。
func buildOpenAPISpecFromRouter(r *mux.Router) ([]byte, error) {
	registeredRoutes, err := collectOpenAPIRouteMethods(r)
	if err != nil {
		return nil, err
	}

	spec := map[string]any{}
	mergeOpenAPISpec(spec, prefixOpenAPIPaths(manualOpenAPISpec()))
	operationSpec := prefixOpenAPIPaths(operationRegistryOpenAPISpec())
	removeOpenAPIOperations(spec, operationSpec)
	overlayOpenAPISpec(spec, operationSpec)
	pruneUnregisteredOpenAPIPaths(spec, registeredRoutes)
	removeAgentLegacyOpenAPISchemas(spec)
	paths := getOrCreateObject(spec, "paths")

	err = r.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		path, err := route.GetPathTemplate()
		if err != nil || path == "" {
			return nil
		}
		if skipOpenAPIRoute(path) {
			return nil
		}

		methods, err := route.GetMethods()
		if err != nil {
			return nil
		}

		fullPath := apiPrefix + path
		pathItem := getOrCreateObject(paths, fullPath)
		pathParams := extractPathParameters(path)
		for _, method := range methods {
			lowerMethod := strings.ToLower(method)
			op := getOrCreateObject(pathItem, lowerMethod)
			if _, ok := op["summary"]; !ok {
				op["summary"] = method + " " + path
			}
			if _, ok := op["responses"]; !ok {
				op["responses"] = map[string]any{"200": map[string]any{"description": "OK"}}
			}
			if len(pathParams) > 0 {
				op["parameters"] = mergeParameters(toParameterList(op["parameters"]), pathParams)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	spec["openapi"] = "3.0.3"
	spec["info"] = map[string]any{
		"title":       openAPITitle,
		"version":     openAPIVersion,
		"description": openAPIDescription,
	}
	spec["servers"] = []map[string]any{{
		"url":         "/",
		"description": "same origin; see paths with /api/core prefix",
	}}
	spec["paths"] = paths

	return json.MarshalIndent(spec, "", "  ")
}

func collectOpenAPIRouteMethods(r *mux.Router) (map[string]map[string]struct{}, error) {
	routes := map[string]map[string]struct{}{}
	err := r.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		path, err := route.GetPathTemplate()
		if err != nil || path == "" {
			return nil
		}
		if skipOpenAPIRoute(path) {
			return nil
		}
		methods, err := route.GetMethods()
		if err != nil {
			return nil
		}
		fullPath := apiPrefix + path
		methodSet := routes[fullPath]
		if methodSet == nil {
			methodSet = map[string]struct{}{}
			routes[fullPath] = methodSet
		}
		for _, method := range methods {
			methodSet[strings.ToLower(method)] = struct{}{}
		}
		return nil
	})
	return routes, err
}

func skipOpenAPIRoute(path string) bool {
	return strings.HasPrefix(path, "/openapi") || path == "/docs"
}

func pruneUnregisteredOpenAPIPaths(spec map[string]any, registeredRoutes map[string]map[string]struct{}) {
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		return
	}
	for path, item := range paths {
		registeredMethods := registeredRoutes[path]
		if len(registeredMethods) == 0 {
			delete(paths, path)
			continue
		}
		pathItem, ok := item.(map[string]any)
		if !ok {
			delete(paths, path)
			continue
		}
		for key := range pathItem {
			lowerKey := strings.ToLower(key)
			if !isOpenAPIHTTPMethod(lowerKey) {
				continue
			}
			if _, ok := registeredMethods[lowerKey]; !ok {
				delete(pathItem, key)
			}
		}
		if !openAPIPathHasOperation(pathItem) {
			delete(paths, path)
		}
	}
}

func removeOpenAPIOperations(dst, src map[string]any) {
	dstPaths, ok := dst["paths"].(map[string]any)
	if !ok {
		return
	}
	srcPaths, ok := src["paths"].(map[string]any)
	if !ok {
		return
	}
	for path, item := range srcPaths {
		dstPathItem, ok := dstPaths[path].(map[string]any)
		if !ok {
			continue
		}
		srcPathItem, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for key := range srcPathItem {
			if isOpenAPIHTTPMethod(strings.ToLower(key)) {
				delete(dstPathItem, key)
			}
		}
	}
}

func removeAgentLegacyOpenAPISchemas(spec map[string]any) {
	components, ok := spec["components"].(map[string]any)
	if !ok {
		return
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		return
	}
	for _, name := range []string{
		"agentABTestCaseDetailListItemOpenAPIResponse",
		"agentABTestCaseDetailListOpenAPIResponse",
		"agentABTestResultOpenAPIResponse",
		"agentCaseDetailsMetricAveragesOpenAPIResponse",
		"agentCaseDetailsQuestionTypeOpenAPIResponse",
		"agentCaseDetailsSummaryOpenAPIResponse",
		"agentEvalReportBadCaseListItemOpenAPIResponse",
		"agentEvalReportBadCaseListOpenAPIResponse",
		"agentEvalReportResultOpenAPIResponse",
		"agentEvalReportTraceCoverageOpenAPIResponse",
		"agentFileContentOpenAPIRequest",
		"agentFileContentOpenAPIResponse",
		"agentTraceCompareOpenAPIResponse",
		"agentTraceDetailOpenAPIResponse",
		"agentTraceSummaryOpenAPIResponse",
	} {
		delete(schemas, name)
	}
}

func openAPIPathHasOperation(pathItem map[string]any) bool {
	for key := range pathItem {
		if isOpenAPIHTTPMethod(strings.ToLower(key)) {
			return true
		}
	}
	return false
}

func isOpenAPIHTTPMethod(method string) bool {
	switch method {
	case "get", "put", "post", "delete", "options", "head", "patch", "trace":
		return true
	default:
		return false
	}
}

func getOrCreateObject(parent map[string]any, key string) map[string]any {
	if existing, ok := parent[key].(map[string]any); ok {
		return existing
	}
	created := map[string]any{}
	parent[key] = created
	return created
}

func extractPathParameters(path string) []map[string]any {
	segments := strings.Split(path, "/")
	params := make([]map[string]any, 0)
	seen := make(map[string]struct{})
	for _, segment := range segments {
		start := strings.Index(segment, "{")
		end := strings.Index(segment, "}")
		if start < 0 || end <= start+1 {
			continue
		}
		name := strings.TrimSpace(segment[start+1 : end])
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		params = append(params, map[string]any{
			"name":     name,
			"in":       "path",
			"required": true,
			"schema": map[string]any{
				"type": "string",
			},
		})
	}
	return params
}

func toParameterList(v any) []map[string]any {
	switch items := v.(type) {
	case []map[string]any:
		return append([]map[string]any(nil), items...)
	case []any:
		result := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if m, ok := item.(map[string]any); ok {
				result = append(result, m)
			}
		}
		return result
	default:
		return nil
	}
}

func mergeParameters(existing, generated []map[string]any) []map[string]any {
	if len(generated) == 0 {
		return existing
	}
	merged := make([]map[string]any, 0, len(existing)+len(generated))
	merged = append(merged, existing...)
	seen := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		name, _ := item["name"].(string)
		inVal, _ := item["in"].(string)
		seen[inVal+":"+name] = struct{}{}
	}
	for _, item := range generated {
		name, _ := item["name"].(string)
		inVal, _ := item["in"].(string)
		key := inVal + ":" + name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, item)
	}
	return merged
}

func mergeOpenAPISpec(dst, src map[string]any) {
	for key, srcVal := range src {
		if dstVal, ok := dst[key]; ok {
			dstMap, dstIsMap := dstVal.(map[string]any)
			srcMap, srcIsMap := srcVal.(map[string]any)
			if dstIsMap && srcIsMap {
				mergeOpenAPISpec(dstMap, srcMap)
				continue
			}
		}
		dst[key] = srcVal
	}
}

func overlayOpenAPISpec(dst, src map[string]any) {
	for key, srcVal := range src {
		if dstVal, ok := dst[key]; ok {
			dstMap, dstIsMap := dstVal.(map[string]any)
			srcMap, srcIsMap := srcVal.(map[string]any)
			if dstIsMap && srcIsMap {
				overlayOpenAPISpec(dstMap, srcMap)
				continue
			}
		}
		dst[key] = srcVal
	}
}

func prefixOpenAPIPaths(spec map[string]any) map[string]any {
	paths, ok := spec["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		return spec
	}
	prefixed := make(map[string]any, len(paths))
	for path, item := range paths {
		if strings.HasPrefix(path, apiPrefix) {
			prefixed[path] = item
			continue
		}
		prefixed[apiPrefix+path] = item
	}
	spec["paths"] = prefixed
	return spec
}
