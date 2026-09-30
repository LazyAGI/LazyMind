package facade

import "net/http"

type Envelope = envelope

func IdentityAndVersion(w http.ResponseWriter, r *http.Request) (string, bool) {
	return identityAndVersion(w, r)
}
func WriteJSON(w http.ResponseWriter, status int, value any) { writeJSON(w, status, value) }
func Fail(w http.ResponseWriter, status int, code, message string, retryable bool) {
	fail(w, status, code, message, retryable)
}
