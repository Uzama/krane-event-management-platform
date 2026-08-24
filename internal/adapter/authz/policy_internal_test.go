package authz

import (
	"strings"
	"testing"

	domainauthz "github.com/Uzama/krane-event-management-platform/internal/domain/authz"
)

// validateRoleCoverage is exercised as a pure function here, not against a
// real database: mutating the shared role_permissions table to simulate an
// incomplete seed would race every other package hitting the same
// krane_test instance (CLAUDE.md: no test may assume it's the only
// occupant of the database).

func TestValidateRoleCoverage_AllKnownRolesPresent_ReturnsNil(t *testing.T) {
	permissions := map[permKey]struct{}{
		{role: "admin", resource: domainauthz.ResourceEvent, action: domainauthz.ActionRead}:       {},
		{role: "contributor", resource: domainauthz.ResourceEvent, action: domainauthz.ActionRead}: {},
		{role: "attendee", resource: domainauthz.ResourceEvent, action: domainauthz.ActionRead}:    {},
	}

	if err := validateRoleCoverage(permissions, knownRoles); err != nil {
		t.Errorf("validateRoleCoverage: %v, want nil", err)
	}
}

func TestValidateRoleCoverage_MissingRole_ReturnsError(t *testing.T) {
	// attendee has zero rows -- exactly what an incomplete seed migration
	// would produce.
	permissions := map[permKey]struct{}{
		{role: "admin", resource: domainauthz.ResourceEvent, action: domainauthz.ActionRead}:       {},
		{role: "contributor", resource: domainauthz.ResourceEvent, action: domainauthz.ActionRead}: {},
	}

	err := validateRoleCoverage(permissions, knownRoles)
	if err == nil {
		t.Fatal("validateRoleCoverage: got nil error, want one naming the missing role")
	}
	if !strings.Contains(err.Error(), "attendee") {
		t.Errorf("error %q does not mention the missing role %q", err.Error(), "attendee")
	}
}

func TestValidateRoleCoverage_EmptyPermissions_ReturnsError(t *testing.T) {
	if err := validateRoleCoverage(map[permKey]struct{}{}, knownRoles); err == nil {
		t.Error("validateRoleCoverage: got nil error for a completely empty seed, want one")
	}
}
