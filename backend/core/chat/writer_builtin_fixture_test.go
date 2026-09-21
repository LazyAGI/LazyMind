package chat

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// These pre-publication regression fixtures originally inspected workflow
// Action names. Keep their existing payload/failure assertions while adapting
// the actual builtin request to that fixture representation. New integration
// tests separately reject the old workflow endpoint.
func adaptLegacyWriterFixture(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/document/providers" {
			var providers []map[string]any
			for _, id := range []string{"feishu", "notion", "github", "wechat", "obsidian"} {
				providers = append(providers, map[string]any{"id": id, "capabilities": []string{"load", "create", "replace", "patch", "revision_check", "media"}})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"providers": providers})
			return
		}
		if r.URL.Path == "/api/document/actions:invoke" {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(400)
				return
			}
			var body map[string]json.RawMessage
			if json.Unmarshal(raw, &body) != nil {
				w.WriteHeader(400)
				return
			}
			var ref string
			_ = json.Unmarshal(body["reference"], &ref)
			if !strings.HasPrefix(ref, "builtin:document.") || !strings.HasSuffix(ref, ".v1") {
				w.WriteHeader(422)
				return
			}
			action := strings.TrimSuffix(strings.TrimPrefix(ref, "builtin:document."), ".v1")
			body["action"], _ = json.Marshal(action)
			raw, _ = json.Marshal(body)
			copy := r.Clone(r.Context())
			copy.URL.Path = "/api/workflow/actions:invoke"
			copy.Body = io.NopCloser(bytes.NewReader(raw))
			r = copy
		}
		next.ServeHTTP(w, r)
	})
}
