// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package projquota

import "unsafe"

// This file holds the kernel ABI: ioctl and quotactl command numbers, the
// three structs that cross the user/kernel boundary, and their sizes. It has
// no build tag so the derivations can be unit-tested on any host; the
// syscalls themselves live in projquota_linux.go.
//
// Every value below is transcribed from torvalds/linux master:
//
//	include/uapi/linux/fs.h          struct fsxattr, FS_IOC_FS{GET,SET}XATTR,
//	                                 FS_XFLAG_PROJINHERIT
//	include/uapi/linux/quota.h       QCMD, PRJQUOTA, Q_GETQUOTA, Q_SETQUOTA,
//	                                 struct if_dqblk, QIF_*
//	include/uapi/linux/dqblk_xfs.h   XQM_CMD, Q_XGETQUOTA, Q_XSETQLIM,
//	                                 struct fs_disk_quota, FS_DQ_*, FS_PROJ_QUOTA
//	include/uapi/linux/magic.h       XFS_SUPER_MAGIC, EXT4_SUPER_MAGIC
//	include/uapi/asm-generic/ioctl.h and arch/{powerpc,mips,sparc}/include/
//	                                 uapi/asm/ioctl.h for the _IOR/_IOW encoding

// ---------------------------------------------------------------------------
// ioctl encoding.
//
// asm-generic/ioctl.h:
//
//	#define _IOC(dir,type,nr,size) \
//		(((dir)  << _IOC_DIRSHIFT) | ((type) << _IOC_TYPESHIFT) | \
//		 ((nr)   << _IOC_NRSHIFT) | ((size) << _IOC_SIZESHIFT))
//
// with _IOC_NRBITS 8 and _IOC_TYPEBITS 8, so the size always starts at bit
// 16. What differs per architecture is the width of the size field and the
// direction bits:
//
//	asm-generic:          _IOC_SIZEBITS 14, _IOC_DIRBITS 2, _IOC_WRITE 1U, _IOC_READ 2U
//	powerpc, mips, sparc: _IOC_SIZEBITS 13, _IOC_DIRBITS 3, _IOC_WRITE 4U, _IOC_READ 2U
//
// The direction field starts right after the size field: bit 30 (generic) or
// bit 29 (powerpc/mips/sparc).

// iocLayout is one architecture family's _IOC encoding.
type iocLayout struct {
	dirShift uintptr // 16 + _IOC_SIZEBITS
	read     uintptr // _IOC_READ
	write    uintptr // _IOC_WRITE
}

var (
	// iocGeneric is asm-generic/ioctl.h: x86, arm, arm64, riscv64, loong64,
	// s390x.
	iocGeneric = iocLayout{dirShift: 16 + 14, read: 2, write: 1}
	// iocPowerMIPS is arch/powerpc and arch/mips (and sparc, which Go does
	// not target).
	iocPowerMIPS = iocLayout{dirShift: 16 + 13, read: 2, write: 4}
)

// encode is _IOC(dir, typ, nr, size).
func (l iocLayout) encode(dir, typ, nr, size uintptr) uintptr {
	return dir<<l.dirShift | size<<16 | typ<<8 | nr
}

// ior is _IOR(typ, nr, size).
func (l iocLayout) ior(typ, nr, size uintptr) uintptr { return l.encode(l.read, typ, nr, size) }

// iow is _IOW(typ, nr, size).
func (l iocLayout) iow(typ, nr, size uintptr) uintptr { return l.encode(l.write, typ, nr, size) }

// fsIocFsGetXattr is FS_IOC_FSGETXATTR, _IOR('X', 31, struct fsxattr).
func (l iocLayout) fsIocFsGetXattr() uintptr { return l.ior('X', 31, sizeofFsxattr) }

// fsIocFsSetXattr is FS_IOC_FSSETXATTR, _IOW('X', 32, struct fsxattr).
func (l iocLayout) fsIocFsSetXattr() uintptr { return l.iow('X', 32, sizeofFsxattr) }

// ---------------------------------------------------------------------------
// struct fsxattr (include/uapi/linux/fs.h).
//
//	struct fsxattr {
//		__u32		fsx_xflags;	/* xflags field value (get/set) */
//		__u32		fsx_extsize;	/* extsize field value (get/set)*/
//		__u32		fsx_nextents;	/* nextents field value (get)	*/
//		__u32		fsx_projid;	/* project identifier (get/set) */
//		__u32		fsx_cowextsize;	/* CoW extsize field value (get/set)*/
//		unsigned char	fsx_pad[8];
//	};
//
// Only u32 and bytes: 28 bytes with no padding on every architecture.
type fsxattr struct {
	Xflags     uint32
	Extsize    uint32
	Nextents   uint32
	Projid     uint32
	Cowextsize uint32
	Pad        [8]byte
}

// sizeofFsxattr is sizeof(struct fsxattr), the size folded into the ioctl
// numbers.
const sizeofFsxattr = unsafe.Sizeof(fsxattr{})

// fsXflagProjinherit is FS_XFLAG_PROJINHERIT: "create with parents projid".
const fsXflagProjinherit = 0x00000200

// invalidProjectID is INVALID_PROJID ((projid_t)-1). fs/file_attr.c
// fileattr_set_prepare refuses it: "!projid_valid(make_kprojid(&init_user_ns,
// fa->fsx_projid))" returns -EINVAL.
const invalidProjectID = ^uint32(0)

