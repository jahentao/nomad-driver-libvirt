package main

import (
	"github.com/jahentao/nomad-driver-libvirt/libvirt"

	log "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/plugins"
)

func main() {
	// Serve the plugin
	plugins.Serve(factory)
}

// factory returns a new instance of the Nomad libvirt driver plugin
func factory(log log.Logger) interface{} {
	return libvirt.NewLibvirtDriver(log)
}
