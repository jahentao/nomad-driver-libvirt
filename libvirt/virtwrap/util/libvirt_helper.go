package util

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	libvirt "github.com/libvirt/libvirt-go"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/api"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt/virtwrap/cli"
)

func init() {
	// this must be called before doing anything else
	// otherwise the registration will always fail with internal error "could not initialize domain event timer"
	libvirt.EventRegisterDefaultImpl()
}

var LifeCycleTranslationMap = map[libvirt.DomainState]api.LifeCycle{
	libvirt.DOMAIN_NOSTATE:     api.NoState,
	libvirt.DOMAIN_RUNNING:     api.Running,
	libvirt.DOMAIN_BLOCKED:     api.Blocked,
	libvirt.DOMAIN_PAUSED:      api.Paused,
	libvirt.DOMAIN_SHUTDOWN:    api.Shutdown,
	libvirt.DOMAIN_SHUTOFF:     api.Shutoff,
	libvirt.DOMAIN_CRASHED:     api.Crashed,
	libvirt.DOMAIN_PMSUSPENDED: api.PMSuspended,
}

var ShutdownReasonTranslationMap = map[libvirt.DomainShutdownReason]api.StateChangeReason{
	libvirt.DOMAIN_SHUTDOWN_UNKNOWN: api.ReasonUnknown,
	libvirt.DOMAIN_SHUTDOWN_USER:    api.ReasonUser,
}

var ShutoffReasonTranslationMap = map[libvirt.DomainShutoffReason]api.StateChangeReason{
	libvirt.DOMAIN_SHUTOFF_UNKNOWN:       api.ReasonUnknown,
	libvirt.DOMAIN_SHUTOFF_SHUTDOWN:      api.ReasonShutdown,
	libvirt.DOMAIN_SHUTOFF_DESTROYED:     api.ReasonDestroyed,
	libvirt.DOMAIN_SHUTOFF_CRASHED:       api.ReasonCrashed,
	libvirt.DOMAIN_SHUTOFF_MIGRATED:      api.ReasonMigrated,
	libvirt.DOMAIN_SHUTOFF_SAVED:         api.ReasonSaved,
	libvirt.DOMAIN_SHUTOFF_FAILED:        api.ReasonFailed,
	libvirt.DOMAIN_SHUTOFF_FROM_SNAPSHOT: api.ReasonFromSnapshot,
}

var CrashedReasonTranslationMap = map[libvirt.DomainCrashedReason]api.StateChangeReason{
	libvirt.DOMAIN_CRASHED_UNKNOWN:  api.ReasonUnknown,
	libvirt.DOMAIN_CRASHED_PANICKED: api.ReasonPanicked,
}

var PausedReasonTranslationMap = map[libvirt.DomainPausedReason]api.StateChangeReason{
	libvirt.DOMAIN_PAUSED_UNKNOWN:         api.ReasonPausedUnknown,
	libvirt.DOMAIN_PAUSED_USER:            api.ReasonPausedUser,
	libvirt.DOMAIN_PAUSED_MIGRATION:       api.ReasonPausedMigration,
	libvirt.DOMAIN_PAUSED_SAVE:            api.ReasonPausedSave,
	libvirt.DOMAIN_PAUSED_DUMP:            api.ReasonPausedDump,
	libvirt.DOMAIN_PAUSED_IOERROR:         api.ReasonPausedIOError,
	libvirt.DOMAIN_PAUSED_WATCHDOG:        api.ReasonPausedWatchdog,
	libvirt.DOMAIN_PAUSED_FROM_SNAPSHOT:   api.ReasonPausedFromSnapshot,
	libvirt.DOMAIN_PAUSED_SHUTTING_DOWN:   api.ReasonPausedShuttingDown,
	libvirt.DOMAIN_PAUSED_SNAPSHOT:        api.ReasonPausedSnapshot,
	libvirt.DOMAIN_PAUSED_CRASHED:         api.ReasonPausedCrashed,
	libvirt.DOMAIN_PAUSED_STARTING_UP:     api.ReasonPausedStartingUp,
	libvirt.DOMAIN_PAUSED_POSTCOPY:        api.ReasonPausedPostcopy,
	libvirt.DOMAIN_PAUSED_POSTCOPY_FAILED: api.ReasonPausedPostcopyFailed,
}

func ConvState(status libvirt.DomainState) api.LifeCycle {
	return LifeCycleTranslationMap[status]
}

