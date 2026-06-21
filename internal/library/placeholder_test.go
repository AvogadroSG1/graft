package library

import "testing"

func TestIsPlaceholder(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"${FOO}":     true,
		"${A}":       true,
		"FOO":        false,
		"":           false,
		"${}":        false,
		"prefix${X}": false,
		"${X}suffix": false,
	}
	for value, want := range cases {
		if got := IsPlaceholder(value); got != want {
			t.Errorf("IsPlaceholder(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestPlaceholderName(t *testing.T) {
	t.Parallel()
	if got := PlaceholderName("${FOO}"); got != "FOO" {
		t.Errorf("PlaceholderName(${FOO}) = %q, want FOO", got)
	}
	if got := PlaceholderName("FOO"); got != "" {
		t.Errorf("PlaceholderName(FOO) = %q, want empty", got)
	}
}
