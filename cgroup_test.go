package main

import "testing"

func TestParseMemory(t *testing.T) {
	cases := map[string]int64{
		"100":  100,
		"1k":   1 << 10,
		"256m": 256 << 20,
		"1g":   1 << 30,
		"1G":   1 << 30,
	}
	for in, want := range cases {
		got, err := parseMemory(in)
		if err != nil {
			t.Errorf("parseMemory(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseMemory(%q) = %d, want %d", in, got, want)
		}
	}

	for _, in := range []string{"", "abc", "10x"} {
		if _, err := parseMemory(in); err == nil {
			t.Errorf("parseMemory(%q) = nil error, want error", in)
		}
	}
}
