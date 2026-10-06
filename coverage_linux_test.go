// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package projquota

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// These tests drive every branch of projquota_linux.go through the seams in
// seams_linux.go, as an ordinary user and without XFS or ext4. The root-only
// integration_linux_test.go drives the real syscalls.

var errInjected = errors.New("injected")

// withSeams snapshots every seam and restores it when the test ends.
func withSeams(t *testing.T) {
	t.Helper()
	a, b, c, d, e, f, g := osOpenFile, unixFstatfs, unixFstat, unixOpenat, readDir, doIoctl, doQuotactlFd
	t.Cleanup(func() {
		osOpenFile, unixFstatfs, unixFstat, unixOpenat, readDir, doIoctl, doQuotactlFd = a, b, c, d, e, f, g
	})
}

// fdPath names the file behind an fd, the way a fake kernel needs to.
func fdPath(fd uintptr) string {
	p, _ := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
	return p
}

// fakeAttrs is a fake FS_IOC_FS{GET,SET}XATTR keyed by path.
type fakeAttrs struct {
	attrs  map[string]fsxattr
	getErr unix.Errno
	setErr unix.Errno
	sets   []string
}

func installAttrs(t *testing.T) *fakeAttrs {
	k := &fakeAttrs{attrs: map[string]fsxattr{}}
	doIoctl = func(fd uintptr, req uintptr, arg unsafe.Pointer) unix.Errno {
		p := fdPath(fd)
		fa := (*fsxattr)(arg)
		switch req {
		case archIOC.fsIocFsGetXattr():
			if k.getErr != 0 {
				return k.getErr
			}
			*fa = k.attrs[p]
		case archIOC.fsIocFsSetXattr():
			if k.setErr != 0 {
				return k.setErr
			}
			k.attrs[p] = *fa
			k.sets = append(k.sets, p)
		default:
			t.Errorf("unexpected ioctl %#x", req)
			return unix.ENOTTY
		}
		return 0
	}
	return k
}

// fsMagic makes fstatfs report the given f_type.
func fsMagic(magic uint32) {
	unixFstatfs = func(fd int, st *unix.Statfs_t) error {
		*st = unix.Statfs_t{}
		st.Type = typeOf(st.Type, magic)
		return nil
	}
}

// typeOf converts a magic to whatever integer type this architecture's
// Statfs_t.Type is (int64, int32 or uint32).
func typeOf[T ~int32 | ~int64 | ~uint32](_ T, magic uint32) T { return T(magic) }

func TestRealSyscallsOnBadFd(t *testing.T) {
	var fa fsxattr
	// qemu-user (the emulated CI lanes) answers an ioctl it has no table
	// entry for with ENOTTY before the fd is ever looked at.
	if e := realIoctl(^uintptr(0), archIOC.fsIocFsGetXattr(), unsafe.Pointer(&fa)); e != unix.EBADF && e != unix.ENOTTY {
		t.Errorf("realIoctl(-1) = %v, want EBADF", e)
	}
	var d ifDqblk
	if e := realQuotactlFd(^uintptr(0), qcmd(qGetQuota, prjQuota), 1, unsafe.Pointer(&d)); e != unix.EBADF && e != unix.ENOSYS {
		t.Errorf("realQuotactlFd(-1) = %v, want EBADF (or ENOSYS before 5.14)", e)
	}
}

func TestDetect(t *testing.T) {
	withSeams(t)
	dir := t.TempDir()

	if _, err := Detect(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Detect(missing) = %v", err)
	}

	fsMagic(xfsSuperMagic)
	if f, err := Detect(dir); f != XFS || err != nil {
		t.Errorf("Detect(xfs) = %v, %v", f, err)
	}
	fsMagic(ext4SuperMagic)
	if f, err := Detect(dir); f != Ext4 || err != nil {
		t.Errorf("Detect(ext4) = %v, %v", f, err)
	}
	fsMagic(0x01021994) // TMPFS_MAGIC
	if _, err := Detect(dir); !errors.Is(err, ErrUnsupportedFilesystem) {
		t.Errorf("Detect(tmpfs) = %v", err)
	}
	unixFstatfs = func(int, *unix.Statfs_t) error { return unix.EIO }
	if _, err := Detect(dir); !errors.Is(err, unix.EIO) || !strings.Contains(err.Error(), "fstatfs") {
		t.Errorf("Detect(fstatfs fails) = %v", err)
	}
}

func TestDetectRealFstatfs(t *testing.T) {
	// The real fstatfs on an O_PATH fd must work: whatever filesystem the
	// temp dir is on, the answer is a Filesystem or ErrUnsupportedFilesystem,
	// never an fstatfs error.
	_, err := Detect(t.TempDir())
	if err != nil && !errors.Is(err, ErrUnsupportedFilesystem) {
		t.Errorf("Detect(tempdir) = %v", err)
	}
}

