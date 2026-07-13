package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"

	"lazymind/core/common"
	"lazymind/core/store"
)

func proxyEvoResponse(
	w http.ResponseWriter,
	r *http.Request,
	method string,
	path string,
	query url.Values,
	body io.Reader,
	defaultAccept string,
) {
	resp, err := openEvoProxyResponse(r, method, path, query, body, defaultAccept)
	if err != nil {
		common.ReplyErrWithData(w, "proxy evo request failed", map[string]any{"detail": err.Error()}, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	writeEvoResponse(w, resp)
}

func openEvoProxyResponse(
	r *http.Request,
	method string,
	path string,
	query url.Values,
	body io.Reader,
	defaultAccept string,
) (*http.Response, error) {
	client := newEvoClient(forwardedEvoProxyHeaders(r, defaultAccept))
	req, err := client.ProxyRequest(r.Context(), method, path, query, body)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

func writeEvoResponse(w http.ResponseWriter, resp *http.Response) {
	copyEvoResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		copyEvoStream(w, resp.Body)
		return
	}
	_, _ = io.Copy(w, resp.Body)
}

func writeProxyResponse(w http.ResponseWriter, proxy *upstreamProxyResponse) {
	if proxy == nil {
		common.ReplyOK(w, map[string]any{})
		return
	}
	if proxy.Header != nil {
		copyEvoResponseHeaders(w.Header(), proxy.Header)
	}
	statusCode := proxyStatusCode(proxy)
	if proxy.BodyBytes != nil {
		if w.Header().Get("Content-Type") == "" {
			if proxy.ContentType != "" {
				w.Header().Set("Content-Type", proxy.ContentType)
			} else {
				w.Header().Set("Content-Type", "application/octet-stream")
			}
		}
		w.WriteHeader(statusCode)
		_, _ = w.Write(proxy.BodyBytes)
		return
	}
	if strings.Contains(proxy.ContentType, "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(proxy.Body)
		return
	}
	if proxy.ContentType != "" {
		w.Header().Set("Content-Type", proxy.ContentType)
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.WriteHeader(statusCode)
	_, _ = io.WriteString(w, fmt.Sprint(proxy.Body))
}

func copyEvoStream(w http.ResponseWriter, body io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

func forwardedEvoProxyHeaders(r *http.Request, defaultAccept string) map[string]string {
	headers := forwardedUpstreamHeaders(r)
	if strings.TrimSpace(defaultAccept) != "" {
		headers["Accept"] = strings.TrimSpace(defaultAccept)
	}
	if accept := strings.TrimSpace(r.Header.Get("Accept")); accept != "" {
		headers["Accept"] = accept
	}
	for _, key := range []string{"Content-Type", "Last-Event-ID"} {
		if value := strings.TrimSpace(r.Header.Get(key)); value != "" {
			headers[key] = value
		}
	}
	return headers
}

func copyEvoResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if skipProxyResponseHeader(key) {
			continue
		}
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func skipProxyResponseHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade", "content-length":
		return true
	default:
		return false
	}
}

func cloneURLValues(values url.Values) url.Values {
	if len(values) == 0 {
		return nil
	}
	cloned := make(url.Values, len(values))
	for key, list := range values {
		cloned[key] = append([]string(nil), list...)
	}
	return cloned
}

func threadProxyPath(threadID, suffix string) string {
	return "/threads/" + url.PathEscape(strings.TrimSpace(threadID)) + suffix
}

func ownerCheckedThreadID(w http.ResponseWriter, r *http.Request) (string, bool) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return "", false
	}
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	if _, err := loadUserThread(db, r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return "", false
	}
	return threadID, true
}
