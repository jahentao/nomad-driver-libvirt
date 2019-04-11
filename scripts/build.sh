#!/bin/bash

set -o errexit

build_locally() {
  DEST="pkg/linux_amd64"
  PLUGDIR="/opt/nomad/data/plugins"
  NAME="nomad-driver-libvirt"

  mkdir -p "${DEST}"
  echo "===> Building libvirt driver binary"
  echo
  go build -o "${DEST}/${NAME}" .

  echo "binary is present in ${DEST}/${NAME}"

  cp "${DEST}/${NAME}" "${PLUGDIR}/"
  echo
  echo "binary is also copied to ${PLUGDIR}/${NAME}"
}

case "${OSTYPE}" in
  darwin*) ./scripts/build-in-docker.sh ;;
  linux*) build_locally ;;
  *)
    echo "${OSTYPE} is not supported"
    exit 1
    ;;
esac
