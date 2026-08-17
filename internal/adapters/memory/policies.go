package memory

import (
	"context"
	"fmt"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
)

var _ ports.PolicyStore = (*Store)(nil)

// policyKey scopes a policy by all three parts of its target key plus its
// version. Every part is in the key rather than a field to compare, so a lookup
// that forgets one is not expressible.
type policyKey struct {
	tenant   ports.TenantID
	customer ports.CustomerID
	target   ports.TargetKey
	version  ports.PolicyVersion
}

// targetKey identifies the target a policy history belongs to.
type targetKey struct {
	tenant   ports.TenantID
	customer ports.CustomerID
	target   ports.TargetKey
}

// PutPolicy appends one version of a target's policy.
//
// The whole operation holds the write lock, which is what makes the read of the
// current version and the append one atomic step. Reading the current version
// under a read lock and then taking the write lock would leave a window in which
// two writers both saw the same current version and both believed they were the
// successor.
func (s *Store) PutPolicy(ctx context.Context, policy ports.TargetPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePolicy(policy); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	target := targetKey{policy.TenantID, policy.CustomerID, policy.Target}
	key := policyKey{policy.TenantID, policy.CustomerID, policy.Target, policy.Version}

	if existing, present := s.policies[key]; present {
		// An identical rewrite is a retry, not a rewrite of history. Anything
		// else is an attempt to change what a recorded version said, which every
		// publication pinning that version depends on not happening.
		if existing == policy {
			return nil
		}
		return fmt.Errorf("%w: version %d is already recorded with different content",
			ports.ErrPolicyConflict, policy.Version)
	}

	if current := s.policyVersions[target]; policy.Version != current+1 {
		return fmt.Errorf("%w: version %d does not follow the current version %d",
			ports.ErrPolicyConflict, policy.Version, current)
	}

	s.policies[key] = policy
	s.policyVersions[target] = policy.Version
	return nil
}

// ActivePolicy returns the highest recorded version for a target.
func (s *Store) ActivePolicy(
	ctx context.Context, tenant ports.TenantID, customer ports.CustomerID, target ports.TargetKey,
) (ports.TargetPolicy, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.TargetPolicy{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	current := s.policyVersions[targetKey{tenant, customer, target}]
	if current == 0 {
		return ports.TargetPolicy{}, false, nil
	}
	policy, present := s.policies[policyKey{tenant, customer, target, current}]
	return policy, present, nil
}

// PolicyAtVersion resolves one recorded version, which is how a publication's
// pinned version is read back.
func (s *Store) PolicyAtVersion(
	ctx context.Context, tenant ports.TenantID, customer ports.CustomerID,
	target ports.TargetKey, version ports.PolicyVersion,
) (ports.TargetPolicy, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.TargetPolicy{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	policy, present := s.policies[policyKey{tenant, customer, target, version}]
	return policy, present, nil
}

// validatePolicy refuses a policy that is structurally unusable, separately from
// the conflict checks that need the store's state.
func validatePolicy(policy ports.TargetPolicy) error {
	switch {
	case policy.TenantID == "":
		return fmt.Errorf("memory: policy has no tenant")
	case policy.CustomerID == "":
		return fmt.Errorf("memory: policy has no customer")
	case policy.Target == "":
		return fmt.Errorf("memory: policy has no target")
	case policy.Version == 0:
		// Zero is not a policy. Admitting it would make "no policy" and "version
		// zero" two states every reader has to distinguish.
		return fmt.Errorf("memory: policy version is zero")
	case policy.RequiredProfileID == "":
		// A policy whose whole job is to bind a required profile, without one,
		// would authorize publication against nothing.
		return fmt.Errorf("memory: policy requires no profile")
	}
	return nil
}
