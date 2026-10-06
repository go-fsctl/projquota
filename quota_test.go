// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package projquota

import (
	"errors"
	"math"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCeilShift(t *testing.T) {
	for _, c := range []struct {
		v     uint64
		shift uint
		want  uint64
	}{
		{0, 9, 0},
		{1, 9, 1},
		{511, 9, 1},
		{512, 9, 1},
		{513, 9, 2},
		{1024, 10, 1},
		{1025, 10, 2},
		// (v + 511) >> 9 would wrap to 0 here; the kernel's quota_btobb
		// does wrap, which is why the Go side does not copy its formula.
		{math.MaxUint64, 9, 1 << 55},
		{math.MaxUint64, 10, 1 << 54},
	} {
		if got := ceilShift(c.v, c.shift); got != c.want {
			t.Errorf("ceilShift(%d, %d) = %d, want %d", c.v, c.shift, got, c.want)
		}
	}
}

func TestValidate(t *testing.T) {
	for _, c := range []struct {
		l    Limits
		want bool
	}{
		{Limits{}, true},
		{Limits{BlockSoft: 10, BlockHard: 10, InodeSoft: 5, InodeHard: 5}, true},
		{Limits{BlockSoft: 10}, true}, // hard 0 = no hard limit
		{Limits{InodeSoft: 10}, true},
		{Limits{BlockSoft: 11, BlockHard: 10}, false},
		{Limits{InodeSoft: 11, InodeHard: 10}, false},
	} {
		err := c.l.validate()
		if (err == nil) != c.want {
			t.Errorf("%+v.validate() = %v, want ok=%v", c.l, err, c.want)
		}
		if err != nil && !errors.Is(err, ErrSoftAboveHard) {
			t.Errorf("%+v.validate() = %v, not ErrSoftAboveHard", c.l, err)
		}
	}
}

func TestXFSEncoding(t *testing.T) {
	d := xfsSetLimits(42, Limits{BlockSoft: 1000, BlockHard: 1 << 20, InodeSoft: 7, InodeHard: 9})
	want := fsDiskQuota{
		Version:      1,
		Flags:        fsProjQuota,
		Fieldmask:    fsDqIsoft | fsDqIhard | fsDqBsoft | fsDqBhard,
		ID:           42,
		BlkSoftlimit: 2,    // 1000 bytes -> 2 basic blocks (rounded up)
		BlkHardlimit: 2048, // 1 MiB -> 2048 basic blocks
		InoSoftlimit: 7,
		InoHardlimit: 9,
	}
	if d != want {
		t.Fatalf("xfsSetLimits = %+v, want %+v", d, want)
	}
	if d.Fieldmask != 0xf {
		t.Errorf("fieldmask = %#x, want 0xf (ISOFT|IHARD|BSOFT|BHARD)", d.Fieldmask)
	}

	got := xfsQuota(&fsDiskQuota{
		BlkSoftlimit: 2, BlkHardlimit: 2048, InoSoftlimit: 7, InoHardlimit: 9,
		Bcount: 3, Icount: 4, Btimer: 1700000000, Itimer: 0,
	})
	wantQ := Quota{
		Limits:     Limits{BlockSoft: 1024, BlockHard: 1 << 20, InodeSoft: 7, InodeHard: 9},
		Bytes:      1536,
		Inodes:     4,
		BlockGrace: time.Unix(1700000000, 0),
	}
	if got != wantQ {
		t.Errorf("xfsQuota = %+v, want %+v", got, wantQ)
	}
}

func TestXFSTimer(t *testing.T) {
	small := &fsDiskQuota{}
	if got := xfsTimer(small, -1, 1); got != -1 {
		t.Errorf("without BIGTIME: %d, want -1 (hi ignored)", got)
	}
	big := &fsDiskQuota{Fieldmask: fsDqBigtime}
	// (u32)timer | (s64)timer_hi << 32
	if got := xfsTimer(big, -1, 1); got != 1<<33-1 {
		t.Errorf("with BIGTIME: %d, want %d", got, int64(1<<33-1))
	}
	q := xfsQuota(&fsDiskQuota{Fieldmask: fsDqBigtime, Itimer: 5, ItimerHi: 1})
	if want := time.Unix(1<<32|5, 0); !q.InodeGrace.Equal(want) {
		t.Errorf("InodeGrace = %v, want %v", q.InodeGrace, want)
	}
	if !q.BlockGrace.IsZero() {
		t.Errorf("BlockGrace = %v, want zero", q.BlockGrace)
	}
}

func TestGenericEncoding(t *testing.T) {
	d := genericSetLimits(Limits{BlockSoft: 1000, BlockHard: 1 << 20, InodeSoft: 7, InodeHard: 9})
	want := ifDqblk{Bsoftlimit: 1, Bhardlimit: 1024, Isoftlimit: 7, Ihardlimit: 9, Valid: qifLimits}
	if d != want {
		t.Fatalf("genericSetLimits = %+v, want %+v", d, want)
	}
	if d.Valid != qifBlimits|qifIlimits || d.Valid&(qifSpace|qifInodes|qifBtime|qifItime) != 0 {
		t.Errorf("valid = %#x: must touch the limits only", d.Valid)
	}
	got := genericQuota(&ifDqblk{
		Bsoftlimit: 1, Bhardlimit: 1024, Isoftlimit: 7, Ihardlimit: 9,
		Curspace: 12345, Curinodes: 3, Itime: 1700000000,
	})
	wantQ := Quota{
		Limits:     Limits{BlockSoft: 1024, BlockHard: 1 << 20, InodeSoft: 7, InodeHard: 9},
		Bytes:      12345, // dqb_curspace is in bytes, not blocks
		Inodes:     3,
		InodeGrace: time.Unix(1700000000, 0),
	}
	if got != wantQ {
		t.Errorf("genericQuota = %+v, want %+v", got, wantQ)
	}
}

func TestFilesystemOf(t *testing.T) {
	if f, err := filesystemOf(0x58465342); f != XFS || err != nil {
		t.Errorf("XFS magic: %v, %v", f, err)
	}
	if f, err := filesystemOf(0xEF53); f != Ext4 || err != nil {
		t.Errorf("ext4 magic: %v, %v", f, err)
	}
	// BTRFS_SUPER_MAGIC: btrfs has qgroups, not project quotas.
	_, err := filesystemOf(0x9123683E)
	if !errors.Is(err, ErrUnsupportedFilesystem) || !strings.Contains(err.Error(), "0x9123683e") {
		t.Errorf("btrfs magic: %v", err)
	}
}

func TestFilesystemString(t *testing.T) {
	for f, want := range map[Filesystem]string{XFS: "xfs", Ext4: "ext4", 9: "Filesystem(9)"} {
		if got := f.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(f), got, want)
		}
	}
}

func TestQuotaError(t *testing.T) {
	e := &QuotaError{Op: "Q_XSETQLIM", Path: "/mnt", ID: 7, Err: syscall.EPERM, Hint: "needs root"}
	if got := e.Error(); !strings.HasSuffix(got, "(needs root)") || !strings.Contains(got, "Q_XSETQLIM") {
		t.Errorf("Error() = %q", got)
	}
	if !errors.Is(e, syscall.EPERM) {
		t.Error("errors.Is(EPERM) = false")
	}
	e.Hint = ""
	if got := e.Error(); strings.Contains(got, "(") && strings.HasSuffix(got, ")") {
		t.Errorf("Error() without hint = %q", got)
	}
}

func TestUnixTime(t *testing.T) {
	if !unixTime(0).IsZero() {
		t.Error("unixTime(0) is not the zero time")
	}
}
