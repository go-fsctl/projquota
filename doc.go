// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

// Package projquota drives Linux project quotas on XFS and ext4 directly
// through the kernel -- FS_IOC_FSGETXATTR / FS_IOC_FSSETXATTR to tag a
// directory tree with a project id, quotactl_fd(2) to set its limits and read
// its usage -- with no cgo and without xfs_quota(8), setquota(8) or
// chattr(1).
//
// A project quota limits a directory tree rather than a user: every inode
// carries a project id, a directory with FS_XFLAG_PROJINHERIT hands its id
// to whatever is created inside it, and the filesystem charges the space and
// inodes of all those files to the project. Once the hard limit is reached,
// writes fail -- with EDQUOT on ext4, but with ENOSPC on XFS, which reports
// an exhausted project quota as a full filesystem (fs/xfs/xfs_trans_dquot.c:
// "if (xfs_dquot_type(dqp) == XFS_DQTYPE_PROJ) return -ENOSPC;"). statfs(2) -- and so df(1) -- of a directory that
// carries FS_XFLAG_PROJINHERIT reports the project's limit as the size of the
// filesystem: the SOFT limit when one is set, else the hard limit
// (fs/xfs/xfs_qm_bhv.c xfs_fill_statvfs_from_dquot, called when project
// quotas are accounted and enforced; fs/ext4/super.c ext4_statfs_project,
// "min_not_zero(dqb_bsoftlimit, dqb_bhardlimit)").
//
// # Requirements
//
//   - Linux 5.14 or later, for quotactl_fd(2). The older quotactl(2) needs
//     the path of the block device under the mount, which this package does
//     not go looking for.
//   - The mount has project quotas enabled. XFS: mount with -o prjquota (or
//     pquota). ext4: a filesystem made with the project feature and inodes
//     larger than 128 bytes (mkfs.ext4 -O quota,project -I 256), mounted with
//     -o prjquota. Without the project feature ext4 refuses any non-zero
//     project id with EOPNOTSUPP (fs/ext4/ioctl.c ext4_ioctl_setproject).
//     The kernel also needs the quota_v2 format (CONFIG_QFMT_V2, module
//     quota_v2): without it the mount itself fails with ESRCH
//     (fs/quota/dquot.c: find_quota_format fails, "return -ESRCH").
//   - XFS project ids above 65535 need the projid32bit feature, the default
//     of mkfs.xfs for years (fs/xfs/xfs_ioctl.c
//     xfs_ioctl_setattr_check_projid returns EINVAL otherwise).
//   - CAP_SYS_ADMIN for SetLimits AND for Usage: check_quotactl_permission
//     in fs/quota/quota.c lets an unprivileged caller read only its own user
//     and group quotas, never a project's.
//   - /etc/projects and /etc/projid are not needed: they are xfs_quota's
//     name tables, and the kernel never reads them.
//
// # On ext4, root is not held to the limit
//
// The generic quota code that ext4 uses lets any writer with
// CAP_SYS_RESOURCE past the hard limits (fs/quota/dquot.c
// ignore_hardlimit: "return capable_noaudit(CAP_SYS_RESOURCE) && ...").
// XFS has no such exemption: root gets ENOSPC like everyone else. A file
// server that writes into an ext4 project directory as root, or with
// CAP_SYS_RESOURCE, is not limited at all; it must write as an
// unprivileged uid, or drop that capability. The integration tests check
// both behaviours.
//
// # The owner of a directory can change its project
//
// Changing a project id with FS_IOC_FSSETXATTR does NOT require privilege:
// vfs_fileattr_set (fs/file_attr.c) asks only inode_owner_or_capable, and
// fileattr_set_prepare forbids a change of project id only to callers
// OUTSIDE the initial user namespace. So the owner of a directory, running
// as an ordinary user on the host, can move it -- or any file they own in
// it -- to another project, or to project 0, and out of the limit.
//
// A provisioner that gives each tenant a project-limited directory must
// therefore NOT give the tenant ownership of the directory or of the files
// in it while the tenant can run code in the initial user namespace. Serve
// it through something that does the writing on the tenant's behalf (a file
// server running under its own uid), or confine the tenant to a user
// namespace, where the kernel refuses the change with EINVAL.
//
// # Units
//
// Limits and usage are in bytes. The kernel ABIs are not: XFS speaks
// 512-byte basic blocks (struct fs_disk_quota) and the generic interface
// 1 KiB blocks for limits (struct if_dqblk, QIF_DQBLKSIZE). Limits are
// rounded UP to those units, exactly as the kernel rounds its own
// conversions (quota_btobb, stoqb), and XFS rounds up again to its
// filesystem block. Usage reads back what the filesystem stored.
//
// Off Linux every operation returns ErrUnsupported.
package projquota
