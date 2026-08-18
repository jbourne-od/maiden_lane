package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

var _ ports.PublicationStore = (*Store)(nil)

// Publish appends one publication for a target.
//
// The succession check and the insert are one statement, so no window exists in
// which two publishers both read the same current version and both believe they
// are its successor. An INSERT ... SELECT whose source yields no row inserts
// nothing, which expresses "your version does not follow the current one" without
// a transaction or a lock the caller has to hold.
//
// The primary key does the rest: a second publisher with the same version loses to
// a unique violation rather than overwriting. That is what makes §14.1's "a
// conflicting publication fails rather than silently overwriting a newer result"
// true at the database, not only in application code — which matters because the
// application is not the only thing that can hold a connection.
func (s *Store) Publish(ctx context.Context, publication ports.Publication) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateStoredPublication(publication); err != nil {
		return err
	}

	tag, err := s.pool.Exec(ctx,
		`INSERT INTO publications
		     (tenant_id, customer_id, target_key, version, policy_version,
		      profile_id, assessment_id, checkpoint_artifact_id,
		      semantic_run_id, execution_id, format)
		 SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		 WHERE $4::bigint = 1 + COALESCE((
		     SELECT max(version) FROM publications
		     WHERE tenant_id = $1 AND customer_id = $2 AND target_key = $3
		 ), 0)`,
		string(publication.TenantID), string(publication.CustomerID), string(publication.Target),
		int64(publication.Version), int64(publication.PolicyVersion),
		string(publication.ProfileID), string(publication.AssessmentID),
		string(publication.CheckpointArtifactID), string(publication.SemanticRunID),
		string(publication.ExecutionID), storedFormat)
	if err != nil {
		if isUniqueViolation(err) {
			// Another publisher took this version between the succession check and
			// the insert. From this caller's point of view it is the same conflict
			// as a rewrite: the belief its write was based on is out of date.
			return s.reportExistingPublication(ctx, publication)
		}
		return fmt.Errorf("postgres: publication could not be written: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	// Nothing was inserted, so the WHERE clause refused. Either this version is
	// already recorded, in which case an identical retry is allowed, or it does not
	// follow the current one.
	return s.reportExistingPublication(ctx, publication)
}

// reportExistingPublication distinguishes a safe retry from a conflict.
//
// A publisher that did not learn whether its write landed must be able to repeat
// it, so identical content under a recorded version succeeds. Different content
// under a recorded version is an attempt to change what a target was published
// with, which is the claim the whole record exists to make reliably.
func (s *Store) reportExistingPublication(ctx context.Context, publication ports.Publication) error {
	recorded, found, err := s.PublicationAtVersion(
		ctx, publication.TenantID, publication.CustomerID, publication.Target, publication.Version)
	if err != nil {
		return err
	}
	if found {
		if recorded == publication {
			return nil
		}
		return fmt.Errorf("%w: version %d is already recorded with different content",
			ports.ErrPublicationConflict, publication.Version)
	}
	return fmt.Errorf("%w: version %d does not follow the current version",
		ports.ErrPublicationConflict, publication.Version)
}

// CurrentPublication returns the highest recorded version for a target: what is
// published there now.
func (s *Store) CurrentPublication(
	ctx context.Context, tenant ports.TenantID, customer ports.CustomerID, target ports.TargetKey,
) (ports.Publication, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.Publication{}, false, err
	}
	return s.scanPublication(ctx,
		`SELECT version, policy_version, profile_id, assessment_id,
		        checkpoint_artifact_id, semantic_run_id, execution_id, format
		 FROM publications
		 WHERE tenant_id = $1 AND customer_id = $2 AND target_key = $3
		 ORDER BY version DESC LIMIT 1`,
		tenant, customer, target,
		string(tenant), string(customer), string(target))
}

