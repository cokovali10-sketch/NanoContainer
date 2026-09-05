//go:build linux

package security

import (
	"encoding/json"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// SeccompProfile represents a seccomp filter configuration.
type SeccompProfile struct {
	DefaultAction string        `json:"defaultAction"`
	Architectures []string      `json:"architectures"`
	Syscalls      []SyscallRule `json:"syscalls"`
}

// SyscallRule defines a rule for a set of syscalls.
type SyscallRule struct {
	Names  []string `json:"names"`
	Action string   `json:"action"`
	Args   []Arg    `json:"args,omitempty"`
}

// Arg represents a condition on a syscall argument.
type Arg struct {
	Index    uint   `json:"index"`
	Value    uint64 `json:"value"`
	ValueTwo uint64 `json:"valueTwo"`
	Op       string `json:"op"`
}

// seccomp action constants
const (
	actionKill   = "SCMP_ACT_KILL_PROCESS"
	actionTrap   = "SCMP_ACT_TRAP"
	actionErrno  = "SCMP_ACT_ERRNO"
	actionAllow  = "SCMP_ACT_ALLOW"
	actionLog    = "SCMP_ACT_LOG"
	actionNotify = "SCMP_ACT_NOTIFY"
)

// seccomp architecture constants
const (
	archAMD64 = "SCMP_ARCH_X86_64"
	archX86   = "SCMP_ARCH_X86"
	archARM64 = "SCMP_ARCH_AARCH64"
)

// seccomp operator constants
const (
	opEqualTo      = "SCMP_CMP_EQ"
	opNotEqualTo   = "SCMP_CMP_NE"
	opGreaterThan  = "SCMP_CMP_GT"
	opGreaterEq    = "SCMP_CMP_GE"
	opLessThan     = "SCMP_CMP_LT"
	opLessEq       = "SCMP_CMP_LE"
	opMaskedEqual  = "SCMP_CMP_MASKED_EQ"
	opMaskedNotEq  = "SCMP_CMP_MASKED_NE"
)

// seccomp syscall number for x86_64
var blockedSyscalls = map[string]int{
	"mount":          165,
	"umount2":        166,
	"pivot_root":     155,
	"reboot":         169,
	"kexec_load":     277,
	"init_module":    175,
	"delete_module":  176,
	"swapon":         114,
	"swapoff":        115,
	"sethostname":    170,
	"setdomainname":  171,
	"iopl":           172,
	"ioperm":         173,
	"pciconfig_read": 278,
	"pciconfig_write":279,
	"usbdevfs":       189,
	"kcmp":           312,
	"process_vm_readv":310,
	"process_vm_writev":311,
	"ptrace":         101,
	"keyctl":         250,
}

// DefaultProfile returns a default seccomp profile that blocks dangerous syscalls.
func DefaultProfile() *SeccompProfile {
	profile := &SeccompProfile{
		DefaultAction: actionAllow,
		Architectures: []string{archAMD64},
		Syscalls:      []SyscallRule{},
	}

	blocked := []string{
		"mount", "umount2", "pivot_root",
		"reboot", "kexec_load",
		"init_module", "delete_module",
		"swapon", "swapoff",
		"sethostname", "setdomainname",
		"iopl", "ioperm",
		"pciconfig_read", "pciconfig_write",
		"usbdevfs",
		"kcmp", "process_vm_readv", "process_vm_writev",
		"ptrace", "keyctl",
	}

	profile.Syscalls = append(profile.Syscalls, SyscallRule{
		Names:  blocked,
		Action: actionKill,
	})

	return profile
}

// ApplySeccomp applies a seccomp filter to the current process using the seccomp(2) syscall.
func ApplySeccomp(profile *SeccompProfile) error {
	if profile == nil {
		return fmt.Errorf("seccomp profile is nil")
	}

	defaultAction, err := parseAction(profile.DefaultAction)
	if err != nil {
		return fmt.Errorf("invalid default action %q: %w", profile.DefaultAction, err)
	}

	architectures := profile.Architectures
	if len(architectures) == 0 {
		architectures = []string{archAMD64}
	}

	for _, archName := range architectures {
		archNum, err := parseArch(archName)
		if err != nil {
			return fmt.Errorf("invalid architecture %q: %w", archName, err)
		}

		for _, rule := range profile.Syscalls {
			ruleAction, err := parseAction(rule.Action)
			if err != nil {
				return fmt.Errorf("invalid action %q for syscall %v: %w", rule.Action, rule.Names, err)
			}

			for _, syscallName := range rule.Names {
				syscallNum, ok := blockedSyscalls[syscallName]
				if !ok {
					return fmt.Errorf("unknown syscall %q", syscallName)
				}

				if err := addSeccompRule(archNum, syscallNum, ruleAction, rule.Args); err != nil {
					return fmt.Errorf("failed to add rule for %s: %w", syscallName, err)
				}
			}
		}

		_ = defaultAction
		_ = archNum
	}

	return nil
}

// addSeccompRule adds a single seccomp rule via the prctl interface.
func addSeccompRule(arch, syscallNum, action int, args []Arg) error {
	// Use seccomp syscall with SECCOMP_SET_MODE_FILTER
	// In practice, we build a BPF program for each rule.
	// Here we use a simplified approach via prctl + seccomp.

	if len(args) > 0 {
		// Build a BPF program with argument comparisons.
		bpfProg := buildBPFWithArgs(arch, syscallNum, action, args)
		return loadBPF(bpfProg)
	}

	// Simple case: no argument checks, use basic BPF.
	bpfProg := buildSimpleBPF(arch, syscallNum, action)
	return loadBPF(bpfProg)
}

// buildSimpleBPF creates a minimal BPF program for a single syscall rule.
func buildSimpleBPF(arch, syscallNum, action int) []syscall.SockFilter {
	return []syscall.SockFilter{
		// Load architecture
		{Op: syscall.BPF_LD | syscall.BPF_W | syscall.BPF_ABS, Jt: 0, Jf: 0, K: 0},
		// Check architecture
		{Op: syscall.BPF_JMP | syscall.BPF_JEQ | syscall.BPF_K, Jt: 1, Jf: 0, K: uint32(arch)},
		// Mismatch: allow
		{Op: syscall.BPF_RET | syscall.BPF_K, Jt: 0, Jf: 0, K: uint32(actionAllowRet())},
		// Load syscall number
		{Op: syscall.BPF_LD | syscall.BPF_W | syscall.BPF_ABS, Jt: 0, Jf: 0, K: 4},
		// Check syscall number
		{Op: syscall.BPF_JMP | syscall.BPF_JEQ | syscall.BPF_K, Jt: 1, Jf: 0, K: uint32(syscallNum)},
		// Mismatch: allow
		{Op: syscall.BPF_RET | syscall.BPF_K, Jt: 0, Jf: 0, K: uint32(actionAllowRet())},
		// Match: apply rule action
		{Op: syscall.BPF_RET | syscall.BPF_K, Jt: 0, Jf: 0, K: uint32(action)},
	}
}

// buildBPFWithArgs creates a BPF program with argument-based conditions.
func buildBPFWithArgs(arch, syscallNum, action int, args []Arg) []syscall.SockFilter {
	prog := []syscall.SockFilter{
		// Load architecture
		{Op: syscall.BPF_LD | syscall.BPF_W | syscall.BPF_ABS, Jt: 0, Jf: 0, K: 0},
		// Check architecture
		{Op: syscall.BPF_JMP | syscall.BPF_JEQ | syscall.BPF_K, Jt: 1, Jf: 0, K: uint32(arch)},
		// Mismatch: allow
		{Op: syscall.BPF_RET | syscall.BPF_K, Jt: 0, Jf: 0, K: uint32(actionAllowRet())},
		// Load syscall number
		{Op: syscall.BPF_LD | syscall.BPF_W | syscall.BPF_ABS, Jt: 0, Jf: 0, K: 4},
		// Check syscall number
		{Op: syscall.BPF_JMP | syscall.BPF_JEQ | syscall.BPF_K, Jt: 0, Jf: 1, K: uint32(syscallNum)},
		// Syscall mismatch: allow
		{Op: syscall.BPF_RET | syscall.BPF_K, Jt: 0, Jf: 0, K: uint32(actionAllowRet())},
	}

	// Add argument checks. Argument offsets: arch(0) + num(4) + arg0(16) + arg1(24) + ...
	offset := uint32(16) // start of arg0 in seccomp_data
	for _, arg := range args {
		argOffset := offset + uint32(arg.Index)*8
		op, err := parseOp(arg.Op)
		if err != nil {
			continue
		}
		// Load argument value
		prog = append(prog, syscall.SockFilter{
			Op: syscall.BPF_LD | syscall.BPF_W | syscall.BPF_ABS, Jt: 0, Jf: 0, K: argOffset,
		})
		// Compare
		prog = append(prog, syscall.SockFilter{
			Op: syscall.BPF_JMP | op | syscall.BPF_K, Jt: 1, Jf: 0, K: uint32(arg.Value),
		})
	}

	// If all checks passed: apply the action
	prog = append(prog, syscall.SockFilter{
		Op: syscall.BPF_RET | syscall.BPF_K, Jt: 0, Jf: 0, K: uint32(action),
	})

	return prog
}

// actionAllowRet returns the BPF return value for allow.
func actionAllowRet() uint32 {
	return 0x7fff0000 // SECCOMP_RET_ALLOW
}

// loadBPF loads and attaches a BPF program using prctl(PR_SET_SECCOMP).
func loadBPF(prog []syscall.SockFilter) error {
	// Build the sock_fprog struct
	fprog := syscall.SockFprog{
		Len:    uint16(len(prog)),
		Filter: &prog[0],
	}

	// Use prctl to set seccomp mode
	_, _, errno := syscall.Syscall6(
		syscall.SYS_PRCTL,
		uintptr(syscall.PR_SET_SECCOMP),
		uintptr(2), // SECCOMP_MODE_FILTER
		uintptr(unsafe.Pointer(&fprog)),
		0, 0, 0,
	)
	if errno != 0 {
		return fmt.Errorf("prctl PR_SET_SECCOMP failed: %v", errno)
	}

	return nil
}

// LoadProfile loads a seccomp profile from a JSON file.
func LoadProfile(path string) (*SeccompProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read seccomp profile: %w", err)
	}

	var profile SeccompProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, fmt.Errorf("failed to parse seccomp profile: %w", err)
	}

	if err := validateProfile(&profile); err != nil {
		return nil, fmt.Errorf("invalid seccomp profile: %w", err)
	}

	return &profile, nil
}

