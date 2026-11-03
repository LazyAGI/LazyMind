package desktopsetup

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeWindowsProcessAndListenerIdentity(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if ok, err := Running(ctx, exe); err != nil || !ok {
		t.Fatalf("own process: %v %v", ok, err)
	}
	wrong := filepath.Join(t.TempDir(), filepath.Base(exe))
	if ok, err := Running(ctx, wrong); err != nil || ok {
		t.Fatalf("same name in another directory: %v %v", ok, err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if !ListenerMatches(ctx, exe, port) {
		t.Fatal("own listener was not recognized")
	}
	if ListenerMatches(ctx, wrong, port) {
		t.Fatal("foreign executable was accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if ListenerMatches(canceled, exe, port) {
		t.Fatal("cancelled probe was accepted")
	}
	if _, err := Running(canceled, exe); err == nil {
		t.Fatal("cancelled process query succeeded")
	}
	listener.Close()
	if ListenerMatches(ctx, exe, port) {
		t.Fatal("closed listener was accepted")
	}
}
