// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package projquota

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ---------------------------------------------------------------------------
// Opening.

// openPath opens any file on a filesystem for quotactl_fd(2) and fstatfs(2).
// O_PATH is enough for both: quotactl_fd takes the fd with CLASS(fd_raw, ...)
// (fs/quota/quota.c), and quotactl(2)'s man page says the fd "may be opened
// with the O_PATH flag". It needs no read permission on the file.
func openPath(path string) (*os.File, error) {
	return osOpenFile(path, unix.O_PATH, 0)
}

// openAttr opens a file for FS_IOC_FS{GET,SET}XATTR, which an O_PATH fd
// cannot carry. O_NONBLOCK keeps a FIFO from blocking the open.
func openAttr(path string) (*os.File, error) {
	return osOpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
}

// ---------------------------------------------------------------------------
// Filesystem detection.

// Detect reports which filesystem path lives on, or ErrUnsupportedFilesystem.
func Detect(path string) (Filesystem, error) {
	f, err := openPath(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return DetectFile(f)
}

// DetectFile is Detect on an open file.
func DetectFile(f *os.File) (Filesystem, error) {
	var st unix.Statfs_t
	err := unixFstatfs(int(f.Fd()), &st)
	runtime.KeepAlive(f)
	if err != nil {
		return 0, &os.PathError{Op: "fstatfs", Path: f.Name(), Err: err}
	}
	// f_type is int64, int32 or uint32 depending on the architecture; the
	// two magics fit in 31 bits, so the conversion is exact for both.
	return filesystemOf(uint32(st.Type))
}

// ---------------------------------------------------------------------------
// Project ids: FS_IOC_FSGETXATTR / FS_IOC_FSSETXATTR.

func getXattr(f *os.File) (fsxattr, error) {
	var fa fsxattr
	errno := doIoctl(f.Fd(), archIOC.fsIocFsGetXattr(), unsafe.Pointer(&fa))
	runtime.KeepAlive(f)
	if errno != 0 {
		return fa, &os.PathError{Op: "FS_IOC_FSGETXATTR", Path: f.Name(), Err: errno}
	}
	return fa, nil
}

func setXattr(f *os.File, fa *fsxattr) error {
	errno := doIoctl(f.Fd(), archIOC.fsIocFsSetXattr(), unsafe.Pointer(fa))
	runtime.KeepAlive(f)
	if errno != 0 {
		return &os.PathError{Op: "FS_IOC_FSSETXATTR", Path: f.Name(), Err: errno}
	}
	return nil
}

// GetProject returns the project id of path and whether FS_XFLAG_PROJINHERIT
// is set on it (new files and directories created inside then take the
// directory's project id). It needs no privilege.
func GetProject(path string) (id uint32, inherit bool, err error) {
	f, err := openAttr(path)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	return GetProjectFile(f)
}

// GetProjectFile is GetProject on an open file (not opened with O_PATH).
func GetProjectFile(f *os.File) (id uint32, inherit bool, err error) {
	fa, err := getXattr(f)
	if err != nil {
		return 0, false, err
	}
	return fa.Projid, fa.Xflags&fsXflagProjinherit != 0, nil
}

// SetProject sets the project id of path, and sets or clears
// FS_XFLAG_PROJINHERIT on it, keeping every other attribute (read, modify,
// write). Only the directory itself changes: files already inside keep their
// project; use SetProjectTree for a populated tree.
//
// inherit only makes sense on a directory; ext4 refuses it on a regular file
// (EOPNOTSUPP).
//
// The kernel lets the OWNER of the file do this, not only root
// (vfs_fileattr_set: inode_owner_or_capable), as long as the caller is in
// the initial user namespace. See the package documentation.
func SetProject(path string, id uint32, inherit bool) error {
	f, err := openAttr(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return SetProjectFile(f, id, inherit)
}

// SetProjectFile is SetProject on an open file (not opened with O_PATH).
func SetProjectFile(f *os.File, id uint32, inherit bool) error {
	if id == invalidProjectID {
		return fmt.Errorf("%w: %d is INVALID_PROJID", ErrInvalidProject, id)
	}
	fa, err := getXattr(f)
	if err != nil {
		return err
	}
	fa.Projid = id
	if inherit {
		fa.Xflags |= fsXflagProjinherit
	} else {
		fa.Xflags &^= fsXflagProjinherit
	}
	return setXattr(f, &fa)
}

// SetProjectTree gives root and everything below it on the same filesystem
// project id: directories get FS_XFLAG_PROJINHERIT too, regular files get
// the id only. Symbolic links, devices, FIFOs and sockets are left alone, as
// are other filesystems mounted inside the tree.
//
// The walk never follows a symbolic link: every entry is opened relative to
// its parent's fd with O_NOFOLLOW, so a name swapped for a link during the
// walk is skipped rather than followed out of the tree. An entry that
// vanishes or turns into a link between listing and opening is skipped.
//
// A regular file with other hard links outside the tree is charged to the
// project all the same: the project id belongs to the inode, not the name.
func SetProjectTree(root string, id uint32) error {
	if id == invalidProjectID {
		return fmt.Errorf("%w: %d is INVALID_PROJID", ErrInvalidProject, id)
	}
	f, err := osOpenFile(root, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	var st unix.Stat_t
	if err := fstat(f, &st); err != nil {
		return err
	}
	return walkTree(f, id, uint64(st.Dev))
}

func fstat(f *os.File, st *unix.Stat_t) error {
	err := unixFstat(int(f.Fd()), st)
	runtime.KeepAlive(f)
	if err != nil {
		return &os.PathError{Op: "fstat", Path: f.Name(), Err: err}
	}
	return nil
}

// walkTree tags dir and descends into it, entry by entry, through openat.
func walkTree(dir *os.File, id uint32, dev uint64) error {
	if err := SetProjectFile(dir, id, true); err != nil {
		return err
	}
	entries, err := readDir(dir, -1)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() && !e.Type().IsRegular() {
			continue
		}
		if err := walkEntry(dir, e.Name(), id, dev); err != nil {
			return err
		}
	}
	return nil
}

// walkEntry opens one entry of dir without following a link and tags it.
func walkEntry(dir *os.File, name string, id uint32, dev uint64) error {
	path := filepath.Join(dir.Name(), name)
	fd, err := unixOpenat(int(dir.Fd()), name,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	runtime.KeepAlive(dir)
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOENT) {
		return nil // became a link, or vanished, since it was listed
	}
	if err != nil {
		return &os.PathError{Op: "openat", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var st unix.Stat_t
	if err := fstat(f, &st); err != nil {
		return err
	}
	if uint64(st.Dev) != dev {
		return nil // another filesystem mounted inside the tree
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		return walkTree(f, id, dev)
	case unix.S_IFREG:
		return SetProjectFile(f, id, false)
	}
	return nil // swapped for a device, FIFO or socket since it was listed
}

// ---------------------------------------------------------------------------
// Limits and usage: quotactl_fd(2).

// quotaHint names the likely cause of a quotactl errno.
func quotaHint(errno unix.Errno) string {
	switch errno {
	case unix.EPERM:
		return "setting limits, and reading a project's usage, require CAP_SYS_ADMIN"
	case unix.ENOSYS:
		return "no quotactl_fd before Linux 5.14, a kernel without CONFIG_QUOTA, or quotas not enabled on this mount"
	case unix.ESRCH:
		return "project quotas are not enabled on this mount: mount with -o prjquota"
	case unix.EROFS:
		return "the mount is read-only"
	case unix.ERANGE:
		return "a limit is larger than the quota format can store"
	}
	return ""
}

// quotactl issues quotactl_fd(f, QCMD(cmd, PRJQUOTA), id, addr).
func quotactl(f *os.File, cmd uint32, op string, id uint32, addr unsafe.Pointer) error {
	errno := doQuotactlFd(f.Fd(), qcmd(cmd, prjQuota), id, addr)
	runtime.KeepAlive(f)
	if errno != 0 {
		return &QuotaError{Op: op, Path: f.Name(), ID: id, Err: errno, Hint: quotaHint(errno)}
	}
	return nil
}

// SetLimits sets the limits of project id on the filesystem holding path
// (any file or directory on it; the mount point is the usual choice). It
// requires CAP_SYS_ADMIN, Linux >= 5.14 (quotactl_fd), and a mount with
// project quotas enforced (XFS: -o prjquota; ext4: a filesystem made with
// -O quota,project and mounted with -o prjquota).
//
// Project id 0 is refused (ErrInvalidProject): on XFS its limits are the
// default limits of every project.
func SetLimits(path string, id uint32, l Limits) error {
	f, err := openPath(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return SetLimitsFile(f, id, l)
}

// SetLimitsFile is SetLimits through any open file on the filesystem.
func SetLimitsFile(f *os.File, id uint32, l Limits) error {
	if id == 0 || id == invalidProjectID {
		return fmt.Errorf("%w: limits cannot be set on project %d", ErrInvalidProject, id)
	}
	if err := l.validate(); err != nil {
		return err
	}
	fsys, err := DetectFile(f)
	if err != nil {
		return err
	}
	if fsys == XFS {
		d := xfsSetLimits(id, l)
		return quotactl(f, qXSetQLim, "Q_XSETQLIM", id, unsafe.Pointer(&d))
	}
	d := genericSetLimits(l)
	return quotactl(f, qSetQuota, "Q_SETQUOTA", id, unsafe.Pointer(&d))
}

// Usage returns the usage and limits of project id on the filesystem holding
// path. It requires CAP_SYS_ADMIN: fs/quota/quota.c
// check_quotactl_permission lets an unprivileged caller read only its own
// user and group quotas, never a project's.
//
// A project XFS has never charged anything to has no quota record
// (xfs_qm_scall_getquota: "If it doesn't exist, we'll get ENOENT back");
// Usage reports it as a zero Quota, which is what it is.
//
// On ext4, Q_GETQUOTA needs write access to the mount
// (fs/quota/quota.c quotactl_cmd_write) and fails with EROFS on a read-only
// one.
func Usage(path string, id uint32) (Quota, error) {
	f, err := openPath(path)
	if err != nil {
		return Quota{}, err
	}
	defer f.Close()
	return UsageFile(f, id)
}

// UsageFile is Usage through any open file on the filesystem.
func UsageFile(f *os.File, id uint32) (Quota, error) {
	if id == invalidProjectID {
		return Quota{}, fmt.Errorf("%w: %d is INVALID_PROJID", ErrInvalidProject, id)
	}
	fsys, err := DetectFile(f)
	if err != nil {
		return Quota{}, err
	}
	if fsys == XFS {
		var d fsDiskQuota
		err := quotactl(f, qXGetQuota, "Q_XGETQUOTA", id, unsafe.Pointer(&d))
		if errors.Is(err, unix.ENOENT) {
			return Quota{}, nil
		}
		if err != nil {
			return Quota{}, err
		}
		return xfsQuota(&d), nil
	}
	var d ifDqblk
	if err := quotactl(f, qGetQuota, "Q_GETQUOTA", id, unsafe.Pointer(&d)); err != nil {
		return Quota{}, err
	}
	return genericQuota(&d), nil
}
