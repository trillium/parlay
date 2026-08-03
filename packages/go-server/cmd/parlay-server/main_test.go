package main

import "testing"

func TestRefuseProductionPort(t *testing.T) {
	cases := []struct {
		addr      string
		wantError bool
	}{
		{":31337", true},
		{"127.0.0.1:31337", true},
		{"127.0.0.1:031337", true},
		{"[::1]:31337", true},
		{":4242", false},
		{"127.0.0.1:4242", false},
		{"localhost", false},
	}

	for _, c := range cases {
		err := refuseProductionPort(c.addr)
		if c.wantError && err == nil {
			t.Errorf("refuseProductionPort(%q): expected error, got nil", c.addr)
		}
		if !c.wantError && err != nil {
			t.Errorf("refuseProductionPort(%q): expected no error, got %v", c.addr, err)
		}
	}
}
