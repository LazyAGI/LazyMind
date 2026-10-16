package codexruntime

import (
	"encoding/binary"
	"errors"
	"golang.org/x/sys/windows"
	"net"
	"net/url"
	"strconv"
	"unsafe"
)

func listenerIdentity(c *runtimeConfig) (any, error) { return c.Endpoint, nil }
func verifyListener(c *runtimeConfig, r *backendRecord) error {
	if !sameProcess(r.Process) || r.Endpoint != c.Endpoint {
		return errors.New("backend identity changed")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return err
	}
	pid, err := tcpListenerPID(port)
	if err != nil {
		return err
	}
	if pid != uint32(r.Process.PID) {
		return errors.New("loopback listener does not belong to backend")
	}
	return nil
}

// TCP_TABLE_OWNER_PID_LISTENER, AF_INET. Refuse a wildcard listener as well as
// a different PID before sending the private capability in the handshake.
func tcpListenerPID(port int) (uint32, error) {
	proc := windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")
	var size uint32
	result, _, _ := proc.Call(0, uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, 3, 0)
	if result != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
		return 0, errors.New("cannot size TCP owner table")
	}
	for i := 0; i < 3; i++ {
		data := make([]byte, size)
		result, _, _ = proc.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, 3, 0)
		if result == uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
			continue
		}
		if result != 0 || len(data) < 4 {
			return 0, errors.New("cannot read TCP owner table")
		}
		count := int(binary.LittleEndian.Uint32(data[:4]))
		if count > (len(data)-4)/24 {
			return 0, errors.New("invalid TCP owner table")
		}
		for n := 0; n < count; n++ {
			row := data[4+n*24 : 4+(n+1)*24]
			if int(binary.BigEndian.Uint16(row[8:10])) == port {
				if row[4] != 127 || row[5] != 0 || row[6] != 0 || row[7] != 1 {
					return 0, errors.New("backend listener must be loopback only")
				}
				return binary.LittleEndian.Uint32(row[20:24]), nil
			}
		}
		return 0, errors.New("backend listener not ready")
	}
	return 0, errors.New("TCP owner table changed repeatedly")
}
func removeStaleListener(c *runtimeConfig, r *backendRecord) error {
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", u.Host)
	if err != nil {
		return errors.New("configured backend address is occupied; refusing takeover")
	}
	return listener.Close()
}
