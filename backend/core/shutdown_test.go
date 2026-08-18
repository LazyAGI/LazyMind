package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// newListener returns a listener on an OS-assigned port for coordinateShutdown
// to serve on. The returned address is what test clients connect to.
func newListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func TestCoordinateShutdownReturnsNilOnContextCancellation(t *testing.T) {
	ln := newListener(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}

	var onCloseCalled int32
	onClose := func() { atomic.StoreInt32(&onCloseCalled, 1) }

	bg1 := make(chan struct{})
	bg2 := make(chan struct{})
	go func() { <-time.After(20 * time.Millisecond); close(bg1) }()
	go func() { <-time.After(10 * time.Millisecond); close(bg2) }()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- coordinateShutdown(ctx, srv, ln, []<-chan struct{}{bg1, bg2}, time.Second, onClose) }()

	// Let the server start serving, then trigger shutdown.
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("coordinateShutdown returned %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("coordinateShutdown did not return within timeout")
	}

	if atomic.LoadInt32(&onCloseCalled) == 0 {
		t.Fatal("onClose was not invoked during shutdown")
	}
}

func TestCoordinateShutdownDrainsInFlightRequest(t *testing.T) {
	// The handler blocks until shutdownBegin, then finishes. A graceful
	// shutdown must let this in-flight request complete with 200 rather than
	// closing the connection.
	shutdownBegin := make(chan struct{})
	handlerDone := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-shutdownBegin
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "drained")
		close(handlerDone)
	})

	ln := newListener(t)
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- coordinateShutdown(ctx, srv, ln, nil, 2*time.Second, nil)
	}()

	// Fire an in-flight request that blocks inside the handler.
	respCh := make(chan *http.Response, 1)
	errReq := make(chan error, 1)
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+ln.Addr().String()+"/", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errReq <- err
			return
		}
		respCh <- resp
	}()

	// Ensure the request has reached the server before cancelling.
	time.Sleep(40 * time.Millisecond)

	// Trigger shutdown and unblock the handler so it can finish during drain.
	cancel()
	close(shutdownBegin)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("coordinateShutdown returned %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("coordinateShutdown did not return within timeout")
	}

	select {
	case err := <-errReq:
		t.Fatalf("in-flight request failed: %v", err)
	case resp := <-respCh:
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("in-flight request status = %d, want 200", resp.StatusCode)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight request did not complete within timeout")
	}

	select {
	case <-handlerDone:
	default:
		t.Fatal("handler did not finish before shutdown returned")
	}
}

func TestCoordinateShutdownWaitsForBackgroundLoops(t *testing.T) {
	ln := newListener(t)
	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// A background loop that only exits a while after ctx cancellation: this
	// proves the coordinator waits rather than returning immediately.
	bg := make(chan struct{})
	bgExited := make(chan struct{})
	go func() {
		<-bg // barrier: only proceed once the test allows the delayed exit
		<-time.After(50 * time.Millisecond)
		close(bgExited)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	coordinatorDone := make(chan error, 1)
	go func() {
		coordinatorDone <- coordinateShutdown(ctx, srv, ln, []<-chan struct{}{bgExited}, time.Second, nil)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	// Now that ctx is cancelled, allow the background loop to proceed toward
	// its delayed exit; the coordinator must keep waiting until bgExited closes.
	close(bg)

	select {
	case <-coordinatorDone:
		// Loop had not exited yet (50ms barrier) — coordinator must still be
		// waiting. If it returned here, it failed to wait for backgroundDone.
		t.Fatal("coordinateShutdown returned before background loop exited")
	case <-time.After(20 * time.Millisecond):
		// Expected: coordinator is still blocked on <-bgExited.
	}

	select {
	case err := <-coordinatorDone:
		if err != nil {
			t.Fatalf("coordinateShutdown returned %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("coordinateShutdown did not return after background loop exited")
	}
}

// TestCoordinateShutdownClosesResourcesAfterBackgroundLoops locks down the
// §3.5 ordering invariant: onClose (which releases Redis/DB) must run only
// after every background loop has exited, so a loop's final tick can never
// race with store/DB close. The background loop records when it observes
// shutdown; onClose asserts the loop had already exited by then.
func TestCoordinateShutdownClosesResourcesAfterBackgroundLoops(t *testing.T) {
	ln := newListener(t)
	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		ReadHeaderTimeout: 5 * time.Second,
	}

	loopExited := make(chan struct{})
	var onCloseSawLoopExited int32
	bg := make(chan struct{})
	go func() {
		<-bg
		<-time.After(30 * time.Millisecond)
		close(loopExited)
	}()

	onClose := func() {
		// This runs after g.Wait(); the loop must already be done.
		select {
		case <-loopExited:
			atomic.StoreInt32(&onCloseSawLoopExited, 1)
		default:
			atomic.StoreInt32(&onCloseSawLoopExited, 0)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- coordinateShutdown(ctx, srv, ln, []<-chan struct{}{loopExited}, time.Second, onClose)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	close(bg)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("coordinateShutdown returned %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("coordinateShutdown did not return within timeout")
	}

	if atomic.LoadInt32(&onCloseSawLoopExited) == 0 {
		t.Fatal("onClose ran before the background loop exited (resource close races with final tick)")
	}
}

func TestCoordinateShutdownReturnsServeError(t *testing.T) {
	// Close the listener before serving so server.Serve fails immediately with
	// a real error (not http.ErrServerClosed).
	ln := newListener(t)
	_ = ln.Close()
	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := coordinateShutdown(ctx, srv, ln, nil, time.Second, nil)
	if err == nil {
		t.Fatal("coordinateShutdown returned nil for a closed listener, want error")
	}
	if errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("coordinateShutdown treated serve failure as ErrServerClosed: %v", err)
	}
}
