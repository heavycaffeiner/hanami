//go:build linux

package securitylinux

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

var platformState struct {
	sync.RWMutex
	status Status
	policy Policy
}

const (
	landlockCreateRulesetVersion = 1
	seccompDataArchOffset        = 4
	seccompDataNrOffset          = 0
	seccompRetAllow              = 0x7fff0000
	seccompRetErrno              = 0x00050000
)

type probeIdentity struct {
	Path string
	Dev  uint64
	Ino  uint64
	Mode uint32
}

func landlockABI() (uint32, error) {
	version, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, 1, 0, 0, 0)
	if errno != 0 {
		return 0, errno
	}
	return uint32(version), nil
}

func landlockAvailable() bool { _, err := landlockABI(); return err == nil }

func applyPlatform(policy Policy) (Status, error) {
	if err := policy.Validate(); err != nil {
		return Status{}, err
	}
	status := Status{Mode: policy.Mode, Architecture: runtime.GOARCH}
	if policy.Mode == ModeOff {
		setPlatformStatus(policy, status)
		return status, nil
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		status.Missing = append(status.Missing, "architecture:"+runtime.GOARCH)
		return finishPlatform(policy, status)
	}
	if policy.wantsLandlock() {
		status.Landlock.Available = landlockAvailable()
		if !status.Landlock.Available {
			status.Landlock.Error = ErrUnsupported
			status.Missing = append(status.Missing, "landlock")
		} else if raw := os.Getenv(HandoffEnvironment); raw != "" {
			if _, err := VerifyHandoff(raw, policy); err != nil {
				status.Landlock.Error = err
				status.Missing = append(status.Missing, "landlock-proof")
			} else {
				status.Landlock.Applied, status.Landlock.Verified = true, true
				status.HandoffVerified = true
				_ = os.Unsetenv(HandoffEnvironment)
			}
		} else {
			if err := ReexecForPolicy(policy); err != nil {
				status.Landlock.Error = err
				status.Missing = append(status.Missing, "landlock-reexec")
			}
		}
	}
	if policy.wantsSeccomp() {
		status.Seccomp.Available = true
		if _, err := ABIForArchitecture(runtime.GOARCH); err != nil {
			status.Seccomp.Available = false
			status.Seccomp.Error = err
			status.Missing = append(status.Missing, "seccomp-architecture")
		} else if err := applySeccomp(policy.DenySyscalls); err != nil {
			status.Seccomp.Error = fmt.Errorf("apply seccomp: %w", err)
			status.Missing = append(status.Missing, "seccomp")
		} else {
			status.Seccomp.Applied, status.Seccomp.Verified = true, seccompEnforced()
			if !status.Seccomp.Verified {
				status.Missing = append(status.Missing, "seccomp-verification")
			}
		}
	}
	return finishPlatform(policy, status)
}

func finishPlatform(policy Policy, status Status) (Status, error) {
	status.Enforced = len(status.Missing) == 0 && (!policy.wantsLandlock() || status.Landlock.Verified) && (!policy.wantsSeccomp() || status.Seccomp.Verified)
	if status.Mode == ModePreferred && !status.Enforced {
		status.Warnings = append(status.Warnings, status.Missing...)
	}
	setPlatformStatus(policy, status)
	if status.Mode == ModeRequired && !status.Enforced {
		return status, fmt.Errorf("%w: %s", ErrRequired, strings.Join(status.Missing, ", "))
	}
	return status, nil
}

func setPlatformStatus(policy Policy, status Status) {
	platformState.Lock()
	platformState.policy, platformState.status = policy.normalized(), status
	platformState.Unlock()
}

func currentPlatformStatus() Status {
	platformState.RLock()
	defer platformState.RUnlock()
	status := platformState.status
	status.Missing = append([]string(nil), status.Missing...)
	status.Warnings = append([]string(nil), status.Warnings...)
	return status
}

// ReexecForPolicy applies Landlock to the locked OS thread, records a kernel
// backed denial probe, closes all unlisted descriptors, and replaces image.
func ReexecForPolicy(policy Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if !policy.wantsLandlock() {
		return errors.New("security: policy does not select landlock")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return fmt.Errorf("%w: %s", ErrUnknownArchitecture, runtime.GOARCH)
	}
	probe, err := selectProbe(policy)
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	rulesetFD, err := buildLandlock(policy)
	if err != nil {
		return fmt.Errorf("build landlock ruleset: %w", err)
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		_ = unix.Close(rulesetFD)
		return err
	}
	if err := restrictLandlock(rulesetFD); err != nil {
		_ = unix.Close(rulesetFD)
		return fmt.Errorf("restrict landlock: %w", err)
	}
	handoff := Handoff{Version: HandoffVersion, PolicyDigest: policy.Digest(), LandlockEnforced: true, ProbePath: probe.Path, ProbeDev: probe.Dev, ProbeIno: probe.Ino, ProbeMode: probe.Mode}
	handoff.Token = handoffToken(handoff)
	raw, err := EncodeHandoff(handoff)
	if err != nil {
		_ = unix.Close(rulesetFD)
		return err
	}
	if err := SealDescriptors(PreserveFDSet{0, 1, 2}); err != nil {
		_ = unix.Close(rulesetFD)
		return err
	}
	if err := os.Setenv(HandoffEnvironment, raw); err != nil {
		_ = unix.Close(rulesetFD)
		return err
	}
	argv0, err := os.Executable()
	if err != nil {
		_ = unix.Close(rulesetFD)
		return err
	}
	return unix.Exec(argv0, os.Args, os.Environ())
}

