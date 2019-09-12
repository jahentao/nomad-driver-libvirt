package api

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/hashicorp/nomad/plugins/drivers"
)

const (
	CPUModeHostModel       = "host-model"
	CPUModeHostPassthrough = "host-passthrough"
	DefaultBridgeName      = "default"
	DefaultNetworkName     = "default"
)

func ConvertTaskConfigToDomainSpec(cfg *drivers.TaskConfig, taskCfg *TaskConfig, domainSpec *DomainSpec) error {
	if taskCfg == nil || domainSpec == nil {
		return fmt.Errorf("neither TaskConfig nor DomainSpec can be null in conversion")
	}

	// domainSpec.Name = taskCfg.Name
	domainSpec.Name = TaskID2DomainName(cfg.ID)

	// set taskid as metadata, used to retrieve handler from task store in driver
	domainSpec.Metadata.NomadMeta.JobName = cfg.JobName
	domainSpec.Metadata.NomadMeta.TaskGroupName = cfg.TaskGroupName
	domainSpec.Metadata.NomadMeta.TaskName = cfg.Name
	domainSpec.Metadata.NomadMeta.AllocID = cfg.AllocID
	domainSpec.Metadata.NomadMeta.TaskID = cfg.ID

	if _, err := os.Stat("/dev/kvm"); os.IsNotExist(err) {
		return fmt.Errorf("/dev/kvm not found")
	} else if err != nil {
		return fmt.Errorf("failed to stat /dev/kvm")
	}

	virtioNetProhibited := false
	if _, err := os.Stat("/dev/vhost-net"); os.IsNotExist(err) {
		fmt.Println("In-kernel virtio-net device emulation '/dev/vhost-net' not present")
		virtioNetProhibited = true
	} else if err != nil {
		return err
	}

	// domain.Spec.SysInfo = &SysInfo{}
	// if vmi.Spec.Domain.Firmware != nil {
	// 	domain.Spec.SysInfo.System = []Entry{
	// 		{
	// 			Name:  "uuid",
	// 			Value: string(vmi.Spec.Domain.Firmware.UUID),
	// 		},
	// 	}

	// 	if vmi.Spec.Domain.Firmware.Bootloader != nil && vmi.Spec.Domain.Firmware.Bootloader.EFI != nil {

	// 		domain.Spec.OS.BootLoader = &Loader{
	// 			Path:     EFIPath,
	// 			ReadOnly: "yes",
	// 			Secure:   "no",
	// 			Type:     "pflash",
	// 		}

	// 		domain.Spec.OS.NVRam = &NVRam{
	// 			NVRam:    filepath.Join("/tmp", domain.Spec.Name),
	// 			Template: EFIVarsPath,
	// 		}
	// 	}
	// }

	// // Take memory from the requested memory
	// if v, ok := vmi.Spec.Domain.Resources.Requests[k8sv1.ResourceMemory]; ok {
	// 	if domain.Spec.Memory, err = QuantityToByte(v); err != nil {
	// 		return err
	// 	}
	// }
	// In case that guest memory is explicitly set, override it
	// if vmi.Spec.Domain.Memory != nil && vmi.Spec.Domain.Memory.Guest != nil {
	// 	if domain.Spec.Memory, err = QuantityToByte(*vmi.Spec.Domain.Memory.Guest); err != nil {
	// 		return err
	// 	}
	// }
	if newMem, err := setMemorySpec(taskCfg.Memory); err == nil {
		domainSpec.Memory = newMem
	} else {
		return err
	}

	// if vmi.Spec.Domain.Memory != nil && vmi.Spec.Domain.Memory.Hugepages != nil {
	// 	domain.Spec.MemoryBacking = &MemoryBacking{
	// 		HugePages: &HugePages{},
	// 	}
	// }

	// volumes := map[string]*v1.Volume{}
	// for _, volume := range vmi.Spec.Volumes {
	// 	volumes[volume.Name] = volume.DeepCopy()
	// }

	// dedicatedThreads := 0
	// autoThreads := 0
	// useIOThreads := false
	// threadPoolLimit := 1

	// if vmi.Spec.Domain.IOThreadsPolicy != nil {
	// 	useIOThreads = true

	// 	if (*vmi.Spec.Domain.IOThreadsPolicy) == v1.IOThreadsPolicyAuto {
	// 		numCPUs := 1
	// 		// Requested CPU's is guaranteed to be no greater than the limit
	// 		if cpuRequests, ok := vmi.Spec.Domain.Resources.Requests[k8sv1.ResourceCPU]; ok {
	// 			numCPUs = int(cpuRequests.Value())
	// 		} else if cpuLimit, ok := vmi.Spec.Domain.Resources.Limits[k8sv1.ResourceCPU]; ok {
	// 			numCPUs = int(cpuLimit.Value())
	// 		}

	// 		threadPoolLimit = numCPUs * 2
	// 	}
	// }
	// for _, diskDevice := range vmi.Spec.Domain.Devices.Disks {
	// 	dedicatedThread := false
	// 	if diskDevice.DedicatedIOThread != nil {
	// 		dedicatedThread = *diskDevice.DedicatedIOThread
	// 	}
	// 	if dedicatedThread {
	// 		useIOThreads = true
	// 		dedicatedThreads += 1
	// 	} else {
	// 		autoThreads += 1
	// 	}
	// }

	// if (autoThreads + dedicatedThreads) > threadPoolLimit {
	// 	autoThreads = threadPoolLimit - dedicatedThreads
	// 	// We need at least one shared thread
	// 	if autoThreads < 1 {
	// 		autoThreads = 1
	// 	}
	// }

	// ioThreadCount := (autoThreads + dedicatedThreads)
	// if ioThreadCount != 0 {
	// 	if domain.Spec.IOThreads == nil {
	// 		domain.Spec.IOThreads = &IOThreads{}
	// 	}
	// 	domain.Spec.IOThreads.IOThreads = uint(ioThreadCount)
	// }

	// currentAutoThread := defaultIOThread
	// currentDedicatedThread := uint(autoThreads + 1)

	// var numQueues *uint
	// virtioBlkMQRequested := (vmi.Spec.Domain.Devices.BlockMultiQueue != nil) && (*vmi.Spec.Domain.Devices.BlockMultiQueue)
	// virtioNetMQRequested := (vmi.Spec.Domain.Devices.NetworkInterfaceMultiQueue != nil) && (*vmi.Spec.Domain.Devices.NetworkInterfaceMultiQueue)
	// if virtioBlkMQRequested || virtioNetMQRequested {
	// 	// Requested CPU's is guaranteed to be no greater than the limit
	// 	if cpuRequests, ok := vmi.Spec.Domain.Resources.Requests[k8sv1.ResourceCPU]; ok {
	// 		numCPUs := uint(cpuRequests.Value())
	// 		numQueues = &numCPUs
	// 	} else if cpuLimit, ok := vmi.Spec.Domain.Resources.Limits[k8sv1.ResourceCPU]; ok {
	// 		numCPUs := uint(cpuLimit.Value())
	// 		numQueues = &numCPUs
	// 	}
	// }

	// devicePerBus := make(map[string]int)
	// for _, disk := range vmi.Spec.Domain.Devices.Disks {
	// 	newDisk := Disk{}

	// 	err := Convert_v1_Disk_To_api_Disk(&disk, &newDisk, devicePerBus, numQueues)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	volume := volumes[disk.Name]
	// 	if volume == nil {
	// 		return fmt.Errorf("No matching volume with name %s found", disk.Name)
	// 	}
	// 	err = Convert_v1_Volume_To_api_Disk(volume, &newDisk, c)
	// 	if err != nil {
	// 		return err
	// 	}

	// 	if useIOThreads {
	// 		ioThreadId := defaultIOThread
	// 		dedicatedThread := false
	// 		if disk.DedicatedIOThread != nil {
	// 			dedicatedThread = *disk.DedicatedIOThread
	// 		}

	// 		if dedicatedThread {
	// 			ioThreadId = currentDedicatedThread
	// 			currentDedicatedThread += 1
	// 		} else {
	// 			ioThreadId = currentAutoThread
	// 			// increment the threadId to be used next but wrap around at the thread limit
	// 			// the odd math here is because thread ID's start at 1, not 0
	// 			currentAutoThread = (currentAutoThread % uint(autoThreads)) + 1
	// 		}
	// 		newDisk.Driver.IOThread = &ioThreadId
	// 	}

	// 	domain.Spec.Devices.Disks = append(domain.Spec.Devices.Disks, newDisk)
	// }

	// if vmi.Spec.Domain.Devices.Watchdog != nil {
	// 	newWatchdog := &Watchdog{}
	// 	err := Convert_v1_Watchdog_To_api_Watchdog(vmi.Spec.Domain.Devices.Watchdog, newWatchdog, c)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	domain.Spec.Devices.Watchdog = newWatchdog
	// }

	// if vmi.Spec.Domain.Devices.Rng != nil {
	// 	newRng := &Rng{}
	// 	err := Convert_v1_Rng_To_api_Rng(vmi.Spec.Domain.Devices.Rng, newRng, c)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	domain.Spec.Devices.Rng = newRng
	// }

	devicePerBus := make(map[string]int)
	for _, diskCfg := range taskCfg.Disks {
		if newDisk, err := setDiskSpec(&diskCfg, devicePerBus); err == nil {
			domainSpec.Devices.Disks = append(domainSpec.Devices.Disks, newDisk)
		} else {
			return err
		}

	}

	// if vmi.Spec.Domain.Clock != nil {
	// 	clock := vmi.Spec.Domain.Clock
	// 	newClock := &Clock{}
	// 	err := Convert_v1_Clock_To_api_Clock(clock, newClock, c)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	domain.Spec.Clock = newClock
	// }

	// if vmi.Spec.Domain.Features != nil {
	// 	domain.Spec.Features = &Features{}
	// 	err := Convert_v1_Features_To_api_Features(vmi.Spec.Domain.Features, domain.Spec.Features, c)
	// 	if err != nil {
	// 		return err
	// 	}
	// }
	// apiOst := &vmi.Spec.Domain.Machine
	// err = Convert_v1_Machine_To_api_OSType(apiOst, &domainSpec.OS.Type, c)
	// if err != nil {
	// 	return err
	// }

	//run qemu-system-x86_64 -machine help to see supported machine type
	domainSpec.OS.Type.Machine = taskCfg.Machine

	// Set VM CPU cores
	// CPU topology will be created everytime, because user can specify
	// number of cores in vmi.Spec.Domain.Resources.Requests/Limits, not only
	// in vmi.Spec.Domain.CPU
	domainSpec.VCPU = &VCPU{
		Placement: "static",
		CPUs:      taskCfg.VCPU,
	}

	// Set VM CPU model and vendor
	if taskCfg.CPU.Model != "" {
		if taskCfg.CPU.Model == CPUModeHostModel || taskCfg.CPU.Model == CPUModeHostPassthrough {
			domainSpec.CPU.Mode = taskCfg.CPU.Model
		} else {
			domainSpec.CPU.Mode = "custom"
			domainSpec.CPU.Model = taskCfg.CPU.Model
		}
	}

	// Adjust guest vcpu config. Currenty will handle vCPUs to pCPUs pinning
	// if vmi.IsCPUDedicated() {
	// 	if err := formatDomainCPUTune(vmi, domain, c); err != nil {
	// 		log.Log.Reason(err).Error("failed to format domain cputune.")
	// 		return err
	// 	}
	// 	if useIOThreads {
	// 		if err := formatDomainIOThreadPin(vmi, domain, c); err != nil {
	// 			log.Log.Reason(err).Error("failed to format domain iothread pinning.")
	// 			return err
	// 		}

	// 	}
	// }

	if taskCfg.CPU.Model == "" {
		domainSpec.CPU.Mode = CPUModeHostModel
	}

	// if vmi.Spec.Domain.Devices.AutoattachGraphicsDevice == nil || *vmi.Spec.Domain.Devices.AutoattachGraphicsDevice == true {
	// 	var heads uint = 1
	// 	var vram uint = 16384
	// 	domain.Spec.Devices.Video = []Video{
	// 		{
	// 			Model: VideoModel{
	// 				Type:  "vga",
	// 				Heads: &heads,
	// 				VRam:  &vram,
	// 			},
	// 		},
	// 	}
	// 	domain.Spec.Devices.Graphics = []Graphics{
	// 		{
	// 			Listen: &GraphicsListen{
	// 				Type:   "socket",
	// 				Socket: fmt.Sprintf("/var/run/kubevirt-private/%s/virt-vnc", vmi.ObjectMeta.UID),
	// 			},
	// 			Type: "vnc",
	// 		},
	// 	}
	// }

	getInterfaceType := func(iface *InterfaceConfig) string {
		if iface.InterfaceBindingMethod == "slirp" {
			// Slirp configuration works only with e1000 or rtl8139
			if iface.Model != "e1000" && iface.Model != "rtl8139" {
				fmt.Println("Network interface type of %s was changed to e1000 due to unsupported interface type by qemu slirp network", iface.Name)
				return "e1000"
			}
			return iface.Model
		}
		if iface.Model != "" {
			return iface.Model
		}
		return "virtio"
	}

	for _, iface := range taskCfg.Interfaces {
		switch iface.InterfaceBindingMethod {
		case "sriov":
			//not sure what to do here
		case "bridge", "masquerade", "slirp", "network":
			ifaceType := getInterfaceType(&iface)
			domainIface := Interface{
				Model: &Model{
					Type: ifaceType,
				},
				Alias: &Alias{
					Name: iface.Name,
				},
			}

			// if UseEmulation unset and at least one NIC model is virtio,
			// /dev/vhost-net must be present as we should have asked for it.
			if ifaceType == "virtio" && virtioNetProhibited {
				return fmt.Errorf("virtio interface cannot be used when in-kernel virtio-net device emulation '/dev/vhost-net' not present")
			}

			// Add a pciAddress if specifed, will be auto-generated if not set
			if iface.PciAddress != "" {
				addr, err := decoratePciAddressField(iface.PciAddress)
				if err != nil {
					return fmt.Errorf("failed to configure interface %s: %v", iface.Name, err)
				}
				domainIface.Address = addr
			}

			if iface.InterfaceBindingMethod == "bridge" || iface.InterfaceBindingMethod == "masquerade" {
				// TODO:(ihar) consider abstracting interface type conversion /
				// detection into drivers
				domainIface.Type = "bridge"
				if iface.SourceName != "" {
					domainIface.Source = InterfaceSource{
						Bridge: iface.SourceName,
					}
				} else {
					domainIface.Source = InterfaceSource{
						Bridge: DefaultBridgeName,
					}
				}

				if iface.BootOrder != nil {
					domainIface.BootOrder = &BootOrder{Order: *iface.BootOrder}
				}
			} else if iface.InterfaceBindingMethod == "network" {
				// TODO:(ihar) consider abstracting interface type conversion /
				// detection into drivers
				domainIface.Type = "network"
				if iface.SourceName != "" {
					domainIface.Source = InterfaceSource{
						Network: iface.SourceName,
					}
				} else {
					domainIface.Source = InterfaceSource{
						Network: DefaultNetworkName,
					}
				}

				if iface.BootOrder != nil {
					domainIface.BootOrder = &BootOrder{Order: *iface.BootOrder}
				}
			} else if iface.InterfaceBindingMethod == "slirp" {
				//not sure what to do here
			}
			domainSpec.Devices.Interfaces = append(domainSpec.Devices.Interfaces, domainIface)
		}
	}

	for _, deviceCfg := range taskCfg.HostDevices {
		if newHostDevice, err := setHostDeviceSpec(&deviceCfg); err == nil {
			domainSpec.Devices.HostDevices = append(domainSpec.Devices.HostDevices, newHostDevice)
		} else {
			return err
		}
	}

	return nil
}

