package libvirt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jahentao/nomad-driver-libvirt/libvirt/virtwrap"
	"github.com/jahentao/nomad-driver-libvirt/libvirt/virtwrap/api"
	"github.com/jahentao/nomad-driver-libvirt/libvirt/virtwrap/stats"
	"github.com/jahentao/nomad-driver-libvirt/libvirt/virtwrap/util"

	"github.com/hashicorp/consul-template/signals"
	hclog "github.com/hashicorp/go-hclog"
	cstructs "github.com/hashicorp/nomad/client/structs"
	"github.com/hashicorp/nomad/drivers/shared/eventer"
	"github.com/hashicorp/nomad/helper/pluginutils/loader"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
	"github.com/hashicorp/nomad/plugins/shared/structs"
	pstructs "github.com/hashicorp/nomad/plugins/shared/structs"
)

const (
	// pluginName is the name of the plugin
	// this is used for logging and (along with the version) for uniquely
	// identifying plugin binaries fingerprinted by the client
	pluginName = "libvirt"

	// pluginVersion allows the client to identify and use newer versions of
	// an installed plugin
	pluginVersion = "v0.1.0"

	// taskHandleVersion is the version of task handle which this driver sets
	// and understands how to decode driver state
	// this is used to allow modification and migration of the task schema
	// used by the plugin
	taskHandleVersion = 1

	// The key populated in Node Attributes to indicate presence of the Qemu driver
	driverAttr        = "driver.libvirt"
	driverVersionAttr = "driver.libvirt.version"

	// fingerprintPeriod is the interval at which the driver will send fingerprint responses
	fingerprintPeriod = 30 * time.Second

	// statsInternal is the interval at which the driver will send stats responses
	statsPeriod = 1 * time.Second
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

	// pluginInfo describes the plugin, and is the response returned for the PluginInfo RPC
	pluginInfo = &base.PluginInfoResponse{
		Type:              base.PluginTypeDriver,
		PluginApiVersions: []string{drivers.ApiVersion010},
		PluginVersion:     pluginVersion,
		Name:              pluginName,
	}

	// configSpec is the specification of the plugin's configuration
	// this is used to validate the configuration specified for the plugin
	// on the client.
	// this is not global, but can be specified on a per-client basis.
	// it is the hcl specification returned by the ConfigSchema RPC
	configSpec = hclspec.NewObject(map[string]*hclspec.Spec{
		// define plugin's agent configuration schema.
		//
		// The schema should be defined using HCL specs and it will be used to
		// validate the agent configuration provided by the user in the
		// `plugin` stanza (https://www.nomadproject.io/docs/configuration/plugin.html).
		//
		//   plugin "nomad-driver-libvirt" {
		//     config {
		//       // libvirt hypervisor drivers reference https://libvirt.org/drivers.html#hypervisor
		//       hypervisor = "qemu"
		//       uri        = "qemu:///system"
		//     }
		//   }
		"hypervisor": hclspec.NewDefault(
			hclspec.NewAttr("hypervisor", "string", false),
			hclspec.NewLiteral(`"qemu"`),
		),
		"uri": hclspec.NewDefault(
			hclspec.NewAttr("uri", "string", false),
			hclspec.NewLiteral(`"qemu:///system"`),
		),
	})

	// capabilities is returned by the Capabilities RPC and indicates what
	// optional features this driver supports
	// this should be set according to the target run time.
	capabilities = &drivers.Capabilities{
		// set plugin's capabilities
		//
		// The plugin's capabilities signal Nomad which extra functionalities
		// are supported. For a list of available options check the docs page:
		// https://godoc.org/github.com/hashicorp/nomad/plugins/drivers#Capabilities
		SendSignals: false,
		Exec:        false,
		FSIsolation: drivers.FSIsolationImage,
	}

	// interface compatibility check
	_ drivers.DriverPlugin = (*Driver)(nil)

	// driver singleton
	// initialized with empty struct so that nomad won't panic if libvirt initialization fails
	driver *Driver = &Driver{}

	// init once
	initOnce sync.Once
)

// Config contains configuration information for the plugin
type Config struct {
	// create decoded plugin configuration struct
	//
	// This struct is the decoded version of the schema defined in the
	// configSpec variable above. It's used to convert the HCL configuration
	// passed by the Nomad agent into Go constructs.
	Hypervisor string `codec:"hypervisor"`
	Uri        string `codec:"uri"`
}



