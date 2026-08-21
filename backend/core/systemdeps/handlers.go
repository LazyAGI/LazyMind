package systemdeps

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"lazymind/core/common"
)

type ffmpegUpdateRequest struct {
	Source     string `json:"source"`
	CustomPath string `json:"customPath,omitempty"`
}

func GetFFmpegDependency(w http.ResponseWriter, r *http.Request) {
	if !IsLocalRuntime() {
		common.ReplyOK(w, systemEnabledStatus())
		return
	}
	runtimeRoot, err := RuntimeRootFromEnv()
	if err != nil {
		common.ReplyErr(w, "local runtime root is not configured", http.StatusServiceUnavailable)
		return
	}
	status, err := DetectFFmpeg(runtimeRoot)
	if err != nil {
		common.ReplyErr(w, "load ffmpeg dependency status failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, status)
}

func CheckFFmpegDependency(w http.ResponseWriter, r *http.Request) {
	GetFFmpegDependency(w, r)
}

func UpdateFFmpegDependency(w http.ResponseWriter, r *http.Request) {
	if !IsLocalRuntime() {
		common.ReplyErr(w, "ffmpeg dependency settings are only supported in local/desktop runtime", http.StatusForbidden)
		return
	}
	runtimeRoot, err := RuntimeRootFromEnv()
	if err != nil {
		common.ReplyErr(w, "local runtime root is not configured", http.StatusServiceUnavailable)
		return
	}
	var req ffmpegUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	source := FFmpegSource(strings.TrimSpace(req.Source))
	switch source {
	case FFmpegSourceCustom, FFmpegSourceBundled:
	default:
		common.ReplyErr(w, "source must be custom or bundled", http.StatusBadRequest)
		return
	}
	if source == FFmpegSourceCustom && strings.TrimSpace(req.CustomPath) == "" {
		common.ReplyErr(w, "customPath is required when source is custom", http.StatusBadRequest)
		return
	}
	status, err := UpdateFFmpegConfig(runtimeRoot, source, req.CustomPath)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	common.ReplyOK(w, status)
}

func InstallFFmpegDependency(w http.ResponseWriter, r *http.Request) {
	if !IsLocalRuntime() {
		common.ReplyErr(w, "bundled ffmpeg install is only supported in local/desktop runtime", http.StatusForbidden)
		return
	}
	runtimeRoot, err := RuntimeRootFromEnv()
	if err != nil {
		common.ReplyErr(w, "local runtime root is not configured", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	status, err := InstallBundledFFmpeg(ctx, runtimeRoot)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	common.ReplyOK(w, status)
}

func GetEditablePPTDependency(w http.ResponseWriter, r *http.Request) {
	if !IsLocalRuntime() {
		common.ReplyOK(w, EditablePPTStatus{Installed: true, AffectedFeatures: []string{"editable_pptx_export"}})
		return
	}
	runtimeRoot, err := RuntimeRootFromEnv()
	if err != nil {
		common.ReplyErr(w, "local runtime root is not configured", http.StatusServiceUnavailable)
		return
	}
	status, err := DetectEditablePPT(runtimeRoot)
	if err != nil {
		common.ReplyErr(w, "load editable PPTX dependency status failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, status)
}

func CheckEditablePPTDependency(w http.ResponseWriter, r *http.Request) {
	GetEditablePPTDependency(w, r)
}

func InstallEditablePPTDependency(w http.ResponseWriter, r *http.Request) {
	if !IsLocalRuntime() {
		common.ReplyErr(w, "editable PPTX dependency install is only supported in local/desktop runtime", http.StatusForbidden)
		return
	}
	runtimeRoot, err := RuntimeRootFromEnv()
	if err != nil {
		common.ReplyErr(w, "local runtime root is not configured", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Minute)
	defer cancel()
	status, err := InstallEditablePPT(ctx, runtimeRoot)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	common.ReplyOK(w, status)
}
