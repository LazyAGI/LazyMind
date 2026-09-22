package cloudsession

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"lazymind/core/cloudclient"
)

type DesktopAuthorizationRequest = cloudclient.DesktopAuthorizationRequest
type DesktopAuthorization = cloudclient.DesktopAuthorization
type DesktopCodeExchange = cloudclient.DesktopCodeExchange

type DesktopHandoffClient interface {
	BeginDesktopAuthorization(context.Context, DesktopAuthorizationRequest) (DesktopAuthorization, error)
	ExchangeDesktopCode(context.Context, DesktopCodeExchange) (TokenPair, error)
}

type LoginCoordinatorDeps struct {
	Session               *Service
	Handoff               DesktopHandoffClient
	CloudOrigin           string
	AuthorizationPath     string
	CallbackListenAddress string
	Locale                string
	AttemptTTL            time.Duration
	Random                io.Reader
	ReportError           func(error)
}

type LoginCoordinator struct {
	mu                    sync.Mutex
	session               *Service
	handoff               DesktopHandoffClient
	cloudOrigin           string
	authorizationPath     string
	callbackListenAddress string
	locale                string
	attemptTTL            time.Duration
	random                io.Reader
	reportError           func(error)
	active                *loginAttempt
}

type loginAttempt struct {
	transactionID string
	callbackURL   string
	state         string
	verifier      string
	previousState State
	listener      net.Listener
	server        *http.Server
	ctx           context.Context
	cancel        context.CancelFunc
	used          bool
}

