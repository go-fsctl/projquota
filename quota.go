// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package projquota

import (
	"errors"
	"fmt"
	"time"
)

// Filesystem is a filesystem this package knows how to drive.
type Filesystem int

const (
	// XFS is driven with the XFS quota ABI (Q_XGETQUOTA/Q_XSETQLIM,
	// struct fs_disk_quota, 512-byte basic blocks).
	XFS Filesystem = iota + 1
	// Ext4 is driven with the generic VFS quota ABI (Q_GETQUOTA/Q_SETQUOTA,
	// struct if_dqblk, 1 KiB block-limit units, usage in bytes). The magic
	// is shared by ext2 and ext3 mounts, which the ext4 driver also serves;
	// only a filesystem made with the project feature accepts project ids.
	Ext4
)

// String returns "xfs" or "ext4".
func (f Filesystem) String() string {
	switch f {
	case XFS:
		return "xfs"
	case Ext4:
		return "ext4"
	}
	return fmt.Sprintf("Filesystem(%d)", int(f))
}

var (
	// ErrUnsupported is returned by every kernel operation off Linux.
	ErrUnsupported = errors.New("projquota: project quotas are only supported on Linux")

	// ErrUnsupportedFilesystem is returned (wrapped, with the statfs magic)
	// for a filesystem that is neither XFS nor ext4.
	ErrUnsupportedFilesystem = errors.New("projquota: filesystem is neither XFS nor ext4")

	// ErrInvalidProject is returned for project id 4294967295, which the
	// kernel reserves as INVALID_PROJID, and by SetLimits for project id 0:
	// on XFS the limits of id 0 are the DEFAULT limits of every project
	// (fs/xfs/xfs_qm_syscalls.c xfs_qm_scall_setqlim: "qlim = id == 0 ?
	// &defq->blk : NULL"), which is never what a per-directory limit means.
	ErrInvalidProject = errors.New("projquota: invalid project id")

	// ErrSoftAboveHard is returned when a non-zero hard limit is below its
	// soft limit. XFS does not report this case: xfs_setqlim_limits only
	// logs "hard < soft" and leaves the old limits in place while the
	// syscall returns success. The check is made here instead.
	ErrSoftAboveHard = errors.New("projquota: soft limit above hard limit")
)

// Limits are the limits of one project. Zero means "no limit".
//
// Block limits are in BYTES. The kernel ABI does not carry bytes, so they
// are rounded UP to the ABI unit: 512 bytes on XFS, 1 KiB on ext4. XFS then
// rounds up again to its filesystem block (XFS_B_TO_FSB). Read the limits
// back with Usage to see what the filesystem actually stored.
type Limits struct {
	BlockSoft uint64 // bytes
	BlockHard uint64 // bytes
	InodeSoft uint64
	InodeHard uint64
}

// validate rejects a non-zero hard limit below its soft limit.
func (l Limits) validate() error {
	if l.BlockHard != 0 && l.BlockSoft > l.BlockHard {
		return fmt.Errorf("%w: blocks soft %d > hard %d", ErrSoftAboveHard, l.BlockSoft, l.BlockHard)
	}
	if l.InodeHard != 0 && l.InodeSoft > l.InodeHard {
		return fmt.Errorf("%w: inodes soft %d > hard %d", ErrSoftAboveHard, l.InodeSoft, l.InodeHard)
	}
	return nil
}

// Quota is the usage and the limits of one project.
type Quota struct {
	Limits

	// Bytes is the space charged to the project. On ext4 it is exact; on
	// XFS the ABI reports 512-byte basic blocks, so it is a multiple of 512.
	Bytes uint64
	// Inodes is the number of inodes charged to the project.
	Inodes uint64

	// BlockGrace and InodeGrace are when the soft-limit grace period
	// expires; zero when the project is not over its soft limit.
	BlockGrace time.Time
	InodeGrace time.Time
}

// ceilShift returns ceil(v / 2^shift) without overflowing near 2^64.
func ceilShift(v uint64, shift uint) uint64 {
	q := v >> shift
	if v&(1<<shift-1) != 0 {
		q++
	}
	return q
}

