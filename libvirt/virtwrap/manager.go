package virtwrap

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/plugins/drivers"
	libvirt "github.com/libvirt/libvirt-go"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/api"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/cli"
	domainerrors "gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/errors"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/stats"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/util"
)

type DomainManager interface {
	SyncVM(*drivers.TaskConfig, *api.TaskConfig, hclog.Logger) (*api.DomainSpec, error)
	KillVM(string) error
	DestroyVM(string) error
	VMState(string) (api.LifeCycle, error)
	DomainIfAddr(string, bool, hclog.Logger) (*api.GuestInterface, error)
	StartDomainMonitor(context.Context) (<-chan api.LibvirtEvent, error)
	StartDomainStatsColloction(context.Context, time.Duration) (<-chan *stats.DomainStats, error)
}

var _ DomainManager = &LibvirtDomainManager{}

type LibvirtDomainManager struct {
	virConn cli.Connection

	// Anytime a get and a set is done on the domain, this lock must be held.
	domainModifyLock sync.Mutex
}

func (l *LibvirtDomainManager) SyncVM(cfg *drivers.TaskConfig, taskCfg *api.TaskConfig, logger hclog.Logger) (*api.DomainSpec, error) {
	l.domainModifyLock.Lock()
	defer l.domainModifyLock.Unlock()

	spec := &api.DomainSpec{}
	if err := api.ConvertTaskConfigToDomainSpec(cfg, taskCfg, spec); err != nil {
		logger.Error("TaskConfig to handleTask Conversion failed")
		return nil, err
	}

	// Set defaults which are not coming from the cluster
	api.SetDefaultsDomainSpec(spec)

	dom, err := l.virConn.LookupDomainByName(spec.Name)
	newDomain := false
	if err != nil {
		// We need the domain but it does not exist, so create it
		if domainerrors.IsNotFound(err) {
			newDomain = true

			dom, err = l.setDomainSpec(spec)
			if err != nil {
				return nil, err
			}
			logger.Debug("Domain defined.")
		} else {
			logger.Error("Getting the domain failed.")
			return nil, err
		}
	}
	defer dom.Free()
	domState, _, err := dom.GetState()
	if err != nil {
		logger.Error("Getting the domain state failed.")
		return nil, err
	}

	// To make sure, that we set the right qemu wrapper arguments,
	// we update the domain XML whenever a VirtualMachineInstance was already defined but not running
	if !newDomain && cli.IsDown(domState) {
		uuid, err := dom.GetUUIDString()
		if err != nil {
			return nil, err
		}
		logger.Debug("existing domain found", "name", spec.Name, "uuid", uuid)

		//set the new domain uuid to old one, otherwise the override will fail
		//refer to https://bugzilla.redhat.com/show_bug.cgi?id=1309271 for details
		spec.UUID = uuid
		dom, err = l.setDomainSpec(spec)
		if err != nil {
			return nil, err
		}
	}

	// TODO Suspend, Pause, ..., for now we only support reaching the running state
	// TODO for migration and error detection we also need the state change reason
	// TODO blocked state
	if cli.IsDown(domState) {
		err = dom.Create()
		if err != nil {
			logger.Debug("Creating the domain according to TaskConfig failed.")
			return nil, err
		}
		// fmt.Println("Domain started.")
	} else if cli.IsPaused(domState) {
		// TODO: if state change reason indicates a system error, we could try something smarter
		err := dom.Resume()
		if err != nil {
			logger.Error("Resuming existing domain failed.")
			return nil, err
		}
		logger.Debug("Domain resumed.")
	} else {
		// not down, not paused, should be running, blocked or pmsuspended
		// running: do nothing
		// pmsuspended: the domain has been suspended by guest power management, probably shouldn't wake it
		// blocked: TODO not sure what this means
		logger.Debug("domain already running or pmsuspended, won't start or resume it here")
	}

	// TODO: check if VirtualMachineInstance Spec and Domain Spec are equal or if we have to sync
	return spec, nil
}

func NewLibvirtDomainManager(connection cli.Connection) (DomainManager, error) {
	manager := LibvirtDomainManager{
		virConn: connection,
	}

	return &manager, nil
}

func (l *LibvirtDomainManager) setDomainSpec(spec *api.DomainSpec) (cli.VirDomain, error) {
	domainSpecXML, err := xml.Marshal(spec)
	if err != nil {
		return nil, err
	}
	return util.SetDomainSpecStr(l.virConn, string(domainSpecXML))
}

