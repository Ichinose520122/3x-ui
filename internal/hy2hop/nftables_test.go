package hy2hop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestReconcileRestoresAndRemovesManagedRules(t *testing.T) {
	forward, _, err := FromStreamSettings("2001:db8::1", 443, managedStream("20000-20002,20004"))
	if err != nil {
		t.Fatal(err)
	}
	const match = `ip6 daddr 2001:db8::1 udp dport { 20000-20002, 20004 } iifname != "lo" fib daddr type local`
	const rule = match + ` counter dnat to :443`
	const table = "table inet xui_hy2_hop { # handle 17\n\tcomment \"3x-ui managed HY2 port hopping\"\n}"
	var live, lastBatch string
	var writes int
	manager := &Manager{run: func(_ context.Context, stdin string, args ...string) (string, error) {
		if args[0] == "-s" {
			if live == "" {
				return "Error: " + missingTable, errors.New("table not found")
			}
			return live, nil
		}
		writes++
		lastBatch = stdin
		if strings.Contains(stdin, "create table inet "+tableName+" {\n") {
			live = table
		} else if stdin == "delete table inet handle 17\n" {
			live = ""
		} else {
			t.Fatalf("unexpected nft batch: %s", stdin)
		}
		return "", nil
	}}
	ctx := t.Context()
	if err := manager.Reconcile(ctx, []Forward{forward}); err != nil || !strings.Contains(lastBatch, rule) || writes != 1 {
		t.Fatalf("first apply: batch=%q writes=%d err=%v", lastBatch, writes, err)
	}
	if guard := strings.Index(lastBatch, match+` socket mark >= 0 return`); guard < 0 || guard > strings.Index(lastBatch, rule) {
		t.Fatalf("existing UDP sockets should be checked before NAT: %s", lastBatch)
	}
	if err := manager.Reconcile(ctx, []Forward{forward}); err != nil || writes != 1 {
		t.Fatalf("unchanged rules should not be rewritten: writes=%d err=%v", writes, err)
	}
	updated, _, err := FromStreamSettings("", 443, managedStream("21000-21002"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(ctx, []Forward{updated}); err != nil || writes != 2 ||
		!strings.HasPrefix(lastBatch, "delete table inet handle 17\ncreate table ") || !strings.Contains(lastBatch, "21000-21002") {
		t.Fatalf("port edit should replace the owned table in one batch: batch=%q writes=%d err=%v", lastBatch, writes, err)
	}
	live = ""
	if err := manager.Reconcile(ctx, []Forward{updated}); err != nil || live == "" || writes != 3 {
		t.Fatalf("rule should recover after firewall reload: table=%q writes=%d err=%v", live, writes, err)
	}
	if err := manager.Reconcile(ctx, nil); err != nil || live != "" || writes != 4 {
		t.Fatalf("disabled inbound should remove its rules: table=%q writes=%d err=%v", live, writes, err)
	}
}

func TestReconcileKeepsUnownedTable(t *testing.T) {
	forward, _, err := FromStreamSettings("", 443, managedStream("20000-20002"))
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{run: func(_ context.Context, stdin string, args ...string) (string, error) {
		if stdin != "" {
			t.Fatalf("would overwrite the administrator's table: %s", stdin)
		}
		return fmt.Sprintf("table inet %s { # handle 17\n\tchain input { # handle 123\n\t\tcomment %q\n\t}\n}", tableName, tableComment), nil
	}}
	if err := manager.Reconcile(t.Context(), []Forward{forward}); err == nil || !strings.Contains(err.Error(), "not owned by 3x-ui") {
		t.Fatalf("got %v, want refusal to overwrite foreign table", err)
	}
}
