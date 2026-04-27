package orchestration

import (
	"context"
	"errors"

	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
)

// ErrForbidden is returned when the calling subject does not own the
// addressed run and is not an admin. Handlers map this to HTTP 403.
var ErrForbidden = errors.New("orchestration: forbidden")

// CheckRunOwner verifies that the calling subject is authorized to operate
// on the given run. The owner is recorded on the run at begin time as
// either an OIDC subject or an API key id. Subjects with the admin role
// bypass the check (consistent with the existing tsioctl admin escape
// hatch).
//
// Returns nil on a match, ErrForbidden on a mismatch.
func CheckRunOwner(_ context.Context, run *Run, subject authapi.Subject) error {
	if run == nil {
		return ErrForbidden
	}

	// Admin-key bypass.
	if subject.Role == policy.RoleAdmin {
		return nil
	}

	switch subject.Kind {
	case "oidc", "session":
		if run.OwnerOIDCSubject != nil && subject.OIDCSubject != "" && *run.OwnerOIDCSubject == subject.OIDCSubject {
			return nil
		}
	case "apikey":
		if run.OwnerAPIKeyID != nil && *run.OwnerAPIKeyID == subject.APIKeyID {
			return nil
		}
	}

	return ErrForbidden
}