func setMemorySpec(cfg MemoryConfig) (Memory, error) {
	if cfg.Value < 0 {
		return Memory{Unit: "B"}, fmt.Errorf("Memory size '%d' must be greater than or equal to 0", cfg.Value)
	}

	var memorySize uint64
	switch cfg.Unit {
	case "gib":
		memorySize = cfg.Value * 1024 * 1024 * 1024
	case "mib":
		memorySize = cfg.Value * 1024 * 1024
	case "kib":
		memorySize = cfg.Value * 1024
	case "b":
		//do nothing
	default:
		return Memory{Unit: "B"}, fmt.Errorf("memory unit for domain not recognized")
	}
	return Memory{
		Value: memorySize,
		Unit:  "B",
	}, nil
}

func setDiskSpec(cfg *DiskConfig, devicePerBus map[string]int) (Disk, error) {
	if cfg == nil {
		return Disk{}, fmt.Errorf("disk config cannot be nil")
	}

	disk := Disk{}
	switch cfg.Device {
	case "disk":
		disk.Device = "disk"
		disk.Type = cfg.Type
		disk.Target.Bus = cfg.TargetBus
		disk.Target.Device = makeDeviceName(cfg.TargetBus, devicePerBus)
		disk.ReadOnly = toApiReadOnly(cfg.ReadOnly)
		//only support file type disk now
		if cfg.Type == "file" {
			disk.Source = DiskSource{File: cfg.Source}
		} else if cfg.Type == "block" {
			disk.Source = DiskSource{Dev: cfg.Source}
		}
	case "lun":
		disk.Device = "lun"
		disk.Target.Bus = cfg.TargetBus
		disk.Target.Device = makeDeviceName(cfg.TargetBus, devicePerBus)
		disk.ReadOnly = toApiReadOnly(cfg.ReadOnly)
	case "floppy":
		disk.Device = "floppy"
		disk.Target.Bus = "fdc"
		disk.Target.Device = makeDeviceName(disk.Target.Bus, devicePerBus)
		disk.ReadOnly = toApiReadOnly(cfg.ReadOnly)
	case "cdrom":
		disk.Device = "cdrom"
		disk.Target.Bus = cfg.TargetBus
		disk.Target.Device = makeDeviceName(cfg.TargetBus, devicePerBus)
		disk.ReadOnly = toApiReadOnly(cfg.ReadOnly)
	default:
		return Disk{}, fmt.Errorf("unknown disk type")
	}

	disk.Driver = &DiskDriver{
		Name:  "qemu",
		Cache: string(cfg.Cache),
	}
	if cfg.Type == "file" {
		disk.Driver.Type = "qcow2"
	} else if cfg.Type == "block" {
		disk.Driver.Type = "raw"
	}

	return disk, nil
}

