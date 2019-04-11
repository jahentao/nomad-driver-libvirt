package libvirt

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/drivers/shared/eventer"
	"github.com/hashicorp/nomad/helper/pluginutils/loader"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
	pstructs "github.com/hashicorp/nomad/plugins/shared/structs"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/api"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/cli"
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

	// libvirt connection, singleton
	domainConn cli.Connection

	// driver singleton
	driver *Driver

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
	logger.Error("NewLibvirtDriver called")

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

		domainConn, err := util.CreateLibvirtConnection()
		if err != nil {
			return
		}

		domainManager, err = virtwrap.NewLibvirtDomainManager(domainConn)
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

		driver = &Driver{
			eventer:         eventer.NewEventer(ctx, logger),
			tasks:           newTaskStore(),
			ctx:             ctx,
			signalShutdown:  cancel,
			domainEventChan: eventChan,
			domainStatsChan: statsChan,
			logger:          logger,
		}

		// start the domain event loop
		go driver.eventLoop(ctx)

		// start the domain stats loop
		go driver.statsLoop(ctx)
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

	bin := "virsh"

	outBytes, err := exec.Command(bin, "--version").Output()
	if err != nil {
		// return no error, as it isn't an error to not find qemu, it just means we
		// can't use it.
		fingerprint.Health = drivers.HealthStateUndetected
		fingerprint.HealthDescription = ""
		return fingerprint
	}
	out := strings.TrimSpace(string(outBytes))

	matches := libvirtVersionRegex.FindStringSubmatch(out)
	if len(matches) != 1 {
		fingerprint.Health = drivers.HealthStateUndetected
		fingerprint.HealthDescription = fmt.Sprintf("Failed to parse libvirt version from %v", out)
		return fingerprint
	}

	fingerprint.Attributes[driverAttr] = pstructs.NewBoolAttribute(true)
	fingerprint.Attributes[driverVersionAttr] = pstructs.NewStringAttribute(matches[0])
	return fingerprint
}

func (d *Driver) RecoverTask(handle *drivers.TaskHandle) error {
	if _, ok := d.tasks.Get(handle.Config.ID); ok {
		// nothing to do if handle found in task store
		return nil
	}

	var handleState taskHandleState
	if err := handle.GetDriverState(&handleState); err != nil {
		return fmt.Errorf("failed to decode driver task state: %v", err)
	}

	// create new handle if not found
	h := &taskHandle{
		resultChan:  make(chan *drivers.ExitResult),
		task:        handle.Config, //contains taskid allocid for future use
		startedAt:   handleState.startedAt,
		completedAt: handleState.completedAt,
		exitResult:  handleState.exitResult,
	}

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
	d.logger.Debug("taskConfig lower cased", "taskconfig", taskConfig)

	handle := drivers.NewTaskHandle(taskHandleVersion)
	handle.Config = cfg

	domainSpec, err := domainManager.SyncVM(cfg, &taskConfig, d.logger)
	if err != nil {
		return nil, nil, err
	}

	// Detect domain address
	d.logger.Debug("get domain if addr waiting for ipv4 address")
	guestIf, err := domainManager.DomainIfAddr(domainSpec.Name, true, d.logger)
	if err != nil {
		d.logger.Error("error getting domain address waiting for ipv4 addr", "error", err)
		return nil, nil, err
	}

	// default value for net, works for the following two cases:
	// 1. the domain has only lo interface
	// 1. or the domain has a non-lo interface but has no ip address assigned
	net := &drivers.DriverNetwork{}

	if guestIf != nil {
		for _, ip := range guestIf.IPs {
			d.logger.Debug("domain interface from ga", "ip", ip.IP, "type", ip.Type, "prefix", ip.Prefix)
			if ip.Type == "ipv4" {
				net.IP = ip.IP
				if len(taskConfig.Interfaces) > 0 && taskConfig.Interfaces[0].InterfaceBindingMethod != "network" {
					net.AutoAdvertise = true
				}
			}
		}
	}

	// Return a driver handle
	h := &taskHandle{
		resultChan: make(chan *drivers.ExitResult),
		task:       cfg, //contains taskid allocid for future use
		startedAt:  time.Now().Round(time.Millisecond),
		net:        net,
	}

	if err := handle.SetDriverState(h.buildState()); err != nil {
		fmt.Println("error persisting handle state")
		return nil, nil, err
	}
	d.tasks.Set(cfg.ID, h)
	// go h.run()

	return handle, nil, nil
}

func (d *Driver) WaitTask(ctx context.Context, taskID string) (<-chan *drivers.ExitResult, error) {
	h, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}
	return h.resultChan, nil
}

func (d *Driver) StopTask(taskID string, timeout time.Duration, signal string) error {
	h, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}
	return h.KillVM()
}

func (d *Driver) DestroyTask(taskID string, force bool) error {
	h, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}
	if err := h.DestroyVM(); err != nil {
		return nil
	}
	d.tasks.Delete(taskID)
	return nil
}

func (d *Driver) InspectTask(taskID string) (*drivers.TaskStatus, error) {
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

	return status, nil
}

func (d *Driver) TaskStats(ctx context.Context, taskID string, interval time.Duration) (<-chan *drivers.TaskResourceUsage, error) {
	h, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	return h.Stats(ctx, interval)
}

func (d *Driver) TaskEvents(ctx context.Context) (<-chan *drivers.TaskEvent, error) {
	return d.eventer.TaskEvents(ctx)
}

func (d *Driver) SignalTask(taskID string, signal string) error {
	return fmt.Errorf("libvirt driver can't signal commands")
}

func (d *Driver) ExecTask(taskID string, cmdArgs []string, timeout time.Duration) (*drivers.ExecTaskResult, error) {
	return nil, fmt.Errorf("libvirt driver can't execute commands")
}

// method of InternalDriverPlugin
func (d *Driver) Shutdown() {
	d.signalShutdown()
	if domainConn != nil {
		domainConn.Close()
		domainConn = nil
	}
}

func (d *Driver) eventLoop(ctx context.Context) {
	for {
		select {
		case event := <-d.domainEventChan:
			d.handleEvent(event)
		case <-ctx.Done():
			// exiting
			return
		}
	}
}

func (d *Driver) handleEvent(event api.LibvirtEvent) {
	h, ok := d.tasks.Get(event.TaskID)
	if !ok {
		return
	}
	fmt.Printf("handling event for task: %s(job name),%s(allocid), %s(taskid)\n", h.task.JobName, h.task.AllocID, h.task.ID)
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
		h.resultChan <- h.exitResult
	}
	// send task event in any case
	d.eventer.EmitEvent(&drivers.TaskEvent{
		TaskID:    h.task.ID,
		AllocID:   h.task.AllocID,
		TaskName:  h.task.Name,
		Timestamp: time.Now(),
		Message:   fmt.Sprintf("domain state change %s, reason: %s\n", event.State, event.Reason),
	})
}

func (d *Driver) statsLoop(ctx context.Context) {
	for {
		select {
		case stat := <-d.domainStatsChan:
			h, ok := d.tasks.Get(stat.TaskID)
			if !ok {
				// silently drop domain stat having no matching task
				continue
			}
			h.HandleStat(stat)
		case <-ctx.Done():
			// exiting
			return
		}
	}
}
