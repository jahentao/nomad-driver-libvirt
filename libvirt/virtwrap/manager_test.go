package virtwrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"gitlab.com/smartding/tlibvirt/virtwrap/api"
	"gitlab.com/smartding/tlibvirt/virtwrap/util"
)

func TestSyncVM(t *testing.T) {
	ctx, _ := context.WithCancel(context.Background())
	//setup libvirt domain manager
	err := util.SetupLibvirt()
	if err != nil {
		panic(err)
	}
	util.StartLibvirt(ctx)
	if err != nil {
		panic(err)
	}
	util.StartVirtlog(ctx)

	domainConn := util.CreateLibvirtConnection()
	defer domainConn.Close()

	domainManager, err := NewLibvirtDomainManager(domainConn)

	taskCfg := readJsonFile("task.json")
	if _, err := domainManager.SyncVM(taskCfg); err != nil {
		fmt.Printf("err = %+v\n", err)
	}

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
