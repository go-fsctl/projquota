// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package projquota

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// The integration tests make real XFS and ext4 filesystems in loop-mounted
// image files and drive the real ioctls and quotactl_fd against them. They
// need root, mkfs.xfs, mkfs.ext4, mount and umount, and skip -- naming what
// is missing -- otherwise. The CI "kernel" job runs them as root and fails
// if any of them skipped.
//
//	sudo -E env "PATH=$PATH" go test -count=1 -v -run TestIntegration ./...

const mib = 1 << 20

// requireRoot skips unless the integration environment is there.
func requireRoot(t *testing.T, tools ...string) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: skipped in -short mode")
	}
	if os.Geteuid() != 0 {
		t.Skip("integration: not root; skipping")
	}
	for _, tool := range append([]string{"mount", "umount"}, tools...) {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("integration: %s not on PATH; skipping", tool)
		}
	}
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// worldDir is a directory every user can traverse, so the unprivileged
// helper below can reach the mount and run a copy of this test binary.
func worldDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "projquota-it-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	if err := os.Chmod(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// mountImage makes a filesystem with mkfs in a sparse image of size bytes
// and loop-mounts it with opts. It returns the mount point.
func mountImage(t *testing.T, size int64, opts string, mkfs ...string) string {
	t.Helper()
	base := worldDir(t)
	img := filepath.Join(base, "fs.img")
	f, err := os.Create(img)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	f.Close()
	run(t, mkfs[0], append(mkfs[1:], img)...)
	mnt := filepath.Join(base, "mnt")
	if err := os.Mkdir(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, "mount", "-o", "loop,"+opts, img, mnt)
	t.Cleanup(func() {
		if out, err := exec.Command("umount", mnt).CombinedOutput(); err != nil {
			t.Errorf("umount %s: %v\n%s", mnt, err, out)
		}
	})
	return mnt
}

func TestIntegrationXFS(t *testing.T) {
	requireRoot(t, "mkfs.xfs")
	// 512 MiB: recent xfsprogs refuses filesystems under 300 MB.
	mnt := mountImage(t, 512*mib, "prjquota", "mkfs.xfs", "-q", "-f")
	// XFS reports an exhausted PROJECT quota as a full filesystem:
	// fs/xfs/xfs_trans_dquot.c, "if (xfs_dquot_type(dqp) ==
	// XFS_DQTYPE_PROJ) return -ENOSPC; return -EDQUOT;".
	exercise(t, mnt, XFS, syscall.ENOSPC)
}

func TestIntegrationExt4(t *testing.T) {
	requireRoot(t, "mkfs.ext4")
	// The project feature needs inodes larger than 128 bytes; the quota
	// feature keeps the quota files as hidden inodes, so no quotacheck or
	// quotaon is needed -- but the kernel must have the quota_v2 format
	// (CONFIG_QFMT_V2), or the mount itself fails with ESRCH.
	mnt := mountImage(t, 128*mib, "prjquota", "mkfs.ext4", "-q", "-F", "-O", "quota,project", "-I", "256")
	exercise(t, mnt, Ext4, syscall.EDQUOT)
}

