package knowledgeruntime

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/gorilla/mux"
	"lazymind/core/capability"
	"lazymind/core/common"
	"lazymind/core/doc"
	"net/http"
	"strings"
)

func (r *Runtime) InternalHandler(secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		actual := req.Header.Get("X-LazyMind-Internal-Token")
		if secret == "" || subtle.ConstantTimeCompare([]byte(actual), []byte(secret)) != 1 {
			common.ReplyErr(w, "forbidden", 403)
			return
		}
		var in PrepareRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if e := decoder.Decode(&in); e != nil {
			common.ReplyErr(w, "invalid request", 400)
			return
		}
		var out any
		var err error
		switch {
		case strings.HasSuffix(req.URL.Path, ":credential"):
			var token string
			var expires int64
			token, expires, err = r.Credential(req.Context(), in.Execution)
			out = map[string]any{"token": token, "expires_at": expires}
		case strings.HasSuffix(req.URL.Path, ":prepare"):
			out, err = r.Prepare(req.Context(), in)
		case strings.HasSuffix(req.URL.Path, ":status"):
			out, err = r.Status(req.Context(), in.Execution, in.ConfirmationID)
		case strings.HasSuffix(req.URL.Path, ":claim"):
			var metadata map[string]any
			metadata, err = r.Claim(req.Context(), in)
			out = map[string]any{"status": "allowed", "metadata": metadata}
		default:
			common.ReplyErr(w, "not found", 404)
			return
		}
		if err == nil && strings.HasSuffix(req.URL.Path, ":prepare") && r.pendingHook != nil {
			if p, ok := out.(Prepared); ok && p.Status == "pending" {
				err = r.pendingHook(req.Context(), in.Execution, p)
			}
		}
		if err != nil {
			replyRuntimeError(w, err)
			return
		}
		common.ReplyJSON(w, out)
	})
}
func (r *Runtime) Decision(w http.ResponseWriter, req *http.Request) {
	var in struct {
		Decision        string `json:"decision"`
		ArgumentsDigest string `json:"arguments_digest"`
		Snapshot        string `json:"snapshot"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&in); err != nil {
		common.ReplyErr(w, "invalid decision", 400)
		return
	}
	if err := r.Decide(req.Context(), common.UserID(req), req.Header.Get("X-Tenant-Id"), mux.Vars(req)["confirmation_id"], in.Decision, in.ArgumentsDigest, in.Snapshot); err != nil {
		replyRuntimeError(w, err)
		return
	}
	common.ReplyJSON(w, map[string]any{"status": in.Decision})
}

func replyRuntimeError(w http.ResponseWriter, err error) {
	code, message := "EXECUTION_REJECTED", err.Error()
	var details map[string]any
	var resource *doc.ResourceError
	var application *capability.Error
	if errors.As(err, &resource) {
		code, message, details = resource.Code, resource.Message, resource.Details
	} else if errors.As(err, &application) {
		code, message, details = string(application.Code), application.Message, application.Details
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "details": details}})
}
