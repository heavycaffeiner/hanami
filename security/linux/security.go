// Package securitylinux provides optional process security integration.
package securitylinux

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/heavycaffeiner/hanami"
)

var (
	ErrUnsupported         = errors.New("security mechanism unsupported")
	ErrRequired            = errors.New("required security enforcement unavailable")
	ErrHandoffInvalid      = errors.New("security handoff is invalid")
	ErrInvalidDescriptors  = errors.New("descriptor preserve set is invalid")
	ErrUnknownArchitecture = errors.New("unsupported architecture")
	ErrPermissionExpansion = errors.New("security permission expansion requires external restart")
)

const (
	HandoffVersion     uint32 = 1
	HandoffEnvironment        = "HANAMI_SECURITY_HANDOFF"
)

type Handoff struct {
	Version          uint32 `json:"version"`
	PolicyDigest     string `json:"policy_digest"`
	LandlockEnforced bool   `json:"landlock_enforced"`
	SeccompEnforced  bool   `json:"seccomp_enforced"`
	ProbePath        string `json:"probe_path,omitempty"`
	ProbeDev         uint64 `json:"probe_dev,omitempty"`
	ProbeIno         uint64 `json:"probe_ino,omitempty"`
	ProbeMode        uint32 `json:"probe_mode,omitempty"`
	Token            string `json:"token"`
}

func NewHandoff(policy Policy, status Status) Handoff {
	h := Handoff{Version: HandoffVersion, PolicyDigest: policy.Digest(), LandlockEnforced: status.Landlock.Verified, SeccompEnforced: status.Seccomp.Verified}
	h.Token = handoffToken(h)
	return h
}

