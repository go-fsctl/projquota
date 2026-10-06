// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux && (ppc64 || ppc64le || mips || mipsle || mips64 || mips64le)

package projquota

// archIOC is the powerpc/mips _IOC encoding (arch/{powerpc,mips}/include/
// uapi/asm/ioctl.h: _IOC_SIZEBITS 13, _IOC_READ 2U, _IOC_WRITE 4U), where
// _IOR puts 0x40000000 in the top bits and _IOW 0x80000000 -- the reverse of
// asm-generic. ioc_linux_test.go checks it against golang.org/x/sys/unix's
// own per-architecture FS_IOC_GETFLAGS / FS_IOC_SETFLAGS.
var archIOC = iocPowerMIPS
