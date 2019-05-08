package api

import (
	"strings"

	"github.com/hashicorp/nomad/plugins/shared/hclspec"
)

type TaskConfig struct {
	Name       string            `codec:"name"`
	Memory     MemoryConfig      `codec:"memory"`
	VCPU       uint32            `codec:"vcpu"`
	CPU        CPUConfig         `codec:"cpu"`
	Disks      []DiskConfig      `codec:"disks"`
	Machine    string            `codec:"machine"`
	Interfaces []InterfaceConfig `codec:"interfaces"`
}

type MemoryConfig struct {
	Value uint64 `codec:"value"`
	Unit  string `codec:"unit"`
}

type DiskConfig struct {
	Device    string `codec:"device"`
	Type      string `codec:"type"`
	Source    string `codec:"source"`
	TargetBus string `codec:"target_bus"`
	ReadOnly  bool   `codec:"read_only"`
	Cache     string `codec:"cache"`
}

// CPU allows specifying the CPU topology.
type CPUConfig struct {
	// Cores specifies the number of cores inside the vmi.
	// Must be a value greater or equal 1.
	Cores uint32 `codec:"cores"`
	// Sockets specifies the number of sockets inside the vmi.
	// Must be a value greater or equal 1.
	Sockets uint32 `codec:"sockets"`
	// Threads specifies the number of threads inside the vmi.
	// Must be a value greater or equal 1.
	Threads uint32 `codec:"threads"`
	// Model specifies the CPU model inside the VMI.
	// List of available models https://github.com/libvirt/libvirt/blob/master/src/cpu/cpu_map.xml.
	// It is possible to specify special cases like "host-passthrough" to get the same CPU as the node
	// and "host-model" to get CPU closest to the node one.
	// For more information see https://libvirt.org/formatdomain.html#elementsCPU.
	// Defaults to host-model.
	// +optional
	Model string `codec:"model"`
	// DedicatedCPUPlacement requests the scheduler to place the domain on a node
	// with enough dedicated pCPUs and pin the vCPUs to it.
	// +optional
	DedicatedCPUPlacement bool `codec:"dedicated_cpu_placement"`
}

type InterfaceConfig struct {
	// Logical name of the interface as well as a reference to the associated networks.
	// Must match the Name of a Network.
	Name string `codec:"name"`
	// Interface model.
	// One of: e1000, e1000e, ne2k_pci, pcnet, rtl8139, virtio.
	// Defaults to virtio.
	// TODO:(ihar) switch to enums once opengen-api supports them. See: https://github.com/kubernetes/kube-openapi/issues/51
	Model string `codec:"model"`
	// BindingMethod specifies the method which will be used to connect the interface to the guest.
	// Defaults to Bridge.
	InterfaceBindingMethod string `codec:"interface_binding_method"`
	// Interface MAC address. For example: de:ad:00:00:be:af or DE-AD-00-00-BE-AF.
	MacAddress string `codec:"mac_address"`
	// BootOrder is an integer value > 0, used to determine ordering of boot devices.
	// Lower values take precedence.
	// Each interface or disk that has a boot order must have a unique value.
	// Interfaces without a boot order are not tried.
	// +optional
	BootOrder *uint `codec:"boot_order"`
	// If specified, the virtual network interface will be placed on the guests pci address with the specifed PCI address. For example: 0000:81:01.10
	// +optional
	PciAddress string `codec:"pci_address"`

	SourceName string `codec:"source_name"`
}

func (t *TaskConfig) ToLower() {
	for idx, i := range t.Interfaces {
		if i.InterfaceBindingMethod != "" {
			t.Interfaces[idx].InterfaceBindingMethod = strings.ToLower(i.InterfaceBindingMethod)
		}
	}
	if t.Memory.Unit != "" {
		t.Memory.Unit = strings.ToLower(t.Memory.Unit)
	}

}

// taskConfigSpec is the hcl specification for the driver config section of
// a taskConfig within a job. It is returned in the TaskConfigSchema RPC
var TaskConfigSpec = hclspec.NewObject(map[string]*hclspec.Spec{
	"name": hclspec.NewAttr("name", "string", true),
	"memory": hclspec.NewBlock("memory", true, hclspec.NewObject(map[string]*hclspec.Spec{
		"value": hclspec.NewAttr("value", "number", true),
		"unit":  hclspec.NewAttr("unit", "string", true),
	})),
	"vcpu": hclspec.NewAttr("vcpu", "number", true),
	"cpu": hclspec.NewBlock("cpu", false, hclspec.NewObject(map[string]*hclspec.Spec{
		"cores":                   hclspec.NewAttr("cores", "number", false),
		"sockets":                 hclspec.NewAttr("sockets", "number", false),
		"threads":                 hclspec.NewAttr("threads", "number", false),
		"model":                   hclspec.NewAttr("model", "string", false),
		"dedicated_cpu_placement": hclspec.NewAttr("dedicated_cpu_placement", "bool", false),
	})),
	"disks": hclspec.NewBlockSet("disks", hclspec.NewObject(map[string]*hclspec.Spec{
		"device":     hclspec.NewAttr("device", "string", false),
		"type":       hclspec.NewAttr("type", "string", false),
		"source":     hclspec.NewAttr("source", "string", false),
		"target_bus": hclspec.NewAttr("target_bus", "string", false),
		"read_only":  hclspec.NewAttr("read_only", "bool", false),
		"cache":      hclspec.NewAttr("cache", "string", false),
	})),
	"machine": hclspec.NewAttr("machine", "string", false),
	"interfaces": hclspec.NewBlockSet("interfaces", hclspec.NewObject(map[string]*hclspec.Spec{
		"name":                     hclspec.NewAttr("name", "string", false),
		"model":                    hclspec.NewAttr("model", "string", false),
		"interface_binding_method": hclspec.NewAttr("interface_binding_method", "string", false),
		"boot_order":               hclspec.NewAttr("boot_order", "number", false),
		"pci_address":              hclspec.NewAttr("pci_address", "string", false),
		"source_name":              hclspec.NewAttr("source_name", "string", false),
	})),
})
