package securitylinux

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

func TestForgedHandoffRejected(t *testing.T) {
	policy := Policy{Mode: ModePreferred}
	handoff := NewHandoff(policy, Status{})
	raw, err := EncodeHandoff(handoff)
	if err != nil {
		t.Fatal(err)
	}
	forged := raw[:len(raw)-1] + "A"
	if _, err := VerifyHandoff(forged, policy); !errors.Is(err, ErrHandoffInvalid) {
		t.Fatalf("expected forged handoff refusal, got %v", err)
	}
}

func TestRequiredAndPreferredModesWithoutSelectedLayers(t *testing.T) {
	for _, mode := range []PolicyMode{ModePreferred, ModeRequired} {
		status, err := Apply(Policy{Mode: mode})
		if err != nil {
			t.Fatalf("mode %s: %v", mode, err)
		}
		if !status.Enforced {
			t.Fatalf("mode %s should be enforced when no layers are selected", mode)
		}
	}
	status, err := Apply(Policy{Mode: ModeOff, Landlock: true})
	if err != nil {
		t.Fatal(err)
	}
	if status.Enforced || status.Landlock.Applied || status.Seccomp.Applied {
		t.Fatalf("off mode attempted enforcement: %+v", status)
	}
}

func TestUnknownArchitectureRejected(t *testing.T) {
	if _, err := ABIForArchitecture("mips"); !errors.Is(err, ErrUnknownArchitecture) {
		t.Fatalf("expected unknown architecture error, got %v", err)
	}
}

func TestPreserveDescriptorValidation(t *testing.T) {
	if err := ValidatePreservedFDs([]int{3, 7}); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePreservedFDs([]int{3, 3}); !errors.Is(err, ErrInvalidDescriptors) {
		t.Fatalf("duplicate set accepted: %v", err)
	}
	if err := ValidatePreservedFDs([]int{-1}); !errors.Is(err, ErrInvalidDescriptors) {
		t.Fatalf("negative descriptor accepted: %v", err)
	}
}

func TestSealDescriptorsMarksUnlistedCloseOnExec(t *testing.T) {
	preserved, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer preserved.Close()
	unlisted, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer unlisted.Close()
	preservedFD := int(preserved.Fd())
	unlistedFD := int(unlisted.Fd())
	if err := SealDescriptors(PreserveFDSet{0, 1, 2, preservedFD}); err != nil {
		t.Fatal(err)
	}
	preservedFlags, err := unix.FcntlInt(uintptr(preservedFD), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if preservedFlags&unix.FD_CLOEXEC != 0 {
		t.Fatal("preserved descriptor is close-on-exec")
	}
	unlistedFlags, err := unix.FcntlInt(uintptr(unlistedFD), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if unlistedFlags&unix.FD_CLOEXEC == 0 {
		t.Fatal("unlisted descriptor survives exec")
	}
}

func TestEffectivePolicyExpansionRequiresExternalRestart(t *testing.T) {
	current := Policy{Mode: ModeRequired, ReadOnlyPaths: []string{"/var/lib/app"}, DenySyscalls: []string{"connect"}}
	desired := Policy{Mode: ModeRequired, ReadOnlyPaths: []string{"/var/lib/app", "/etc/app"}}
	comparison := CompareEffectivePolicy(current, desired)
	if comparison.Kind != PermissionExpansion || !comparison.ExternalRestartRequired {
		t.Fatalf("expected expansion requiring external restart: %+v", comparison)
	}
}

func TestProcessMechanismStateOff(t *testing.T) {
	status, err := Apply(Policy{Mode: ModeOff})
	if err != nil {
		t.Fatal(err)
	}
	current := CurrentStatus()
	if current.Mode != ModeOff || current.Enforced || status.Enforced {
		t.Fatalf("unexpected off state: status=%+v current=%+v", status, current)
	}
}

func TestLandlockRightsMatchKernelABI(t *testing.T) {
	if RightExecute != 1 || RightWriteFile != 2 || RightReadFile != 4 || RightReadDirectory != 8 || RightRemoveDirectory != 16 || RightRemoveFile != 32 || RightTruncate != 1<<14 {
		t.Fatalf("Landlock rights mismatch: execute=%x write=%x read=%x dir=%x", RightExecute, RightWriteFile, RightReadFile, RightReadDirectory)
	}
}

func TestExceptExecPolicyCanReadCurrentExecutableForReexec(t *testing.T) {
	policy := Policy{Mode: ModeRequired, Landlock: true, ExceptExec: true}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if pathGranted(policy, executable) {
		t.Fatal("the application policy unexpectedly grants the executable before Hanami adds its reexec rule")
	}
	ruleset, err := buildLandlock(policy)
	if err != nil {
		t.Fatalf("building an ExceptExec ruleset for the current executable: %v", err)
	}
	if err := unix.Close(ruleset); err != nil {
		t.Fatal(err)
	}
}

func TestSeccompDeniedOperationSubprocess(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test disabled in short mode")
	}
	if os.Getenv("HANAMI_SECCOMP_CHILD") == "1" {
		if err := applySeccomp([]string{"getpid"}); err != nil {
			os.Exit(3)
		}
		_, _, errno := unix.RawSyscall(unix.SYS_GETPID, 0, 0, 0)
		if errno != unix.EPERM {
			os.Exit(4)
		}
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestSeccompDeniedOperationSubprocess", "-test.v")
	cmd.Env = append(os.Environ(), "HANAMI_SECCOMP_CHILD=1")
	err := cmd.Run()
	if err != nil {
		t.Fatalf("seccomp denied subprocess failed: %v", err)
	}
}

func TestRestrictedSyscallMappings(t *testing.T) {
	for _, name := range []string{"ptrace", "process_vm_readv", "process_vm_writev", "mount", "kexec_load", "kexec_file_load", "bpf", "userfaultfd"} {
		if nr, ok := syscallNumber("amd64", name); !ok || nr == 0 {
			t.Fatalf("amd64 mapping missing for %s: %d %t", name, nr, ok)
		}
		if nr, ok := syscallNumber("arm64", name); !ok || nr == 0 {
			t.Fatalf("arm64 mapping missing for %s: %d %t", name, nr, ok)
		}
	}
	if _, ok := syscallNumber("amd64", "unknown"); ok {
		t.Fatal("unknown syscall accepted")
	}
}

func TestSeccompFilterX32GuardTargetsKill(t *testing.T) {
	filters, err := buildSeccompFilter("amd64", []string{"getpid"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filters) < 7 {
		t.Fatalf("filter too short: %d", len(filters))
	}
	guard := filters[4]
	if guard.Code != unix.BPF_JMP|unix.BPF_JGE|unix.BPF_K || guard.K != 0x40000000 {
		t.Fatalf("missing x32 guard: %+v", guard)
	}
	target := 4 + 1 + int(guard.Jt)
	if target >= len(filters) || filters[target].Code != unix.BPF_RET|unix.BPF_K || filters[target].K != unix.SECCOMP_RET_KILL_PROCESS {
		t.Fatalf("x32 guard does not target kill: target=%d filter=%+v", target, filters[target])
	}
}