func EncodeHandoff(h Handoff) (string, error) {
	if err := validateHandoffShape(h); err != nil {
		return "", err
	}
	b, err := json.Marshal(h)
	if err != nil {
		return "", fmt.Errorf("encode security handoff: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func DecodeHandoff(raw string) (Handoff, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Handoff{}, fmt.Errorf("%w: decode: %v", ErrHandoffInvalid, err)
	}
	var h Handoff
	if err := json.Unmarshal(b, &h); err != nil {
		return Handoff{}, fmt.Errorf("%w: parse: %v", ErrHandoffInvalid, err)
	}
	if err := validateHandoffShape(h); err != nil {
		return Handoff{}, err
	}
	if handoffToken(h) != h.Token {
		return Handoff{}, fmt.Errorf("%w: token mismatch", ErrHandoffInvalid)
	}
	return h, nil
}

func VerifyHandoff(raw string, policy Policy) (Handoff, error) {
	h, err := DecodeHandoff(raw)
	if err != nil {
		return Handoff{}, err
	}
	if h.PolicyDigest != policy.Digest() {
		return Handoff{}, fmt.Errorf("%w: policy digest mismatch", ErrHandoffInvalid)
	}
	if policy.wantsLandlock() && !h.LandlockEnforced {
		return Handoff{}, fmt.Errorf("%w: landlock is not verified", ErrHandoffInvalid)
	}
	if err := verifyKernelEnforcement(h, policy); err != nil {
		return Handoff{}, err
	}
	return h, nil
}
func validateHandoffShape(h Handoff) error {
	if h.Version != HandoffVersion || h.PolicyDigest == "" || h.Token == "" {
		return fmt.Errorf("%w: unsupported version or missing fields", ErrHandoffInvalid)
	}
	if h.LandlockEnforced && (h.ProbePath == "" || h.ProbeDev == 0 || h.ProbeIno == 0 || h.ProbeMode == 0) {
		return fmt.Errorf("%w: missing landlock probe identity", ErrHandoffInvalid)
	}
	return nil
}

func handoffToken(h Handoff) string {
	b, _ := json.Marshal(struct {
		Version                 uint32
		PolicyDigest, ProbePath string
		ProbeDev, ProbeIno      uint64
		ProbeMode               uint32
		Landlock, Seccomp       bool
	}{h.Version, h.PolicyDigest, h.ProbePath, h.ProbeDev, h.ProbeIno, h.ProbeMode, h.LandlockEnforced, h.SeccompEnforced})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type PreserveFDSet []int

func (s PreserveFDSet) Validate() error {
	seen := make(map[int]struct{}, len(s))
	for _, fd := range s {
		if fd < 0 {
			return fmt.Errorf("%w: negative fd %d", ErrInvalidDescriptors, fd)
		}
		if _, ok := seen[fd]; ok {
			return fmt.Errorf("%w: duplicate fd %d", ErrInvalidDescriptors, fd)
		}
		seen[fd] = struct{}{}
	}
	return nil
}

func ValidatePreservedFDs(fds []int) error { return PreserveFDSet(fds).Validate() }

type PolicyMode uint8

const (
	ModeOff PolicyMode = iota
	ModePreferred
	ModeRequired
)

const (
	Off       = ModeOff
	Preferred = ModePreferred
	Required  = ModeRequired
)

func (m PolicyMode) String() string {
	switch m {
	case ModeOff:
		return "off"
	case ModePreferred:
		return "preferred"
	case ModeRequired:
		return "required"
	default:
		return "unknown"
	}
}

func (m PolicyMode) valid() bool { return m <= ModeRequired }

// Rights use the kernel Landlock bit values, not local ordinal values.
type Rights uint64

const (
	RightExecute         Rights = 1 << 0
	RightWriteFile       Rights = 1 << 1
	RightReadFile        Rights = 1 << 2
	RightReadDirectory   Rights = 1 << 3
	RightRemoveDirectory Rights = 1 << 4
	RightRemoveFile      Rights = 1 << 5
	RightMakeCharacter   Rights = 1 << 6
	RightMakeDirectory   Rights = 1 << 7
	RightMakeRegular     Rights = 1 << 8
	RightMakeSocket      Rights = 1 << 9
	RightMakeFIFO        Rights = 1 << 10
	RightMakeBlock       Rights = 1 << 11
	RightMakeSymlink     Rights = 1 << 12
	RightRefer           Rights = 1 << 13
	RightTruncate        Rights = 1 << 14
	RightIoctlDevice     Rights = 1 << 15
)

type Grant struct {
	Path   string `json:"path"`
	Access Rights `json:"access"`
}

type Policy struct {
	Mode            PolicyMode `json:"mode"`
	ReadOnlyPaths   []string   `json:"read_only_paths,omitempty"`
	WritablePaths   []string   `json:"writable_paths,omitempty"`
	ExecutablePaths []string   `json:"executable_paths,omitempty"`
	Grants          []Grant    `json:"grants,omitempty"`
	ExceptExec      bool       `json:"except_exec,omitempty"`
	DenySyscalls    []string   `json:"deny_syscalls,omitempty"`
	Landlock        bool       `json:"landlock"`
	Seccomp         bool       `json:"seccomp"`
}

func (p Policy) wantsLandlock() bool {
	return p.Landlock || len(p.ReadOnlyPaths)+len(p.WritablePaths)+len(p.ExecutablePaths)+len(p.Grants) != 0
}
func (p Policy) wantsSeccomp() bool { return p.Seccomp || len(p.DenySyscalls) != 0 }

// Apply validates and installs the requested process-wide mechanisms.
func Apply(policy Policy) (Status, error) { return applyPlatform(policy) }

// CurrentStatus reports the last verified mechanism state for this process.
func CurrentStatus() Status { return currentPlatformStatus() }
func (p Policy) Validate() error {
	if !p.Mode.valid() {
		return fmt.Errorf("security: invalid policy mode %d", p.Mode)
	}
	for name, paths := range map[string][]string{"read-only": p.ReadOnlyPaths, "writable": p.WritablePaths, "executable": p.ExecutablePaths} {
		seen := make(map[string]struct{}, len(paths))
		for _, path := range paths {
			if path == "" || !strings.HasPrefix(path, "/") {
				return fmt.Errorf("security: %s path must be absolute: %q", name, path)
			}
			if _, ok := seen[path]; ok {
				return fmt.Errorf("security: duplicate %s path %q", name, path)
			}
			seen[path] = struct{}{}
		}
	}
	for _, grant := range p.Grants {
		if grant.Path == "" || !strings.HasPrefix(grant.Path, "/") {
			return fmt.Errorf("security: grant path must be absolute: %q", grant.Path)
		}
		if grant.Access == 0 {
			return fmt.Errorf("security: grant access is empty for %q", grant.Path)
		}
	}
	seen := make(map[string]struct{}, len(p.DenySyscalls))
	for _, name := range p.DenySyscalls {
		if name == "" {
			return errors.New("security: empty denied syscall")
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("security: duplicate denied syscall %q", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func (p Policy) normalized() Policy {
	p.ReadOnlyPaths = slices.Clone(p.ReadOnlyPaths)
	p.WritablePaths = slices.Clone(p.WritablePaths)
	p.ExecutablePaths = slices.Clone(p.ExecutablePaths)
	p.Grants = slices.Clone(p.Grants)
	p.DenySyscalls = slices.Clone(p.DenySyscalls)
	sort.Strings(p.ReadOnlyPaths)
	sort.Strings(p.WritablePaths)
	sort.Strings(p.ExecutablePaths)
	sort.Strings(p.DenySyscalls)
	slices.SortFunc(p.Grants, func(a, b Grant) int {
		if a.Path < b.Path {
			return -1
		}
		if a.Path > b.Path {
			return 1
		}
		if a.Access < b.Access {
			return -1
		}
		if a.Access > b.Access {
			return 1
		}
		return 0
	})
	return p
}

func (p Policy) Digest() string {
	data, _ := json.Marshal(p.normalized())
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Status describes what the backend actually established, rather than only
// what the policy requested.
type Status struct {
	Mode                    PolicyMode
	Architecture            string
	Landlock                LayerStatus
	Seccomp                 LayerStatus
	Missing                 []string
	Warnings                []string
	Enforced                bool
	HandoffVerified         bool
	ExternalRestartRequired bool
}

type LayerStatus struct {
	Available bool
	Applied   bool
	Verified  bool
	Error     error
}

func (s Status) String() string {
	parts := []string{"mode=" + s.Mode.String(), "arch=" + s.Architecture,
		fmt.Sprintf("enforced=%t", s.Enforced), fmt.Sprintf("landlock=%t/%t", s.Landlock.Applied, s.Landlock.Verified),
		fmt.Sprintf("seccomp=%t/%t", s.Seccomp.Applied, s.Seccomp.Verified)}
	if len(s.Missing) != 0 {
		parts = append(parts, "missing="+strings.Join(s.Missing, ","))
	}
	if len(s.Warnings) != 0 {
		parts = append(parts, "warnings="+strings.Join(s.Warnings, ","))
	}
	return strings.Join(parts, " ")
}

// Integration is the reusable pre-composition security integration.
type Integration[C any] struct {
	build func(C) (Policy, error)
}

func (i *Integration[C]) Name() string { return "security/linux" }

func (i *Integration[C]) Prepare(ctx context.Context, config C) (hanami.Prepared, error) {
	if err := ctx.Err(); err != nil {
		return hanami.Prepared{}, err
	}
	policy, err := i.build(config)
	if err != nil {
		return hanami.Prepared{}, err
	}
	status, err := Apply(policy)
	if err != nil {
		return hanami.Prepared{}, err
	}
	return hanami.Prepared{Values: []any{status}}, nil
}

// WithPolicy selects security as a pre-composition integration.
func WithPolicy[C any](build func(C) (Policy, error)) hanami.Option[C] {
	return hanami.WithIntegration[C](&Integration[C]{build: build})
}

// WithPolicyValue is useful when policy construction does not depend on config.
func WithPolicyValue[C any](policy Policy) hanami.Option[C] {
	return WithPolicy(func(C) (Policy, error) { return policy, nil })
}

type ChangeKind uint8

const (
	NoChange ChangeKind = iota
	MoreRestrictive
	PermissionExpansion
	ModeChange
)

func (k ChangeKind) String() string {
	switch k {
	case NoChange:
		return "no_change"
	case MoreRestrictive:
		return "more_restrictive"
	case PermissionExpansion:
		return "permission_expansion"
	case ModeChange:
		return "mode_change"
	default:
		return "unknown"
	}
}

type PolicyComparison struct {
	Kind                    ChangeKind
	AddedReadOnlyPaths      []string
	AddedWritablePaths      []string
	AddedExecutablePaths    []string
	AddedSyscalls           []string
	RemovedReadOnlyPaths    []string
	RemovedWritablePaths    []string
	RemovedExecutablePaths  []string
	RemovedSyscalls         []string
	AddedGrants             []Grant
	RemovedGrants           []Grant
	ModeChanged             bool
	LandlockChanged         bool
	SeccompChanged          bool
	ExceptExecChanged       bool
	ExternalRestartRequired bool
}

func CompareEffectivePolicy(current, desired Policy) PolicyComparison {
	current, desired = current.normalized(), desired.normalized()
	result := PolicyComparison{ModeChanged: current.Mode != desired.Mode, LandlockChanged: current.Landlock != desired.Landlock, SeccompChanged: current.Seccomp != desired.Seccomp, ExceptExecChanged: current.ExceptExec != desired.ExceptExec}
	result.AddedReadOnlyPaths, result.RemovedReadOnlyPaths = differences(current.ReadOnlyPaths, desired.ReadOnlyPaths)
	result.AddedWritablePaths, result.RemovedWritablePaths = differences(current.WritablePaths, desired.WritablePaths)
	result.AddedExecutablePaths, result.RemovedExecutablePaths = differences(current.ExecutablePaths, desired.ExecutablePaths)
	result.AddedSyscalls, result.RemovedSyscalls = differences(current.DenySyscalls, desired.DenySyscalls)
	result.AddedGrants, result.RemovedGrants = grantDifferences(current.Grants, desired.Grants)
	if result.ModeChanged {
		result.Kind = ModeChange
	}
	expansion := len(result.AddedReadOnlyPaths)+len(result.AddedWritablePaths)+len(result.AddedExecutablePaths)+len(result.AddedSyscalls)+len(result.AddedGrants) > 0 || (desired.Landlock && !current.Landlock) || (desired.Seccomp && !current.Seccomp)
	restriction := len(result.RemovedReadOnlyPaths)+len(result.RemovedWritablePaths)+len(result.RemovedExecutablePaths)+len(result.RemovedSyscalls)+len(result.RemovedGrants) > 0 || (current.Landlock && !desired.Landlock) || (current.Seccomp && !desired.Seccomp)
	if expansion {
		result.Kind = PermissionExpansion
		result.ExternalRestartRequired = true
	} else if result.Kind == NoChange && restriction {
		result.Kind = MoreRestrictive
	}
	if result.ModeChanged && (desired.Mode == ModeOff || current.Mode == ModeOff) {
		result.ExternalRestartRequired = true
	}
	return result
}

func grantDifferences(current, desired []Grant) (added, removed []Grant) {
	cm := make(map[string]Rights, len(current))
	dm := make(map[string]Rights, len(desired))
	for _, g := range current {
		cm[g.Path] = g.Access
	}
	for _, g := range desired {
		dm[g.Path] = g.Access
	}
	for _, g := range desired {
		if cm[g.Path] != g.Access {
			added = append(added, g)
		}
	}
	for _, g := range current {
		if dm[g.Path] != g.Access {
			removed = append(removed, g)
		}
	}
	return added, removed
}

func grantsExpanded(current, desired []Grant) bool {
	cm := make(map[string]Rights, len(current))
	for _, g := range current {
		cm[g.Path] = g.Access
	}
	for _, g := range desired {
		if cm[g.Path]&g.Access != g.Access {
			return true
		}
	}
	return false
}

func differences(current, desired []string) (added, removed []string) {
	cm, dm := make(map[string]struct{}, len(current)), make(map[string]struct{}, len(desired))
	for _, x := range current {
		cm[x] = struct{}{}
	}
	for _, x := range desired {
		dm[x] = struct{}{}
	}
	for _, x := range desired {
		if _, ok := cm[x]; !ok {
			added = append(added, x)
		}
	}
	for _, x := range current {
		if _, ok := dm[x]; !ok {
			removed = append(removed, x)
		}
	}
	return added, removed
}
func architecture() string { return runtime.GOARCH }
