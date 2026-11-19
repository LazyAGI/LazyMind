package desktopsetup

import "errors"

// ErrQuitRequired means the user must finish closing the desktop application
// before setup can continue. Inspection and launch failures are separate errors.
var ErrQuitRequired = errors.New("agent must exit before setup can continue")
