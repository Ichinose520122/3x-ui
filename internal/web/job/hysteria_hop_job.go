package job

import (
	"context"
	"errors"

	"github.com/mhsanaei/3x-ui/v3/internal/hy2hop"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

type HysteriaHopJob struct {
	ctx      context.Context
	manager  *hy2hop.Manager
	requests chan struct{}
	done     chan struct{}
	lastErr  string
}

func NewHysteriaHopJob(ctx context.Context) *HysteriaHopJob {
	j := &HysteriaHopJob{
		ctx: ctx, manager: hy2hop.NewManager(), requests: make(chan struct{}, 1), done: make(chan struct{}),
	}
	go j.run()
	return j
}

// Request coalesces edits and periodic checks without blocking the inbound runtime.
func (j *HysteriaHopJob) Request() {
	select {
	case j.requests <- struct{}{}:
	default:
	}
}

func (j *HysteriaHopJob) run() {
	defer close(j.done)
	for j.ctx.Err() == nil {
		select {
		case <-j.ctx.Done():
			return
		case <-j.requests:
			j.reconcile()
		}
	}
}

func (j *HysteriaHopJob) reconcile() {
	var err error
	if (&service.XrayService{}).IsXrayRunning() {
		forwards, warnings, loadErr := (&service.InboundService{}).DesiredHysteriaHops(j.ctx)
		if loadErr != nil {
			err = loadErr
		} else {
			err = errors.Join(warnings, j.manager.Reconcile(j.ctx, forwards))
		}
	} else {
		err = j.manager.Reconcile(j.ctx, nil)
	}
	if j.ctx.Err() != nil {
		return
	}
	if err == nil {
		if j.lastErr != "" {
			logger.Info("HY2 port forwarding reconciled")
		}
		j.lastErr = ""
	} else if detail := err.Error(); detail != j.lastErr {
		j.lastErr = detail
		logger.Warning("HY2 port forwarding: ", detail)
	}
}

func (j *HysteriaHopJob) Stop(ctx context.Context, cleanup bool) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-j.done:
		if cleanup {
			return j.manager.Reconcile(ctx, nil)
		}
		return nil
	}
}
