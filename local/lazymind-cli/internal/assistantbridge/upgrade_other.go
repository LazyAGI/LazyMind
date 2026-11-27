//go:build !darwin && !windows && !linux

package assistantbridge

import (
	"context"
	"errors"
	"net"
)

func verifyBridgePeer(context.Context, int, net.Conn, string) (string, error) {
	return "", errors.New("legacy Bridge ownership cannot be verified on this platform")
}