// ---------------------------------------------------------------------------
// quotactl commands (include/uapi/linux/quota.h, dqblk_xfs.h).
//
//	#define SUBCMDSHIFT 8
//	#define QCMD(cmd, type)  (((cmd) << SUBCMDSHIFT) | ((type) & SUBCMDMASK))
//	#define PRJQUOTA  2
//	#define Q_GETQUOTA 0x800007
//	#define Q_SETQUOTA 0x800008
//	#define XQM_CMD(x)	(('X'<<8)+(x))
//	#define Q_XGETQUOTA	XQM_CMD(3)
//	#define Q_XSETQLIM	XQM_CMD(4)
const (
	subCmdShift = 8
	subCmdMask  = 0x00ff

	prjQuota = 2

	qGetQuota  = 0x800007
	qSetQuota  = 0x800008
	qXGetQuota = 'X'<<8 + 3
	qXSetQLim  = 'X'<<8 + 4
)

// qcmd is QCMD(cmd, type).
func qcmd(cmd, typ uint32) uint32 { return cmd<<subCmdShift | typ&subCmdMask }

// ---------------------------------------------------------------------------
// struct if_dqblk (include/uapi/linux/quota.h), used by Q_GETQUOTA/Q_SETQUOTA.
//
//	struct if_dqblk {
//		__u64 dqb_bhardlimit;
//		__u64 dqb_bsoftlimit;
//		__u64 dqb_curspace;
//		__u64 dqb_ihardlimit;
//		__u64 dqb_isoftlimit;
//		__u64 dqb_curinodes;
//		__u64 dqb_btime;
//		__u64 dqb_itime;
//		__u32 dqb_valid;
//	};
//
// Block limits are in QIF_DQBLKSIZE (1 << 10) units; dqb_curspace is in
// bytes (fs/quota/quota.c copy_to_if_dqblk: "dst->dqb_curspace =
// src->d_space" while the limits go through stoqb()).
//
// sizeof is 72 where __u64 is 8-aligned (every 64-bit ABI and 32-bit ARM
// EABI) and 68 on i386. Go aligns uint64 to 4 on all 32-bit targets, so
// without the explicit Pad the Go struct would be 68 bytes on arm and mips
// and the kernel's 72-byte copy_to_user would run 4 bytes past it. With Pad
// it is 72 everywhere: never smaller than the kernel's copy.
type ifDqblk struct {
	Bhardlimit uint64
	Bsoftlimit uint64
	Curspace   uint64
	Ihardlimit uint64
	Isoftlimit uint64
	Curinodes  uint64
	Btime      uint64
	Itime      uint64
	Valid      uint32
	Pad        uint32
}

// QIF_* validity bits.
const (
	qifBlimits = 1 << 0
	qifSpace   = 1 << 1
	qifIlimits = 1 << 2
	qifInodes  = 1 << 3
	qifBtime   = 1 << 4
	qifItime   = 1 << 5
	qifLimits  = qifBlimits | qifIlimits

	// qifDqblkSizeBits is QIF_DQBLKSIZE_BITS: if_dqblk block limits are
	// in 1 KiB units.
	qifDqblkSizeBits = 10
)

// ---------------------------------------------------------------------------
// struct fs_disk_quota (include/uapi/linux/dqblk_xfs.h), used by Q_XGETQUOTA
// and Q_XSETQLIM. "all the blk units are in BBs (Basic Blocks) of 512 bytes."
//
// Every field is naturally aligned and the total is a multiple of 8, so the
// layout is 112 bytes with no implicit padding on every architecture.
type fsDiskQuota struct {
	Version      int8   // d_version
	Flags        int8   // d_flags: FS_{USER,PROJ,GROUP}_QUOTA
	Fieldmask    uint16 // d_fieldmask
	ID           uint32 // d_id
	BlkHardlimit uint64 // d_blk_hardlimit (BB)
	BlkSoftlimit uint64 // d_blk_softlimit (BB)
	InoHardlimit uint64 // d_ino_hardlimit
	InoSoftlimit uint64 // d_ino_softlimit
	Bcount       uint64 // d_bcount (BB)
	Icount       uint64 // d_icount
	Itimer       int32  // d_itimer
	Btimer       int32  // d_btimer
	Iwarns       uint16 // d_iwarns
	Bwarns       uint16 // d_bwarns
	ItimerHi     int8   // d_itimer_hi
	BtimerHi     int8   // d_btimer_hi
	RtbtimerHi   int8   // d_rtbtimer_hi
	Padding2     int8   // d_padding2
	RtbHardlimit uint64 // d_rtb_hardlimit
	RtbSoftlimit uint64 // d_rtb_softlimit
	Rtbcount     uint64 // d_rtbcount
	Rtbtimer     int32  // d_rtbtimer
	Rtbwarns     uint16 // d_rtbwarns
	Padding3     int16  // d_padding3
	Padding4     [8]byte
}

const (
	fsDquotVersion = 1      // FS_DQUOT_VERSION
	fsProjQuota    = 1 << 1 // FS_PROJ_QUOTA
	fsDqIsoft      = 1 << 0 // FS_DQ_ISOFT
	fsDqIhard      = 1 << 1 // FS_DQ_IHARD
	fsDqBsoft      = 1 << 2 // FS_DQ_BSOFT
	fsDqBhard      = 1 << 3 // FS_DQ_BHARD
	fsDqBigtime    = 1 << 15

	// bbShift is XFS_BB_SHIFT: a basic block is 512 bytes.
	bbShift = 9
)

// ---------------------------------------------------------------------------
// Filesystem magic numbers (include/uapi/linux/magic.h).
const (
	xfsSuperMagic  = 0x58465342 // "XFSB"
	ext4SuperMagic = 0xEF53     // shared by ext2/ext3/ext4, all driven by ext4.ko
)

var (
	abiSizeofFsxattr     = unsafe.Sizeof(fsxattr{})
	abiSizeofIfDqblk     = unsafe.Sizeof(ifDqblk{})
	abiSizeofFsDiskQuota = unsafe.Sizeof(fsDiskQuota{})
)
