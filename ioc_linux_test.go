// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package projquota

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// TestArchIOCMatchesXSys checks this architecture's _IOC encoding against
// golang.org/x/sys/unix, which generates FS_IOC_GETFLAGS (_IOR('f', 1, long))
// and FS_IOC_SETFLAGS (_IOW('f', 2, long)) per architecture from the kernel
// headers. If ioc_*_linux.go picked the wrong family for GOARCH, the
// direction bits would disagree here.
func TestArchIOCMatchesXSys(t *testing.T) {
	long := unsafe.Sizeof(uintptr(0))
	if got, want := archIOC.ior('f', 1, long), uintptr(unix.FS_IOC_GETFLAGS); got != want {
		t.Errorf("%s: _IOR('f', 1, long) = %#x, x/sys FS_IOC_GETFLAGS = %#x", runtime.GOARCH, got, want)
	}
	if got, want := archIOC.iow('f', 2, long), uintptr(unix.FS_IOC_SETFLAGS); got != want {
		t.Errorf("%s: _IOW('f', 2, long) = %#x, x/sys FS_IOC_SETFLAGS = %#x", runtime.GOARCH, got, want)
	}
	switch runtime.GOARCH {
	case "ppc64", "ppc64le", "mips", "mipsle", "mips64", "mips64le":
		if archIOC != iocPowerMIPS {
			t.Errorf("%s uses %+v, want the powerpc/mips layout", runtime.GOARCH, archIOC)
		}
	default:
		if archIOC != iocGeneric {
			t.Errorf("%s uses %+v, want the asm-generic layout", runtime.GOARCH, archIOC)
		}
	}
}
