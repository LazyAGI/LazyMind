package facade

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/workflow/controlstore"
)

const hostLogGrantLifetime = time.Minute

func hostLogSignature(runID, owner string, binding controlstore.Binding, claims string) []byte {
	mac := hmac.New(sha256.New, []byte(binding.CredentialHash))
	_ = json.NewEncoder(mac).Encode([]string{"lazymind.workflow-log.v1", runID, owner, binding.ConnectorID, binding.DriverSession, binding.Provider, claims})
	return mac.Sum(nil)
}

func issueHostLogGrant(runID, owner string, binding controlstore.Binding, cutoff, now time.Time) string {
	claims := strconv.FormatInt(cutoff.UnixMilli(), 10) + "." + strconv.FormatInt(now.Add(hostLogGrantLifetime).UnixMilli(), 10)
	return claims + "." + base64.RawURLEncoding.EncodeToString(hostLogSignature(runID, owner, binding, claims))
}

func verifyHostLogGrant(grant, runID, owner string, binding controlstore.Binding, now time.Time) (int64, bool) {
	claims, signature, ok := strings.Cut(grant, ".")
	if !ok {
		return 0, false
	}
	expires, signature, ok := strings.Cut(signature, ".")
	cutoff, cutoffErr := strconv.ParseInt(claims, 10, 64)
	deadline, deadlineErr := strconv.ParseInt(expires, 10, 64)
	decoded, signatureErr := base64.RawURLEncoding.DecodeString(signature)
	return cutoff, ok && cutoffErr == nil && deadlineErr == nil && signatureErr == nil &&
		binding.CredentialHash != "" && cutoff > 0 && cutoff <= deadline && now.UnixMilli() < deadline &&
		hmac.Equal(decoded, hostLogSignature(runID, owner, binding, claims+"."+expires))
}

func (h Handler) VerifyHostLogGrant(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	input := struct {
		Grant string `json:"grant"`
	}{}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input) != nil {
		fail(w, http.StatusBadRequest, "INVALID_LOG_GRANT", "Invalid workflow log authorization", false)
		return
	}
	runID, owner := mux.Vars(r)["session_id"], r.Header.Get("X-User-Id")
	session, readErr := h.logSession(r.Context(), runID, owner)
	binding, bindingErr := controlstore.DecodeBinding(session)
	cutoff, valid := verifyHostLogGrant(input.Grant, runID, owner, binding, time.Now())
	if readErr != nil || bindingErr != nil || !valid || binding.ConnectorID == "" || binding.DriverSession == "" {
		fail(w, http.StatusForbidden, "INVALID_LOG_GRANT", "Workflow log authorization is invalid or expired", false)
		return
	}
	if credential := r.Header.Get("X-Workflow-Host-Credential"); credential != "" {
		if _, err := controlstore.AuthorizeHost(session, binding.ConnectorID, credential); err != nil {
			fail(w, http.StatusForbidden, "INVALID_LOG_GRANT", "Workflow log pairing changed", false)
			return
		}
	}
	through := cutoff
	if (session.Status == "completed" || session.Status == "failed") && session.UpdatedAt.UnixMilli() < through {
		through = session.UpdatedAt.UnixMilli()
	}
	writeJSON(w, http.StatusOK, envelope{Data: map[string]any{
		"connector_id": binding.ConnectorID, "driver_session_id": binding.DriverSession, "provider": binding.Provider,
		"cutoff_ms": through, "started_ms": session.CreatedAt.UnixMilli(),
	}})
}