func selectProbe(policy Policy) (probeIdentity, error) {
	for _, path := range []string{"/proc/1/status", "/proc/self/status", "/etc/hosts"} {
		if pathGranted(policy, path) {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		return probeIdentity{Path: path, Dev: uint64(st.Dev), Ino: uint64(st.Ino), Mode: uint32(info.Mode())}, nil
	}
	return probeIdentity{}, fmt.Errorf("%w: policy grants all denial probes", ErrRequired)
}

func pathGranted(policy Policy, target string) bool {
	for _, path := range append(append(append([]string{}, policy.ReadOnlyPaths...), policy.WritablePaths...), policy.ExecutablePaths...) {
		if path == "/" || target == path || strings.HasPrefix(target, strings.TrimSuffix(path, "/")+"/") {
			return true
		}
	}
	for _, grant := range policy.Grants {
		if grant.Path == "/" || target == grant.Path || strings.HasPrefix(target, strings.TrimSuffix(grant.Path, "/")+"/") {
			return true
		}
	}
	return false
}

func buildLandlock(policy Policy) (int, error) {
	abi, err := landlockABI()
	if err != nil {
		return -1, err
	}
	handled := supportedFilesystemRights(abi)
	if policy.ExceptExec {
		handled &^= uint64(RightExecute)
	}
	var attr unix.LandlockRulesetAttr
	attr.Access_fs = handled
	fd, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0, 0, 0, 0)
	if errno != 0 {
		return -1, errno
	}
	add := func(path string, rights uint64) error { return addLandlockRule(int(fd), path, rights&attr.Access_fs) }
	readRights := uint64(RightReadFile | RightReadDirectory)
	if !policy.ExceptExec {
		readRights |= uint64(RightExecute)
	}
	for _, path := range policy.ReadOnlyPaths {
		if err := add(path, readRights); err != nil {
			_ = unix.Close(int(fd))
			return -1, err
		}
	}
	for _, path := range policy.WritablePaths {
		if err := add(path, handled); err != nil {
			_ = unix.Close(int(fd))
			return -1, err
		}
	}
	for _, path := range policy.ExecutablePaths {
		if policy.ExceptExec {
			_ = unix.Close(int(fd))
			return -1, fmt.Errorf("execute path selected with ExceptExec: %s", path)
		}
		if err := add(path, uint64(RightExecute|RightReadFile|RightReadDirectory)); err != nil {
			_ = unix.Close(int(fd))
			return -1, err
		}
	}
	for _, grant := range policy.Grants {
		if uint64(grant.Access)&^attr.Access_fs != 0 {
			_ = unix.Close(int(fd))
			return -1, fmt.Errorf("unsupported Landlock rights for %s", grant.Path)
		}
		if err := add(grant.Path, uint64(grant.Access)); err != nil {
			_ = unix.Close(int(fd))
			return -1, err
		}
	}
	if executable, err := os.Executable(); err == nil {
		rights := uint64(RightReadFile)
		if !policy.ExceptExec {
			rights |= uint64(RightExecute)
		}
		if err := add(executable, rights); err != nil {
			_ = unix.Close(int(fd))
			return -1, err
		}
	}
	if _, err := os.Stat("/dev/null"); err == nil {
		if err := add("/dev/null", uint64(RightReadFile|RightWriteFile)); err != nil {
			_ = unix.Close(int(fd))
			return -1, err
		}
	}
	return int(fd), nil
}
func addLandlockRule(ruleset int, path string, rights uint64) error {
	fd, err := unix.Open(filepath.Clean(path), unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	rule := unix.LandlockPathBeneathAttr{Allowed_access: rights, Parent_fd: int32(fd)}
	_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func restrictLandlock(rulesetFD int) error {
	_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(rulesetFD), 0, 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func supportedFilesystemRights(abi uint32) uint64 {
	rights := uint64(RightExecute | RightWriteFile | RightReadFile | RightReadDirectory | RightRemoveDirectory | RightRemoveFile | RightMakeCharacter | RightMakeDirectory | RightMakeRegular | RightMakeSocket | RightMakeFIFO | RightMakeBlock | RightMakeSymlink)
	if abi >= 2 {
		rights |= uint64(RightRefer)
	}
	if abi >= 3 {
		rights |= uint64(RightTruncate)
	}
	if abi >= 5 {
		rights |= uint64(RightIoctlDevice)
	}
	return rights
}
func ABIForArchitecture(arch string) (uint32, error) {
	switch arch {
	case "amd64":
		return unix.AUDIT_ARCH_X86_64, nil
	case "arm64":
		return unix.AUDIT_ARCH_AARCH64, nil
	default:
		return 0, fmt.Errorf("%w: %s", ErrUnknownArchitecture, arch)
	}
}

func applySeccomp(denied []string) error {
	filters, err := buildSeccompFilter(runtime.GOARCH, denied)
	if err != nil {
		return err
	}
	prog := unix.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	_, _, errno := unix.Syscall6(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&prog)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func buildSeccompFilter(archName string, denied []string) ([]unix.SockFilter, error) {
	arch, err := ABIForArchitecture(archName)
	if err != nil {
		return nil, err
	}
	filters := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: seccompDataArchOffset},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: arch, Jt: 1, Jf: 0},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: seccompDataNrOffset},
	}
	for _, name := range denied {
		if _, ok := syscallNumber(archName, name); !ok {
			return nil, fmt.Errorf("unknown syscall %q", name)
		}
	}
	n := len(denied)
	filters = append(filters, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K, K: 0x40000000, Jt: uint8(n + 2), Jf: 0})
	for i, name := range denied {
		nr, _ := syscallNumber(archName, name)
		filters = append(filters, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: nr, Jt: uint8(n - i), Jf: 0})
	}
	filters = append(filters, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: seccompRetAllow}, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: seccompRetErrno | uint32(unix.EPERM)}, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS})
	return filters, nil
}