// exercise is the same scenario on either filesystem.
func exercise(t *testing.T, mnt string, want Filesystem, full syscall.Errno) {
	if got, err := Detect(mnt); got != want || err != nil {
		t.Fatalf("Detect(%s) = %v, %v; want %v", mnt, got, err, want)
	}

	const id = 4242
	share := filepath.Join(mnt, "share")
	if err := os.Mkdir(share, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SetProject(share, id, true); err != nil {
		t.Fatalf("SetProject: %v", err)
	}
	if got, inh, err := GetProject(share); got != id || !inh || err != nil {
		t.Fatalf("GetProject = %d, %v, %v; want %d, true", got, inh, err, id)
	}

	// Nothing charged yet.
	q, err := Usage(mnt, id)
	if err != nil {
		t.Fatalf("Usage before: %v", err)
	}
	t.Logf("%v before: %+v", want, q)

	// Hard limits only: statfs then reports the hard limit.
	l := Limits{BlockHard: 8 * mib, InodeHard: 1000}
	if err := SetLimits(mnt, id, l); err != nil {
		t.Fatalf("SetLimits: %v", err)
	}
	q, err = Usage(mnt, id)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if q.Limits != l {
		t.Errorf("Usage limits = %+v, want %+v", q.Limits, l)
	}

	// statfs of the PROJINHERIT directory is the project's view.
	var st unix.Statfs_t
	if err := unix.Statfs(share, &st); err != nil {
		t.Fatal(err)
	}
	if got := uint64(st.Blocks) * uint64(st.Bsize); got != l.BlockHard {
		t.Errorf("statfs(share) size = %d, want the %d-byte hard limit", got, l.BlockHard)
	}
	if uint64(st.Files) != l.InodeHard {
		t.Errorf("statfs(share) files = %d, want %d", st.Files, l.InodeHard)
	}

	// A file created inside inherits the project. It is written by an
	// UNPRIVILEGED process: on ext4 a writer with CAP_SYS_RESOURCE ignores
	// the hard limit (fs/quota/dquot.c ignore_hardlimit), which the first
	// CI run showed by writing 16 MiB as root under an 8 MiB limit.
	if err := os.Chown(share, nobody, nobody); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(share, "data")
	written, werr := fillAsNobody(t, mnt, data, 16*mib)
	t.Logf("%v: uid %d wrote %d bytes before %v", want, nobody, written, werr)
	if werr != full {
		t.Fatalf("writing 16 MiB under an 8 MiB limit ended with %v, want %v", werr, full)
	}
	if written > l.BlockHard || written < l.BlockHard/2 {
		t.Errorf("EDQUOT after %d bytes, want it near the %d-byte limit", written, l.BlockHard)
	}
	if got, inh, err := GetProject(data); got != id || inh || err != nil {
		t.Errorf("GetProject(data) = %d, %v, %v; want %d, false", got, inh, err, id)
	}

	unix.Sync()
	q, err = Usage(mnt, id)
	if err != nil {
		t.Fatalf("Usage after: %v", err)
	}
	t.Logf("%v after: %+v", want, q)
	if q.Bytes < written/2 || q.Bytes > l.BlockHard {
		t.Errorf("Usage bytes = %d after writing %d under %d", q.Bytes, written, l.BlockHard)
	}
	if q.Inodes < 2 { // the directory and the file
		t.Errorf("Usage inodes = %d, want >= 2", q.Inodes)
	}

	// Root past the limit: XFS still refuses, ext4 lets CAP_SYS_RESOURCE
	// through. The package documentation warns about the second.
	_, rerr := fill(filepath.Join(share, "rootdata"), 2*mib)
	t.Logf("%v: root writing past the limit: %v", want, rerr)
	if want == XFS && !errors.Is(rerr, syscall.ENOSPC) {
		t.Errorf("xfs: root wrote past the project limit (%v)", rerr)
	}
	if want == Ext4 && rerr != nil {
		t.Errorf("ext4: root was held to the project limit (%v); CAP_SYS_RESOURCE should ignore it", rerr)
	}
	os.Remove(filepath.Join(share, "rootdata"))

	// A soft limit takes over statfs.
	l.BlockSoft = 4 * mib
	if err := SetLimits(mnt, id, l); err != nil {
		t.Fatalf("SetLimits(soft): %v", err)
	}
	if err := unix.Statfs(share, &st); err != nil {
		t.Fatal(err)
	}
	if got := uint64(st.Blocks) * uint64(st.Bsize); got != l.BlockSoft {
		t.Errorf("statfs(share) size = %d, want the %d-byte soft limit", got, l.BlockSoft)
	}

	// Rounding: 1000 bytes is rounded up, never down.
	if err := SetLimits(mnt, id, Limits{BlockHard: 1000}); err != nil {
		t.Fatalf("SetLimits(1000): %v", err)
	}
	if q, err := Usage(mnt, id); err != nil || q.BlockHard < 1000 || q.BlockHard > 64*1024 {
		t.Errorf("1000-byte limit stored as %d (%v)", q.BlockHard, err)
	}

	// A project nobody ever used.
	if q, err := Usage(mnt, 777777); err != nil || q.Bytes != 0 || q.Inodes != 0 {
		t.Errorf("Usage(unused project) = %+v, %v", q, err)
	}

	// A populated tree.
	tree := filepath.Join(mnt, "tree")
	for _, d := range []string{"tree", "tree/a", "tree/a/b"} {
		if err := os.Mkdir(filepath.Join(mnt, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"tree/f0", "tree/a/f1", "tree/a/b/f2"} {
		if err := os.WriteFile(filepath.Join(mnt, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc/hostname", filepath.Join(tree, "link")); err != nil {
		t.Fatal(err)
	}
	if err := SetProjectTree(tree, 99); err != nil {
		t.Fatalf("SetProjectTree: %v", err)
	}
	for p, wantInh := range map[string]bool{
		"tree": true, "tree/a": true, "tree/a/b": true,
		"tree/f0": false, "tree/a/f1": false, "tree/a/b/f2": false,
	} {
		if got, inh, err := GetProject(filepath.Join(mnt, p)); got != 99 || inh != wantInh || err != nil {
			t.Errorf("GetProject(%s) = %d, %v, %v; want 99, %v", p, got, inh, err, wantInh)
		}
	}
	if got, _, _ := GetProject("/etc/hostname"); got == 99 {
		t.Error("SetProjectTree followed a symlink out of the tree")
	}

	ownerCanEscape(t, mnt)
}

// fill writes zeros to path in 1 MiB chunks until max bytes or an error,
// and returns how much was written and the first error (including the one
// fsync or close reports).
func fill(path string, max uint64) (uint64, error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	buf := make([]byte, mib)
	var n uint64
	for n < max {
		w, err := f.Write(buf)
		n += uint64(w)
		if err != nil {
			f.Close()
			return n, err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return n, err
	}
	return n, f.Close()
}

// ownerCanEscape is the positive control for the package documentation's
// warning: an UNPRIVILEGED owner of a project directory, in the initial user
// namespace, can take it out of its project.
const nobody = 65534

// helper runs this test binary's TestHelper as uid nobody with the given
// environment and returns its output.
func helper(t *testing.T, mnt string, env ...string) string {
	t.Helper()
	// A copy of this test binary the unprivileged user can execute.
	bin := filepath.Join(filepath.Dir(mnt), "projquota.test")
	if _, err := os.Stat(bin); err != nil {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		copyFile(t, self, bin)
	}
	cmd := exec.Command(bin, "-test.run=^TestHelper$", "-test.v")
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: nobody, Gid: nobody}}
	out, err := cmd.CombinedOutput()
	t.Logf("helper as uid %d:\n%s", nobody, out)
	if err != nil {
		t.Fatalf("the helper failed: %v", err)
	}
	return string(out)
}

// fillAsNobody runs fill as uid nobody and returns what it wrote and the
// errno it stopped on (0 for none).
func fillAsNobody(t *testing.T, mnt, path string, max uint64) (uint64, syscall.Errno) {
	t.Helper()
	out := helper(t, mnt, "PROJQUOTA_HELPER_FILL="+path, fmt.Sprintf("PROJQUOTA_HELPER_MAX=%d", max))
	var n uint64
	var errno uintptr
	i := strings.Index(out, "FILLED ")
	if i < 0 {
		t.Fatalf("no FILLED line from the helper")
	}
	if _, err := fmt.Sscanf(out[i:], "FILLED %d %d", &n, &errno); err != nil {
		t.Fatalf("helper output: %v", err)
	}
	return n, syscall.Errno(errno)
}

func ownerCanEscape(t *testing.T, mnt string) {
	dir := filepath.Join(mnt, "tenant")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SetProject(dir, 31337, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(dir, nobody, nobody); err != nil {
		t.Fatal(err)
	}

	helper(t, mnt, "PROJQUOTA_HELPER_CLEAR="+dir)
	if got, inh, err := GetProject(dir); got != 0 || inh || err != nil {
		t.Errorf("after the owner's change: project %d inherit %v (%v); want 0, false", got, inh, err)
	}
}

// TestHelper runs only as the unprivileged child of the integration tests.
func TestHelper(t *testing.T) {
	clear, path := os.Getenv("PROJQUOTA_HELPER_CLEAR"), os.Getenv("PROJQUOTA_HELPER_FILL")
	if clear == "" && path == "" {
		t.Skip("helper process for TestIntegration*; not run directly")
	}
	if os.Geteuid() == 0 {
		t.Fatal("the helper must not run as root")
	}
	if clear != "" {
		if err := SetProject(clear, 0, false); err != nil {
			t.Fatalf("the owner could not clear the project: %v", err)
		}
		return
	}
	var max uint64
	fmt.Sscanf(os.Getenv("PROJQUOTA_HELPER_MAX"), "%d", &max)
	n, err := fill(path, max)
	var errno syscall.Errno
	if err != nil && !errors.As(err, &errno) {
		t.Fatalf("fill: %v", err)
	}
	fmt.Printf("FILLED %d %d\n", n, uintptr(errno))
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
