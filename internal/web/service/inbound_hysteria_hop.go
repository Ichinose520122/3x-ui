package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/hy2hop"

	"gorm.io/gorm"
)

type managedHysteriaHop struct {
	inbound *model.Inbound
	forward hy2hop.Forward
}

func inboundHysteriaHop(inbound *model.Inbound) (managedHysteriaHop, bool, error) {
	if inbound.Protocol != model.Hysteria {
		return managedHysteriaHop{}, false, nil
	}
	forward, managed, err := hy2hop.FromStreamSettings(inbound.Listen, inbound.Port, inbound.StreamSettings)
	if err != nil {
		return managedHysteriaHop{}, false, err
	}
	return managedHysteriaHop{inbound: inbound, forward: forward}, managed, nil
}

func validateHysteriaHop(inbound *model.Inbound) error {
	hop, managed, err := inboundHysteriaHop(inbound)
	if err != nil {
		return err
	}
	if managed && inbound.NodeID == nil && inbound.Enable {
		return hy2hop.CheckAvailability(context.Background(), hop.forward)
	}
	return nil
}

// DesiredHysteriaHops reads the node's own enabled rows and rechecks imported configurations.
func (s *InboundService) DesiredHysteriaHops(ctx context.Context) (forwards []hy2hop.Forward, warnings error, err error) {
	var rows []*model.Inbound
	err = database.GetDB().WithContext(ctx).Model(model.Inbound{}).
		Where("enable = ? AND node_id IS NULL AND protocol = ?", true, model.Hysteria).
		Select("id", "tag", "remark", "protocol", "port", "listen", "stream_settings").Order("id").Find(&rows).Error
	if err != nil {
		return nil, nil, err
	}
	var configured []managedHysteriaHop
	var issues []error
	for _, row := range rows {
		hop, managed, err := inboundHysteriaHop(row)
		if err != nil {
			issues = append(issues, fmt.Errorf("inbound %s: %w", row.Tag, err))
		} else if managed {
			configured = append(configured, hop)
		}
	}
	if len(configured) == 0 {
		return nil, errors.Join(issues...), nil
	}
	var otherRows []*model.Inbound
	err = database.GetDB().WithContext(ctx).Model(model.Inbound{}).
		Where("enable = ? AND node_id IS NULL AND protocol <> ?", true, model.Hysteria).
		Select("id", "tag", "remark", "protocol", "port", "listen", "stream_settings", "settings").Order("id").Find(&otherRows).Error
	if err != nil {
		return nil, nil, err
	}
	rows = append(rows, otherRows...)

	for _, hop := range configured {
		if conflict := hop.conflict(rows, configured); conflict != nil {
			issues = append(issues, fmt.Errorf("inbound %s: %s", hop.inbound.Tag, conflict))
			continue
		}
		forwards = append(forwards, hop.forward)
	}
	return forwards, errors.Join(issues...), nil
}

func (h managedHysteriaHop) conflict(rows []*model.Inbound, hops []managedHysteriaHop) *portConflictDetail {
	for _, row := range rows {
		if row.Id != h.inbound.Id && h.forward.Contains(row.Port) &&
			listenOverlaps(inboundBindAddr(h.inbound), inboundBindAddr(row)) &&
			inboundTransports(row.Protocol, row.StreamSettings, row.Settings)&transportUDP != 0 {
			return hysteriaHopConflict(row, row.Port, false)
		}
	}
	for _, other := range hops {
		if other.inbound.Id != h.inbound.Id && listenOverlaps(inboundBindAddr(h.inbound), inboundBindAddr(other.inbound)) {
			if port := h.forward.OverlapPort(other.forward); port != 0 {
				return hysteriaHopConflict(other.inbound, port, true)
			}
		}
	}
	return nil
}

func hysteriaHopConflict(row *model.Inbound, port int, reserved bool) *portConflictDetail {
	return &portConflictDetail{
		InboundID: row.Id, Remark: row.Remark, Tag: row.Tag, Listen: row.Listen,
		Port: port, Transports: transportUDP, HysteriaHop: reserved,
	}
}

func checkHysteriaHopConflictTx(db *gorm.DB, inbound *model.Inbound, ignoreID int) (*portConflictDetail, error) {
	hop, managed, err := inboundHysteriaHop(inbound)
	if err != nil {
		return nil, err
	}
	if !managed && inboundTransports(inbound.Protocol, inbound.StreamSettings, inbound.Settings)&transportUDP == 0 {
		return nil, nil
	}
	var rows []*model.Inbound
	q := db.Model(model.Inbound{}).Where("enable = ? AND id <> ?", true, ignoreID)
	if inbound.NodeID == nil {
		q = q.Where("node_id IS NULL")
	} else {
		q = q.Where("node_id = ?", *inbound.NodeID)
	}
	if !managed {
		q = q.Where("protocol = ?", model.Hysteria)
	}
	if err := q.Select("id", "tag", "remark", "protocol", "port", "listen", "stream_settings", "settings").Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	var existing []managedHysteriaHop
	for _, row := range rows {
		if row.Protocol != model.Hysteria {
			continue
		}
		other, on, err := inboundHysteriaHop(row)
		if err != nil || !on {
			continue
		}
		if other.forward.Contains(inbound.Port) && listenOverlaps(inboundBindAddr(inbound), inboundBindAddr(row)) {
			return hysteriaHopConflict(row, inbound.Port, true), nil
		}
		existing = append(existing, other)
	}
	if managed {
		return hop.conflict(rows, existing), nil
	}
	return nil, nil
}