type LoginStart struct {
	AuthorizationURL string `json:"authorization_url"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

func NewLoginCoordinator(deps LoginCoordinatorDeps) (*LoginCoordinator, error) {
	origin, err := url.Parse(strings.TrimSpace(deps.CloudOrigin))
	if err != nil || origin.Host == "" || origin.User != nil || (origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" || origin.Fragment != "" ||
		(origin.Scheme != "https" && !(origin.Scheme == "http" && isLoopbackCloudHost(origin.Hostname()))) {
		return nil, errors.New("LazyMind Cloud login origin is invalid")
	}
	if deps.Session == nil || deps.Handoff == nil {
		return nil, errors.New("LazyMind Cloud login dependencies are incomplete")
	}
	path := strings.TrimSpace(deps.AuthorizationPath)
	if path == "" {
		path = "/zh/desktop/authorize"
	}
	if !strings.HasPrefix(path, "/") {
		return nil, errors.New("LazyMind Cloud authorization path is invalid")
	}
	callbackListenAddress := strings.TrimSpace(deps.CallbackListenAddress)
	if callbackListenAddress == "" {
		callbackListenAddress = "127.0.0.1:0"
	}
	callbackHost, callbackPort, err := net.SplitHostPort(callbackListenAddress)
	if err != nil || (callbackHost != "127.0.0.1" && callbackHost != "0.0.0.0") {
		return nil, errors.New("LazyMind Cloud callback listener is invalid")
	}
	port, err := strconv.Atoi(callbackPort)
	if err != nil || port < 0 || port > 65535 {
		return nil, errors.New("LazyMind Cloud callback listener is invalid")
	}
	ttl := deps.AttemptTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	random := deps.Random
	if random == nil {
		random = rand.Reader
	}
	locale := strings.ToLower(strings.TrimSpace(deps.Locale))
	if locale == "" {
		locale = "zh"
	}
	if locale != "zh" && locale != "en" {
		return nil, errors.New("LazyMind Cloud login locale is invalid")
	}
	return &LoginCoordinator{
		session: deps.Session, handoff: deps.Handoff, cloudOrigin: origin.Scheme + "://" + origin.Host,
		authorizationPath: path, callbackListenAddress: callbackListenAddress,
		locale: locale, attemptTTL: ttl, random: random, reportError: deps.ReportError,
	}, nil
}

func (c *LoginCoordinator) Start(ctx context.Context) (LoginStart, error) {
	state, err := randomURLToken(c.random, 32)
	if err != nil {
		return LoginStart{}, err
	}
	verifier, err := randomURLToken(c.random, 32)
	if err != nil {
		return LoginStart{}, err
	}
	listener, err := net.Listen("tcp4", c.callbackListenAddress)
	if err != nil {
		return LoginStart{}, err
	}
	_, callbackPort, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		return LoginStart{}, err
	}
	callbackURL := "http://" + net.JoinHostPort("127.0.0.1", callbackPort) + "/cloud-auth/callback"
	challengeBytes := sha256.Sum256([]byte(verifier))
	authorization, err := c.handoff.BeginDesktopAuthorization(ctx, DesktopAuthorizationRequest{
		RedirectURI: callbackURL, CallbackURL: callbackURL, State: state, Locale: c.locale,
		CodeChallenge: base64.RawURLEncoding.EncodeToString(challengeBytes[:]),
	})
	if err != nil {
		_ = listener.Close()
		return LoginStart{}, err
	}
	if !c.validAuthorizationURL(authorization.AuthorizationURL) {
		_ = listener.Close()
		return LoginStart{}, errors.New("LazyMind Cloud returned an invalid authorization transaction")
	}
	attemptCtx, cancel := context.WithTimeout(context.Background(), c.attemptTTL)
	attempt := &loginAttempt{
		transactionID: authorization.TransactionID, callbackURL: callbackURL,
		state: state, verifier: verifier, previousState: c.session.currentState(),
		listener: listener, ctx: attemptCtx, cancel: cancel,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/cloud-auth/callback", func(w http.ResponseWriter, r *http.Request) {
		c.callback(attempt, w, r)
	})
	attempt.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	c.mu.Lock()
	previous := c.active
	c.active = attempt
	c.mu.Unlock()
	if previous != nil {
		c.closeAttempt(previous)
	}
	c.session.setState(StateAuthorizing)
	go func() { _ = attempt.server.Serve(listener) }()
	go func() {
		<-attemptCtx.Done()
		c.finishAttempt(attempt, attempt.previousState)
	}()
	return LoginStart{AuthorizationURL: authorization.AuthorizationURL, ExpiresInSeconds: int(c.attemptTTL.Seconds())}, nil
}

func (c *LoginCoordinator) Cancel() {
	c.mu.Lock()
	attempt := c.active
	c.active = nil
	c.mu.Unlock()
	if attempt != nil {
		c.closeAttempt(attempt)
		c.session.setState(attempt.previousState)
	}
}

func (c *LoginCoordinator) callback(attempt *loginAttempt, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	states, codes, callbackErrors := query["state"], query["code"], query["error"]
	if len(states) != 1 || len(states[0]) == 0 || len(states[0]) > 256 ||
		subtle.ConstantTimeCompare([]byte(states[0]), []byte(attempt.state)) != 1 {
		writeCallbackHTML(w, http.StatusBadRequest, "Authorization was rejected")
		return
	}
	code := ""
	isCancellation := len(query) == 2 && len(codes) == 0 && len(callbackErrors) == 1 && callbackErrors[0] == "access_denied"
	isAuthorization := len(query) == 2 && len(callbackErrors) == 0 && len(codes) == 1 && len(codes[0]) > 0 && len(codes[0]) <= 4096
	if !isCancellation && !isAuthorization {
		writeCallbackHTML(w, http.StatusBadRequest, "Authorization was rejected")
		return
	}
	if isAuthorization {
		code = codes[0]
	}
	c.mu.Lock()
	if c.active != attempt || attempt.used {
		c.mu.Unlock()
		writeCallbackHTML(w, http.StatusConflict, "Authorization was already consumed")
		return
	}
	attempt.used = true
	c.mu.Unlock()
	if isCancellation {
		c.session.setState(attempt.previousState)
		writeCallbackHTML(w, http.StatusOK, "Authorization was cancelled. You can return to LazyMind.")
		go c.finishAttempt(attempt, attempt.previousState)
		return
	}
	c.session.setState(StateExchanging)
	pair, err := c.handoff.ExchangeDesktopCode(attempt.ctx, DesktopCodeExchange{
		TransactionID: attempt.transactionID, RedirectURI: attempt.callbackURL, CallbackURL: attempt.callbackURL,
		Code: code, CodeVerifier: attempt.verifier,
	})
	if err == nil {
		err = c.session.Establish(attempt.ctx, pair)
	}
	if err != nil {
		if c.reportError != nil {
			c.reportError(err)
		}
		c.session.setState(attempt.previousState)
		writeCallbackHTML(w, http.StatusBadGateway, "Authorization could not be completed")
	} else {
		writeCallbackHTML(w, http.StatusOK, "Authorization completed. You can return to LazyMind.")
	}
	go c.finishAttempt(attempt, c.session.currentState())
}

func (c *LoginCoordinator) finishAttempt(attempt *loginAttempt, state State) {
	c.mu.Lock()
	if c.active != attempt {
		c.mu.Unlock()
		return
	}
	c.active = nil
	c.mu.Unlock()
	c.closeAttempt(attempt)
	if c.session.currentState() != StateSignedIn {
		c.session.setState(state)
	}
}

func (c *LoginCoordinator) closeAttempt(attempt *loginAttempt) {
	attempt.cancel()
	_ = attempt.server.Close()
	_ = attempt.listener.Close()
}

func (c *LoginCoordinator) validAuthorizationURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	return parsed.Scheme+"://"+parsed.Host == c.cloudOrigin && parsed.Path == c.authorizationPath
}

func isLoopbackCloudHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func randomURLToken(reader io.Reader, size int) (string, error) {
	body := make([]byte, size)
	if _, err := io.ReadFull(reader, body); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func writeCallbackHTML(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Cache-Control", "no-store")
	closingScript := ""
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		closingScript = "<script>if(window.opener&&!window.opener.closed){window.opener.focus()}window.close()</script>"
	}
	body := fmt.Sprintf("<!doctype html><meta charset=utf-8><title>LazyMind</title><style>body{font:16px system-ui;margin:3rem;line-height:1.6}</style><p>%s</p>%s", html.EscapeString(message), closingScript)
	// finishAttempt closes the callback server asynchronously. Send a complete,
	// length-delimited response before cleanup can close the connection.
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}