// PublicationAtVersion resolves one recorded version.
func (s *Store) PublicationAtVersion(
	ctx context.Context, tenant ports.TenantID, customer ports.CustomerID,
	target ports.TargetKey, version ports.PublicationVersion,
) (ports.Publication, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.Publication{}, false, err
	}
	return s.scanPublication(ctx,
		`SELECT version, policy_version, profile_id, assessment_id,
		        checkpoint_artifact_id, semantic_run_id, execution_id, format
		 FROM publications
		 WHERE tenant_id = $1 AND customer_id = $2 AND target_key = $3 AND version = $4`,
		tenant, customer, target,
		string(tenant), string(customer), string(target), int64(version))
}

// scanPublication reads one row and rebuilds the publication from its key plus its
// columns.
//
// The key parts come from the caller's arguments rather than the row, as they do
// for policies: they are what the query matched on, so re-reading them would add
// columns to trust without adding anything to learn.
func (s *Store) scanPublication(
	ctx context.Context, query string,
	tenant ports.TenantID, customer ports.CustomerID, target ports.TargetKey,
	arguments ...any,
) (ports.Publication, bool, error) {
	var (
		version       int64
		policyVersion int64
		profile       string
		assessment    string
		checkpoint    string
		run           string
		execution     string
		format        int
	)
	err := s.pool.QueryRow(ctx, query, arguments...).Scan(
		&version, &policyVersion, &profile, &assessment,
		&checkpoint, &run, &execution, &format)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Publication{}, false, nil
	}
	if err != nil {
		return ports.Publication{}, false,
			fmt.Errorf("postgres: publication could not be read: %w", err)
	}
	if format != storedFormat {
		// A row this build does not understand is refused rather than interpreted,
		// for the same reason every other table's rows are.
		return ports.Publication{}, false, fmt.Errorf(
			"%w: publication row uses storage format %d, this build understands %d",
			ErrIntegrity, format, storedFormat)
	}

	// Every column below has a CHECK constraint, so reaching any of these means a
	// constraint was removed or the row was written by something that bypassed it.
	// Refusing beats returning a publication the port says cannot exist: a caller
	// given one would treat an unauditable record as an authorization.
	if version <= 0 || policyVersion <= 0 {
		return ports.Publication{}, false, fmt.Errorf(
			"%w: publication row records version %d under policy version %d",
			ErrIntegrity, version, policyVersion)
	}
	if profile == "" || assessment == "" || checkpoint == "" || run == "" || execution == "" {
		return ports.Publication{}, false, fmt.Errorf(
			"%w: publication row at version %d is missing a pinned identity",
			ErrIntegrity, version)
	}

	return ports.Publication{
		TenantID:             tenant,
		CustomerID:           customer,
		Target:               target,
		Version:              ports.PublicationVersion(version),
		PolicyVersion:        ports.PolicyVersion(policyVersion),
		ProfileID:            semantic.ProfileID(profile),
		AssessmentID:         semantic.AssessmentID(assessment),
		CheckpointArtifactID: semantic.CheckpointArtifactID(checkpoint),
		SemanticRunID:        semantic.SemanticRunID(run),
		ExecutionID:          semantic.ExecutionID(execution),
	}, true, nil
}

func validateStoredPublication(publication ports.Publication) error {
	switch {
	case publication.TenantID == "":
		return errors.New("postgres: publication has no tenant")
	case publication.CustomerID == "":
		return errors.New("postgres: publication has no customer")
	case publication.Target == "":
		return errors.New("postgres: publication has no target")
	case publication.Version == 0:
		return errors.New("postgres: publication version is zero")
	case publication.PolicyVersion == 0:
		return errors.New("postgres: publication pins no policy version")
	case publication.ProfileID == "":
		return errors.New("postgres: publication pins no profile")
	case publication.AssessmentID == "":
		return errors.New("postgres: publication pins no assessment")
	case publication.CheckpointArtifactID == "":
		return errors.New("postgres: publication pins no checkpoint")
	case publication.SemanticRunID == "":
		return errors.New("postgres: publication pins no semantic run")
	case publication.ExecutionID == "":
		return errors.New("postgres: publication pins no execution")
	}
	return nil
}
