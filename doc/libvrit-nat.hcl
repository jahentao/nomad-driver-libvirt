job "centosjob" {
  region = "zhejiang"
  datacenters = ["dc-01"]

  type = "batch"
  group "centosgroup" {
    count = 1
    task "centostask" {
      driver = "libvirt"

      config {
        name = "centostask"
        memory {
          value = 1
          unit = "Gib"
        }
        vcpu = 1
        disks = [
          {
            device = "disk"
            type = "file"
            source = "/var/lib/libvirt/images/centos.qcow2"
            target_bus = "virtio"
          }
        ]
        machine = "pc"
        interfaces = [
          {
            name = "eth0"
            model = "virtio"
            interface_binding_method = "Network"
            source_name = "default"
          }
        ]
      }
    }
  }
}
