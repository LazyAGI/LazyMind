package contract

import "testing"

func TestPageRequestNormalizePageSize(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "zero", in: 0, want: DefaultPageSize},
		{name: "negative", in: -5, want: DefaultPageSize},
		{name: "min", in: 1, want: 1},
		{name: "max", in: 100, want: 100},
		{name: "over max", in: 101, want: MaxPageSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (PageRequest{PageSize: tt.in}).Normalize().PageSize; got != tt.want {
				t.Fatalf("PageSize = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPageRequestNormalizePreservesPageToken(t *testing.T) {
	token := "  offset:20  "
	got := (PageRequest{PageSize: 20, PageToken: token}).Normalize()
	if got.PageToken != token {
		t.Fatalf("PageToken = %q, want %q", got.PageToken, token)
	}
}
