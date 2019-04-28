package cli

import (
	"fmt"
	"sync"
	"time"

	libvirt "github.com/libvirt/libvirt-go"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/errors"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/stats"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/statsconv"
)

type Connection interface {
	LookupDomainByName(name string) (VirDomain, error)
	DomainDefineXML(xml string) (VirDomain, error)
	Close() (int, error)
	DomainEventLifecycleRegister(callback libvirt.DomainEventLifecycleCallback) error
	AgentEventLifecycleRegister(callback libvirt.DomainEventAgentLifecycleCallback) error
	QemuAgentCommand(command string, domainName string) (string, error)
	SetReconnectChan(reconnect chan bool)
	GetDomainStats(statsTypes libvirt.DomainStatsTypes, flags libvirt.ConnectGetAllDomainStatsFlags) ([]*stats.DomainStats, error)
	ReconnectIfNecessary() error
}

type LibvirtConnection struct {
	Connect       *libvirt.Connect
	user          string
	pass          string
	uri           string
	alive         bool
	stop          chan struct{}
	reconnect     chan bool
	reconnectLock *sync.Mutex
}

type VirDomain interface {
	GetState() (libvirt.DomainState, int, error)
	Create() error
	Resume() error
	DestroyFlags(flags libvirt.DomainDestroyFlags) error
	Destroy() error
	ShutdownFlags(flags libvirt.DomainShutdownFlags) error
	Undefine() error
	GetName() (string, error)
	GetUUIDString() (string, error)
	GetXMLDesc(flags libvirt.DomainXMLFlags) (string, error)
	GetMetadata(tipus libvirt.DomainMetadataType, uri string, flags libvirt.DomainModificationImpact) (string, error)
	OpenConsole(devname string, stream *libvirt.Stream, flags libvirt.DomainConsoleFlags) error
	Migrate(*libvirt.Connect, libvirt.DomainMigrateFlags, string, string, uint64) (*libvirt.Domain, error)
	Free() error
	QemuAgentCommand(command string, timeout libvirt.DomainQemuAgentCommandTimeout, flags uint32) (string, error)
}

func NewConnection(uri string, user string, pass string, checkInterval time.Duration) (Connection, error) {

	var err error
	var virConn *libvirt.Connect

	virConn, err = newConnection(uri, user, pass)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to libvirt daemon: %v", err)
	}

	lvConn := &LibvirtConnection{
		Connect: virConn, user: user, pass: pass, uri: uri, alive: true,
		reconnectLock: &sync.Mutex{},
		stop:          make(chan struct{}),
	}

	return lvConn, nil
}

// TODO: needs a functional test.
func newConnection(uri string, user string, pass string) (*libvirt.Connect, error) {
	callback := func(creds []*libvirt.ConnectCredential) {
		for _, cred := range creds {
			if cred.Type == libvirt.CRED_AUTHNAME {
				cred.Result = user
				cred.ResultLen = len(cred.Result)
			} else if cred.Type == libvirt.CRED_PASSPHRASE {
				cred.Result = pass
				cred.ResultLen = len(cred.Result)
			}
		}
	}
	auth := &libvirt.ConnectAuth{
		CredType: []libvirt.ConnectCredentialType{
			libvirt.CRED_AUTHNAME, libvirt.CRED_PASSPHRASE,
		},
		Callback: callback,
	}
	virConn, err := libvirt.NewConnectWithAuth(uri, auth, 0)

	return virConn, err
}

func (l *LibvirtConnection) Close() (int, error) {
	close(l.stop)
	return l.Connect.Close()
}

func (l *LibvirtConnection) LookupDomainByName(name string) (VirDomain, error) {
	if err := l.ReconnectIfNecessary(); err != nil {
		return nil, err
	}

	domain, err := l.Connect.LookupDomainByName(name)
	if err != nil {
		l.checkConnectionLost(err)
		return nil, err
	}
	return domain, nil
}

func (l *LibvirtConnection) ReconnectIfNecessary() error {
	l.reconnectLock.Lock()
	defer l.reconnectLock.Unlock()
	// TODO add a reconnect backoff, and immediately return an error in these cases
	// We need this to avoid swamping libvirt with reconnect tries
	if !l.alive {
		var err error

		l.Connect, err = newConnection(l.uri, l.user, l.pass)
		if err != nil {
			return err
		}
		l.alive = true

		if l.reconnect != nil {
			// Notify the callback about the reconnect through channel.
			// This way we give the callback a chance to emit an error to the watcher
			// ListWatcher will re-register automatically afterwards
			l.reconnect <- true
		}
	}
	return nil
}