func TestGetSetProject(t *testing.T) {
	withSeams(t)
	k := installAttrs(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")

	if _, _, err := GetProject(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("GetProject(missing) = %v", err)
	}
	if err := SetProject(missing, 1, true); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("SetProject(missing) = %v", err)
	}

	// Read-modify-write: the other flags and fields survive.
	k.attrs[dir] = fsxattr{Xflags: 0x8 | 0x80, Extsize: 4096, Projid: 3}
	if err := SetProject(dir, 42, true); err != nil {
		t.Fatalf("SetProject: %v", err)
	}
	if got, want := k.attrs[dir], (fsxattr{Xflags: 0x8 | 0x80 | fsXflagProjinherit, Extsize: 4096, Projid: 42}); got != want {
		t.Errorf("after SetProject(42, true): %+v, want %+v", got, want)
	}
	if id, inh, err := GetProject(dir); id != 42 || !inh || err != nil {
		t.Errorf("GetProject = %d, %v, %v", id, inh, err)
	}
	if err := SetProject(dir, 0, false); err != nil {
		t.Fatalf("SetProject(0, false): %v", err)
	}
	if got, want := k.attrs[dir], (fsxattr{Xflags: 0x8 | 0x80, Extsize: 4096}); got != want {
		t.Errorf("after SetProject(0, false): %+v, want %+v", got, want)
	}

	if err := SetProject(dir, invalidProjectID, true); !errors.Is(err, ErrInvalidProject) {
		t.Errorf("SetProject(INVALID_PROJID) = %v", err)
	}

	k.setErr = unix.EPERM
	if err := SetProject(dir, 5, true); !errors.Is(err, unix.EPERM) || !strings.Contains(err.Error(), "FS_IOC_FSSETXATTR") {
		t.Errorf("SetProject(set fails) = %v", err)
	}
	k.getErr = unix.ENOTTY
	if err := SetProject(dir, 5, true); !errors.Is(err, unix.ENOTTY) || !strings.Contains(err.Error(), "FS_IOC_FSGETXATTR") {
		t.Errorf("SetProject(get fails) = %v", err)
	}
	if _, _, err := GetProject(dir); !errors.Is(err, unix.ENOTTY) {
		t.Errorf("GetProject(get fails) = %v", err)
	}
}

