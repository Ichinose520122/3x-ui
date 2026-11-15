package service

import (
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const managedHopStream = `{"finalmask":{"quicParams":{"udpHop":{"ports":"20000-20100","manageServerPorts":true}}}}`

func TestManagedHysteriaHopConflicts(t *testing.T) {
	setupConflictDB(t)
	seedInboundConflict(t, "managed-hy2", "", 443, model.Hysteria, managedHopStream, `{}`)
	seedInboundConflict(t, "existing-udp", "", 20250, model.WireGuard, "", `{}`)

	cases := []struct {
		name    string
		inbound model.Inbound
		want    string
	}{
		{"UDP port inside managed range", model.Inbound{Enable: true, Port: 20050, Protocol: model.TUIC}, "managed-hy2"},
		{"TCP port inside managed range", model.Inbound{Enable: true, Port: 20050, Protocol: model.VLESS}, ""},
		{"advertised range alone", model.Inbound{Enable: true, Port: 8443, Protocol: model.Hysteria, StreamSettings: `{"finalmask":{"quicParams":{"udpHop":{"ports":"20000-20100"}}}}`}, ""},
		{"overlapping managed ranges", model.Inbound{Enable: true, Port: 8443, Protocol: model.Hysteria, StreamSettings: managedHopStream}, "managed-hy2"},
		{"manage range around existing UDP listener", model.Inbound{Enable: true, Port: 8443, Protocol: model.Hysteria, StreamSettings: strings.Replace(managedHopStream, "20000-20100", "20200-20300", 1)}, "existing-udp"},
		{"unrelated node", model.Inbound{Enable: true, Port: 20050, Protocol: model.Hysteria, NodeID: new(1)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conflict, err := checkPortConflictTx(database.GetDB(), &tc.inbound, 0)
			var detail string
			if conflict != nil {
				detail = conflict.String()
			}
			if err != nil {
				detail = err.Error()
			}
			if tc.want == "" && detail != "" || tc.want != "" && !strings.Contains(detail, tc.want) {
				t.Fatalf("got %q, want conflict with %q", detail, tc.want)
			}
		})
	}
}

func TestDesiredHysteriaHops(t *testing.T) {
	setupConflictDB(t)
	seedInboundConflict(t, "active", "", 443, model.Hysteria, managedHopStream, `{}`)
	seedInboundConflict(t, "disabled", "", 444, model.Hysteria, managedHopStream, `{}`)
	seedInboundConflictNode(t, "remote", "", 445, model.Hysteria, managedHopStream, `{}`, new(1))
	seedInboundConflict(t, "legacy", "", 446, model.Hysteria, `{"finalmask":{"quicParams":{"udpHop":{"ports":"20000-20100"}}}}`, `{}`)
	db := database.GetDB()
	if err := db.Model(model.Inbound{}).Where("tag = ?", "disabled").Update("enable", false).Error; err != nil {
		t.Fatal(err)
	}
	svc := &InboundService{}
	forwards, warnings, err := svc.DesiredHysteriaHops(t.Context())
	if err != nil || warnings != nil || len(forwards) != 1 || forwards[0].Port != 443 {
		t.Fatalf("expected one active local forward, got %v, warnings=%v, err=%v", forwards, warnings, err)
	}
	seedInboundConflict(t, "restored-udp", "", 20050, model.WireGuard, "", `{}`)
	forwards, warnings, err = svc.DesiredHysteriaHops(t.Context())
	if err != nil || warnings == nil || !strings.Contains(warnings.Error(), "restored-udp") || len(forwards) != 0 {
		t.Fatalf("expected a restored conflict to suppress the forward, got %v, warnings=%v, err=%v", forwards, warnings, err)
	}
	if err = db.Model(model.Inbound{}).Where("tag = ?", "active").Update("stream_settings", `{}`).Error; err != nil {
		t.Fatal(err)
	}
	forwards, warnings, err = svc.DesiredHysteriaHops(t.Context())
	if err != nil || warnings != nil || len(forwards) != 0 {
		t.Fatalf("clearing opt-in should clear desired rules, got %v, warnings=%v, err=%v", forwards, warnings, err)
	}
}
