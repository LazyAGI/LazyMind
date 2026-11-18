package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestUpdateListenerFailureWaitsForAdmittedHTTPHandler(t *testing.T) {
	loop := newUpdateLoopBarrier(t)
	listenerReady := make(chan net.Listener, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	a := &App{runtime: &Runtime{scheduler: loop, workerPollInterval: time.Hour}, server: &http.Server{
		Addr:        "127.0.0.1:0",
		BaseContext: func(listener net.Listener) context.Context { listenerReady <- listener; return context.Background() },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(entered)
			<-release
			w.WriteHeader(http.StatusNoContent)
		}),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { _ = a.server.Close() })
	returned := make(chan error, 1)
	go func() { returned <- a.Run(ctx) }()
	awaitUpdateSignal(t, loop.entered)
	var listener net.Listener
	select {
	case listener = <-listenerReady:
	case <-time.After(3 * time.Second):
		t.Fatal("listener was not created")
	}
	requestDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://" + listener.Addr().String() + "/")
		if response != nil {
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	awaitUpdateSignal(t, entered)
	_ = listener.Close()
	awaitUpdateSignal(t, loop.cancelled)
	loop.unblock()
	awaitUpdateSignal(t, loop.finished)
	select {
	case err := <-returned:
		t.Fatalf("App.Run abandoned an admitted HTTP handler: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-returned:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("lost listener error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("App.Run did not finish")
	}
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("admitted HTTP response did not complete")
	}
}
