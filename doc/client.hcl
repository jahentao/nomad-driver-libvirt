region = "zhejiang"
datacenter = "dc-01"
log_level = "DEBUG"
data_dir = "/tmp/client1"
# contains a complied nomad-driver-libvirt binary
plugin_dir = "/opt/nomad/data/plugins"
client{
  enabled = true
  servers = ["localhost:4647"]
}
plugin "libvirt" {
  config {
    # libvirt Hypervisor drivers reference https://libvirt.org/drivers.html#hypervisor
    # nomad-driver-libvirt support only qemu/kvm hypervisor at present
    hypervisor = "qemu"
    # and only develop and test local libvirt connection
    uri = "qemu:///system"
  }
}
ports{
  http = 5656
}