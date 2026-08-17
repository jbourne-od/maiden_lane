package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// uniqueViolation is PostgreSQL's SQLSTATE for a duplicate key. It is matched on
// the code rather than on message text, which is localized and unstable.
const uniqueViolation = "23505"

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}

var _ ports.PolicyStore = (*Store)(nil)

// PutPolicy appends one version of a target's policy.
//
// The succession check and the insert are one statement, so no window exists in
// which two writers both read the same current version and both believe they are
// its successor. An INSERT ... SELECT whose source yields no row inserts nothing,
// which is how "your version does not follow the current one" is expressed without
// a transaction or a lock the caller has to hold.
//
// The primary key does the rest: a second writer with the same version loses to a
// unique violation rather than overwriting, which is what makes a recorded version
// immutable at the database rather than only in application code.
func (s *Store) PutPolicy(ctx context.Context, policy ports.TargetPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateStoredPolicy(policy); err != nil {
		return err
	}

	tag, err := s.pool.Exec(ctx,
		`INSERT INTO target_policies
		     (tenant_id, customer_id, target_key, version, required_profile_id, format)
		 SELECT $1, $2, $3, $4, $5, $6
		 WHERE $4::bigint = 1 + COALESCE((
		     SELECT max(version) FROM target_policies
		     WHERE tenant_id = $1 AND customer_id = $2 AND target_key = $3
		 ), 0)`,
		string(policy.TenantID), string(policy.CustomerID), string(policy.Target),
		int64(policy.Version), string(policy.RequiredProfileID), storedFormat)
	if err != nil {
		if isUniqueViolation(err) {
			// Another writer took this version between the succession check and
			// the insert. It is the same conflict as a rewrite from the caller's
			// point of view: the belief the write was based on is out of date.
			return s.reportExistingPolicy(ctx, policy)
		}
		return fmt.Errorf("postgres: policy could not be written: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	// Nothing was inserted, so the WHERE clause refused. Either the version is
	// already recorded, in which case an identical retry is allowed, or it does
	// not follow the current one.
	return s.reportExistingPolicy(ctx, policy)
}

// reportExistingPolicy distinguishes a safe retry from a conflict.
//
// A caller that does not know whether its write landed must be able to repeat it,
// so byte-identical content under a recorded version succeeds. Different content
// under a recorded version is an attempt to rewrite history, which every
// publication pinning that version depends on being refused.
func (s *Store) reportExistingPolicy(ctx context.Context, policy ports.TargetPolicy) error {
	recorded, found, err := s.PolicyAtVersion(
		ctx, policy.TenantID, policy.CustomerID, policy.Target, policy.Version)
	if err != nil {
		return err
	}
	if found {
		if recorded == policy {
			return nil
		}
		return fmt.Errorf("%w: version %d is already recorded with different content",
			ports.ErrPolicyConflict, policy.Version)
	}
	return fmt.Errorf("%w: version %d does not follow the current version",
		ports.ErrPolicyConflict, policy.Version)
}

// ActivePolicy returns the highest recorded version for a target.
func (s *Store) ActivePolicy(
	ctx context.Context, tenant ports.TenantID, customer ports.CustomerID, target ports.TargetKey,
) (ports.TargetPolicy, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.TargetPolicy{}, false, err
	}
	return s.scanPolicy(ctx,
		`SELECT version, required_profile_id, format FROM target_policies
		 WHERE tenant_id = $1 AND customer_id = $2 AND target_key = $3
		 ORDER BY version DESC LIMIT 1`,
		tenant, customer, target,
		string(tenant), string(customer), string(target))
}

// PolicyAtVersion resolves one recorded version.
func (s *Store) PolicyAtVersion(
	ctx context.Context, tenant ports.TenantID, customer ports.CustomerID,
	target ports.TargetKey, version ports.PolicyVersion,
) (ports.TargetPolicy, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.TargetPolicy{}, false, err
	}
	return s.scanPolicy(ctx,
		`SELECT version, required_profile_id, format FROM target_policies
		 WHERE tenant_id = $1 AND customer_id = $2 AND target_key = $3 AND version = $4`,
		tenant, customer, target,
		string(tenant), string(customer), string(target), int64(version))
}

// scanPolicy reads one row and rebuilds the policy from its key plus its columns.
//
// The key parts come from the caller's arguments rather than the row, which is
// not laziness: they are what the query matched on, so re-reading them would add
// columns to trust without adding anything to learn.
func (s *Store) scanPolicy(
	ctx context.Context, query string,
	tenant ports.TenantID, customer ports.CustomerID, target ports.TargetKey,
	arguments ...any,
) (ports.TargetPolicy, bool, error) {
	var (
		version int64
		profile string
		format  int
	)
	err := s.pool.QueryRow(ctx, query, arguments...).Scan(&version, &profile, &format)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.TargetPolicy{}, false, nil
	}
	if err != nil {
		return ports.TargetPolicy{}, false, fmt.Errorf("postgres: policy could not be read: %w", err)
	}
	if format != storedFormat {
		// A row this build does not understand is refused rather than
		// interpreted, for the same reason plans and executions refuse one.
		return ports.TargetPolicy{}, false, fmt.Errorf(
			"%w: policy row uses storage format %d, this build understands %d",
			ErrIntegrity, format, storedFormat)
	}
	if version <= 0 {
		// The column has a CHECK, so reaching this means the constraint was
		// removed or bypassed. Refusing beats returning a policy the port says
		// cannot exist.
		return ports.TargetPolicy{}, false, fmt.Errorf(
			"%w: policy row records version %d", ErrIntegrity, version)
	}
	return ports.TargetPolicy{
		TenantID:          tenant,
		CustomerID:        customer,
		Target:            target,
		Version:           ports.PolicyVersion(version),
		RequiredProfileID: semantic.ProfileID(profile),
	}, true, nil
}

func validateStoredPolicy(policy ports.TargetPolicy) error {
	switch {
	case policy.TenantID == "":
		return errors.New("postgres: policy has no tenant")
	case policy.CustomerID == "":
		return errors.New("postgres: policy has no customer")
	case policy.Target == "":
		return errors.New("postgres: policy has no target")
	case policy.Version == 0:
		return errors.New("postgres: policy version is zero")
	case policy.RequiredProfileID == "":
		return errors.New("postgres: policy requires no profile")
	}
	return nil
}
