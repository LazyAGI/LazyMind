package skillv2

import (
	"strings"
	"time"
)

const (
	CallModeDisabled = "disabled" // Legacy API alias.
	CallModeManual   = "manual"
	CallModeOnDemand = "on_demand"
	CallModePriority = "priority"

	DefaultInjectLimit = 20
)

func NormalizeCallMode(callMode string, enabled bool) string {
	switch strings.ToLower(strings.TrimSpace(callMode)) {
	case CallModeDisabled, CallModeManual:
		return CallModeManual
	case CallModeOnDemand, "ondemand", "auto":
		return CallModeOnDemand
	case CallModePriority, "always", "prefer":
		return CallModePriority
	}
	if enabled {
		return CallModeOnDemand
	}
	return CallModeManual
}

func CallModeEnabled(callMode string) bool {
	return NormalizeCallMode(callMode, false) != CallModeManual
}

func NextSortRank(now time.Time) int64 {
	return now.UnixMilli()
}

func ResolveCallMode(callMode *string, enabled *bool, currentMode string, currentEnabled bool) (string, bool, bool) {
	nextEnabled := currentEnabled
	nextMode := NormalizeCallMode(currentMode, currentEnabled)
	changed := false
	if enabled != nil {
		nextEnabled = *enabled
		changed = true
		if !nextEnabled {
			nextMode = CallModeManual
		} else if nextMode == CallModeManual {
			nextMode = CallModeOnDemand
		}
	}
	if callMode != nil {
		nextMode = NormalizeCallMode(*callMode, nextEnabled)
		changed = true
		nextEnabled = CallModeEnabled(nextMode)
	}
	if !changed {
		return nextMode, nextEnabled, false
	}
	return nextMode, nextEnabled, true
}

func ValidCallMode(callMode string) bool {
	switch strings.ToLower(strings.TrimSpace(callMode)) {
	case CallModeDisabled, CallModeManual, CallModeOnDemand, CallModePriority:
		return true
	default:
		return false
	}
}
