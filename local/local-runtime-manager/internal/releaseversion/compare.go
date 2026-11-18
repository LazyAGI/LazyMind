package releaseversion

import (
	"fmt"
	"regexp"

	"golang.org/x/mod/semver"
)

var fullVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+]|$)`)

// Compare accepts full package SemVer (without a v prefix) and returns -1, 0
// or 1. Build metadata does not change precedence. Invalid input fails closed.
func Compare(installed, candidate string) (int, error) {
	for _, version := range []string{installed, candidate} {
		// x/mod also accepts vMAJOR and vMAJOR.MINOR; package versions do not.
		if !fullVersion.MatchString(version) || !semver.IsValid("v"+version) {
			return 0, fmt.Errorf("invalid package version %q", version)
		}
	}
	return semver.Compare("v"+installed, "v"+candidate), nil
}
