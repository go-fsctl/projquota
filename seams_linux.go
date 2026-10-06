// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package projquota

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Indirection seams over the system calls this package makes. The error
// branches of every call -- EPERM, EOPNOTSUPP, ENOSYS, ESRCH, ENOENT, a
// failing fstatfs -- only happen against particular kernels and mounts, so
// the tests swap these for fault-injecting fakes and cover every branch as a
// normal user. Production code uses the real implementations assigned here;
// the root-only integration tests drive the genuine syscalls on real XFS and
// ext4 mounts.
var (
	osOpenFile  = os.OpenFile
	unixFstatfs = unix.Fstatfs
	unixFstat   = unix.Fstat
	unixOpenat  = unix.Openat
	readDir     = (*os.File).ReadDir

	// doIoctl issues FS_IOC_FSGETXATTR / FS_IOC_FSSETXATTR. The argument
	// stays an unsafe.Pointer until the syscall itself, as the
	// unsafe.Pointer rules require.
	doIoctl = realIoctl

	// doQuotactlFd issues quotactl_fd(2) (Linux >= 5.14).
	doQuotactlFd = realQuotactlFd
)

// realIoctl is ioctl(fd, req, arg).
func realIoctl(fd uintptr, req uintptr, arg unsafe.Pointer) unix.Errno {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, req, uintptr(arg))
	return errno
}

// realQuotactlFd is quotactl_fd(fd, cmd, id, addr).
func realQuotactlFd(fd uintptr, cmd uint32, id uint32, addr unsafe.Pointer) unix.Errno {
	_, _, errno := unix.Syscall6(unix.SYS_QUOTACTL_FD, fd, uintptr(cmd), uintptr(id), uintptr(addr), 0, 0)
	return errno
}