// SaveProfile saves a seccomp profile to a JSON file.
func SaveProfile(profile *SeccompProfile, path string) error {
	if profile == nil {
		return fmt.Errorf("profile is nil")
	}

	if err := validateProfile(profile); err != nil {
		return fmt.Errorf("invalid profile: %w", err)
	}

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal profile: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write profile: %w", err)
	}

	return nil
}

// validateProfile validates a seccomp profile.
func validateProfile(profile *SeccompProfile) error {
	if profile.DefaultAction == "" {
		return fmt.Errorf("defaultAction is required")
	}

	validActions := map[string]bool{
		actionKill: true, actionTrap: true, actionErrno: true,
		actionAllow: true, actionLog: true, actionNotify: true,
	}

	if !validActions[profile.DefaultAction] {
		return fmt.Errorf("invalid default action: %s", profile.DefaultAction)
	}

	for i, rule := range profile.Syscalls {
		if !validActions[rule.Action] {
			return fmt.Errorf("invalid action %q in syscalls[%d]", rule.Action, i)
		}
		if len(rule.Names) == 0 {
			return fmt.Errorf("empty names in syscalls[%d]", i)
		}
	}

	return nil
}

// parseAction converts a string action to its numeric BPF return value.
func parseAction(action string) (int, error) {
	switch action {
	case actionKill:
		return 0x00030000, nil // SECCOMP_RET_KILL_PROCESS
	case actionTrap:
		return 0x00050000, nil // SECCOMP_RET_TRAP
	case actionErrno:
		return 0x00050000 | 1, nil // SECCOMP_RET_ERRNO + EPERM
	case actionAllow:
		return int(actionAllowRet()), nil
	case actionLog:
		return 0x7ffc0000, nil // SECCOMP_RET_LOG
	case actionNotify:
		return 0x7fc00000, nil // SECCOMP_RET_USER_NOTIF
	default:
		return 0, fmt.Errorf("unknown action: %s", action)
	}
}

