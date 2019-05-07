package libvirt

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	cstructs "github.com/hashicorp/nomad/client/structs"
	"github.com/hashicorp/nomad/drivers/shared/eventer"
	"github.com/hashicorp/nomad/helper/pluginutils/loader"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
	pstructs "github.com/hashicorp/nomad/plugins/shared/structs"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/api"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/stats"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/util"
)

const (
	// pluginName is the name of the plugin
	pluginName = "libvirt"

	// fingerprintPeriod is the interval at which the driver will send fingerprint responses
	fingerprintPeriod = 30 * time.Second

	// The key populated in Node Attributes to indicate presence of the Qemu driver
	driverAttr        = "driver.libvirt"
	driverVersionAttr = "driver.libvirt.version"

	// taskHandleVersion is the version of task handle which this driver sets
	// and understands how to decode driver state
	taskHandleVersion = 1
)

var (
	// PluginID is the qemu plugin metadata registered in the plugin
	// catalog.
	PluginID = loader.PluginID{
		Name:       pluginName,
		PluginType: base.PluginTypeDriver,
	}

	// PluginConfig is the qemu driver factory function registered in the
	// plugin catalog.
	// PluginConfig = &loader.InternalPluginConfig{
	// 	Config:  map[string]interface{}{},
	// 	Factory: func(l hclog.Logger) interface{} { return NewLibvirtDriver(l) },
	// }

	libvirtVersionRegex = regexp.MustCompile(`\d+\.\d+\.\d+`)

	// pluginInfo is the response returned for the PluginInfo RPC
	pluginInfo = &base.PluginInfoResponse{
		Type:              base.PluginTypeDriver,
		PluginApiVersions: []string{drivers.ApiVersion010},
		PluginVersion:     "0.1.0",
		Name:              pluginName,
	}

	// configSpec is the hcl specification returned by the ConfigSchema RPC
	configSpec = hclspec.NewObject(map[string]*hclspec.Spec{})

	// capabilities is returned by the Capabilities RPC and indicates what
	// optional features this driver supports
	capabilities = &drivers.Capabilities{
		SendSignals: false,
		Exec:        false,
		FSIsolation: drivers.FSIsolationImage,
	}

	_ drivers.DriverPlugin = (*Driver)(nil)

	// libvirt domain manager, singleton
	domainManager virtwrap.DomainManager

	// driver singleton
	// initialized with empty struct so that nomad won't panic if libvirt initialization fails
	driver *Driver = &Driver{}

	// init once
	initOnce sync.Once
)

type Driver struct {
	// eventer is used to handle multiplexing of TaskEvents calls such that an
	// event can be broadcast to all callers
	eventer *eventer.Eventer
	// ctx is the context for the driver. It is passed to other subsystems to
	// coordinate shutdown
	ctx context.Context

	// signalShutdown is called when the driver is shutting down and cancels the
	// ctx passed to any subsystems
	signalShutdown context.CancelFunc

	// tasks is the in memory datastore mapping taskIDs to taskHandles
	tasks *taskStore

	// domain event channel
	domainEventChan <-chan api.LibvirtEvent

	// domain stats channel
	domainStatsChan <-chan *stats.DomainStats
	// logger will log to the Nomad agent
	logger hclog.Logger
}

func NewLibvirtDriver(logger hclog.Logger) drivers.DriverPlugin {
	ctx, cancel := context.WithCancel(context.Background())
	logger = logger.Named(pluginName)
	logger.Debug("NewLibvirtDriver called")

	// NewLibvirtDriver will be called multiple times
	// Although I dont think multiple NewLibvirtDriver might run at the same time
	// but just to be sure, use sync.Once here
	initOnce.Do(func() {
		// err := util.SetupLibvirt(logger)
		// if err != nil {
		// 	logger.Error("failed to setup libvirt")
		// }
		// util.StartLibvirt(ctx, logger)
		// util.StartVirtlog(ctx)

		// initialize what we can, even when libvirt initialization fails
		driver.logger = logger
		driver.eventer = eventer.NewEventer(ctx, logger)
		driver.ctx = ctx
		driver.signalShutdown = cancel

		domainConn, err := util.CreateLibvirtConnection()
		if err != nil {
			return
		}

		domainManager, err = virtwrap.NewLibvirtDomainManager(domainConn, logger)
		if err != nil {
			return
		}

		eventChan, err := domainManager.StartDomainMonitor(ctx)
		if err != nil {
			return
		}

		statsChan, err := domainManager.StartDomainStatsColloction(ctx, 1*time.Second)
		if err != nil {
			return
		}

		driver.tasks = newTaskStore()
		driver.domainEventChan = eventChan
		driver.domainStatsChan = statsChan

		// start the domain event loop
		go driver.eventLoop()

		// start the domain stats loop
		go driver.statsLoop()
	})

	return driver

}

