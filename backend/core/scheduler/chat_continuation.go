package scheduler

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
)

const continuationHeader = "X-LazyMind-Scheduled-Continuation"

// A capability lives only until the admitted scheduler request reaches Core
// or the HTTP exchange fails. It carries no authority for another chat body.
var pendingChatContinuations sync.Map

type scheduledChatContinuation struct {
	host, user string
	body       []byte
}

func dispatchScheduledChat(req *http.Request, taskID string, body []byte) (*http.Response, error) {
	host := req.URL.Hostname()
	if host != "localhost" && !net.ParseIP(host).IsLoopback() {
		return http.DefaultClient.Do(req)
	}
	token := taskID + "." + rand.Text()
	pendingChatContinuations.Store(token, &scheduledChatContinuation{req.URL.Host, req.Header.Get("X-User-Id"), body})
	defer pendingChatContinuations.Delete(token)
	req.Header.Set(continuationHeader, token)
	// Preserve the client's redirect policy without forwarding local authority.
	client := *http.DefaultClient
	redirect := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		next.Header.Del(continuationHeader)
		if redirect != nil {
			return redirect(next, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return client.Do(req)
}

// ConsumeScheduledChatContinuation identifies one previously admitted request
// when it returns over loopback. Ordinary route authentication still applies.
func ConsumeScheduledChatContinuation(req *http.Request) bool {
	token := req.Header.Get(continuationHeader)
	if token == "" {
		return false
	}
	req.Header.Del(continuationHeader)
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() || req.Method != http.MethodPost || req.URL.Path != "/conversations:chat" || req.URL.RawQuery != "" || req.Header.Get("Origin") != "" {
		return false
	}
	value, ok := pendingChatContinuations.Load(token)
	if !ok {
		return false
	}
	continuation := value.(*scheduledChatContinuation)
	if req.Host != continuation.host || req.Header.Get("X-User-Id") != continuation.user || req.ContentLength != int64(len(continuation.body)) {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, req.ContentLength+1))
	req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	return err == nil && bytes.Equal(body, continuation.body) && pendingChatContinuations.CompareAndDelete(token, value)
}