type Driver struct {
	// eventer is used to handle multiplexing of TaskEvents calls such that an
	// event can be broadcast to all callers
	eventer *eventer.Eventer

	//config is the plugin configuration set by the SetConfig RPC
	config *Config

	// nomadConfig is the client config from Nomad
	nomadConfig *base.ClientDriverConfig

	// tasks is the in memory datastore mapping taskIDs to taskHandles
	tasks *taskStore

	// ctx is the context for the driver. It is passed to other subsystems to
	// coordinate shutdown
	ctx context.Context

	// signalShutdown is called when the driver is shutting down and cancels the
	// ctx passed to any subsystems
	signalShutdown context.CancelFunc

	// libvirt domain manager
	domainManager virtwrap.DomainManager

	// domain event channel
	domainEventChan <-chan api.LibvirtEvent

	// domain stats channel
	domainStatsChan <-chan *stats.DomainStats

	// logger will log to the Nomad agent
	logger hclog.Logger
}

// NewPlugin returns a new example driver plugin
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
		driver.config = &Config{}

		domainConn, err := util.CreateLibvirtConnection()
		if err != nil {
			return
		}

		domainManager, err := virtwrap.NewLibvirtDomainManager(domainConn, logger)
		if err != nil {
			return
		}

		eventChan, err := domainManager.StartDomainEventMonitor(ctx)
		if err != nil {
			return
		}

		statsChan, err := domainManager.StartDomainStatsColloction(ctx, statsPeriod)
		if err != nil {
			return
		}

		driver.tasks = newTaskStore()
		driver.domainManager = domainManager
		driver.domainEventChan = eventChan
		driver.domainStatsChan = statsChan

		// start the domain event loop
		go driver.eventLoop()

		// start the domain stats loop
		go driver.statsLoop()
	})

	return driver

}

// PluginInfo returns information describing the plugin.
func (d *Driver) PluginInfo() (*base.PluginInfoResponse, error) {
	return pluginInfo, nil
}

// ConfigSchema returns the plugin configuration schema.
func (d *Driver) ConfigSchema() (*hclspec.Spec, error) {
	return configSpec, nil
}

// SetConfig is called by the client to pass the configuration for the plugin.
func (d *Driver) SetConfig(cfg *base.Config) error {
	var config Config
	if len(cfg.PluginConfig) != 0 {
		if err := base.MsgPackDecode(cfg.PluginConfig, &config); err != nil {
			return err
		}
	}

	// Save the configuration to the plugin
	d.config = &config

	// parse and validated any configuration value if necessary.
	//
	// If your driver agent configuration requires any complex validation
	// (some dependency between attributes) or special data parsing (the
	// string "10s" into a time.Interval) you can do it here and update the
	// value in d.config.
	//
	// we check if the hypervisor and uri specified by the user is
	// supported by the plugin.
	hypervisor := strings.ToLower(d.config.Hypervisor)
	uri := strings.ToLower(d.config.Uri)
	switch hypervisor {
	case "qemu":
		d.logger.Debug("plugin config hypervisor: qemu")
		// TODO need more accurate validation, like regexp
		if !strings.HasPrefix(uri, "qemu") {
			return fmt.Errorf("invalid qemu uri %s", d.config.Uri)
		}
		d.logger.Debug("plugin config qemu uri: ", d.config.Uri)

	// TODO support other hypervisor
	//case "xen":
	//	d.logger.Debug("plugin config hypervisor: xen")

	default:
		return fmt.Errorf("invalid hypervisor %s", d.config.Hypervisor)
	}

	if hypervisor != "qemu" && hypervisor != "lxc" {
		return fmt.Errorf("invalid hypervisor %s", d.config.Hypervisor)
	}

	// Save the Nomad agent configuration
	if cfg.AgentConfig != nil {
		d.nomadConfig = cfg.AgentConfig.Driver
	}

	// TODO: initialize any extra requirements if necessary.
	//
	// Here you can use the config values to initialize any resources that are
	// shared by all tasks that use this driver, such as a daemon process.

	return nil
}

