package assistantbridge

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func verifyBridgePeer(_ context.Context, pid int, conn net.Conn, logPath string) (string, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	if !bridgeOwnsLinuxSocket(dir, conn) {
		return "", errors.New("previous Bridge does not own the TCP connection")
	}
	image, err := os.Readlink(filepath.Join(dir, "exe"))
	executable := strings.TrimSuffix(image, " (deleted)")
	if err != nil || !filepath.IsAbs(executable) {
		return "", errors.New("previous Bridge executable could not be verified")
	}
	if logPath == "" {
		return executable, nil
	}
	for _, fd := range []string{"1", "2"} {
		if path, err := os.Readlink(filepath.Join(dir, "fd", fd)); err == nil && sameBridgePath(path, logPath) {
			return executable, nil
		}
	}
	return "", errors.New("previous Bridge does not use this environment's log")
}

func bridgeOwnsLinuxSocket(dir string, conn net.Conn) bool {
	remote := conn.RemoteAddr().(*net.TCPAddr)
	local := conn.LocalAddr().(*net.TCPAddr)
	table := "tcp"
	if remote.IP.To4() == nil {
		table = "tcp6"
	}
	raw, err := os.ReadFile(filepath.Join(dir, "net", table))
	if err != nil {
		return false
	}
	var inode string
	for _, line := range strings.Split(string(raw), "\n") {
		field := strings.Fields(line)
		if len(field) > 9 && field[3] == "01" && linuxSocketAddress(field[1], remote) && linuxSocketAddress(field[2], local) {
			inode = field[9]
			break
		}
	}
	if inode == "" {
		return false
	}
	fds, err := os.ReadDir(filepath.Join(dir, "fd"))
	if err != nil {
		return false
	}
	for _, fd := range fds {
		if link, _ := os.Readlink(filepath.Join(dir, "fd", fd.Name())); link == "socket:["+inode+"]" {
			return true
		}
	}
	return false
}

func linuxSocketAddress(encoded string, address *net.TCPAddr) bool {
	host, port, ok := strings.Cut(encoded, ":")
	n, err := strconv.ParseUint(port, 16, 16)
	if !ok || err != nil || n != uint64(address.Port) {
		return false
	}
	ip, err := hex.DecodeString(host)
	if err != nil || len(ip)%4 != 0 {
		return false
	}
	// /proc prints TCP addresses as native-endian uint32 words.
	for i := 0; i < len(ip); i += 4 {
		binary.BigEndian.PutUint32(ip[i:], binary.NativeEndian.Uint32(ip[i:]))
	}
	return net.IP(ip).Equal(address.IP)
}
