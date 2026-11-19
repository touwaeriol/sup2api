//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Landlock restricts what the plugin process can reach (CONTRACTS §43.8):
// files, path-based and abstract unix sockets, signals and, with strict
// network, TCP and UDP ports. The ruleset handles every right the running
// kernel knows (best effort: rights of newer ABIs are dropped) and grants
// only the allow list of landlockRules.
//
// x/sys has the types and most constants but no wrappers for the three
// system calls, so they are called directly.

// Rights of include/uapi/linux/landlock.h that x/sys v0.48 does not define
// (values checked against the header, TestLandlockConstantsMatchUAPI).
const (
	llFSResolveUnix     = 1 << 16 // LANDLOCK_ACCESS_FS_RESOLVE_UNIX, ABI 9
	llNetBindUDP        = 1 << 2  // LANDLOCK_ACCESS_NET_BIND_UDP, ABI 10
	llNetConnectSendUDP = 1 << 3  // LANDLOCK_ACCESS_NET_CONNECT_SEND_UDP, ABI 10
)

// landlockABIRights lists what each Landlock ABI version adds; the kernel
// versions are in CONTRACTS §43.8 (Layers).
var landlockABIRights = []struct {
	abi             int
	fs, net, scoped uint64
}{
	{abi: 1, fs: unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM},
	{abi: 2, fs: unix.LANDLOCK_ACCESS_FS_REFER},
	{abi: 3, fs: unix.LANDLOCK_ACCESS_FS_TRUNCATE},
	{abi: 4, net: unix.LANDLOCK_ACCESS_NET_BIND_TCP | unix.LANDLOCK_ACCESS_NET_CONNECT_TCP},
	{abi: 5, fs: unix.LANDLOCK_ACCESS_FS_IOCTL_DEV},
	{abi: 6, scoped: unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET | unix.LANDLOCK_SCOPE_SIGNAL},
	{abi: 9, fs: llFSResolveUnix},
	{abi: 10, net: llNetBindUDP | llNetConnectSendUDP},
}

// landlockHandled is the ruleset for a kernel of Landlock ABI abi. Network
// rights are handled only with strict network: they are a second layer
// below seccomp, and a plugin started without strict network may dial.
func landlockHandled(abi int, strictNetwork bool) unix.LandlockRulesetAttr {
	var attr unix.LandlockRulesetAttr
	for _, r := range landlockABIRights {
		if r.abi > abi {
			break
		}
		attr.Access_fs |= r.fs
		attr.Access_net |= r.net
		attr.Scoped |= r.scoped
	}
	if !strictNetwork {
		attr.Access_net = 0
	}
	return attr
}

// Rights granted below the allow-listed paths.
const (
	llRead = unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR
	// The plugin binary: plugin-exec executes it after restricting itself.
	llBinary = unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_EXECUTE
	// go-plugin's socket directory: the plugin reserves a name with
	// os.CreateTemp (a regular file it removes), binds its server socket
	// there and connects to the core's broker socket in the same directory.
	llSocketDir = unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | llFSResolveUnix
	// The data directory of a plugin granted "fs.data": files and
	// directories, moves inside it; no sockets, fifos, links or devices.
	llDataDir = llRead | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_REFER
	llDevNull = unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE
	// Rights that apply to a regular file; the others are refused on one.
	llFileRights = unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
)

// timeZoneFiles are read by every plugin: time.Local comes from
// /etc/localtime, and time.LoadLocation reads /usr/share/zoneinfo unless the
// plugin embeds time/tzdata.
var timeZoneFiles = []string{"/usr/share/zoneinfo", "/etc/localtime"}

// caCertPaths are crypto/x509's system roots on Linux (its certFiles and
// certDirectories) plus the trust stores the distributions link them to.
// Only a plugin granted "net" gets them: it makes TLS connections through
// the egress tunnel. SSL_CERT_FILE and SSL_CERT_DIR are added when set.
var caCertPaths = []string{
	"/etc/ssl/certs", "/etc/ssl/cert.pem", "/etc/ssl/ca-bundle.pem", "/etc/pki/tls", "/etc/pki/ca-trust/extracted",
	"/usr/share/ca-certificates", "/usr/local/share/ca-certificates",
}

// envUnixSocketDir is go-plugin's EnvUnixSocketDir: the directory, inside the
// plugin's private run directory, that holds both unix sockets.
const envUnixSocketDir = "PLUGIN_UNIX_SOCKET_DIR"

func applyLandlock(o *execOptions) error {
	if !o.Landlock {
		return nil
	}
	abi, err := landlockABI()
	if err != nil || abi < 1 {
		return nil // no Landlock: the core logged what is missing at startup (LogStatus)
	}
	attr := landlockHandled(abi, o.StrictNetwork)
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("landlock_create_ruleset: %w", errno)
	}
	ruleset := int(fd)
	defer unix.Close(ruleset)

	for _, r := range landlockRules(o, os.Getenv) {
		if err := landlockAllowPath(ruleset, r.path, r.access&attr.Access_fs); err != nil {
			return err
		}
	}
	// No network rule: with strict network every TCP and UDP port is denied.
	// no_new_privs is already set (applyLimits), as Landlock requires.
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), 0, 0); errno != 0 {
		return fmt.Errorf("landlock_restrict_self: %w", errno)
	}
	return nil
}

type landlockRule struct {
	path   string
	access uint64
}

// landlockRules is the allow list (CONTRACTS §43.8): the plugin binary,
// go-plugin's socket directory, /dev/null, time zones, and only when
// granted the data directory ("fs.data") and the CA certificates ("net").
// The rest of the run directory (TMPDIR) and the shared temporary directory
// are not granted.
func landlockRules(o *execOptions, getenv func(string) string) []landlockRule {
	rules := []landlockRule{
		{o.Binary, llBinary},
		{getenv(envUnixSocketDir), llSocketDir},
		// The Go runtime opens /dev/null for a closed standard descriptor.
		{"/dev/null", llDevNull},
	}
	for _, p := range timeZoneFiles {
		rules = append(rules, landlockRule{p, llRead})
	}
	if o.DataDir != "" {
		rules = append(rules, landlockRule{o.DataDir, llDataDir})
	}
	if o.CACerts {
		paths := append([]string{getenv("SSL_CERT_FILE")}, caCertPaths...)
		paths = append(paths, filepath.SplitList(getenv("SSL_CERT_DIR"))...)
		for _, p := range paths {
			rules = append(rules, landlockRule{p, llRead})
		}
	}
	kept := rules[:0]
	for _, r := range rules {
		if r.path != "" {
			kept = append(kept, r)
		}
	}
	return kept
}

// landlockABI returns the Landlock ABI version of the kernel.
func landlockABI() (int, error) {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0, errno
	}
	return int(v), nil
}

// landlockAllowPath grants access below path. A path that does not exist is
// skipped; on anything but a directory only the file rights are granted.
func landlockAllowPath(ruleset int, path string, access uint64) error {
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("landlock: stat %s: %w", path, err)
	}
	if !st.IsDir() {
		access &= llFileRights
	}
	if access == 0 {
		return nil // nothing this kernel handles is granted here
	}
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("landlock: open %s: %w", path, err)
	}
	defer unix.Close(fd)
	attr := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(fd)}
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH,
		uintptr(unsafe.Pointer(&attr)), 0, 0, 0); errno != 0 {
		return fmt.Errorf("landlock_add_rule %s: %w", path, errno)
	}
	return nil
}