func syscallNumber(arch, name string) (uint32, bool) {
	if arch != "amd64" && arch != "arm64" {
		return 0, false
	}
	var common map[string]uint32
	if arch == "amd64" {
		common = map[string]uint32{"read": 0, "write": 1, "open": 2, "close": 3, "ptrace": 101, "mount": 165, "kexec_load": 246, "process_vm_readv": 310, "process_vm_writev": 311, "kexec_file_load": 320, "bpf": 321, "userfaultfd": 323, "getpid": 39, "openat": 257, "connect": 42, "bind": 49, "execve": 59, "execveat": 322}
	} else {
		common = map[string]uint32{"read": 63, "write": 64, "openat": 56, "close": 57, "ptrace": 117, "mount": 40, "kexec_load": 104, "process_vm_readv": 270, "process_vm_writev": 271, "kexec_file_load": 294, "bpf": 280, "userfaultfd": 282, "getpid": 172, "connect": 203, "bind": 200, "execve": 221, "execveat": 281}
	}
	nr, ok := common[name]
	return nr, ok
}
func seccompEnforced() bool {
	value, _, errno := unix.RawSyscall6(unix.SYS_PRCTL, unix.PR_GET_SECCOMP, 0, 0, 0, 0, 0)
	return errno == 0 && value == 2
}

func verifyKernelEnforcement(h Handoff, policy Policy) error {
	if policy.wantsLandlock() && (!h.LandlockEnforced || verifyLandlockProbe(h, policy) != nil) {
		return fmt.Errorf("%w: landlock kernel state is not verified", ErrHandoffInvalid)
	}
	return nil
}

func verifyLandlockProbe(h Handoff, policy Policy) error {
	if h.ProbePath == "" || h.ProbeDev == 0 || h.ProbeIno == 0 || h.ProbeMode == 0 || pathGranted(policy, h.ProbePath) {
		return ErrHandoffInvalid
	}
	f, err := os.Open(h.ProbePath)
	if err == nil {
		_ = f.Close()
		return fmt.Errorf("%w: probe remained readable", ErrHandoffInvalid)
	}
	if errors.Is(err, syscall.ENOENT) {
		return fmt.Errorf("%w: probe disappeared", ErrHandoffInvalid)
	}
	if !errors.Is(err, syscall.EACCES) && !errors.Is(err, syscall.EPERM) {
		return err
	}
	return nil
}

// SealDescriptors preserves only the named descriptors across exec. Every
// other open descriptor is marked close-on-exec, so the current Go runtime
// keeps its poller and signal descriptors until unix.Exec replaces the image.
func SealDescriptors(preserve PreserveFDSet) error {
	if err := preserve.Validate(); err != nil {
		return err
	}
	allowed := make(map[int]struct{}, len(preserve))
	for _, fd := range preserve {
		allowed[fd] = struct{}{}
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		flags := unix.FD_CLOEXEC
		if _, ok := allowed[fd]; ok {
			flags = 0
		}
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, flags); err != nil && !errors.Is(err, syscall.EBADF) {
			return err
		}
	}
	return nil
}
