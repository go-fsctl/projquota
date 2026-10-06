// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux && !(ppc64 || ppc64le || mips || mipsle || mips64 || mips64le)

package projquota

// archIOC is the asm-generic _IOC encoding (_IOC_SIZEBITS 14, _IOC_WRITE 1,
// _IOC_READ 2) used by every Linux architecture Go targets except powerpc and
// mips. ioc_linux_test.go checks it against golang.org/x/sys/unix's own
// per-architecture FS_IOC_GETFLAGS / FS_IOC_SETFLAGS.
var archIOC = iocGeneric
