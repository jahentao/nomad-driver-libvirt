package util

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

var LibvirtVersionRegex = regexp.MustCompile(`\d+\.\d+\.\d+`)

func RequireLibvirt(t *testing.T) {
	bin := "virsh"
	outBytes, err := exec.Command(bin, "--version").Output()
	if err != nil {
		t.Skip("skipping, Libvirt not present")
	}
	out := strings.TrimSpace(string(outBytes))

	matches := LibvirtVersionRegex.FindStringSubmatch(out)
	if len(matches) != 1 {
		t.Skip("skipping, Libvirt not present")
	}
}