// unixTime turns a kernel timer (seconds since the epoch, 0 = not running)
// into a time.Time (zero when not running).
func unixTime(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// xfsSetLimits builds the Q_XSETQLIM argument: the four limits, rounded up to
// 512-byte basic blocks the way the kernel's own quota_btobb does.
func xfsSetLimits(id uint32, l Limits) fsDiskQuota {
	return fsDiskQuota{
		Version:      fsDquotVersion,
		Flags:        fsProjQuota,
		Fieldmask:    fsDqIsoft | fsDqIhard | fsDqBsoft | fsDqBhard,
		ID:           id,
		BlkHardlimit: ceilShift(l.BlockHard, bbShift),
		BlkSoftlimit: ceilShift(l.BlockSoft, bbShift),
		InoHardlimit: l.InodeHard,
		InoSoftlimit: l.InodeSoft,
	}
}

// xfsTimer decodes a d_*timer/_hi pair: fs/quota/quota.c
// copy_from_xfs_dqblk_ts, "if (d->d_fieldmask & FS_DQ_BIGTIME) return
// (u32)timer | (s64)timer_hi << 32; return timer;".
func xfsTimer(d *fsDiskQuota, lo int32, hi int8) int64 {
	if d.Fieldmask&fsDqBigtime != 0 {
		return int64(uint32(lo)) | int64(hi)<<32
	}
	return int64(lo)
}

// xfsQuota decodes a Q_XGETQUOTA result.
func xfsQuota(d *fsDiskQuota) Quota {
	return Quota{
		Limits: Limits{
			BlockSoft: d.BlkSoftlimit << bbShift,
			BlockHard: d.BlkHardlimit << bbShift,
			InodeSoft: d.InoSoftlimit,
			InodeHard: d.InoHardlimit,
		},
		Bytes:      d.Bcount << bbShift,
		Inodes:     d.Icount,
		BlockGrace: unixTime(xfsTimer(d, d.Btimer, d.BtimerHi)),
		InodeGrace: unixTime(xfsTimer(d, d.Itimer, d.ItimerHi)),
	}
}

// genericSetLimits builds the Q_SETQUOTA argument. Only QIF_LIMITS is marked
// valid, so usage and timers are left alone (copy_from_if_dqblk maps
// QIF_BLIMITS to QC_SPC_SOFT|QC_SPC_HARD and QIF_ILIMITS to
// QC_INO_SOFT|QC_INO_HARD, nothing else).
func genericSetLimits(l Limits) ifDqblk {
	return ifDqblk{
		Bhardlimit: ceilShift(l.BlockHard, qifDqblkSizeBits),
		Bsoftlimit: ceilShift(l.BlockSoft, qifDqblkSizeBits),
		Ihardlimit: l.InodeHard,
		Isoftlimit: l.InodeSoft,
		Valid:      qifLimits,
	}
}

// genericQuota decodes a Q_GETQUOTA result.
func genericQuota(d *ifDqblk) Quota {
	return Quota{
		Limits: Limits{
			BlockSoft: d.Bsoftlimit << qifDqblkSizeBits,
			BlockHard: d.Bhardlimit << qifDqblkSizeBits,
			InodeSoft: d.Isoftlimit,
			InodeHard: d.Ihardlimit,
		},
		Bytes:      d.Curspace,
		Inodes:     d.Curinodes,
		BlockGrace: unixTime(int64(d.Btime)),
		InodeGrace: unixTime(int64(d.Itime)),
	}
}

// filesystemOf maps a statfs f_type to a Filesystem.
func filesystemOf(magic uint32) (Filesystem, error) {
	switch magic {
	case xfsSuperMagic:
		return XFS, nil
	case ext4SuperMagic:
		return Ext4, nil
	}
	return 0, fmt.Errorf("%w (statfs f_type %#x)", ErrUnsupportedFilesystem, magic)
}

// QuotaError is a failed quotactl_fd(2) call.
type QuotaError struct {
	Op   string // "Q_XSETQLIM", "Q_XGETQUOTA", "Q_SETQUOTA" or "Q_GETQUOTA"
	Path string // the file the call was made through
	ID   uint32 // the project id
	Err  error  // the errno
	Hint string // the likely cause of that errno, or ""
}

func (e *QuotaError) Error() string {
	s := fmt.Sprintf("projquota: quotactl_fd(%s) project %d on %s: %v", e.Op, e.ID, e.Path, e.Err)
	if e.Hint != "" {
		s += " (" + e.Hint + ")"
	}
	return s
}

// Unwrap returns the errno, so errors.Is(err, unix.EPERM) works.
func (e *QuotaError) Unwrap() error { return e.Err }