func (l *LibvirtConnection) DomainDefineXML(xml string) (VirDomain, error) {
	if err := l.ReconnectIfNecessary(); err != nil {
		return nil, err
	}

	dom, err := l.Connect.DomainDefineXML(xml)
	if err != nil {
		l.checkConnectionLost(err)
		return nil, err
	}
	return dom, nil
}

func (l *LibvirtConnection) checkConnectionLost(err error) {
	l.reconnectLock.Lock()
	defer l.reconnectLock.Unlock()

	if errors.IsOk(err) {
		return
	}

	libvirtError, ok := err.(libvirt.Error)
	if !ok {
		return
	}

	switch libvirtError.Code {
	case
		libvirt.ERR_INTERNAL_ERROR,
		libvirt.ERR_INVALID_CONN,
		libvirt.ERR_AUTH_CANCELLED,
		libvirt.ERR_NO_MEMORY,
		libvirt.ERR_AUTH_FAILED,
		libvirt.ERR_SYSTEM_ERROR,
		libvirt.ERR_RPC:
		l.alive = false
		// fmt.Println("Connection to libvirt lost.")
	}
}

func IsDown(domState libvirt.DomainState) bool {
	switch domState {
	case libvirt.DOMAIN_NOSTATE, libvirt.DOMAIN_SHUTDOWN, libvirt.DOMAIN_SHUTOFF, libvirt.DOMAIN_CRASHED:
		return true

	}
	return false
}

func IsPaused(domState libvirt.DomainState) bool {
	switch domState {
	case libvirt.DOMAIN_PAUSED:
		return true

	}
	return false
}

func (l *LibvirtConnection) DomainEventLifecycleRegister(callback libvirt.DomainEventLifecycleCallback) error {
	if err := l.ReconnectIfNecessary(); err != nil {
		return err
	}

	_, err := l.Connect.DomainEventLifecycleRegister(nil, callback)
	if err != nil {
		l.checkConnectionLost(err)
		return err
	}
	return nil
}

func (l *LibvirtConnection) AgentEventLifecycleRegister(callback libvirt.DomainEventAgentLifecycleCallback) error {
	if err := l.ReconnectIfNecessary(); err != nil {
		return err
	}

	_, err := l.Connect.DomainEventAgentLifecycleRegister(nil, callback)
	if err != nil {
		l.checkConnectionLost(err)
		return err
	}
	return nil
}

// Execute a command on the Qemu guest agent
// command - the qemu command, for example this gets the interfaces: {"execute":"guest-network-get-interfaces"}
// domainName -  the qemu domain name
func (l *LibvirtConnection) QemuAgentCommand(command string, domainName string) (string, error) {
	domain, err := l.Connect.LookupDomainByName(domainName)
	result, err := domain.QemuAgentCommand(command, libvirt.DOMAIN_QEMU_AGENT_COMMAND_DEFAULT, uint32(0))
	return result, err
}

func (l *LibvirtConnection) SetReconnectChan(reconnect chan bool) {
	l.reconnect = reconnect
}

func (l *LibvirtConnection) GetDomainStats(statsTypes libvirt.DomainStatsTypes, flags libvirt.ConnectGetAllDomainStatsFlags) ([]*stats.DomainStats, error) {
	domStats, err := l.GetAllDomainStats(statsTypes, flags)
	if err != nil {
		return nil, err
	}

	var list []*stats.DomainStats
	for _, domStat := range domStats {
		var err error

		memStats, err := domStat.Domain.MemoryStats(uint32(libvirt.DOMAIN_MEMORY_STAT_NR), 0)
		if err != nil {
			return list, err
		}

		stat := &stats.DomainStats{}
		stat.Timestamp = time.Now().UnixNano()
		err = statsconv.Convert_libvirt_DomainStats_to_stats_DomainStats(domStat.Domain, &domStat, memStats, stat)
		if err != nil {
			return list, err
		}

		list = append(list, stat)
		domStat.Domain.Free()
	}

	return list, nil
}

func (l *LibvirtConnection) GetAllDomainStats(statsTypes libvirt.DomainStatsTypes, flags libvirt.ConnectGetAllDomainStatsFlags) ([]libvirt.DomainStats, error) {
	if err := l.ReconnectIfNecessary(); err != nil {
		return nil, err
	}

	doms := []*libvirt.Domain{}
	domStats, err := l.Connect.GetAllDomainStats(doms, statsTypes, flags)
	if err != nil {
		l.checkConnectionLost(err)
		return nil, err
	}
	return domStats, nil
}