func (d *Driver) PluginInfo() (*base.PluginInfoResponse, error) {
	return pluginInfo, nil
}

func (d *Driver) ConfigSchema() (*hclspec.Spec, error) {
	return configSpec, nil
}

func (d *Driver) SetConfig(cfg *base.Config) error {
	return nil
}

func (d *Driver) TaskConfigSchema() (*hclspec.Spec, error) {
	return api.TaskConfigSpec, nil
}

func (d *Driver) Capabilities() (*drivers.Capabilities, error) {
	return capabilities, nil
}

func (d *Driver) Fingerprint(ctx context.Context) (<-chan *drivers.Fingerprint, error) {
	ch := make(chan *drivers.Fingerprint)
	go d.handleFingerprint(ctx, ch)
	return ch, nil
}

func (d *Driver) handleFingerprint(ctx context.Context, ch chan *drivers.Fingerprint) {
	d.logger.Debug("handleFingerpring called")
	defer close(ch)
	ticker := time.NewTimer(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			ticker.Reset(fingerprintPeriod)
			ch <- d.buildFingerprint()
		}
	}
}

func (d *Driver) buildFingerprint() *drivers.Fingerprint {
	fingerprint := &drivers.Fingerprint{
		Attributes:        map[string]*pstructs.Attribute{},
		Health:            drivers.HealthStateHealthy,
		HealthDescription: drivers.DriverHealthy,
	}

	if domainManager == nil || domainManager.IsManagerAlive() == false {
		fingerprint.Health = drivers.HealthStateUnhealthy
		fingerprint.HealthDescription = "no libvirt connection"
	}
	return fingerprint
}

func (d *Driver) RecoverTask(handle *drivers.TaskHandle) error {
	if handle == nil {
		return fmt.Errorf("error: handle cannot be nil in RecoverTask")
	}

	if _, ok := d.tasks.Get(handle.Config.ID); ok {
		// nothing to do if handle found in task store
		return nil
	}

	var handleState taskHandleState
	if err := handle.GetDriverState(&handleState); err != nil {
		return fmt.Errorf("failed to decode driver task state: %v", err)
	}

	// create new handle from restored state from state db
	// libvirt doesn't track the creation/compeltion time of domains
	// so I'm tracking those myself
	h := &taskHandle{
		resultChan:  make(chan *drivers.ExitResult),
		task:        handle.Config,
		startedAt:   handleState.startedAt,
		completedAt: handleState.completedAt,
		exitResult:  handleState.exitResult,
	}

	// set the in memory handle in task store
	d.tasks.Set(handle.Config.ID, h)

	return nil
}

func (d *Driver) StartTask(cfg *drivers.TaskConfig) (*drivers.TaskHandle, *drivers.DriverNetwork, error) {
	if _, ok := d.tasks.Get(cfg.ID); ok {
		return nil, nil, fmt.Errorf("task with ID %q already started", cfg.ID)
	}

	var taskConfig api.TaskConfig

	if err := cfg.DecodeDriverConfig(&taskConfig); err != nil {
		return nil, nil, fmt.Errorf("failed to decode task config: %v", err)
	}

	// make all relevant strings lower case before processing
	taskConfig.ToLower()

	handle := drivers.NewTaskHandle(taskHandleVersion)
	handle.Config = cfg

	// define and start domain
	domainSpec, err := domainManager.SyncVM(cfg, &taskConfig)
	if err != nil {
		return nil, nil, err
	}

	// Detect domain address
	// stop and undefine domain if can't get ipv4 address
	guestIf, err := domainManager.DomainIfAddr(domainSpec.Name, true)
	if err != nil {
		d.logger.Error("error getting domain address waiting for ipv4 addr", "error", err)
		domainManager.KillVM(domainSpec.Name)
		domainManager.DestroyVM(domainSpec.Name)
		return nil, nil, err
	}

	// default value for net, works for the following two cases:
	// 1. the domain has only lo interface
	// 1. or the domain has a non-lo interface but has no ip address assigned
	drvNet := &drivers.DriverNetwork{}

	if guestIf != nil {
		for _, ip := range guestIf.IPs {
			d.logger.Debug("domain interface from guest agent", "ip", ip.IP, "type", ip.Type, "prefix", ip.Prefix)
			if ip.Type == "ipv4" {
				drvNet.IP = ip.IP
				if len(taskConfig.Interfaces) > 0 && taskConfig.Interfaces[0].InterfaceBindingMethod != "network" {
					drvNet.AutoAdvertise = true
				}
			}
		}
	}

	// Return a driver handle
	h := &taskHandle{
		resultChan: make(chan *drivers.ExitResult),
		task:       cfg, //contains taskid allocid for future use
		startedAt:  time.Now().Round(time.Millisecond),
		net:        drvNet,
		resourceUsage: &cstructs.TaskResourceUsage{
			ResourceUsage: &cstructs.ResourceUsage{
				MemoryStats: &cstructs.MemoryStats{},
				CpuStats:    &cstructs.CpuStats{},
			},
		}, // initial empty usage data, so that we won't return nil in stats channel
	}

	if err := handle.SetDriverState(h.buildState()); err != nil {
		d.logger.Error("error persisting handle state")
		return nil, nil, err
	}
	d.tasks.Set(cfg.ID, h)

	d.logger.Debug("returning from starttask")
	return handle, drvNet, nil
}

