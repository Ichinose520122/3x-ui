package hy2hop

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	tableName    = "xui_hy2_hop"
	tableComment = "3x-ui managed HY2 port hopping"
	chainName    = "prerouting"
	missingTable = "No such file or directory"
)

var errUnsupportedPlatform = errors.New("automatic HY2 port forwarding requires Linux")

// Manager reconciles the one nftables table reserved for panel-managed HY2 forwards.
type Manager struct {
	mu          sync.Mutex
	run         func(context.Context, string, ...string) (string, error)
	clean       bool
	wantedRules string
	liveRules   string
}

func NewManager() *Manager {
	return &Manager{run: runNFT}
}

// CheckAvailability dry-runs the rule on the node before saving an active inbound.
func CheckAvailability(ctx context.Context, forward Forward) error {
	m := NewManager()
	batch, _, err := m.prepare(ctx, renderTable([]Forward{forward}))
	if err != nil {
		return err
	}
	_, err = m.run(ctx, batch, "-c", "-f", "-")
	return err
}

func (m *Manager) Reconcile(ctx context.Context, forwards []Forward) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	if len(forwards) == 0 && m.clean {
		return nil
	}

	wanted := renderTable(forwards)
	batch, live, err := m.prepare(ctx, wanted)
	if err != nil {
		if len(forwards) == 0 && m.wantedRules == "" && ctx.Err() == nil {
			m.clean = errors.Is(err, errUnsupportedPlatform)
			return nil
		}
		return err
	}
	if live != "" && wanted == m.wantedRules && live == m.liveRules {
		return nil
	}
	if batch == "" {
		m.clean = true
		m.wantedRules, m.liveRules = "", ""
		return nil
	}

	if _, err := m.run(ctx, batch, "-f", "-"); err != nil {
		return err
	}
	m.clean = wanted == ""
	m.wantedRules = wanted
	m.liveRules = ""
	if m.clean {
		return nil
	}
	live, err = m.listTable(ctx)
	if err != nil {
		return err
	}
	if ownedTableHandle(live) == 0 {
		return fmt.Errorf("could not verify ownership of nftables table inet %s", tableName)
	}
	if !strings.Contains(live, "\n\tchain "+chainName+" {") || strings.Count(live, " dnat to :") != len(forwards) {
		return fmt.Errorf("nftables table inet %s is missing HY2 forwarding rules", tableName)
	}
	m.liveRules = live
	return nil
}

func (m *Manager) prepare(ctx context.Context, wanted string) (batch, live string, err error) {
	live, err = m.listTable(ctx)
	if err != nil {
		if strings.Contains(live, missingTable) {
			return wanted, "", nil
		}
		return "", "", err
	}
	if handle := ownedTableHandle(live); handle != 0 {
		// The handle guards against the name being reused after the lookup.
		return fmt.Sprintf("delete table inet handle %d\n%s", handle, wanted), live, nil
	}
	if wanted == "" {
		return "", "", nil
	}
	return "", "", fmt.Errorf("nftables table inet %s is already in use and is not owned by 3x-ui", tableName)
}

func (m *Manager) listTable(ctx context.Context) (string, error) {
	return m.run(ctx, "", "-s", "-n", "-a", "list", "table", "inet", tableName)
}

func ownedTableHandle(output string) uint64 {
	header, contents, _ := strings.Cut(output, "\n")
	handle, ok := strings.CutPrefix(header, "table inet "+tableName+" { # handle ")
	if !ok {
		return 0
	}
	comment, _, _ := strings.Cut(contents, "\n")
	if strings.TrimSuffix(strings.TrimSpace(comment), ";") != "comment "+strconv.Quote(tableComment) {
		return 0
	}
	id, _ := strconv.ParseUint(strings.TrimSpace(handle), 10, 64)
	return id
}

func renderTable(forwards []Forward) string {
	if len(forwards) == 0 {
		return ""
	}
	var b strings.Builder
	// nftables 1.0.6 drops chains and rules nested inside a create table command.
	fmt.Fprintf(&b, "create table inet %s { comment %q; }\n", tableName, tableComment)
	fmt.Fprintf(&b, "add chain inet %s %s { type nat hook prerouting priority dstnat + 1; policy accept; }\n", tableName, chainName)
	for _, f := range forwards {
		match := ""
		switch {
		case f.Listen.Is4():
			match = fmt.Sprintf("ip daddr %s ", f.Listen)
		case f.Listen.Is6():
			match = fmt.Sprintf("ip6 daddr %s ", f.Listen)
		case f.v6Only:
			match = "meta nfproto ipv6 "
		}
		ports := make([]string, 0, len(f.ranges))
		for _, r := range f.ranges {
			if r.first == r.last {
				ports = append(ports, strconv.Itoa(r.first))
			} else {
				ports = append(ports, fmt.Sprintf("%d-%d", r.first, r.last))
			}
		}
		prefix := fmt.Sprintf("add rule inet %s %s %sudp dport { %s } iifname != \"lo\" fib daddr type local", tableName, chainName, match, strings.Join(ports, ", "))
		// A bound UDP socket takes priority over hopping, including services outside the panel.
		fmt.Fprintf(&b, "%s socket mark >= 0 return\n%s counter dnat to :%d\n", prefix, prefix, f.Port)
	}
	return b.String()
}

func runNFT(parent context.Context, stdin string, args ...string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errUnsupportedPlatform
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nft", args...)
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")
	cmd.Stdin = strings.NewReader(stdin)
	output, err := cmd.Output()
	out := strings.TrimSpace(string(output))
	if errors.Is(err, exec.ErrNotFound) {
		return out, errors.New("automatic HY2 port forwarding requires nftables (nft)")
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			out = strings.TrimSpace(string(exit.Stderr))
		}
		return out, fmt.Errorf("nftables for HY2 port forwarding: %w (%s)", err, out)
	}
	return out, nil
}