// DomainIfAddr retrieves interface info from qemu guest agent
// if wait4ipv4 = true, it will try to return a non-lo interface with a ipv4 address, unless the MAX_ATTEMPT is reached
// if wait4ipv4 = false, it will try to return a non-lo interface, with or without a ipv4 address, unless the MAX_ATTEMPT is reached
func (l *LibvirtDomainManager) DomainIfAddr(name string, wait4ipv4 bool, logger hclog.Logger) (*api.GuestInterface, error) {
	dom, err := l.virConn.LookupDomainByName(name)
	if err != nil {
		if domainerrors.IsNotFound(err) {
			logger.Error("domain not found")
		} else {
			logger.Error("failed to look up domain")
		}
		return nil, err
	}
	defer dom.Free()

	attempt := 0
	const MAX_ATTEMPT = 10
	var virtErrRegExp = regexp.MustCompile("Code=(\\d+),")
	nonLoFound := false

	for attempt < MAX_ATTEMPT {
		// About the 3rd parameter in QemuAgentCommand, the flags parameter is reserved for future use - apps must just pass 0 for now. Use 'uint32' as the type for the flags parameter. ref: https://github.com/libvirt/libvirt-go/issues/17#issuecomment-295312759
		var err error
		result, err := dom.QemuAgentCommand("{\"execute\":\"guest-network-get-interfaces\"}", libvirt.DOMAIN_QEMU_AGENT_COMMAND_DEFAULT, uint32(0))
		if err != nil {
			logger.Debug("error from qemu agent command", "err", err)
			matchs := virtErrRegExp.FindStringSubmatch(err.Error())
			if len(matchs) == 2 {
				if i, err := strconv.Atoi(matchs[1]); err == nil {
					logger.Debug("got error code from qemu agent command", "code", i)
					if i == 86 {
						attempt++
						time.Sleep(5 * time.Second)
						continue
					}
				}
			}
		}

		if err != nil {
			// for errors other than 86(ga not connected), just return immediately
			logger.Debug("unknown error occurred from qemu agent response", "err", err)
			return nil, err
		}
		logger.Debug("got interface info", "attempt", attempt, "result", result)

		//decoding guest agent response
		interfaces := api.GuestNetworkInterface{}
		if err := json.Unmarshal([]byte(result), &interfaces); err != nil {
			logger.Error("error unmarshallin guest network interface result", "err", err)
			return nil, err
		}
		// we assume the domain has non-lo interface, and only return the first none-lo interface
		for _, iface := range interfaces.Interfaces {
			if iface.Name == "lo" {
				continue
			}
			// non-lo interface found
			nonLoFound = true
			if wait4ipv4 == false {
				// if no ipv4 address is required for the non-lo interface, return here
				return &iface, nil
			}
			for _, ip := range iface.IPs {
				if ip.Type == "ipv4" {
					return &iface, nil
				}
			}
		}
		// only lo interface found, or no ipv4 address found for the non-lo interface
		// sleep shorter, as we have valid response from guest agent
		time.Sleep(3 * time.Second)
		attempt++
	}

	if nonLoFound == false {
		// if we got here because the domain only has a lo interface
		return nil, fmt.Errorf("no non-lo interface found after MAX_ATTEMPT")
	}
	// if we got here because the domain has on-lo interface, but has no ipv4 address
	return nil, fmt.Errorf("non-lo interface found, but has no ipv4 address after MAX_ATTEMPT")
}

func (l *LibvirtDomainManager) KillVM(domainName string) error {
	l.domainModifyLock.Lock()
	defer l.domainModifyLock.Unlock()

	dom, err := l.virConn.LookupDomainByName(domainName)
	if err != nil {
		// If the VirtualMachineInstance does not exist, we are done
		if domainerrors.IsNotFound(err) {
			return nil
		} else {
			fmt.Println("Getting the domain failed.")
			return err
		}
	}
	defer dom.Free()

	domState, _, err := dom.GetState()
	if err != nil {
		if domainerrors.IsNotFound(err) {
			return nil
		}
		fmt.Println("Getting the domain state failed.")
		return err
	}

	if domState == libvirt.DOMAIN_RUNNING || domState == libvirt.DOMAIN_PAUSED || domState == libvirt.DOMAIN_SHUTDOWN {
		err = dom.DestroyFlags(libvirt.DOMAIN_DESTROY_GRACEFUL)
		if err != nil {
			if domainerrors.IsNotFound(err) {
				return nil
			}
			fmt.Println("Destroying the domain gracefully failed, trying again by force")
			dom.Destroy()
			return err
		}
		fmt.Println("Domain stopped.")
		return nil
	}

	fmt.Println("Domain not running or paused, nothing to do.")
	return nil
}

