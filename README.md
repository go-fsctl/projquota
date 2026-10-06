# go-fsctl/projquota

[![Go Reference](https://pkg.go.dev/badge/github.com/go-fsctl/projquota.svg)](https://pkg.go.dev/github.com/go-fsctl/projquota)
[![License: BSD-3-Clause](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![CI](https://github.com/go-fsctl/projquota/actions/workflows/ci.yml/badge.svg)](https://github.com/go-fsctl/projquota/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/badge/coverage-100%25-brightgreen)](https://github.com/go-fsctl/projquota/actions/workflows/ci.yml)

Pure-Go Linux **project quotas** for XFS and ext4: give a directory tree a
project id, limit its space and inodes, and read its usage, straight through
the kernel — `FS_IOC_FSGETXATTR` / `FS_IOC_FSSETXATTR` and `quotactl_fd(2)` —
with **no cgo** and **no shelling out** to `xfs_quota`, `setquota` or
`chattr`.

This is the project-quota member of the [`go-fsctl`](https://github.com/go-fsctl)
family, alongside [`go-fsctl/btrfs`](https://github.com/go-fsctl/btrfs) (whose
qgroups are btrfs's own answer to the same question),
[`go-fsctl/zfs`](https://github.com/go-fsctl/zfs) and
[`go-fsctl/loop`](https://github.com/go-fsctl/loop).

## API

```go
import "github.com/go-fsctl/projquota"

// Which filesystem is this? (fstatfs f_type: XFS or ext4, else an error)
fs, err := projquota.Detect("/srv/shares")

// Tag a fresh directory: project 4242, and FS_XFLAG_PROJINHERIT so that
// everything created inside it joins the project.   FS_IOC_FSSETXATTR
err = projquota.SetProject("/srv/shares/alice", 4242, true)
id, inherit, err := projquota.GetProject("/srv/shares/alice")

// Tag a directory that already has content (never follows a symlink, stays
// on one filesystem).
err = projquota.SetProjectTree("/srv/shares/bob", 4243)

// Limit the project (bytes; 0 = no limit).   quotactl_fd Q_XSETQLIM / Q_SETQUOTA
err = projquota.SetLimits("/srv/shares", 4242, projquota.Limits{
    BlockSoft: 9 << 30,
    BlockHard: 10 << 30,
    InodeHard: 1_000_000,
})

// Usage and limits.   quotactl_fd Q_XGETQUOTA / Q_GETQUOTA
q, err := projquota.Usage("/srv/shares", 4242)
// q.Bytes, q.Inodes, q.BlockHard, q.BlockSoft, q.BlockGrace, ...
```

Each path function has a `…File(*os.File, …)` twin (`GetProjectFile`,
`SetProjectFile`, `SetLimitsFile`, `UsageFile`, `DetectFile`). The limits
functions take any file or directory on the filesystem, usually the mount
point. Off Linux every function returns `projquota.ErrUnsupported`.

| Operation | Kernel interface |
|---|---|
| `GetProject`, `SetProject`, `SetProjectTree` | `FS_IOC_FSGETXATTR`, `FS_IOC_FSSETXATTR` (read-modify-write, every other flag kept) |
| `SetLimits` on XFS | `quotactl_fd(QCMD(Q_XSETQLIM, PRJQUOTA))`, `struct fs_disk_quota`, 512-byte basic blocks |
| `SetLimits` on ext4 | `quotactl_fd(QCMD(Q_SETQUOTA, PRJQUOTA))`, `struct if_dqblk`, 1 KiB block limits |
| `Usage` on XFS / ext4 | `Q_XGETQUOTA` / `Q_GETQUOTA` |
| `Detect` | `fstatfs` `f_type`: `XFS_SUPER_MAGIC`, `EXT4_SUPER_MAGIC` |

## Requirements

- **Linux ≥ 5.14**, for `quotactl_fd(2)`. The older `quotactl(2)` needs the
  block device behind the mount; this package does not go looking for it.
- **Project quotas on the mount.** XFS: `mount -o prjquota`. ext4: a
  filesystem made with `mkfs.ext4 -O quota,project -I 256` (the project
  feature needs inodes larger than 128 bytes) and mounted with `-o prjquota`.
- **`CAP_SYS_ADMIN` for `SetLimits` and for `Usage`.** The kernel lets an
  unprivileged caller read only its own user and group quotas, never a
  project's (`fs/quota/quota.c`, `check_quotactl_permission`).
- XFS project ids above 65535 need the `projid32bit` feature, the `mkfs.xfs`
  default for years.
- `/etc/projects` and `/etc/projid` are **not** needed: they are
  `xfs_quota`'s name tables and the kernel never reads them.

## ⛔ Do not give the tenant ownership of its directory

Changing a project id does **not** need privilege. `vfs_fileattr_set`
(`fs/file_attr.c`) asks only that the caller own the file
(`inode_owner_or_capable`), and `fileattr_set_prepare` refuses a project
change only to callers *outside* the initial user namespace. So the owner of
a project directory, as an ordinary user on the host, can move it — or any
file they own inside it — to project 0, out of the limit.

A provisioner that gives each tenant a project-limited directory must keep the
directory, and the files in it, owned by someone other than the tenant while
the tenant can run code in the initial user namespace: serve it through a
file server that writes on the tenant's behalf under its own uid, or confine
the tenant to a user namespace, where the kernel answers `EINVAL`. The
integration tests prove the hazard rather than assert it: an unprivileged
owner clears its project id on real XFS and ext4.

## Units and rounding

`Limits` and `Quota` are in bytes. The kernel ABIs are not: XFS speaks
512-byte basic blocks and the generic interface 1 KiB blocks for limits, so
limits are rounded **up** to those units, as the kernel's own conversions
(`quota_btobb`, `stoqb`) do — and XFS rounds up again to its filesystem
block. `Usage` reads back what was stored. On ext4 `Quota.Bytes` is exact; on
XFS it is a multiple of 512.

Three things the kernel does that this package makes explicit:

- **XFS silently ignores a hard limit below its soft limit**:
  `xfs_setqlim_limits` logs `hard < soft`, keeps the old limits and the call
  returns success. `SetLimits` refuses it with `ErrSoftAboveHard`.
- **Project 0 on XFS is the default for every project**
  (`xfs_qm_scall_setqlim`: `qlim = id == 0 ? &defq->blk : NULL`). `SetLimits`
  refuses id 0 with `ErrInvalidProject`.
- **XFS has no record of a project that never used anything** and answers
  `ENOENT`; `Usage` reports that as a zero `Quota`.

`statfs(2)` — and so `df` — of a directory carrying `FS_XFLAG_PROJINHERIT`
reports the project's limit as the size of the filesystem: the **soft** limit
when one is set, else the hard limit.

## Testing

```sh
# Unprivileged: ABI layouts and ioctl numbers, unit conversions, and every
# kernel-call branch through fault-injecting seams. 100% coverage on Linux.
GOWORK=off go test ./...

# Root: real XFS and ext4 in loop-mounted images (needs mkfs.xfs, mkfs.ext4).
GOWORK=off go test -c -o projquota.test . && sudo ./projquota.test -test.v -test.run '^TestIntegration'
```

CI runs the unit tests natively on amd64 and arm64 with a 100.0% coverage
floor, under QEMU on riscv64, loong64, ppc64le and s390x, and the root
integration tests on a hosted runner's own kernel.

## License

BSD-3-Clause. See [LICENSE](LICENSE).
