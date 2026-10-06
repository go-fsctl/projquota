// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package projquota

import (
	"testing"
	"unsafe"
)

// The C layouts below are computed by hand from the uapi headers quoted in
// abi.go; every field of all three structs is naturally aligned, so the C
// offsets are the running sums.

func TestStructSizes(t *testing.T) {
	for _, c := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"struct fsxattr", abiSizeofFsxattr, 28},
		// 72 is the C size on every 64-bit ABI and on 32-bit ARM EABI; i386
		// is 68, and a buffer larger than the kernel's copy is harmless.
		{"struct if_dqblk", abiSizeofIfDqblk, 72},
		{"struct fs_disk_quota", abiSizeofFsDiskQuota, 112},
	} {
		if c.got != c.want {
			t.Errorf("sizeof(%s) = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestFsxattrOffsets(t *testing.T) {
	var fa fsxattr
	for _, c := range []struct {
		name      string
		got, want uintptr
	}{
		{"fsx_xflags", unsafe.Offsetof(fa.Xflags), 0},
		{"fsx_extsize", unsafe.Offsetof(fa.Extsize), 4},
		{"fsx_nextents", unsafe.Offsetof(fa.Nextents), 8},
		{"fsx_projid", unsafe.Offsetof(fa.Projid), 12},
		{"fsx_cowextsize", unsafe.Offsetof(fa.Cowextsize), 16},
		{"fsx_pad", unsafe.Offsetof(fa.Pad), 20},
	} {
		if c.got != c.want {
			t.Errorf("offsetof(%s) = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestIfDqblkOffsets(t *testing.T) {
	var d ifDqblk
	for _, c := range []struct {
		name      string
		got, want uintptr
	}{
		{"dqb_bhardlimit", unsafe.Offsetof(d.Bhardlimit), 0},
		{"dqb_bsoftlimit", unsafe.Offsetof(d.Bsoftlimit), 8},
		{"dqb_curspace", unsafe.Offsetof(d.Curspace), 16},
		{"dqb_ihardlimit", unsafe.Offsetof(d.Ihardlimit), 24},
		{"dqb_isoftlimit", unsafe.Offsetof(d.Isoftlimit), 32},
		{"dqb_curinodes", unsafe.Offsetof(d.Curinodes), 40},
		{"dqb_btime", unsafe.Offsetof(d.Btime), 48},
		{"dqb_itime", unsafe.Offsetof(d.Itime), 56},
		{"dqb_valid", unsafe.Offsetof(d.Valid), 64},
	} {
		if c.got != c.want {
			t.Errorf("offsetof(%s) = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestFsDiskQuotaOffsets(t *testing.T) {
	var d fsDiskQuota
	for _, c := range []struct {
		name      string
		got, want uintptr
	}{
		{"d_version", unsafe.Offsetof(d.Version), 0},
		{"d_flags", unsafe.Offsetof(d.Flags), 1},
		{"d_fieldmask", unsafe.Offsetof(d.Fieldmask), 2},
		{"d_id", unsafe.Offsetof(d.ID), 4},
		{"d_blk_hardlimit", unsafe.Offsetof(d.BlkHardlimit), 8},
		{"d_blk_softlimit", unsafe.Offsetof(d.BlkSoftlimit), 16},
		{"d_ino_hardlimit", unsafe.Offsetof(d.InoHardlimit), 24},
		{"d_ino_softlimit", unsafe.Offsetof(d.InoSoftlimit), 32},
		{"d_bcount", unsafe.Offsetof(d.Bcount), 40},
		{"d_icount", unsafe.Offsetof(d.Icount), 48},
		{"d_itimer", unsafe.Offsetof(d.Itimer), 56},
		{"d_btimer", unsafe.Offsetof(d.Btimer), 60},
		{"d_iwarns", unsafe.Offsetof(d.Iwarns), 64},
		{"d_bwarns", unsafe.Offsetof(d.Bwarns), 66},
		{"d_itimer_hi", unsafe.Offsetof(d.ItimerHi), 68},
		{"d_btimer_hi", unsafe.Offsetof(d.BtimerHi), 69},
		{"d_rtbtimer_hi", unsafe.Offsetof(d.RtbtimerHi), 70},
		{"d_padding2", unsafe.Offsetof(d.Padding2), 71},
		{"d_rtb_hardlimit", unsafe.Offsetof(d.RtbHardlimit), 72},
		{"d_rtb_softlimit", unsafe.Offsetof(d.RtbSoftlimit), 80},
		{"d_rtbcount", unsafe.Offsetof(d.Rtbcount), 88},
		{"d_rtbtimer", unsafe.Offsetof(d.Rtbtimer), 96},
		{"d_rtbwarns", unsafe.Offsetof(d.Rtbwarns), 100},
		{"d_padding3", unsafe.Offsetof(d.Padding3), 102},
		{"d_padding4", unsafe.Offsetof(d.Padding4), 104},
	} {
		if c.got != c.want {
			t.Errorf("offsetof(%s) = %d, want %d", c.name, c.got, c.want)
		}
	}
}

// The expected values are the ones strace prints for xfs_io's
// FS_IOC_FSGETXATTR on x86-64 (0x801c581f) and their powerpc/mips mirror
// image, where _IOC_READ and _IOC_WRITE sit one bit lower and swap order.
func TestIoctlNumbers(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want uintptr
	}{
		{"generic FS_IOC_FSGETXATTR", iocGeneric.fsIocFsGetXattr(), 0x801c581f},
		{"generic FS_IOC_FSSETXATTR", iocGeneric.fsIocFsSetXattr(), 0x401c5820},
		{"powerpc/mips FS_IOC_FSGETXATTR", iocPowerMIPS.fsIocFsGetXattr(), 0x401c581f},
		{"powerpc/mips FS_IOC_FSSETXATTR", iocPowerMIPS.fsIocFsSetXattr(), 0x801c5820},
	} {
		if c.got != c.want {
			t.Errorf("%s = %#x, want %#x", c.name, c.got, c.want)
		}
	}
}

func TestQuotactlCommands(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want uint32
	}{
		{"QCMD(Q_GETQUOTA, PRJQUOTA)", qcmd(qGetQuota, prjQuota), 0x80000702},
		{"QCMD(Q_SETQUOTA, PRJQUOTA)", qcmd(qSetQuota, prjQuota), 0x80000802},
		{"QCMD(Q_XGETQUOTA, PRJQUOTA)", qcmd(qXGetQuota, prjQuota), 0x580302},
		{"QCMD(Q_XSETQLIM, PRJQUOTA)", qcmd(qXSetQLim, prjQuota), 0x580402},
		{"QCMD masks the type", qcmd(qGetQuota, 0x102), 0x80000702},
	} {
		if c.got != c.want {
			t.Errorf("%s = %#x, want %#x", c.name, c.got, c.want)
		}
	}
}
