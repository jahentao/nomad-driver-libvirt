package util

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/jahentao/nomad-driver-libvirt/libvirt/virtwrap/cli"

	hclog "github.com/hashicorp/go-hclog"
)

var (
	// init once
	initOnce sync.Once
	// test logger
	testLogger hclog.Logger
)

func GetTestLogger() hclog.Logger {
	initOnce.Do(func() {
		testLogger = hclog.New(&hclog.LoggerOptions{
			Name:  "nomad-driver-libvirt-test",
			Level: hclog.LevelFromString("DEBUG"),
		})
	})

	return testLogger
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
				fmt.Errorf("failed to setup error pipe for libvirtd: %v", err)
				panic(err)
			}

			go func() {
				scanner := bufio.NewScanner(reader)
				for scanner.Scan() {
					logger.Info("libvirtd", scanner.Text())
				}

				if err := scanner.Err(); err != nil {
					logger.Error("failed to read libvirt", "err", err)
				}
			}()

			err = cmd.Start()
			if err != nil {
				fmt.Errorf("failed to start libvirtd: %v", err)
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
				fmt.Errorf("failed to start virtlogd: %v", err)
				panic(err)
			}

			go func() {
				defer close(exitChan)
				cmd.Wait()
			}()

			select {
			case <-ctx.Done():
				cmd.Process.Kill()
				return
			case <-exitChan:
			}

			// this sleep is to avoid consumming all resources in the
			// event of a libvirtd crash loop.
			time.Sleep(time.Second)
		}
	}()
}

func CreateLibvirtConnection() (cli.Connection, error) {
	libvirtUri := "qemu:///system"
	domainConn, err := cli.NewConnection(libvirtUri, "", "", 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("error making libvirtd connection: %v", err)
	}

	return domainConn, nil
}
