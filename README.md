[TOC]

# Complie driver

不同于[nomad-skeleton-driver-plugin][1]使用Go Module（要求golang 1.12.x），本项目使用vendor进行库依赖管理。

first install golang `1.12.x` 

```shell script
# download
go get -u -v jahentao/nomad-driver-libvirt

# build
cd $GOPATH/src/github.com/jahentao/nomad-driver-libvirt/
./scripts/build.sh

# driver will display in pkg directory
```

# Usage

## prepare

- [install nomad] `v0.9+` 
- [install libvirt] `4.0+`,
- prepare an vm image, [install] or [download & config]

 example configuration files are in [doc](./doc) directory.

```shell script
cd $GOPATH/src/github.com/jahentao/nomad-driver-libvirt/doc

# setup nomad server and client
nohup /usr/bin/nomad agent -config server.hcl > server.out &
nohup /usr/bin/nomad agent -config client.hcl > client.out &
```

here, there is an centos image in `/var/lib/libvirt/images/centos.qcow2`, with username `centos` and password `123456`

## network mode

run in NAT network mode:
```shell script
/usr/bin/nomad run libvrit-nat.hcl
```

run in bridge network mode:
```shell script
# setup bridge network

# run
/usr/bin/nomad run libvrit-nat.hcl
```


[1]: https://github.com/hashicorp/nomad-skeleton-driver-plugin