package core

import "testing"

func TestParsePingTarget(t *testing.T) {
	t.Parallel()

	_, address, kind, err := parsePingTarget("dc5")
	if err != nil || address != "91.108.56.130" || kind != "数据中心" {
		t.Fatalf("dc target = %q, %q, %v", address, kind, err)
	}
	_, address, kind, err = parsePingTarget("example.com")
	if err != nil || address != "example.com" || kind != "域名" {
		t.Fatalf("domain target = %q, %q, %v", address, kind, err)
	}
	if _, _, _, err := parsePingTarget("example.com;id"); err == nil {
		t.Fatal("unsafe target was accepted")
	}
}

func TestParsePingOutput(t *testing.T) {
	t.Parallel()

	average, loss, ok := parsePingOutput(
		"3 packets transmitted, 3 received, 0% packet loss\n" +
			"rtt min/avg/max/mdev = 10.1/20.4/31.0/2.0 ms",
	)
	if !ok || average != "20ms" || loss != "0%" {
		t.Fatalf("ping result = %q, %q, %v", average, loss, ok)
	}
}
