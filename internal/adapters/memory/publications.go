package memory

import (
	"context"
	"fmt"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
)

var _ ports.PublicationStore = (*Store)(nil)

// publicationKey scopes a publication by all three parts of its target key plus
// its version, so a lookup that forgets one is not expressible.
type publicationKey struct {
	tenant   ports.TenantID
	customer ports.CustomerID
	target   ports.TargetKey
	version  ports.PublicationVersion
}

// Publish appends one publication for a target.
//
// The whole operation holds the write lock, which is what makes reading the
// current version and appending one atomic step. Reading under a read lock and
// then taking the write lock would leave a window in which two publishers both saw
// the same current version and both believed they were its successor — which is
// precisely the silent overwrite §14.1 forbids.
func (s *Store) Publish(ctx context.Context, publication ports.Publication) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePublication(publication); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	target := targetKey{publication.TenantID, publication.CustomerID, publication.Target}
	key := publicationKey{
		publication.TenantID, publication.CustomerID, publication.Target, publication.Version,
	}

	if existing, present := s.publications[key]; present {
		// An identical repeat is a retry, not a rewrite. Anything else would change
		// what a recorded version says was published, which is the one thing a
		// publication record exists to be trusted about.
		if existing == publication {
			return nil
		}
		return fmt.Errorf("%w: version %d is already recorded with different content",
			ports.ErrPublicationConflict, publication.Version)
	}

	if current := s.publicationVersions[target]; publication.Version != current+1 {
		return fmt.Errorf("%w: version %d does not follow the current version %d",
			ports.ErrPublicationConflict, publication.Version, current)
	}

	s.publications[key] = publication
	s.publicationVersions[target] = publication.Version
	return nil
}

// CurrentPublication returns the highest recorded version for a target.
func (s *Store) CurrentPublication(
	ctx context.Context, tenant ports.TenantID, customer ports.CustomerID, target ports.TargetKey,
) (ports.Publication, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.Publication{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	current := s.publicationVersions[targetKey{tenant, customer, target}]
	if current == 0 {
		return ports.Publication{}, false, nil
	}
	publication, present := s.publications[publicationKey{tenant, customer, target, current}]
	return publication, present, nil
}

// PublicationAtVersion resolves one recorded version, which is how a superseded
// publication stays explainable.
func (s *Store) PublicationAtVersion(
	ctx context.Context, tenant ports.TenantID, customer ports.CustomerID,
	target ports.TargetKey, version ports.PublicationVersion,
) (ports.Publication, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.Publication{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	publication, present := s.publications[publicationKey{tenant, customer, target, version}]
	return publication, present, nil
}

// validatePublication refuses a record that could not be audited, separately from
// the conflict checks that need the store's state.
//
// Every pinned identity is required. A publication is only worth anything because
// the decision behind it can be re-derived from what it names, so a record missing
// any one of them is not a weaker record — it is an unverifiable claim that
// something was authorized.
func validatePublication(publication ports.Publication) error {
	switch {
	case publication.TenantID == "":
		return fmt.Errorf("memory: publication has no tenant")
	case publication.CustomerID == "":
		return fmt.Errorf("memory: publication has no customer")
	case publication.Target == "":
		return fmt.Errorf("memory: publication has no target")
	case publication.Version == 0:
		// Zero means never published. Admitting it as a version would make "never
		// published" and "published at version zero" two states to distinguish.
		return fmt.Errorf("memory: publication version is zero")
	case publication.PolicyVersion == 0:
		return fmt.Errorf("memory: publication pins no policy version")
	case publication.ProfileID == "":
		return fmt.Errorf("memory: publication pins no profile")
	case publication.AssessmentID == "":
		return fmt.Errorf("memory: publication pins no assessment")
	case publication.CheckpointArtifactID == "":
		return fmt.Errorf("memory: publication pins no checkpoint")
	case publication.SemanticRunID == "":
		return fmt.Errorf("memory: publication pins no semantic run")
	case publication.ExecutionID == "":
		return fmt.Errorf("memory: publication pins no execution")
	}
	return nil
}
