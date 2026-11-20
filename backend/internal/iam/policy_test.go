package iam

import (
	"errors"
	"testing"
	"time"
)

func TestAuthorize_Scenarios(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	past := now.Add(-1 * time.Hour)

	base := &MembershipContext{
		UserRefID:      "user-1",
		OrganizationID: "org-a",
		Status:         "active",
		ExpiresAt:      &future,
		Roles: []RoleGrant{
			{RoleCode: RoleOrgAdmin, ServiceAreaID: ""},
		},
	}

	if err := Authorize(base, "org-a", "area-1", PermUnitManage, now); err != nil {
		t.Fatalf("expected active org_admin to be authorized, got %v", err)
	}

	// Cross-organization negative test (SEC-001 / NFR-SEC-02)
	if err := Authorize(base, "org-b", "area-1", PermUnitManage, now); !errors.Is(err, ErrCrossOrgAccess) {
		t.Fatalf("expected ErrCrossOrgAccess for org-b, got %v", err)
	}

	// Revoked membership negative test (FR-005)
	revoked := *base
	revoked.Status = "revoked"
	if err := Authorize(&revoked, "org-a", "area-1", PermUnitManage, now); !errors.Is(err, ErrMembershipInactive) {
		t.Fatalf("expected ErrMembershipInactive for revoked membership, got %v", err)
	}

	// Expired membership negative test
	expired := *base
	expired.ExpiresAt = &past
	if err := Authorize(&expired, "org-a", "area-1", PermUnitManage, now); !errors.Is(err, ErrMembershipInactive) {
		t.Fatalf("expected ErrMembershipInactive for expired membership, got %v", err)
	}

	// Service-area ABAC scope negative test
	scoped := &MembershipContext{
		UserRefID:      "user-2",
		OrganizationID: "org-a",
		Status:         "active",
		Roles: []RoleGrant{
			{RoleCode: RoleOpsManager, ServiceAreaID: "area-bandung-utara"},
		},
	}
	if err := Authorize(scoped, "org-a", "area-bandung-selatan", PermUnitManage, now); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for mismatched service area, got %v", err)
	}
	if err := Authorize(scoped, "org-a", "area-bandung-utara", PermUnitManage, now); err != nil {
		t.Fatalf("expected matching service area to be authorized, got %v", err)
	}
}

func TestCheckMakerChecker(t *testing.T) {
	if err := CheckMakerChecker("user-1", "user-1"); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("expected ErrSelfApproval on same principal, got %v", err)
	}
	if err := CheckMakerChecker("user-1", ""); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("expected ErrSelfApproval on empty approver, got %v", err)
	}
	if err := CheckMakerChecker("user-1", "user-2"); err != nil {
		t.Fatalf("expected distinct maker/checker to pass, got %v", err)
	}
}
