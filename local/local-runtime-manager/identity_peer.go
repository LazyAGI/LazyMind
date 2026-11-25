package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// The address locates a socket. Only the kernel peer and the live process
// ancestry authorize use of the startup result.
type identityPeerListener struct {
	*net.UnixListener
	path string
}

func (l *identityPeerListener) Close() error {
	err := l.UnixListener.Close()
	if err != nil {
		return err
	}
	return os.Remove(filepath.Dir(l.path))
}

const abstractIdentityPrefix = "@lm-id-"

func listenIdentityPeer() (net.Listener, string, error) {
	// Abstract AF_UNIX addresses are automatically released by the kernel
	// and fit even when Windows TEMP contains a long profile or Unicode path.
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, "", err
		}
		name := abstractIdentityPrefix + hex.EncodeToString(nonce[:])
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Net: "unix", Name: name})
		if err != nil {
			return nil, "", err
		}
		return listener, name, nil
	}
	// Darwin has no abstract namespace, and its per-user TMPDIR can approach
	// sockaddr_un's length limit. Protect the short pathname with a private directory.
	dir, err := os.MkdirTemp("/tmp", "lm-id-")
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, "s")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Net: "unix", Name: path})
	if err != nil {
		_ = os.Remove(dir)
		return nil, "", err
	}
	return &identityPeerListener{listener, path}, path, nil
}

func dialIdentityPeer(ctx context.Context, address string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		nonce, ok := strings.CutPrefix(address, abstractIdentityPrefix)
		if !ok || len(nonce) != 32 {
			return nil, os.ErrInvalid
		}
		if _, err := hex.DecodeString(nonce); err != nil {
			return nil, err
		}
	} else {
		dir := filepath.Dir(address)
		if filepath.Clean(address) != address || filepath.Dir(dir) != "/tmp" ||
			!strings.HasPrefix(filepath.Base(dir), "lm-id-") || filepath.Base(address) != "s" {
			return nil, os.ErrInvalid
		}
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", address)
}

func identityPeerProcess(conn net.Conn) (desktopupdate.ProcessIdentity, error) {
	socket, ok := conn.(*net.UnixConn)
	if !ok {
		return desktopupdate.ProcessIdentity{}, os.ErrInvalid
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return desktopupdate.ProcessIdentity{}, err
	}
	var pid int
	var queryErr error
	err = raw.Control(func(fd uintptr) { pid, queryErr = identityPeerPID(fd) })
	if err != nil || queryErr != nil {
		return desktopupdate.ProcessIdentity{}, errors.Join(err, queryErr)
	}
	return desktopupdate.IdentifyProcess(pid)
}

func identityPeerDescends(ctx context.Context, child, ancestor desktopupdate.ProcessIdentity) bool {
	current := child
	// Allow the shells process-compose starts on Windows and macOS. Reused
	// parent PIDs cannot denote an instance that started after its child.
	for depth := 0; depth < 64 && ctx.Err() == nil; depth++ {
		if current.PID == ancestor.PID || current.StartID < ancestor.StartID {
			return false
		}
		parentPID, err := identityPeerParent(current.PID)
		if err != nil || parentPID <= 0 {
			return false
		}
		live, _ := desktopupdate.SameProcess(current)
		parent, err := desktopupdate.IdentifyProcess(parentPID)
		if !live || err != nil || parent.StartID > current.StartID {
			return false
		}
		if parent == ancestor {
			stillChild, _ := desktopupdate.SameProcess(child)
			return stillChild && ctx.Err() == nil
		}
		current = parent
	}
	return false
}

func identityPeerExecutable(actor desktopupdate.ProcessIdentity) (string, error) {
	path, err := identityPeerImagePath(actor.PID)
	if err != nil {
		return "", err
	}
	live, err := desktopupdate.SameProcess(actor)
	if err != nil {
		return "", err
	}
	if !live || !filepath.IsAbs(path) {
		return "", os.ErrInvalid
	}
	return path, nil
}
