package hy2hop

import (
	"strings"
	"testing"
)

func TestManagedPortsAreExplicitAndValidated(t *testing.T) {
	legacy := `{"finalmask":{"quicParams":{"udpHop":{"ports":"20000-50000"}}}}`
	if _, managed, err := FromStreamSettings("", 443, legacy); managed || err != nil {
		t.Fatalf("legacy advertised range changed the firewall: managed=%v, err=%v", managed, err)
	}
	withMask := strings.Replace(managedStream("20000-20010"), `"quicParams":`, `"udp":[{"type":"udphop","settings":{"remotePorts":"21000-21010"}}],"quicParams":`, 1)
	if _, _, err := FromStreamSettings("", 443, withMask); err == nil || !strings.Contains(err.Error(), "client-only udphop") {
		t.Fatalf("a client-only mask would advertise a different range: %v", err)
	}
	for _, ports := range []string{"", "0", "65536", "20002-20000", "20000,", "20000\nflush ruleset", "443"} {
		t.Run(ports, func(t *testing.T) {
			if _, _, err := FromStreamSettings("", 443, managedStream(ports)); err == nil || !strings.Contains(err.Error(), "HY2 hop ports") {
				t.Fatalf("got %v for invalid range %q", err, ports)
			}
		})
	}
	forward, managed, err := FromStreamSettings("", 20005, managedStream("20000-20010, 20012"))
	if err != nil || !managed || !forward.Contains(20000) || !forward.Contains(20012) || forward.Contains(20005) || forward.Contains(20011) {
		t.Fatalf("range includes wrong ports: %+v, managed=%v, err=%v", forward, managed, err)
	}
}

func managedStream(ports string) string {
	return `{"finalmask":{"quicParams":{"udpHop":{"ports":"` + strings.ReplaceAll(ports, "\n", `\n`) + `","manageServerPorts":true}}}}`
}
