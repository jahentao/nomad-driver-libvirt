package libvirt

import (
	"context"
	"sync"
	"time"

	cstructs "github.com/hashicorp/nomad/client/structs"
	cpustats "github.com/hashicorp/nomad/helper/stats"
	"github.com/hashicorp/nomad/plugins/drivers"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/api"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/stats"
)

type taskHandle struct {
	resultChan        chan *drivers.ExitResult
	task              *drivers.TaskConfig
	startedAt         time.Time
	completedAt       time.Time
	exitResult        *drivers.ExitResult
	net               *drivers.DriverNetwork
	resourceUsage     *cstructs.TaskResourceUsage
	resourceUsageLock sync.Mutex
	prevDomainStat    *stats.DomainStats
}

type taskHandleState struct {
	startedAt   time.Time
	completedAt time.Time
	exitResult  *drivers.ExitResult
}

func (h *taskHandle) KillVM() error {
	return domainManager.KillVM(api.TaskID2DomainName(h.task.ID))
}

func (h *taskHandle) DestroyVM() error {
	return domainManager.DestroyVM(api.TaskID2DomainName(h.task.ID))
}

func (h *taskHandle) buildState() *taskHandleState {
	return &taskHandleState{
		startedAt:   h.startedAt,
		completedAt: h.completedAt,
		exitResult:  h.exitResult,
	}
}

// Stats starts collecting stats from the docker daemon and sends them on the
// returned channel.
func (h *taskHandle) Stats(ctx context.Context, interval time.Duration) (<-chan *cstructs.TaskResourceUsage, error) {
	ch := make(chan *cstructs.TaskResourceUsage)

	go func(ctx context.Context, out chan *cstructs.TaskResourceUsage, interval time.Duration) {
		timer := time.NewTimer(interval)
		for {
			select {
			case <-timer.C:
				// out put resource usage
				select {
				case out <- h.getResourceUsage():
				default:
					//drop usage data if blocked
				}
				timer.Reset(interval)
			case <-ctx.Done():
				// close channel and return when done
				close(out)
				return
			}
		}
	}(ctx, ch, interval)

	return ch, nil
}

func (h *taskHandle) getResourceUsage() *cstructs.TaskResourceUsage {
	h.resourceUsageLock.Lock()
	defer h.resourceUsageLock.Unlock()
	return h.resourceUsage
}

func (h *taskHandle) setResourceUsage(ru *cstructs.TaskResourceUsage) {
	h.resourceUsageLock.Lock()
	defer h.resourceUsageLock.Unlock()
	h.resourceUsage = ru
}

func (h *taskHandle) HandleStat(stat *stats.DomainStats) {
	if h.prevDomainStat == nil {
		// if no previous record of domainstat, we can't compute cpu mhz, so we just save the current DomainStats and return
		h.prevDomainStat = stat
		return
	}

	ms := &cstructs.MemoryStats{
		// RSS comes in KiB from libvirt, but ResourceUsage requires B
		RSS: stat.Memory.RSS * 1024,
	}

	cpuMhz := float64((stat.Cpu.Time - h.prevDomainStat.Cpu.Time)) / float64((stat.Timestamp - h.prevDomainStat.Timestamp))
	cpuMhz = cpuMhz * cpustats.CPUMHzPerCore()

	h.prevDomainStat = stat

	cs := &cstructs.CpuStats{
		TotalTicks: cpuMhz,
	}

	resourceUsage := &cstructs.TaskResourceUsage{
		ResourceUsage: &cstructs.ResourceUsage{
			MemoryStats: ms,
			CpuStats:    cs,
		},
		Timestamp: stat.Timestamp,
	}
	h.setResourceUsage(resourceUsage)
}
