package hy2hop

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

type Forward struct {
	Port   int
	Listen netip.Addr
	v6Only bool
	ranges []portRange
}

type portRange struct {
	first int
	last  int
}

// FromStreamSettings reads the panel's opt-in from an inbound's persisted UDP Hop settings.
func FromStreamSettings(listen string, port int, raw string) (Forward, bool, error) {
	var stream struct {
		Sockopt struct {
			V6Only bool `json:"v6only"`
		} `json:"sockopt"`
		FinalMask struct {
			UDP []struct {
				Type string `json:"type"`
			} `json:"udp"`
			QuicParams struct {
				UDPHop struct {
					Ports             string `json:"ports"`
					ManageServerPorts bool   `json:"manageServerPorts"`
				} `json:"udpHop"`
			} `json:"quicParams"`
		} `json:"finalmask"`
	}
	if err := json.Unmarshal([]byte(raw), &stream); err != nil {
		if stream.FinalMask.QuicParams.UDPHop.ManageServerPorts || strings.Contains(raw, `"manageServerPorts"`) {
			return Forward{}, false, fmt.Errorf("invalid managed HY2 hop settings: %w", err)
		}
		return Forward{}, false, nil
	}
	hop := stream.FinalMask.QuicParams.UDPHop
	if !hop.ManageServerPorts {
		return Forward{}, false, nil
	}
	for _, mask := range stream.FinalMask.UDP {
		if strings.EqualFold(mask.Type, "udphop") {
			return Forward{}, true, errors.New("managed HY2 hopping uses the inbound UDP Hop range; remove the client-only udphop mask from this inbound")
		}
	}
	if port < 1 || port > 65535 {
		return Forward{}, true, errors.New("managed HY2 hopping requires an inbound port between 1 and 65535")
	}
	forward := Forward{Port: port, v6Only: stream.Sockopt.V6Only}
	if listen = strings.TrimSpace(listen); listen != "" {
		address, err := netip.ParseAddr(listen)
		address = address.Unmap()
		if err != nil || address.Zone() != "" || address.IsLoopback() || address.IsMulticast() {
			return Forward{}, true, errors.New("managed HY2 hopping requires a wildcard or local unicast IP listen address")
		}
		if !address.IsUnspecified() {
			forward.Listen = address
		}
	}
	ranges, err := parseRanges(hop.Ports, port)
	if err != nil {
		return Forward{}, true, err
	}
	forward.ranges = ranges
	return forward, true, nil
}

func parseRanges(ports string, target int) ([]portRange, error) {
	var ranges []portRange
	for part := range strings.SplitSeq(ports, ",") {
		left, right, hasRange := strings.Cut(part, "-")
		first, okFirst := portNumber(left)
		last, okLast := first, okFirst
		if hasRange {
			last, okLast = portNumber(right)
		}
		if !okFirst || !okLast || first > last {
			return nil, errors.New("HY2 hop ports must be comma-separated UDP ports or ranges between 1 and 65535")
		}
		if target < first || target > last {
			ranges = append(ranges, portRange{first, last})
			continue
		}
		if first < target {
			ranges = append(ranges, portRange{first, target - 1})
		}
		if target < last {
			ranges = append(ranges, portRange{target + 1, last})
		}
	}
	if len(ranges) == 0 {
		return nil, errors.New("HY2 hop ports must include a port other than the inbound port")
	}
	slices.SortFunc(ranges, func(a, b portRange) int { return a.first - b.first })
	merged := ranges[:0]
	for _, r := range ranges {
		if i := len(merged) - 1; i >= 0 && r.first <= merged[i].last+1 {
			merged[i].last = max(merged[i].last, r.last)
		} else {
			merged = append(merged, r)
		}
	}
	return merged, nil
}

func portNumber(raw string) (int, bool) {
	value := strings.TrimSpace(raw)
	if value == "" || strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return 0, false
	}
	n, err := strconv.Atoi(value)
	return n, err == nil && n >= 1 && n <= 65535
}

func (f Forward) Contains(port int) bool {
	return slices.ContainsFunc(f.ranges, func(r portRange) bool { return r.first <= port && port <= r.last })
}

// OverlapPort reports the first source port claimed by both rules.
func (f Forward) OverlapPort(other Forward) int {
	for i, j := 0, 0; i < len(f.ranges) && j < len(other.ranges); {
		a, b := f.ranges[i], other.ranges[j]
		if first := max(a.first, b.first); first <= min(a.last, b.last) {
			return first
		}
		if a.last < b.last {
			i++
		} else {
			j++
		}
	}
	return 0
}
