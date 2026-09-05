//go:build linux

package security

import (
	"encoding/json"
	"fmt"
	"os"
)

// SecurityPolicy combines all security settings for a container.
type SecurityPolicy struct {
	Seccomp          *SeccompProfile        `json:"seccomp"`
	Capabilities     *ContainerCapabilities `json:"capabilities"`
	ReadOnlyRootfs   bool                   `json:"read_only_rootfs"`
	NoNewPrivileges  bool                   `json:"no_new_privileges"`
	AppArmor         string                 `json:"apparmor_profile,omitempty"`
	UserNamespace    bool                   `json:"user_namespace"`
	PidNamespace     bool                   `json:"pid_namespace"`
	NetworkNamespace bool                   `json:"network_namespace"`
}

// DefaultPolicy returns a restrictive default security policy.
func DefaultPolicy() *SecurityPolicy {
	return &SecurityPolicy{
		Seccomp:          DefaultProfile(),
		Capabilities:     DefaultCapabilities(),
		ReadOnlyRootfs:   true,
		NoNewPrivileges:  true,
		AppArmor:         "",
		UserNamespace:    true,
		PidNamespace:     true,
		NetworkNamespace: true,
	}
}

// ApplyPolicy applies all security settings to the current process.
func ApplyPolicy(policy *SecurityPolicy) error {
	if policy == nil {
		return fmt.Errorf("security policy is nil")
	}

	// Apply seccomp filter
	if policy.Seccomp != nil {
		if err := ApplySeccomp(policy.Seccomp); err != nil {
			return fmt.Errorf("failed to apply seccomp: %w", err)
		}
	}

	// Apply capabilities
	if policy.Capabilities != nil {
		if err := ApplyCapabilities(policy.Capabilities); err != nil {
			return fmt.Errorf("failed to apply capabilities: %w", err)
		}
	}

	// Apply no new privileges
	if policy.NoNewPrivileges {
		if err := NoNewPrivileges(); err != nil {
			return fmt.Errorf("failed to set no_new_privileges: %w", err)
		}
	}

	return nil
}

// LoadPolicy loads a security policy from a JSON file.
func LoadPolicy(path string) (*SecurityPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read policy file: %w", err)
	}

	var policy SecurityPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("failed to parse policy: %w", err)
	}

	if err := validatePolicy(&policy); err != nil {
		return nil, fmt.Errorf("invalid policy: %w", err)
	}

	return &policy, nil
}

// SavePolicy saves a security policy to a JSON file.
func SavePolicy(policy *SecurityPolicy, path string) error {
	if policy == nil {
		return fmt.Errorf("policy is nil")
	}

	if err := validatePolicy(policy); err != nil {
		return fmt.Errorf("invalid policy: %w", err)
	}

	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal policy: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write policy: %w", err)
	}

	return nil
}

// validatePolicy validates a security policy.
func validatePolicy(policy *SecurityPolicy) error {
	if policy.Seccomp != nil {
		if err := validateProfile(policy.Seccomp); err != nil {
			return fmt.Errorf("invalid seccomp profile: %w", err)
		}
	}

	if policy.Capabilities != nil {
		for _, cap := range policy.Capabilities.Keep {
			if _, err := resolveCapability(cap); err != nil {
				return fmt.Errorf("invalid capability in keep list: %s", cap)
			}
		}
		for _, cap := range policy.Capabilities.Drop {
			if _, err := resolveCapability(cap); err != nil {
				return fmt.Errorf("invalid capability in drop list: %s", cap)
			}
		}
	}

	return nil
}
