//go:build linux

// Command vminit is the init of the virtual machine that runs this package's
// tests on a chosen kernel (next/deploy/ci/sandbox-kernel.sh): it mounts
// the pseudo file systems, brings up loopback, runs /sandbox.test as uid 1000
// (the image's sub2api user) with the arguments the kernel passes to init,
// prints the exit code and powers the machine off.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func main() {
	code := run()
	fmt.Printf("SANDBOX-VM: exit %d\n", code)
	unix.Sync()
	_ = unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
}

func run() int {
	for _, m := range []struct{ src, dir, fs string }{
		{"proc", "/proc", "proc"}, {"sysfs", "/sys", "sysfs"},
		{"securityfs", "/sys/kernel/security", "securityfs"}, {"devtmpfs", "/dev", "devtmpfs"}, {"tmpfs", "/tmp", "tmpfs"},
	} {
		_ = os.MkdirAll(m.dir, 0o755)
		if err := unix.Mount(m.src, m.dir, m.fs, 0, ""); err != nil {
			fmt.Println("mount", m.dir, err)
			return 1
		}
	}
	if err := os.Chmod("/tmp", 0o1777); err != nil {
		fmt.Println(err)
		return 1
	}
	if err := loopbackUp(); err != nil {
		fmt.Println("loopback:", err)
		return 1
	}
	var u unix.Utsname
	_ = unix.Uname(&u)
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	lsm, _ := os.ReadFile("/sys/kernel/security/lsm")
	fmt.Printf("SANDBOX-VM: kernel %s, Landlock ABI %d (errno %d), lsm %q\n", unix.ByteSliceToString(u.Release[:]), int(abi), errno, lsm)

	// The kernel passes what follows "--" on its command line to init.
	cmd := exec.Command("/sandbox.test", append([]string{"-test.v", "-test.count=1"}, os.Args[1:]...)...)
	cmd.Dir = "/tmp"
	cmd.Env = []string{"PATH=/", "HOME=/tmp", "TMPDIR=/tmp"}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1000, Gid: 1000}}
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Println(err)
		return 1
	}
	return 0
}

func loopbackUp() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	ifr, err := unix.NewIfreq("lo")
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, ifr); err != nil {
		return err
	}
	ifr.SetUint16(ifr.Uint16() | unix.IFF_UP)
	return unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifr)
}