func (d *Driver) WaitTask(ctx context.Context, taskID string) (<-chan *drivers.ExitResult, error) {
	d.logger.Debug("waittaks called")
	h, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}
	d.logger.Debug("wait task returning")
	return h.resultChan, nil
}

func (d *Driver) StopTask(taskID string, timeout time.Duration, signal string) error {
	d.logger.Debug("stoptask called")
	h, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}
	d.logger.Debug("stoptask returning")
	return h.KillVM()
}

func (d *Driver) DestroyTask(taskID string, force bool) error {
	d.logger.Debug("destroytask called")
	h, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}
	if err := h.DestroyVM(); err != nil {
		return err
	}
	d.tasks.Delete(taskID)
	d.logger.Debug("destroytask returning")
	return nil
}

func (d *Driver) InspectTask(taskID string) (*drivers.TaskStatus, error) {
	d.logger.Debug("inspecttask called")
	h, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	status := &drivers.TaskStatus{
		ID:              h.task.ID,
		Name:            h.task.Name,
		StartedAt:       h.startedAt,
		CompletedAt:     h.completedAt,
		NetworkOverride: h.net,
		ExitResult:      h.exitResult,
	}

	status.State = drivers.TaskStateUnknown

	s, err := domainManager.VMState(api.TaskID2DomainName(h.task.ID))
	if err != nil {
		return nil, err
	}

	switch s {
	case api.Running, api.Blocked, api.Paused, api.Shutdown, api.PMSuspended:
		status.State = drivers.TaskStateRunning
	case api.Shutoff, api.Crashed:
		status.State = drivers.TaskStateExited
	}

	d.logger.Debug("inspecttask returning")
	return status, nil
}

func (d *Driver) TaskStats(ctx context.Context, taskID string, interval time.Duration) (<-chan *drivers.TaskResourceUsage, error) {
	d.logger.Debug("taskstats called")
	h, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	d.logger.Debug("taskstats returning")
	return h.Stats(ctx, interval)
}

func (d *Driver) TaskEvents(ctx context.Context) (<-chan *drivers.TaskEvent, error) {
	d.logger.Debug("task events called and returning")
	return d.eventer.TaskEvents(ctx)
}

func (d *Driver) SignalTask(taskID string, signal string) error {
	d.logger.Debug("signal task called  and returning")
	return fmt.Errorf("libvirt driver can't signal commands")
}

func (d *Driver) ExecTask(taskID string, cmdArgs []string, timeout time.Duration) (*drivers.ExecTaskResult, error) {
	d.logger.Debug("exec task called and returning")
	return nil, fmt.Errorf("libvirt driver can't execute commands")
}

// method of InternalDriverPlugin
// func (d *Driver) Shutdown() {
// 	d.signalShutdown()
// 	if domainConn != nil {
// 		domainConn.Close()
// 		domainConn = nil
// 	}
// }

func (d *Driver) eventLoop() {
	d.logger.Debug("eventLoop called")
	for {
		select {
		case event := <-d.domainEventChan:
			h, ok := d.tasks.Get(event.TaskID)
			if !ok {
				// silently drop domain event having no matching task
				continue
			}
			d.eventer.EmitEvent(h.HandleEvent(event))
		case <-d.ctx.Done():
			// exiting
			d.logger.Debug("breaking from eventLoop")
			return
		}
	}
}

func (d *Driver) statsLoop() {
	d.logger.Debug("statsLoop called")
	for {
		select {
		case stat := <-d.domainStatsChan:
			h, ok := d.tasks.Get(stat.TaskID)
			if !ok {
				// silently drop domain stat having no matching task
				continue
			}
			h.HandleStat(stat)
		case <-d.ctx.Done():
			// exiting
			d.logger.Debug("breaking statsLoop")
			return
		}
	}
}
