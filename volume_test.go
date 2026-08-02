package main

import "testing"

func TestParseVolume(t *testing.T) {
	cases := []struct {
		spec     string
		wantHost string
		wantCont string
		wantRO   bool
		wantErr  bool
	}{
		{spec: "/host/data:/data", wantHost: "/host/data", wantCont: "/data"},
		{spec: "/host/data:/data:ro", wantHost: "/host/data", wantCont: "/data", wantRO: true},
		{spec: "/host/data:/data/../../etc", wantHost: "/host/data", wantCont: "/etc"}, // Clean() caps ".." at root, stays absolute
		{spec: "/host/data:relative", wantErr: true},
		{spec: "/host/data:..", wantErr: true},
		{spec: "/host/data", wantErr: true},
		{spec: "", wantErr: true},
		{spec: "/host/data:/data:rw", wantErr: true},
	}

	for _, c := range cases {
		host, cont, ro, err := parseVolume(c.spec)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseVolume(%q) = nil error, want error", c.spec)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseVolume(%q) unexpected error: %v", c.spec, err)
			continue
		}
		if host != c.wantHost || cont != c.wantCont || ro != c.wantRO {
			t.Errorf("parseVolume(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.spec, host, cont, ro, c.wantHost, c.wantCont, c.wantRO)
		}
	}
}