// TaskConfigSchema returns the HCL schema for the configuration of a task.
func (d *Driver) TaskConfigSchema() (*hclspec.Spec, error) {
	return api.TaskConfigSpec, nil
}

// Capabilities returns the features supported by the driver.
func (d *Driver) Capabilities() (*drivers.Capabilities, error) {
	return capabilities, nil
}

// Fingerprint returns a channel that will be used to send health information
// and other driver specific node attributes.
func (d *Driver) Fingerprint(ctx context.Context) (<-chan *drivers.Fingerprint, error) {
	ch := make(chan *drivers.Fingerprint)
	go d.handleFingerprint(ctx, ch)
	return ch, nil
}

// handleFingerprint manages the channel and the flow of fingerprint data.
func (d *Driver) handleFingerprint(ctx context.Context, ch chan<- *drivers.Fingerprint) {
	d.logger.Debug("handleFingerprint called")
	defer close(ch)

	// Nomad expects the initial fingerprint to be sent immediately
	ticker := time.NewTimer(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			// after the initial fingerprint we can set the proper fingerprint
			// period
			ticker.Reset(fingerprintPeriod)
			ch <- d.buildFingerprint()
		}
	}
}

// buildFingerprint returns the driver's fingerprint data
func (d *Driver) buildFingerprint() *drivers.Fingerprint {
	fingerprint := &drivers.Fingerprint{
		Attributes:        map[string]*pstructs.Attribute{},
		Health:            drivers.HealthStateHealthy,
		HealthDescription: drivers.DriverHealthy,
	}

	// TODO: implement fingerprinting logic to populate health and driver
	// attributes.
	//
	// Fingerprinting is used by the plugin to relay two important information
	// to Nomad: health state and node attributes.
	//
	// If the plugin reports to be unhealthy, or doesn't send any fingerprint
	// data in the expected interval of time, Nomad will restart it.
	//
	// Node attributes can be used to report any relevant information about
	// the node in which the plugin is running (specific library availability,
	// installed versions of a software etc.). These attributes can then be
	// used by an operator to set job constrains.
	//
	// In the example below we check if the libvirt executive binary specified by the user exists
	// in the node.
	binary := "libvirtd"
	// TODO: Need know node attributes
	// find specific d.config.hypervisor is present, like qemu -> 'qemu-system-x86_64'

	cmd := exec.Command("which", binary)
	if err := cmd.Run(); err != nil {
		return &drivers.Fingerprint{
			Health:            drivers.HealthStateUndetected,
			HealthDescription: fmt.Sprintf("libvirt %s not found", binary),
		}
	}

	// We also set the libvirt and its version as attributes
	cmd = exec.Command(binary, "--version")
	if out, err := cmd.Output(); err != nil {
		d.logger.Warn("failed to find libvirt version: %v", err)
	} else {
		re := regexp.MustCompile("[0-9]\\.[0-9]\\.[0-9]")
		version := re.FindString(string(out))

		fingerprint.Attributes[driverVersionAttr] = structs.NewStringAttribute(version)
		fingerprint.Attributes[driverAttr] = structs.NewStringAttribute(binary)
	}

	if d.domainManager == nil || d.domainManager.IsManagerAlive() == false {
		fingerprint.Health = drivers.HealthStateUnhealthy
		fingerprint.HealthDescription = "no libvirt connection"
	}
	return fingerprint
}

// RecoverTask recreates the in-memory state of a task from a TaskHandle.
func (d *Driver) RecoverTask(handle *drivers.TaskHandle) error {
	if handle == nil {
		return fmt.Errorf("error: handle cannot be nil in RecoverTask")
	}

	if _, ok := d.tasks.Get(handle.Config.ID); ok {
		// nothing to do if handle found in task store
		return nil
	}

	var taskState TaskState
	if err := handle.GetDriverState(&taskState); err != nil {
		return fmt.Errorf("failed to decode driver task state: %v", err)
	}

	var driverConfig api.TaskConfig
	if err := taskState.TaskConfig.DecodeDriverConfig(&driverConfig); err != nil {
		return fmt.Errorf("failed to decode driver config: %v", err)
	}

	// create new handle from restored state from state db
	// libvirt doesn't track the creation/completion time of domains
	// so I'm tracking those myself
	h := &taskHandle{
		domainManager: d.domainManager,
		resultChan:    make(chan *drivers.ExitResult),
		task:          handle.Config,
		startedAt:     taskState.startedAt,
		completedAt:   taskState.completedAt,
		exitResult:    taskState.exitResult,
	}

	// set the in memory handle in task store
	d.tasks.Set(handle.Config.ID, h)

	return nil
}