func setHostDeviceSpec(cfg *HostDeviceConfig) (HostDevice, error) {
	if cfg == nil {
		return HostDevice{}, fmt.Errorf("HostDevice config cannot be nil")
	}

	hostDevice := HostDevice{}
	switch cfg.Type {
	case "pci":
		hostDevice.Type = cfg.Type
		hostDevice.Managed = cfg.Managed
		hostDevice.Source.Address = &Address{
			Domain:   cfg.Domain,
			Bus:      cfg.Bus,
			Slot:     cfg.Slot,
			Function: cfg.Function,
		}
	}
	return hostDevice, nil

}

func makeDeviceName(bus string, devicePerBus map[string]int) string {
	index := devicePerBus[bus]
	devicePerBus[bus] += 1

	prefix := ""
	switch bus {
	case "virtio":
		prefix = "vd"
	case "sata", "scsi":
		prefix = "sd"
	case "fdc":
		prefix = "fd"
	default:
		fmt.Printf("Unrecognized bus '%s'", bus)
		return ""
	}
	return formatDeviceName(prefix, index)
}

// port of http://elixir.free-electrons.com/linux/v4.15/source/drivers/scsi/sd.c#L3211
func formatDeviceName(prefix string, index int) string {
	base := int('z' - 'a' + 1)
	name := ""

	for index >= 0 {
		name = string('a'+(index%base)) + name
		index = (index / base) - 1
	}
	return prefix + name
}

