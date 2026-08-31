// Package exporter exposes provider-neutral capability discovery and export routes.
// Individual providers own their dependencies, upstream protocol, and output format.
package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common"
	"lazymind/core/systemdeps"
)

const htmlPresentationProviderID = "html-presentation"

type provider struct {
	capabilities http.HandlerFunc
	export       http.HandlerFunc
}

var providers = map[string]provider{
	htmlPresentationProviderID: {
		capabilities: htmlPresentationCapabilities,
		export:       exportHTMLPresentation,
	},
}

func resolveProvider(r *http.Request) (provider, bool) {
	id := strings.ToLower(strings.TrimSpace(mux.Vars(r)["provider_id"]))
	value, ok := providers[id]
	return value, ok
}

// Capabilities handles GET /exporters/{provider_id}:capabilities.
func Capabilities(w http.ResponseWriter, r *http.Request) {
	value, ok := resolveProvider(r)
	if !ok {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}
	value.capabilities(w, r)
}

// Export handles POST /exporters/{provider_id}:export.
func Export(w http.ResponseWriter, r *http.Request) {
	value, ok := resolveProvider(r)
	if !ok {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}
	value.export(w, r)
}

func capabilityPayload(editable bool) map[string]any {
	editableFormat := map[string]any{
		"id":        "editable-pptx",
		"available": editable,
	}
	if !editable {
		editableFormat["dependency"] = map[string]any{
			"id":           "editable-ppt-dependency",
			"settings_url": "/settings?section=system_tools#editable-ppt-dependency",
		}
	}
	return map[string]any{
		"provider_id": htmlPresentationProviderID,
		"formats": []map[string]any{
			{"id": "raster-pptx", "available": true},
			{"id": "pdf", "available": true},
			editableFormat,
		},
	}
}

func htmlPresentationCapabilities(w http.ResponseWriter, r *http.Request) {
	if systemdeps.IsLocalRuntime() {
		enabled := false
		if runtimeRoot, err := systemdeps.RuntimeRootFromEnv(); err == nil {
			if status, detectErr := systemdeps.DetectEditablePPT(runtimeRoot); detectErr == nil {
				enabled = status.Installed
			}
		}
		common.ReplyOK(w, capabilityPayload(enabled))
		return
	}

	upstream := common.ChatServiceEndpoint() + "/api/workflow/ppt/capabilities"
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream, nil)
	if err != nil {
		common.ReplyErr(w, "build PPT capabilities request failed", http.StatusInternalServerError)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		common.ReplyErr(w, "PPT capabilities upstream unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		common.ReplyErr(w, "PPT capabilities upstream error", http.StatusBadGateway)
		return
	}
	var capabilities struct {
		EditablePptx bool `json:"editable_pptx"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&capabilities); err != nil {
		common.ReplyErr(w, "invalid PPT capabilities response", http.StatusBadGateway)
		return
	}
	common.ReplyOK(w, capabilityPayload(capabilities.EditablePptx))
}

func exportHTMLPresentation(w http.ResponseWriter, r *http.Request) {
	upstream := common.ChatServiceEndpoint() + "/api/workflow/ppt/export"
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream, r.Body)
	if err != nil {
		common.ReplyErr(w, "build upstream request failed", http.StatusInternalServerError)
		return
	}
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		common.ReplyErr(w, "ppt export upstream unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		message := string(body)
		if message == "" {
			message = fmt.Sprintf("presentation export failed with status %d", resp.StatusCode)
		}
		common.ReplyErr(w, message, resp.StatusCode)
		return
	}
	for _, key := range []string{"Content-Type", "Content-Disposition"} {
		if value := resp.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.presentationml.presentation")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}
