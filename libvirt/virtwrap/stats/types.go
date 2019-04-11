package stats

type DomainStats struct {
	// the following aren't really needed for stats, but it's practical to report
	// OTOH, the whole "Domain" is too much data to be unconditionally reported
	Name   string
	TaskID string
	// omitted from libvirt-go: Domain
	// omitted from libvirt-go: State
	Cpu *DomainStatsCPU
	// new, see below
	Memory *DomainStatsMemory
	// omitted from libvirt-go: Balloon
	Vcpu  []DomainStatsVcpu
	Net   []DomainStatsNet
	Block []DomainStatsBlock
	// omitted from libvirt-go: Perf
	Timestamp int64 // UnixNano
}

type DomainStatsCPU struct {
	TimeSet   bool
	Time      uint64
	UserSet   bool
	User      uint64
	SystemSet bool
	System    uint64
}

type DomainStatsVcpu struct {
	StateSet bool
	State    int // VcpuState
	TimeSet  bool
	Time     uint64
}

type DomainStatsNet struct {
	NameSet    bool
	Name       string
	RxBytesSet bool
	RxBytes    uint64
	RxPktsSet  bool
	RxPkts     uint64
	RxErrsSet  bool
	RxErrs     uint64
	RxDropSet  bool
	RxDrop     uint64
	TxBytesSet bool
	TxBytes    uint64
	TxPktsSet  bool
	TxPkts     uint64
	TxErrsSet  bool
	TxErrs     uint64
	TxDropSet  bool
	TxDrop     uint64
}

type DomainStatsBlock struct {
	NameSet         bool
	Name            string
	BackingIndexSet bool
	BackingIndex    uint
	PathSet         bool
	Path            string
	RdReqsSet       bool
	RdReqs          uint64
	RdBytesSet      bool
	RdBytes         uint64
	RdTimesSet      bool
	RdTimes         uint64
	WrReqsSet       bool
	WrReqs          uint64
	WrBytesSet      bool
	WrBytes         uint64
	WrTimesSet      bool
	WrTimes         uint64
	FlReqsSet       bool
	FlReqs          uint64
	FlTimesSet      bool
	FlTimes         uint64
	ErrorsSet       bool
	Errors          uint64
	AllocationSet   bool
	Allocation      uint64
	CapacitySet     bool
	Capacity        uint64
	PhysicalSet     bool
	Physical        uint64
}

// mimic existing structs, but data is taken from
// DomainMemoryStat
type DomainStatsMemory struct {
	UnusedSet        bool
	Unused           uint64
	AvailableSet     bool
	Available        uint64
	ActualBalloonSet bool
	ActualBalloon    uint64
	RSSSet           bool
	RSS              uint64
}