func toApiReadOnly(src bool) *ReadOnly {
	if src {
		return &ReadOnly{}
	}
	return nil
}

// SetDefaultsDomainSpec set default values for domain spec that are not set by user
func SetDefaultsDomainSpec(domainSpec *DomainSpec) {
	domainSpec.XmlNS = "http://libvirt.org/schemas/domain/qemu/1.0"
	if domainSpec.Type == "" {
		domainSpec.Type = "kvm"
	}
	if domainSpec.Clock == nil {
		domainSpec.Clock = setDefaultClock()
	}
	if domainSpec.Features == nil {
		domainSpec.Features = setDefaultFeatures()
	}
	setDefaultsOSType(&domainSpec.OS.Type)

	console, serial := setDefaultConsoleSerial()
	domainSpec.Devices.Consoles = append(domainSpec.Devices.Consoles, console)
	domainSpec.Devices.Serials = append(domainSpec.Devices.Serials, serial)

	domainSpec.Devices.Channels = append(domainSpec.Devices.Channels, setDefaultQemuAgentChannel())
}

// setDefaultClock set default value according to default domain xml generated by virt-manager
func setDefaultClock() *Clock {
	clock := &Clock{}
	clock.Offset = "utc"

	newTimer := Timer{Name: "rtc"}
	newTimer.TickPolicy = "catchup"
	clock.Timer = append(clock.Timer, newTimer)

	newTimer = Timer{Name: "pit"}
	newTimer.TickPolicy = "delay"
	clock.Timer = append(clock.Timer, newTimer)

	newTimer = Timer{Name: "hpet"}
	newTimer.Present = "no"
	clock.Timer = append(clock.Timer, newTimer)

	return clock
}

