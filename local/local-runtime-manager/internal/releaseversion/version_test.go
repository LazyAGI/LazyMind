package releaseversion

import "testing"

func TestCompareInstallerVersions(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		want        int
	}{
		{"0.3.0-alpha.4", "0.3.0", -1},
		{"0.3.0-alpha.9", "0.3.0-alpha.10", -1},
		{"0.3.0-beta.1", "0.3.0-rc.1", -1},
		{"0.3.0-rc.1", "0.3.0", -1},
		{"0.3.0+one", "0.3.0+two", 0},
		{"1.10.0", "1.9.0", 1},
		{"1.2.3", "1.2.3", 0},
	} {
		t.Run(tc.left+"_to_"+tc.right, func(t *testing.T) {
			got, err := Compare(tc.left, tc.right)
			if err != nil || got != tc.want {
				t.Fatalf("Compare(%q, %q) = %d, %v; want %d", tc.left, tc.right, got, err, tc.want)
			}
		})
	}
}

func TestCompareRejectsInvalidVersions(t *testing.T) {
	for _, value := range []string{"", "v1.2.3", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03", "1.2.3-alpha.01", "1.2.3\n", " 1.2.3", "garbage", "1.2.3-", "1.2.3+", "1.2.3-alpha..1", "1.2.3+build..1", "1.2.3-alpha_1", "1.2.3+bad!"} {
		if _, err := Compare(value, "1.2.3"); err == nil {
			t.Errorf("accepted invalid installed version %q", value)
		}
		if _, err := Compare("1.2.3", value); err == nil {
			t.Errorf("accepted invalid candidate version %q", value)
		}
	}
}

func TestCompletePrereleasePrecedence(t *testing.T) {
	// SemVer's normative example, exercised in both directions and at equality.
	ordered := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0"}
	for leftIndex, left := range ordered {
		for rightIndex, right := range ordered {
			want := 0
			if leftIndex < rightIndex {
				want = -1
			} else if leftIndex > rightIndex {
				want = 1
			}
			if got, err := Compare(left, right); err != nil || got != want {
				t.Errorf("Compare(%q, %q) = %d, %v; want %d", left, right, got, err, want)
			}
		}
	}
	if got, err := Compare("1.0.0-alpha.1+one", "1.0.0-alpha.1+two"); err != nil || got != 0 {
		t.Errorf("prerelease build metadata changed precedence: %d, %v", got, err)
	}
}
