package evalset

import "testing"

func TestEscapeLikePattern(t *testing.T) {
	tests := []struct {
		name    string
		keyword string
		want    string
	}{
		{name: "backslash", keyword: `\`, want: `\`},
		{name: "percent", keyword: `100%`, want: `100!%`},
		{name: "underscore", keyword: `case_1`, want: `case!_1`},
		{name: "escape", keyword: `bang!`, want: `bang!!`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeLikePattern(tt.keyword); got != tt.want {
				t.Fatalf("escapeLikePattern(%q) = %q, want %q", tt.keyword, got, tt.want)
			}
		})
	}
}

func TestContainsLikePatternWrapsEscapedKeyword(t *testing.T) {
	if got := containsLikePattern(`\`); got != `%\%` {
		t.Fatalf("containsLikePattern(backslash) = %q, want %q", got, `%\%`)
	}
}