// parseArch converts a string architecture to its numeric value.
func parseArch(arch string) (int, error) {
	switch arch {
	case archAMD64:
		return 0xC000003E, nil
	case archX86:
		return 0x00000003, nil
	case archARM64:
		return 0xC00000B7, nil
	default:
		return 0, fmt.Errorf("unknown architecture: %s", arch)
	}
}

// parseOp converts a string operator to its BPF comparison code.
func parseOp(op string) (uint32, error) {
	switch op {
	case opEqualTo:
		return syscall.BPF_JEQ | syscall.BPF_K, nil
	case opNotEqualTo:
		return syscall.BPF_JEQ | syscall.BPF_K, nil
	case opGreaterThan:
		return syscall.BPF_JGT | syscall.BPF_K, nil
	case opGreaterEq:
		return syscall.BPF_JGE | syscall.BPF_K, nil
	case opLessThan:
		return syscall.BPF_JLT | syscall.BPF_K, nil
	case opLessEq:
		return syscall.BPF_JLE | syscall.BPF_K, nil
	case opMaskedEqual:
		return syscall.BPF_JEQ | syscall.BPF_K, nil
	case opMaskedNotEq:
		return syscall.BPF_JEQ | syscall.BPF_K, nil
	default:
		return 0, fmt.Errorf("unknown operator: %s", op)
	}
}
