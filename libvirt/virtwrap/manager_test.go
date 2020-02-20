package virtwrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hashicorp/nomad/client/testutil"
	"github.com/hashicorp/nomad/helper/uuid"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/jahentao/nomad-driver-libvirt/libvirt/virtwrap/util"
	"github.com/stretchr/testify/require"
	"io/ioutil"
	"os"
	"testing"

	"github.com/jahentao/nomad-driver-libvirt/libvirt/virtwrap/api"
)

func TestManageLifecycle(t *testing.T) {
	testutil.RequireRoot(t)

	ctx, _ := context.WithCancel(context.Background())
	logger := util.GetTestLogger()
	require := require.New(t)

	myTaskCfg := &api.TaskConfig{
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
		Machine: "pc-i440fx-cosmic",
		Interfaces: []api.InterfaceConfig{
			{
				Name:                   "eth0",
				Model:                  "virtio",
				InterfaceBindingMethod: "Network",
				SourceName:             "default",
			},
		},
	}

	driverTaskConfig := &drivers.TaskConfig{
		ID:            uuid.Generate(),
		JobName:       "centosjob",
		TaskGroupName: "centosgroup",
		Name:          "centostask",
		Resources: &drivers.Resources{
			NomadResources: &structs.AllocatedTaskResources{
				Cpu:    structs.AllocatedCpuResources{CpuShares: 100},
				Memory: structs.AllocatedMemoryResources{MemoryMB: 300},
			},
			LinuxResources: &drivers.LinuxResources{
				CPUShares:        100,
				MemoryLimitBytes: 314572800,
			},
		},
		AllocID: uuid.Generate(),
	}

	// 1. setup libvirt domain manager
	err := util.SetupLibvirt(logger)
	if err != nil {
		panic(err)
	}
	// the libvirtd.service should be running
	util.StartLibvirt(ctx, logger)

	// 2. create connection
	domainConn, err := util.CreateLibvirtConnection()
	if domainConn == nil || err != nil {
		panic(err)
	}
	defer domainConn.Close()

	domainManager, _ := NewLibvirtDomainManager(domainConn, logger)

	myTaskCfg.ToLower()

	// 3. create kvm instance
	domainSpec, err := domainManager.SyncVM(driverTaskConfig, myTaskCfg)
	if err != nil {
		t.Fatalf("error define the kvm instance: %v", err)
	}

	// 4. judge vm domain is running
	state, err := domainManager.VMState(driverTaskConfig.ID)
	require.NoError(err)
	require.Equal(api.Running, state)

	// 5. stop and destroy domain after test
	err = domainManager.KillVM(domainSpec.Name)
	if err != nil {
		t.Fatalf("error stop the kvm instance: %v", err)
	}
	err = domainManager.DestroyVM(domainSpec.Name)
	if err != nil {
		t.Fatalf("error undefine the kvm instance: %v", err)
	}

}

func TestSyncVM(t *testing.T) {
	//ctx, _ := context.WithCancel(context.Background())
	////setup libvirt domain manager
	//err := util.SetupLibvirt()
	//if err != nil {
	//	panic(err)
	//}
	//util.StartLibvirt(ctx)
	//if err != nil {
	//	panic(err)
	//}
	//util.StartVirtlog(ctx)
	//
	//domainConn := util.CreateLibvirtConnection()
	//defer domainConn.Close()
	//
	//domainManager, err := NewLibvirtDomainManager(domainConn)
	//
	//taskCfg := readJsonFile("task.json")
	//if _, err := domainManager.SyncVM(taskCfg); err != nil {
	//	fmt.Printf("err = %+v\n", err)
	//}

}

func readJsonFile(path string) *api.TaskConfig {
	jsonFile, err := os.Open(path)
	// if we os.Open returns an error then handle it
	if err != nil {
		fmt.Println(err)
	}

	fmt.Printf("Successfully Opened %s\n", path)
	// defer the closing of our jsonFile so that we can parse it later on
	defer jsonFile.Close()

	// read our opened xmlFile as a byte array.
	byteValue, _ := ioutil.ReadAll(jsonFile)

	// we initialize our Users array
	var taskCfg api.TaskConfig

	// we unmarshal our byteArray which contains our
	// jsonFile's content into 'users' which we defined above
	if err := json.Unmarshal(byteValue, &taskCfg); err != nil {
		fmt.Printf("error unmarshaling json file, %v\n", err)
	}

	if b, err := json.MarshalIndent(taskCfg, "", "  "); err == nil {
		os.Stdout.Write(b)
	}
	return &taskCfg
}