// setDefaultFeatures set default value according to default domain xml generated by virt-manager
func setDefaultFeatures() *Features {
	features := &Features{}
	features.ACPI = &FeatureEnabled{}
	features.APIC = &FeatureEnabled{}
	return features
}

// setDefaultsOSType set default ostype
func setDefaultsOSType(ostype *OSType) {
	if ostype == nil {
		return
	}

	if ostype.OS == "" {
		ostype.OS = "hvm"
	}

	if ostype.Arch == "" {
		ostype.Arch = "x86_64"
	}

	// q35 is an alias of the newest q35 machine type.
	// TODO: we probably want to select concrete type in the future for "future-backwards" compatibility.
	if ostype.Machine == "" {
		ostype.Machine = "q35"
	}
}

// setDefaultConsoleSerial set default console and serial so that you can connect to a domain via virsh console
// according to https://wiki.libvirt.org/page/Unable_to_connect_to_console_of_a_running_domain,
// if you want to connect to a domain's serial console via `virsh console`, you should do the following setting:
// 1. add a console and serial setting in domain define xml, as defined in the following function. I put these 2 setting in the same function because they work together
// 1. add a grub kernel setting
func setDefaultConsoleSerial() (Console, Serial) {
	// Add mandatory console device
	var serialPort uint = 0
	var serialType string = "serial"
	c := Console{
		Type: "pty",
		Target: &ConsoleTarget{
			Type: &serialType,
			Port: &serialPort,
		},
	}

	s := Serial{
		Type: "pty",
		Target: &SerialTarget{
			Port: &serialPort,
		},
	}
	return c, s
}

