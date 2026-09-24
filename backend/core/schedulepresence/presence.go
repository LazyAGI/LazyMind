// Package schedulepresence gates automatic schedules and notification delivery
// while the managed Desktop window is closed. Other runtime work is unaffected.
package schedulepresence

import (
	"os"
	"strings"
	"sync/atomic"
	"time"
)

const LeaseDuration = 15 * time.Second

var desktopLeaseUntil atomic.Int64

func desktopProfile() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("LAZYMIND_RUNTIME_PROFILE")), "desktop")
}

// Active is always true outside the managed Desktop profile. A Desktop runtime
// starts paused and needs a live window to renew its short lease.
func Active() bool {
	return !desktopProfile() || desktopLeaseUntil.Load() > time.Now().UnixNano()
}

// SetActive revokes or renews the Desktop window lease. The lease also expires
// if Electron exits unexpectedly without sending a close request.
func SetActive(active bool) {
	if !active {
		desktopLeaseUntil.Store(0)
		return
	}
	desktopLeaseUntil.Store(time.Now().Add(LeaseDuration).UnixNano())
}
