package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProcessComposeProbeRejectsUnrelatedHTTPServer(t *testing.T) {
	manager := &ProcessComposeManager{}
	for _, test := range []struct {
		name   string
		status int
		want   bool
	}{
		{name: "process list", status: http.StatusOK, want: true},
		{name: "token required", status: http.StatusUnauthorized, want: true},
		{name: "unrelated server", status: http.StatusNotFound, want: false},
		{name: "server failure", status: http.StatusInternalServerError, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
			}))
			server.Start()
			defer server.Close()
			port := server.Listener.Addr().(*net.TCPAddr).Port
			if got := manager.ProbeAPI(port, time.Second); got != test.want {
				t.Fatalf("ProbeAPI status %d = %t, want %t", test.status, got, test.want)
			}
		})
	}
}
