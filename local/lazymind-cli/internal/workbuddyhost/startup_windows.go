package workbuddyhost

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Read the persistent user environment, never the possibly stale environment
// inherited by LazyMind. Explorer passes this setting to subsequent GUI launches.
type windowsStartupEnvironment struct {
	keyPath string
	name    string
	notify  func() error
}

func (e windowsStartupEnvironment) Get() (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, e.keyPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, _, err := key.GetStringValue(e.name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	return value, err
}

func (e windowsStartupEnvironment) Set(value string) error { return e.write(&value) }
func (e windowsStartupEnvironment) Unset() error           { return e.write(nil) }
func (e windowsStartupEnvironment) Refresh() error         { return e.notify() }

func (e windowsStartupEnvironment) write(value *string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, e.keyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	if value == nil {
		err = key.DeleteValue(e.name)
		if errors.Is(err, registry.ErrNotExist) {
			err = nil
		}
	} else {
		err = key.SetStringValue(e.name, *value)
	}
	key.Close()
	if err != nil {
		return err
	}
	return e.notify()
}

// https://learn.microsoft.com/windows/win32/winmsg/wm-settingchange
// Updating HKCU alone does not refresh Explorer's launch environment.
func broadcastEnvironmentChange() error {
	message, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return err
	}
	var result uintptr
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	ok, _, callErr := proc.Call(0xffff, 0x001a, 0, uintptr(unsafe.Pointer(message)), 0x0002, 2000, uintptr(unsafe.Pointer(&result)))
	runtime.KeepAlive(message)
	if ok == 0 {
		if callErr == syscall.Errno(0) {
			callErr = windows.ERROR_TIMEOUT
		}
		return fmt.Errorf("refresh Windows desktop environment: %w", callErr)
	}
	return nil
}

func configureDesktopStartup(ctx context.Context, home string, port int, enabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return updateStartupEnvironment(home, port, enabled, windowsStartupEnvironment{
		keyPath: `Environment`, name: desktopDebugPortEnv, notify: broadcastEnvironmentChange,
	})
}
