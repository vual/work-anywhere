package cli

import "testing"

func TestParseDaemonStart(t *testing.T) {
	cases := []struct {
		args       []string
		background bool
		ok         bool
	}{
		{nil, false, true},
		{[]string{"--background"}, true, true},
		{[]string{"--quiet"}, false, false},
		{[]string{"--background", "extra"}, false, false},
	}
	for _, c := range cases {
		background, err := parseDaemonStart(c.args)
		if (err == nil) != c.ok || background != c.background {
			t.Fatalf("parseDaemonStart(%v) = %v, %v", c.args, background, err)
		}
	}
}

func TestAutostartIsCommand(t *testing.T) {
	if !IsCommand([]string{"autostart", "status"}) {
		t.Fatal("autostart must be a CLI command")
	}
	if err := runAutostart([]string{"bogus"}); err == nil {
		t.Fatal("unknown autostart action must fail")
	}
}