// setDefaultQemuAgentChannel creates the channel for qemu guest agent communication
func setDefaultQemuAgentChannel() (channel Channel) {
	channel.Type = "unix"
	// let libvirt decide which path to use
	channel.Source = nil
	channel.Target = &ChannelTarget{
		Name: "org.qemu.guest_agent.0",
		Type: "virtio",
	}

	return
}

func decoratePciAddressField(addressField string) (*Address, error) {
	dbsfFields, err := ParsePciAddress(addressField)
	if err != nil {
		return nil, err
	}
	decoratedAddrField := &Address{
		Type:     "pci",
		Domain:   "0x" + dbsfFields[0],
		Bus:      "0x" + dbsfFields[1],
		Slot:     "0x" + dbsfFields[2],
		Function: "0x" + dbsfFields[3],
	}
	return decoratedAddrField, nil
}

const PCI_ADDRESS_PATTERN = `^([\da-fA-F]{4}):([\da-fA-F]{2}):([\da-fA-F]{2}).([0-7]{1})$`

// ParsePciAddress returns an array of PCI DBSF fields (domain, bus, slot, function)
func ParsePciAddress(pciAddress string) ([]string, error) {
	pciAddrRegx, err := regexp.Compile(PCI_ADDRESS_PATTERN)
	if err != nil {
		return nil, fmt.Errorf("failed to compile pci address pattern, %v", err)
	}
	res := pciAddrRegx.FindStringSubmatch(pciAddress)
	if len(res) == 0 {
		return nil, fmt.Errorf("failed to parse pci address %s", pciAddress)
	}
	return res[1:], nil
}

func TaskID2DomainName(taskID string) string {
	// task id takes the form of {allcID}/{taskName}/{taskID}
	// allocID and taskID should only contain [a-z0-9\-]), and be less than 64 characters in length.
	// refer to https://www.nomadproject.io/docs/job-specification/index.html for nomad naming convensions
	// but for max portability, domain names in libvirt should only contain [a-z0-9-_] according to  https://libvirt.org/docs/libvirt-appdev-guide-python/en-US/html/libvirt_application_development_guide_using_python-Guest_Domains.html
	// so I will replace the / in taskID with _
	return strings.Replace(taskID, "/", "_", 2)
}