func ConvReason(status libvirt.DomainState, reason int) api.StateChangeReason {
	switch status {
	case libvirt.DOMAIN_SHUTDOWN:
		return ShutdownReasonTranslationMap[libvirt.DomainShutdownReason(reason)]
	case libvirt.DOMAIN_SHUTOFF:
		return ShutoffReasonTranslationMap[libvirt.DomainShutoffReason(reason)]
	case libvirt.DOMAIN_CRASHED:
		return CrashedReasonTranslationMap[libvirt.DomainCrashedReason(reason)]
	case libvirt.DOMAIN_PAUSED:
		return PausedReasonTranslationMap[libvirt.DomainPausedReason(reason)]
	default:
		return api.ReasonUnknown
	}
}

func SetupLibvirt(logger hclog.Logger) error {

	// TODO: setting permissions and owners is not part of device plugins.
	// Configure these manually right now on "/dev/kvm"
	stats, err := os.Stat("/dev/kvm")
	if err == nil {
		s, ok := stats.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("can't convert file stats to unix/linux stats")
		}
		err := os.Chown("/dev/kvm", int(s.Uid), 107)
		if err != nil {
			return err
		}
		err = os.Chmod("/dev/kvm", 0666)
		if err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	qemuConf, err := os.OpenFile("/etc/libvirt/qemu.conf", os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer qemuConf.Close()
	// // We are in a container, don't try to stuff qemu inside special cgroups
	// _, err = qemuConf.WriteString("cgroup_controllers = [ ]\n")
	// if err != nil {
	// 	return err
	// }

	// If hugepages exist, tell libvirt about them
	_, err = os.Stat("/dev/hugepages")
	if err == nil {
		_, err = qemuConf.WriteString("hugetlbfs_mount = \"/dev/hugepages\"\n")
	} else if !os.IsNotExist(err) {
		return err
	}

	// Let libvirt log to stderr
	libvirtConf, err := os.OpenFile("/etc/libvirt/libvirtd.conf", os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer libvirtConf.Close()
	_, err = libvirtConf.WriteString("log_outputs = \"1:stderr\"\n")
	if err != nil {
		return err
	}

	return nil
}

func StartLibvirt(ctx context.Context, logger hclog.Logger) {
	// we spawn libvirt from virt-launcher in order to ensure the libvirtd+qemu process
	// doesn't exit until virt-launcher is ready for it to. Virt-launcher traps signals
	// to perform special shutdown logic. These processes need to live in the same
	// container.

	go func() {
		for {
			exitChan := make(chan struct{})
			cmd := exec.Command("/usr/sbin/libvirtd")

			// connect libvirt's stderr to our own stdout in order to see the logs in the container logs
			reader, err := cmd.StderrPipe()
			if err != nil {
				logger.Error("failed to setup error pipe for libvirtd", "error", err)
				panic(err)
			}

			go func() {
				scanner := bufio.NewScanner(reader)
				for scanner.Scan() {
					logger.Error("libvirtd", "err"+scanner.Text())
				}

				if err := scanner.Err(); err != nil {
					logger.Error("failed to read libvirt", "err", err)
				}
			}()

			err = cmd.Start()
			if err != nil {
				logger.Error("failed to start libvirtd", "error", err)
				panic(err)
			}

			go func() {
				defer close(exitChan)
				cmd.Wait()
			}()

			select {
			case <-ctx.Done():
				logger.Error("context done, killing libvirtd")
				cmd.Process.Kill()
				return
			case <-exitChan:
				logger.Error("libvirtd exited, restarting")
			}

			// this sleep is to avoid consumming all resources in the
			// event of a libvirtd crash loop.
			time.Sleep(time.Second)
		}
	}()
}

func StartVirtlog(ctx context.Context) {
	go func() {
		for {
			var args []string
			args = append(args, "-f")
			args = append(args, "/etc/libvirt/virtlogd.conf")
			cmd := exec.Command("/usr/sbin/virtlogd", args...)

			exitChan := make(chan struct{})

			err := cmd.Start()
			if err != nil {
				fmt.Printf("failed to start virtlogd: %v\n", err)
				panic(err)
			}

			go func() {
				defer close(exitChan)
				cmd.Wait()
			}()

			select {
			case <-ctx.Done():
				fmt.Println("context done, killing virlogd")
				cmd.Process.Kill()
				return
			case <-exitChan:
				fmt.Println("virlogd exited, restarting")
			}

			// this sleep is to avoid consumming all resources in the
			// event of a libvirtd crash loop.
			time.Sleep(time.Second)
		}
	}()
}

func SetDomainSpecStr(virConn cli.Connection, wantedSpec string) (cli.VirDomain, error) {
	dom, err := virConn.DomainDefineXML(wantedSpec)
	if err != nil {
		fmt.Println("DomainDefineXML  failed.")
		return nil, err
	}
	return dom, nil
}

func CreateLibvirtConnection() (cli.Connection, error) {
	libvirtUri := "qemu:///system"
	domainConn, err := cli.NewConnection(libvirtUri, "", "", 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("error making libvirtd connection: %v", err)
	}

	return domainConn, nil
}