// StartTask returns a task handle and a driver network if necessary.
func (d *Driver) StartTask(cfg *drivers.TaskConfig) (*drivers.TaskHandle, *drivers.DriverNetwork, error) {
	if _, ok := d.tasks.Get(cfg.ID); ok {
		return nil, nil, fmt.Errorf("task with ID %q already started", cfg.ID)
	}

	var driverConfig api.TaskConfig

	if err := cfg.DecodeDriverConfig(&driverConfig); err != nil {
		return nil, nil, fmt.Errorf("failed to decode task config: %v", err)
	}

	// make all relevant strings lower case before processing
	driverConfig.ToLower()
	d.logger.Info("starting task", "driver_cfg", hclog.Fmt("%+v", driverConfig))

	handle := drivers.NewTaskHandle(taskHandleVersion)
	handle.Config = cfg

	// TODO: implement driver specific mechanism to start the task.
	//
	// Once the task is started you will need to store any relevant runtime
	// information in a taskHandle and TaskState. The taskHandle will be
	// stored in-memory in the plugin and will be used to interact with the
	// task.
	//
	// The TaskState will be returned to the Nomad client inside a
	// drivers.TaskHandle instance. This TaskHandle will be sent back to plugin
	// if the task ever needs to be recovered, so the TaskState should contain
	// enough information to handle that.

	// define and start domain
	domainSpec, err := d.domainManager.SyncVM(cfg, &driverConfig)
	if err != nil {
		return nil, nil, err
	}

	// Detect domain address
	// stop and undefine domain if can't get ipv4 address
	guestIf, err := d.domainManager.DomainIfAddr(domainSpec.Name, true)
	if err != nil {
		d.logger.Error("error getting domain address waiting for ipv4 addr", "error", err)
		d.domainManager.KillVM(domainSpec.Name)
		d.domainManager.DestroyVM(domainSpec.Name)
		return nil, nil, err
	}

	// default value for net, works for the following two cases:
	// 1. the domain has only lo interface
	// 2. or the domain has a non-lo interface but has no ip address assigned
	drvNet := &drivers.DriverNetwork{}

	if guestIf != nil {
		for _, ip := range guestIf.IPs {
			d.logger.Debug("domain interface from guest agent", "ip", ip.IP, "type", ip.Type, "prefix", ip.Prefix)
			if ip.Type == "ipv4" {
				drvNet.IP = ip.IP
				if len(driverConfig.Interfaces) > 0 && driverConfig.Interfaces[0].InterfaceBindingMethod != "network" {
					drvNet.AutoAdvertise = true
				}
			}
		}
	}

	// Return a driver handle
	h := &taskHandle{
		domainManager: d.domainManager,
		resultChan:    make(chan *drivers.ExitResult),
		task:          cfg, //contains taskid allocid for future use
		startedAt:     time.Now().Round(time.Millisecond),
		net:           drvNet,
		resourceUsage: &cstructs.TaskResourceUsage{
			ResourceUsage: &cstructs.ResourceUsage{
				MemoryStats: &cstructs.MemoryStats{},
				CpuStats:    &cstructs.CpuStats{},
			},
		}, // initial empty usage data, so that we won't return nil in stats channel
	}

	//config := &plugin.ClientConfig{
	//	HandshakeConfig:  base.Handshake,
	//	Plugins:          map[string]plugin.Plugin{"executor": p},
	//	Cmd:              exec.Command(bin, "executor", string(c)),
	//	AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
	//	Logger:           logger.Named("executor"),
	//}
	//
	//if driverConfig != nil {
	//	config.MaxPort = driverConfig.ClientMaxPort
	//	config.MinPort = driverConfig.ClientMinPort
	//} else {
	//	config.MaxPort = ExecutorDefaultMaxPort
	//	config.MinPort = ExecutorDefaultMinPort
	//}

	if err := handle.SetDriverState(h.buildState(cfg)); err != nil {
		d.logger.Error("error persisting handle state")
		return nil, nil, err
	}
	d.tasks.Set(cfg.ID, h)

	d.logger.Debug("returning from starttask")
	return handle, drvNet, nil
}

