//go:build !darwin && !windows

package workbuddyhost

import "context"

// WorkBuddy desktop integration is supported on macOS and Windows.
func configureDesktopStartup(context.Context, string, int, bool) error { return nil }

func checkDesktopStartup(context.Context, string, int) error { return nil }
