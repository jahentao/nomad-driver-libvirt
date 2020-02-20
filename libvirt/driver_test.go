package libvirt

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jahentao/nomad-driver-libvirt/libvirt/virtwrap/api"
	"github.com/jahentao/nomad-driver-libvirt/libvirt/virtwrap/util"

	"github.com/hashicorp/nomad/helper/testlog"
	"github.com/hashicorp/nomad/helper/uuid"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/plugins/drivers"
	dtestutil "github.com/hashicorp/nomad/plugins/drivers/testutils"
	"github.com/hashicorp/nomad/testutil"
	"github.com/stretchr/testify/require"
)

func TestLibvirtDriver_Fingerprint(t *testing.T) {
	t.Parallel()
	util.RequireLibvirt(t)

	require := require.New(t)
	d := NewLibvirtDriver(testlog.HCLogger(t)).(*Driver)
	harness := dtestutil.NewDriverHarness(t, d)

	fingerCh, err := harness.Fingerprint(context.Background())
	require.NoError(err)
	select {
	case finger := <-fingerCh:
		require.Equal(drivers.HealthStateHealthy, finger.Health)
		// TODO The Attributes of finger not set in code refactoring 'ca2ac306' commit
		//require.True(finger.Attributes["driver.libvirt"].GetBool())
		//require.NotEmpty(finger.Attributes["driver.libvirt.version"].GetString())
	case <-time.After(time.Duration(testutil.TestMultiplier()*5) * time.Second):
		require.Fail("timeout receiving fingerprint")
	}
}

var (
	task = &drivers.TaskConfig{
		ID:      uuid.Generate(),
		AllocID: uuid.Generate(),
		Name:    "test",
		Resources: &drivers.Resources{
			NomadResources: &structs.AllocatedTaskResources{
				Memory: structs.AllocatedMemoryResources{
					MemoryMB: 300,
				},
				Cpu: structs.AllocatedCpuResources{
					CpuShares: 1024,
				},
			},
			LinuxResources: &drivers.LinuxResources{
				CPUShares:        1024,
				MemoryLimitBytes: 300 * 1024 * 1024,
			},
		},
	}

	taskCfg = &api.TaskConfig{
		Name: "centostask",
		Memory: api.MemoryConfig{
			Value: 1,
			Unit:  "gib",
		},
		VCPU: 1,
		CPU:  api.CPUConfig{},
		Disks: []api.DiskConfig{
			{
				Device:    "disk",
				Type:      "file",
				Source:    "/pan/iso/centos7.0.qcow2", // should be prepared
				TargetBus: "virtio",
			},
		},
		Machine: "pc",
		Interfaces: []api.InterfaceConfig{
			{
				Name:                   "eth0",
				Model:                  "virtio",
				InterfaceBindingMethod: "Network",
				SourceName:             "default",
			},
		},
	}
)


func TestLibvirtDriver_Start_Stop_Destroy_Task(t *testing.T) {
	util.RequireLibvirt(t)

	assert := require.New(t)

	// prepare test case

	d := NewLibvirtDriver(testlog.HCLogger(t)).(*Driver)
	harness := dtestutil.NewDriverHarness(t, d)

	assert.NoError(task.EncodeConcreteDriverConfig(&taskCfg))

	cleanup := harness.MkAllocDir(task, false)
	defer cleanup()

	// Start task
	handle, network, err := harness.StartTask(task)
	assert.NoError(err)
	assert.NotNil(network)
	assert.NotEmpty(network.IP)
	assert.NotNil(handle)

	//libvirtHandle, ok := d.tasks.Get(task.ID)
	_, ok := d.tasks.Get(task.ID)
	assert.True(ok)

	// TODO test other features that libvirt driver support, such as vnc graphics

	testutil.WaitForResult(func() (bool, error) {
		status, err := d.InspectTask(task.ID)
		assert.NoError(err)
		if status.State == drivers.TaskStateRunning {
			return true, nil
		}
		return false, fmt.Errorf("task in state: %v", status.State)
	}, func(err error) {
		t.Fatalf("task failed to start: %v", err)
	})

	//Test that killing vm marks task as stopped
	assert.NoError(d.StopTask(task.ID, 5*time.Second, "kill"))
	// Destroy the task/vm after test
	defer d.DestroyTask(task.ID, false)

	testutil.WaitForResult(func() (bool, error) {
		status, err := d.InspectTask(task.ID)
		assert.NoError(err)
		if status.State == drivers.TaskStateExited {
			return true, nil
		}
		return false, fmt.Errorf("task in state: %v", status.State)
	}, func(err error) {
		t.Fatalf("task was not marked as stopped: %v", err)
	})
}

// TODO simulate task crash and then recover task
func TestLibvirtDriver_Start_Stop_Recover_Task(t *testing.T) {
	util.RequireLibvirt(t)

	require := require.New(t)

	d := NewLibvirtDriver(testlog.HCLogger(t)).(*Driver)
	harness := dtestutil.NewDriverHarness(t, d)

	require.NoError(task.EncodeConcreteDriverConfig(&taskCfg))

	cleanup := harness.MkAllocDir(task, false)
	defer cleanup()

	// Test start task
	handle, _, err := harness.StartTask(task)
	require.NoError(err)
	require.NotNil(handle)

	libvirtHandle, ok := d.tasks.Get(task.ID)
	require.NotNil(libvirtHandle)
	require.True(ok)

	testutil.WaitForResult(func() (bool, error) {
		status, err := d.InspectTask(task.ID)
		require.NoError(err)
		if status.State == drivers.TaskStateRunning {
			return true, nil
		}
		return false, fmt.Errorf("task in state: %v", status.State)
	}, func(err error) {
		t.Fatalf("task failed to start: %v", err)
	})

	// Missing the task handle
	d.tasks.Delete(task.ID)

	// Test recover the missed task
	recoverHandle := handle.Copy()
	require.NoError(d.RecoverTask(recoverHandle))

	d.StopTask(task.ID, 5*time.Second, "kill")

	// Destroy the task/vm after test
	defer d.DestroyTask(task.ID, false)

	// Test after recovery and stop task
	testutil.WaitForResult(func() (bool, error) {
		status, err := d.InspectTask(task.ID)
		require.NoError(err)
		if status.State == drivers.TaskStateExited {
			return true, nil
		}
		return false, fmt.Errorf("task in state: %v", status.State)
	}, func(err error) {
		t.Fatalf("task failed to stop: %v", err)
	})
}