func (l *LibvirtDomainManager) DestroyVM(domName string) error {
	l.domainModifyLock.Lock()
	defer l.domainModifyLock.Unlock()

	dom, err := l.virConn.LookupDomainByName(domName)
	if err != nil {
		// If the domain does not exist, we are done
		if domainerrors.IsNotFound(err) {
			return nil
		} else {
			fmt.Println("Getting the domain failed.")
			return err
		}
	}
	defer dom.Free()

	err = dom.Undefine()
	if err != nil {
		fmt.Println("Undefining the domain failed.")
		return err
	}
	fmt.Println("Domain undefined.")
	return nil
}

func (l *LibvirtDomainManager) VMState(domName string) (api.LifeCycle, error) {
	dom, err := l.virConn.LookupDomainByName(domName)
	if err != nil {
		if domainerrors.IsNotFound(err) {
			fmt.Println("domain not found")
		} else {
			fmt.Println("failed to look up domain")
		}
		return api.NoState, err
	}
	defer dom.Free()

	s, _, err := dom.GetState()
	if err != nil {
		if !domainerrors.IsNotFound(err) {
			fmt.Printf("Could not fetch the Domain state: %+v\n", err)
		}
		return api.NoState, err
	}
	return util.ConvState(s), nil

}

func (l *LibvirtDomainManager) StartDomainMonitor(ctx context.Context) (<-chan api.LibvirtEvent, error) {
	eventChan := make(chan api.LibvirtEvent, 10)

	domainEventLifecycleCallback := func(c *libvirt.Connect, d *libvirt.Domain, event *libvirt.DomainEventLifecycle) {
		fmt.Printf("domain event recv: %+v\n", event)
		name, err := d.GetName()
		if err != nil {
			fmt.Printf("domain event callback: error getting domain name for event: %s, error: %+v\n", event, err)
		}

		status, reason, err := d.GetState()
		if err != nil {
			if !domainerrors.IsNotFound(err) {
				fmt.Printf("Could not fetch the Domain state: %+v\n", err)
			}
			return
		}
		apiState := util.ConvState(status)
		apiReason := util.ConvReason(status, reason)

		metadata := &api.NomadMetaData{}
		metadataXML, err := d.GetMetadata(libvirt.DOMAIN_METADATA_ELEMENT, "http://harmonycloud.cn", libvirt.DOMAIN_AFFECT_CONFIG)
		err = xml.Unmarshal([]byte(metadataXML), metadata)
		if err != nil {
			fmt.Printf("failed to unmarshal domain metadata, %+v\n", err)
			return
		}
		if metadata.TaskID == "" {
			// domain created by us must have taskid as metadata
			fmt.Printf("empty taskid for domain %s\n", name)
			return
		}

		fmt.Printf("event parsed: %s(domain name), %s(taskid), %s(state), %s(reason)\n", name, metadata.TaskID, apiState, apiReason)

		select {
		case eventChan <- api.LibvirtEvent{
			DomainName: name,
			TaskID:     metadata.TaskID,
			State:      apiState,
			Reason:     apiReason,
		}:
		default:
			fmt.Println("Libvirt event channel is full, dropping event.")
		}
	}

	err := l.virConn.DomainEventLifecycleRegister(domainEventLifecycleCallback)
	if err != nil {
		fmt.Printf("failed to register domain event callback with libvirt: %+v\n", err)
		return nil, err
	}

	// start receiving domain event
	go func() {
		for {
			select {
			case <-ctx.Done():
				// context done, return
				return
			default:
				if err := libvirt.EventRunDefaultImpl(); err != nil {
					// Listening to libvirt events failed, retrying
					time.Sleep(time.Second)
				}
			}
		}
	}()

	return eventChan, nil
}

func (l *LibvirtDomainManager) StartDomainStatsColloction(ctx context.Context, interval time.Duration) (<-chan *stats.DomainStats, error) {
	statsChan := make(chan *stats.DomainStats)

	timer := time.NewTimer(interval)

	// stats collection
	go func() {
		for {
			select {
			case <-timer.C:
				// collect stats for all running domain
				l.collectRunningDomainStats(statsChan)
				timer.Reset(interval)
			case <-ctx.Done():
				// close channel when done
				fmt.Println("context canceled, stopping domain stats collection")
				close(statsChan)
				return
			}
		}
	}()

	return statsChan, nil
}

func (l *LibvirtDomainManager) collectRunningDomainStats(statsChan chan *stats.DomainStats) {
	statsTypes := libvirt.DOMAIN_STATS_CPU_TOTAL | libvirt.DOMAIN_STATS_VCPU | libvirt.DOMAIN_STATS_INTERFACE | libvirt.DOMAIN_STATS_BLOCK
	flags := libvirt.CONNECT_GET_ALL_DOMAINS_STATS_RUNNING

	stats, err := l.virConn.GetDomainStats(statsTypes, flags)
	if err != nil {
		fmt.Printf("error getting domain stats = %+v\n", err)
		return
	}

	for _, stat := range stats {
		statsChan <- stat
	}
}
