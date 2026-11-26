//go:build !darwin || !lazymind_unsigned_test

package desktopupdate

import "context"

func ReleaseCompletedUnsignedInstall(context.Context, string, string) {}

func discardCancelledUnsignedInstall(string, *NativeRecord) error { return nil }