// WaitTask returns a channel used to notify Nomad when a task exits.
func (d *Driver) WaitTask(ctx context.Context, taskID string) (<-chan *drivers.ExitResult, error) {
	d.logger.Debug("waittaks called")
	h, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	d.logger.Debug("wait task returning")
	return h.resultChan, nil
}

// StopTask stops a running task with the given signal and within the timeout window.
func (d *Driver) StopTask(taskID string, timeout time.Duration, signal string) error {
	d.logger.Debug("StopTask called")
	h, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	// implement driver specific logic to stop a task.
	//
	// The StopTask function is expected to stop a running task by sending the
	// given signal to it. If the task does not stop during the given timeout,
	// the driver must forcefully kill the task.
	d.logger.Debug("StopTask returning")
	return h.KillVM()
}

// DestroyTask cleans up and removes a task that has terminated.
func (d *Driver) DestroyTask(taskID string, force bool) error {
	d.logger.Debug("DestroyTask called")
	h, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	// implement driver specific logic to destroy a complete task.
	//
	// Destroying a task includes removing any resources used by task and any
	// local references in the plugin. If force is set to true the task should
	// be destroyed even if it's currently running.
	if err := h.DestroyVM(); err != nil {
		return err
	}

	d.tasks.Delete(taskID)
	d.logger.Debug("DestroyTask returning")
	return nil
}

// InspectTask returns detailed status information for the referenced taskID.
func (d *Driver) InspectTask(taskID string) (*drivers.TaskStatus, error) {
	d.logger.Debug("InspectTask called")
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

	s, err := d.domainManager.VMState(api.TaskID2DomainName(h.task.ID))
	if err != nil {
		return nil, err
	}

	// reflect the domain actual status
	switch s {
	case api.Running, api.Blocked, api.Paused, api.Shutdown, api.PMSuspended:
		status.State = drivers.TaskStateRunning
	case api.Shutoff, api.Crashed:
		status.State = drivers.TaskStateExited
	}

	d.logger.Debug("InspectTask returning")
	return status, nil
}

// TaskStats returns a channel which the driver should send stats to at the given interval.
func (d *Driver) TaskStats(ctx context.Context, taskID string, interval time.Duration) (<-chan *drivers.TaskResourceUsage, error) {
	d.logger.Debug("TaskStats called")
	h, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	// TODO: implement driver specific logic to send task stats.
	//
	// This function returns a channel that Nomad will use to listen for task
	// stats (e.g., CPU and memory usage) in a given interval. It should send
	// stats until the context is canceled or the task stops running.
	d.logger.Debug("TaskStats returning")
	return h.Stats(ctx, interval)
}

// TaskEvents returns a channel that the plugin can use to emit task related events.
func (d *Driver) TaskEvents(ctx context.Context) (<-chan *drivers.TaskEvent, error) {
	d.logger.Debug("task events called and returning")
	return d.eventer.TaskEvents(ctx)
}

// SignalTask forwards a signal to a task.
// This is an optional capability.
func (d *Driver) SignalTask(taskID string, signal string) error {
	d.logger.Debug("signal task called  and returning")

	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	// TODO: implement driver specific signal handling logic.
	//
	// The given signal must be forwarded to the target taskID. If this plugin
	// doesn't support receiving signals (capability SendSignals is set to
	// false) you can just return nil.
	sig := os.Interrupt
	if s, ok := signals.SignalLookup[signal]; ok {
		sig = s
	} else {
		d.logger.Warn("unknown signal to send to task, using SIGINT instead", "signal", signal, "task_id", handle.task.ID)
	}
	return handle.Signal(sig)
}

// ExecTask returns the result of executing the given command inside a task.
// This is an optional capability.
func (d *Driver) ExecTask(taskID string, cmdArgs []string, timeout time.Duration) (*drivers.ExecTaskResult, error) {
	d.logger.Debug("ExecTask called and returning")
	// TODO: implement driver specific logic to execute commands in a task.
	return nil, fmt.Errorf("libvirt driver does not support execute commands")
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
