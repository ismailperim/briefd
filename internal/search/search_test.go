package search

import "testing"

func TestBuildMatch(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"retry policy", `"retry" OR "policy"`},
		{`"quoted" AND (injection) NOT x*`, `"quoted" OR "and" OR "injection" OR "not" OR "x"`},
		{"Ödeme iadesi", `"ödeme" OR "iadesi"`},
		{"payment-service v2", `"payment" OR "service" OR "v2"`},
	}
	for _, tt := range tests {
		if got := BuildMatch(tt.in); got != tt.want {
			t.Errorf("BuildMatch(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
