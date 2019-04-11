package main

import (
	log "github.com/hashicorp/go-hclog"
	"gitlab.com/harmonyedge/nomad-driver-libvirt/libvirt"

	"github.com/hashicorp/nomad/plugins"
)

func main() {
	// Serve the plugin
	plugins.Serve(factory)
}

// factory returns a new instance of the LXC driver plugin
func factory(log log.Logger) interface{} {
	return libvirt.NewLibvirtDriver(log)
}
