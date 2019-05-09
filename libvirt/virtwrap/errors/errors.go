package errors

import (
	"errors"
	"fmt"

	libvirt "github.com/libvirt/libvirt-go"
)

var NoTaskIDInDomainMeta = errors.New("no TaskID in domain metadata")
var InvalidFormatDomainMeta = errors.New("invalid format domain metadata")
var EmptyDomainMeta = errors.New("no meta data found in namespace harmonycloud")

func checkError(err error, expectedError libvirt.ErrorNumber) bool {
	libvirtError, ok := err.(libvirt.Error)
	if ok {
		return libvirtError.Code == expectedError
	}

	return false
}

// IsNotFound detects libvirt's ERR_NO_DOMAIN. It accepts both error and libvirt.Error (as returned by GetLastError function).
func IsNotFound(err error) bool {
	return checkError(err, libvirt.ERR_NO_DOMAIN)
}

// IsInvalidOperation detects libvirt's VIR_ERR_OPERATION_INVALID. It accepts both error and libvirt.Error (as returned by GetLastError function).
func IsInvalidOperation(err error) bool {
	return checkError(err, libvirt.ERR_OPERATION_INVALID)
}

// IsOk detects libvirt's ERR_OK. It accepts both error and libvirt.Error (as returned by GetLastError function).
func IsOk(err error) bool {
	return checkError(err, libvirt.ERR_OK)
}

func FormatLibvirtError(err error) string {
	var libvirtError string
	lerr, ok := err.(libvirt.Error)
	if ok {
		libvirtError = fmt.Sprintf("LibvirtError(Code=%d, Domain=%d, Message='%s')",
			lerr.Code, lerr.Domain, lerr.Message)
	}

	return libvirtError
}
