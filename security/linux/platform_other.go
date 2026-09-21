//go:build !linux

package securitylinux

import (
	"fmt"
	"runtime"
)

func applyPlatform(policy Policy) (Status, error) {
	if err := policy.Validate(); err != nil {
		return Status{}, err
	}
	status := Status{Mode: policy.Mode, Architecture: runtime.GOARCH}
	if policy.Mode == ModeOff {
		return status, nil
	}
	if policy.wantsLandlock() {
		status.Missing = append(status.Missing, "landlock")
	}
	if policy.wantsSeccomp() {
		status.Missing = append(status.Missing, "seccomp")
	}
	status.Warnings = append(status.Warnings, status.Missing...)
	if policy.Mode == ModeRequired && len(status.Missing) != 0 {
		return status, fmt.Errorf("%w: %v", ErrRequired, status.Missing)
	}
	return status, nil
}

func currentPlatformStatus() Status { return Status{Architecture: runtime.GOARCH} }

func verifyKernelEnforcement(Handoff, Policy) error {
	return fmt.Errorf("%w: Linux security is unavailable on %s", ErrUnsupported, runtime.GOOS)
}

func ABIForArchitecture(arch string) (uint32, error) {
	return 0, fmt.Errorf("%w: %s", ErrUnknownArchitecture, arch)
}

func SealDescriptors(preserve PreserveFDSet) error {
	return preserve.Validate()
}
