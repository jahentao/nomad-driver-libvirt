package libvirt

import (
	"context"
	"fmt"
	"sync"
	"time"

	cstructs "github.com/hashicorp/nomad/client/structs"
	cpustats "github.com/hashicorp/nomad/helper/stats"
	"github.com/hashicorp/nomad/plugins/drivers"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/api"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/stats"
)

type taskHandle struct {
	domainManager     virtwrap.DomainManager
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
	return h.domainManager.KillVM(api.TaskID2DomainName(h.task.ID))
}

func (h *taskHandle) DestroyVM() error {
	return h.domainManager.DestroyVM(api.TaskID2DomainName(h.task.ID))
}

func (h *taskHandle) buildState() *taskHandleState {
	return &taskHandleState{
		startedAt:   h.startedAt,
		completedAt: h.completedAt,
		exitResult:  h.exitResult,
	}
}

func (h *taskHandle) Stats(ctx context.Context, interval time.Duration) (<-chan *cstructs.TaskResourceUsage, error) {
	ch := make(chan *cstructs.TaskResourceUsage)

	go func() {
		timer := time.NewTimer(interval)
		for {
			select {
			case <-timer.C:
				// out put resource usage
				select {
				case ch <- h.getResourceUsage():
				default:
					//drop usage data if blocked
				}
				timer.Reset(interval)
			case <-ctx.Done():
				// close channel and return when done
				close(ch)
				return
			}
		}
	}()

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

func (h *taskHandle) HandleEvent(event api.LibvirtEvent) *drivers.TaskEvent {
	switch event.State {
	case api.Shutoff, api.Crashed:
		exitCode := 0
		if event.State == api.Crashed {
			exitCode = -1
		}
		h.completedAt = time.Now().Round(time.Millisecond)
		// domain stopped, notify task runner
		// Only Shutoff and Crashed considered terminal state
		// Other states including Blocked, Paused, ShuttingDown, PMSuspended considered temporary, only send event to task runner in these cases
		// TODO is setting exitcode to -1 the correct way to signal a domain failure?
		// TODO is it possible for a domain to be oom killed? how to detect that from libvirt domain event?
		h.exitResult = &drivers.ExitResult{
			ExitCode:  exitCode,
			Signal:    0,
			OOMKilled: false,
			Err:       nil,
		}
		// someone called driver.WaitTask again after job stop command is issued
		// so 2 people (this mysterious someone and task runner) are waiting for the exitresult here
		// so I sendout 2 exitResult here
		h.resultChan <- h.exitResult
		h.resultChan <- h.exitResult
	}
	return &drivers.TaskEvent{
		TaskID:    h.task.ID,
		AllocID:   h.task.AllocID,
		TaskName:  h.task.Name,
		Timestamp: time.Now(),
		Message:   fmt.Sprintf("domain state change %s, reason: %s\n", event.State, event.Reason),
	}
}
