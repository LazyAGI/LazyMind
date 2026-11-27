package assistantbridge

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func verifyBridgePeer(ctx context.Context, pid int, conn net.Conn, logPath string) (string, error) {
	pidText := strconv.Itoa(pid)
	// Verify the accepted socket rather than an unrelated listener sharing its port.
	sockets, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-a", "-p", pidText, "-iTCP", "-sTCP:ESTABLISHED", "-Fn").Output()
	if err != nil || !strings.Contains("\n"+string(sockets), "\nn"+conn.RemoteAddr().String()+"->"+conn.LocalAddr().String()+"\n") {
		return "", errors.New("previous Bridge does not own the TCP connection")
	}
	image, err := exec.CommandContext(ctx, "/bin/ps", "-ww", "-p", pidText, "-o", "comm=").Output()
	executable := strings.TrimSpace(string(image))
	if err != nil || !filepath.IsAbs(executable) {
		return "", errors.New("previous Bridge executable could not be verified")
	}
	if logPath == "" {
		return executable, nil
	}
	// Legacy assistant start opens this environment's log and assigns it to both
	// standard descriptors. Unlike health and endpoint files, this is OS state.
	logs, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-a", "-p", pidText, "-d", "1,2", "-Fn").Output()
	if err == nil {
		for _, line := range strings.Split(string(logs), "\n") {
			if path, ok := strings.CutPrefix(line, "n"); ok && sameBridgePath(path, logPath) {
				return executable, nil
			}
		}
	}
	return "", errors.New("previous Bridge does not use this environment's log")
}