// tree builds root/{a/{f1, b/{f2}}, f0, link -> /etc, fifo}.
func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"a", "a/b"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"f0", "a/f1", "a/b/f2"} {
		if err := os.WriteFile(filepath.Join(root, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSetProjectTree(t *testing.T) {
	withSeams(t)
	k := installAttrs(t)
	root := tree(t)

	if err := SetProjectTree(root, 9); err != nil {
		t.Fatalf("SetProjectTree: %v", err)
	}
	want := map[string]fsxattr{
		root:                          {Projid: 9, Xflags: fsXflagProjinherit},
		filepath.Join(root, "a"):      {Projid: 9, Xflags: fsXflagProjinherit},
		filepath.Join(root, "a/b"):    {Projid: 9, Xflags: fsXflagProjinherit},
		filepath.Join(root, "f0"):     {Projid: 9},
		filepath.Join(root, "a/f1"):   {Projid: 9},
		filepath.Join(root, "a/b/f2"): {Projid: 9},
	}
	if len(k.attrs) != len(want) {
		t.Errorf("tagged %d files, want %d: %v", len(k.attrs), len(want), k.sets)
	}
	for p, w := range want {
		if got := k.attrs[p]; got != w {
			t.Errorf("%s: %+v, want %+v", p, got, w)
		}
	}
}

func TestSetProjectTreeErrors(t *testing.T) {
	withSeams(t)
	k := installAttrs(t)
	root := tree(t)

	if err := SetProjectTree(root, invalidProjectID); !errors.Is(err, ErrInvalidProject) {
		t.Errorf("INVALID_PROJID: %v", err)
	}
	// The root must be a real directory, not a link to one.
	if err := SetProjectTree(filepath.Join(root, "link"), 1); !errors.Is(err, unix.ELOOP) {
		t.Errorf("root is a link: %v", err)
	}

	unixFstat = func(int, *unix.Stat_t) error { return unix.EIO }
	if err := SetProjectTree(root, 1); !errors.Is(err, unix.EIO) || !strings.Contains(err.Error(), "fstat") {
		t.Errorf("fstat(root) fails: %v", err)
	}
	unixFstat = unix.Fstat

	k.setErr = unix.EPERM
	if err := SetProjectTree(root, 1); !errors.Is(err, unix.EPERM) {
		t.Errorf("root not settable: %v", err)
	}
	k.setErr = 0

	readDir = func(*os.File, int) ([]fs.DirEntry, error) { return nil, unix.EIO }
	if err := SetProjectTree(root, 1); !errors.Is(err, unix.EIO) {
		t.Errorf("readdir fails: %v", err)
	}
	readDir = (*os.File).ReadDir

	// A failing child aborts the walk.
	k.setErr = 0
	calls := 0
	real := doIoctl
	doIoctl = func(fd uintptr, req uintptr, arg unsafe.Pointer) unix.Errno {
		if req == archIOC.fsIocFsSetXattr() && strings.HasSuffix(fdPath(fd), "/f0") {
			calls++
			return unix.EPERM
		}
		return real(fd, req, arg)
	}
	if err := SetProjectTree(root, 1); !errors.Is(err, unix.EPERM) || calls != 1 {
		t.Errorf("child not settable: %v (calls %d)", err, calls)
	}
	doIoctl = real
}

func TestWalkEntryRaces(t *testing.T) {
	withSeams(t)
	k := installAttrs(t)
	root := tree(t)

	// Listed as a directory or file, then gone or turned into a link:
	// skipped. Any other openat failure stops the walk.
	for _, c := range []struct {
		errno unix.Errno
		fails bool
	}{{unix.ELOOP, false}, {unix.ENOENT, false}, {unix.EACCES, true}} {
		unixOpenat = func(dirfd int, name string, flags int, mode uint32) (int, error) {
			if name == "a" {
				return -1, c.errno
			}
			return unix.Openat(dirfd, name, flags, mode)
		}
		k.attrs = map[string]fsxattr{}
		err := SetProjectTree(root, 2)
		if c.fails {
			if !errors.Is(err, c.errno) || !strings.Contains(err.Error(), "openat") {
				t.Errorf("%v: %v", c.errno, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v: %v", c.errno, err)
		}
		if _, ok := k.attrs[filepath.Join(root, "a")]; ok {
			t.Errorf("%v: a was tagged", c.errno)
		}
		if _, ok := k.attrs[filepath.Join(root, "f0")]; !ok {
			t.Errorf("%v: f0 was not tagged", c.errno)
		}
	}
	unixOpenat = unix.Openat

	// fstat of a child fails.
	unixFstat = func(fd int, st *unix.Stat_t) error {
		if strings.HasSuffix(fdPath(uintptr(fd)), "/f0") {
			return unix.EIO
		}
		return unix.Fstat(fd, st)
	}
	if err := SetProjectTree(root, 2); !errors.Is(err, unix.EIO) {
		t.Errorf("fstat(child) fails: %v", err)
	}

	// A child on another filesystem (a mount inside the tree) is skipped,
	// and so is one that turned into a device since it was listed.
	unixFstat = func(fd int, st *unix.Stat_t) error {
		err := unix.Fstat(fd, st)
		switch p := fdPath(uintptr(fd)); {
		case strings.HasSuffix(p, "/a"):
			st.Dev++
		case strings.HasSuffix(p, "/f0"):
			st.Mode = unix.S_IFCHR | 0o600
		}
		return err
	}
	k.attrs = map[string]fsxattr{}
	if err := SetProjectTree(root, 3); err != nil {
		t.Fatalf("SetProjectTree: %v", err)
	}
	for _, p := range []string{"a", "a/f1", "f0"} {
		if _, ok := k.attrs[filepath.Join(root, p)]; ok {
			t.Errorf("%s was tagged", p)
		}
	}
	if len(k.attrs) != 1 {
		t.Errorf("tagged %v, want the root only", k.sets)
	}
}

// fakeQuota is a fake quotactl_fd that records its call and answers with
// a canned struct or errno.
type fakeQuota struct {
	cmd, id uint32
	errno   unix.Errno
	xfs     fsDiskQuota
	generic ifDqblk
	gotXFS  fsDiskQuota
	gotGen  ifDqblk
}

func installQuota() *fakeQuota {
	q := &fakeQuota{}
	doQuotactlFd = func(fd uintptr, cmd, id uint32, addr unsafe.Pointer) unix.Errno {
		q.cmd, q.id = cmd, id
		if q.errno != 0 {
			return q.errno
		}
		switch cmd >> subCmdShift {
		case qXSetQLim:
			q.gotXFS = *(*fsDiskQuota)(addr)
		case qXGetQuota:
			*(*fsDiskQuota)(addr) = q.xfs
		case qSetQuota:
			q.gotGen = *(*ifDqblk)(addr)
		case qGetQuota:
			*(*ifDqblk)(addr) = q.generic
		}
		return 0
	}
	return q
}

func TestSetLimits(t *testing.T) {
	withSeams(t)
	q := installQuota()
	dir := t.TempDir()
	l := Limits{BlockSoft: 1 << 20, BlockHard: 2 << 20, InodeSoft: 10, InodeHard: 20}

	if err := SetLimits(filepath.Join(dir, "missing"), 1, l); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	for _, id := range []uint32{0, invalidProjectID} {
		if err := SetLimits(dir, id, l); !errors.Is(err, ErrInvalidProject) {
			t.Errorf("id %d: %v", id, err)
		}
	}
	if err := SetLimits(dir, 1, Limits{BlockSoft: 2, BlockHard: 1}); !errors.Is(err, ErrSoftAboveHard) {
		t.Errorf("soft > hard: %v", err)
	}
	fsMagic(0x01021994)
	if err := SetLimits(dir, 1, l); !errors.Is(err, ErrUnsupportedFilesystem) {
		t.Errorf("tmpfs: %v", err)
	}

	fsMagic(xfsSuperMagic)
	if err := SetLimits(dir, 7, l); err != nil {
		t.Fatalf("xfs: %v", err)
	}
	if q.cmd != qcmd(qXSetQLim, prjQuota) || q.id != 7 || q.gotXFS != xfsSetLimits(7, l) {
		t.Errorf("xfs call: cmd %#x id %d arg %+v", q.cmd, q.id, q.gotXFS)
	}

	fsMagic(ext4SuperMagic)
	if err := SetLimits(dir, 8, l); err != nil {
		t.Fatalf("ext4: %v", err)
	}
	if q.cmd != qcmd(qSetQuota, prjQuota) || q.id != 8 || q.gotGen != genericSetLimits(l) {
		t.Errorf("ext4 call: cmd %#x id %d arg %+v", q.cmd, q.id, q.gotGen)
	}

	q.errno = unix.EPERM
	err := SetLimits(dir, 8, l)
	var qe *QuotaError
	if !errors.As(err, &qe) || qe.Op != "Q_SETQUOTA" || qe.ID != 8 || !errors.Is(err, unix.EPERM) ||
		!strings.Contains(err.Error(), "CAP_SYS_ADMIN") {
		t.Errorf("EPERM: %v", err)
	}
}

func TestUsage(t *testing.T) {
	withSeams(t)
	q := installQuota()
	dir := t.TempDir()

	if _, err := Usage(filepath.Join(dir, "missing"), 1); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	if _, err := Usage(dir, invalidProjectID); !errors.Is(err, ErrInvalidProject) {
		t.Errorf("INVALID_PROJID: %v", err)
	}
	fsMagic(0x01021994)
	if _, err := Usage(dir, 1); !errors.Is(err, ErrUnsupportedFilesystem) {
		t.Errorf("tmpfs: %v", err)
	}

	fsMagic(xfsSuperMagic)
	q.xfs = fsDiskQuota{BlkHardlimit: 4, Bcount: 2, Icount: 1}
	got, err := Usage(dir, 5)
	if err != nil || got != xfsQuota(&q.xfs) || q.cmd != qcmd(qXGetQuota, prjQuota) || q.id != 5 {
		t.Errorf("xfs: %+v, %v (cmd %#x)", got, err, q.cmd)
	}
	q.errno = unix.ENOENT // no dquot yet: nothing charged, no limits
	if got, err := Usage(dir, 5); err != nil || got != (Quota{}) {
		t.Errorf("xfs ENOENT: %+v, %v", got, err)
	}
	q.errno = unix.ESRCH
	if _, err := Usage(dir, 5); !errors.Is(err, unix.ESRCH) || !strings.Contains(err.Error(), "prjquota") {
		t.Errorf("xfs ESRCH: %v", err)
	}

	fsMagic(ext4SuperMagic)
	q.errno = 0
	q.generic = ifDqblk{Bhardlimit: 4, Curspace: 3000, Curinodes: 2}
	got, err = Usage(dir, 6)
	if err != nil || got != genericQuota(&q.generic) || q.cmd != qcmd(qGetQuota, prjQuota) || q.id != 6 {
		t.Errorf("ext4: %+v, %v (cmd %#x)", got, err, q.cmd)
	}
	q.errno = unix.ENOENT // only XFS turns ENOENT into "nothing"
	if _, err := Usage(dir, 6); !errors.Is(err, unix.ENOENT) {
		t.Errorf("ext4 ENOENT: %v", err)
	}
}

func TestQuotaHint(t *testing.T) {
	for errno, want := range map[unix.Errno]string{
		unix.EPERM:  "CAP_SYS_ADMIN",
		unix.ENOSYS: "5.14",
		unix.ESRCH:  "prjquota",
		unix.EROFS:  "read-only",
		unix.ERANGE: "larger",
		unix.EIO:    "",
	} {
		got := quotaHint(errno)
		if (want == "" && got != "") || !strings.Contains(got, want) {
			t.Errorf("quotaHint(%v) = %q, want it to mention %q", errno, got, want)
		}
	}
}
