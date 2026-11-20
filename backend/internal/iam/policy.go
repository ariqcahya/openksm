package iam

import (
	"errors"
	"time"
)

var (
	ErrForbidden          = errors.New("forbidden: permission denied")
	ErrCrossOrgAccess     = errors.New("forbidden: cross-organization access denied")
	ErrMembershipInactive = errors.New("forbidden: membership is not active or has expired")
	ErrSelfApproval       = errors.New("forbidden: maker and checker must be different principals")
)

// Role constants matching PRD/SRS Section 4.
const (
	RoleInstanceOwner = "instance_owner"
	RoleInstanceAdmin = "instance_admin"
	RoleOrgOwner       = "org_owner"
	RoleOrgAdmin       = "org_admin"
	RoleTreasurer      = "treasurer"
	RoleBillingOfficer = "billing_officer"
	RoleCashier        = "cashier"
	RoleOpsManager     = "ops_manager"
	RoleCollector      = "collection_officer"
	RoleCustomer       = "customer"
)

// Permission constants.
const (
	PermOrgApply          = "organization:apply"
	PermOrgApprove        = "organization:approve"
	PermOrgManage         = "organization:manage"
	PermMembershipInv     = "membership:invite"
	PermMembershipRev     = "membership:revoke"
	PermUnitManage        = "unit:manage"
	PermAreaManage        = "service_area:manage"
	PermCustomerManage    = "customer:manage"
	PermCollectionAssign  = "collection:assign"
	PermCollectionExecute = "collection:execute"
	PermTaskRead          = "task:read"
	PermBillingManage     = "billing:manage"
	PermPaymentRecord     = "payment:record"
	PermPaymentVerify     = "payment:verify"
	PermPaymentReverse    = "payment:reverse"
	PermFinanceRead       = "finance:read"
	PermAuditRead         = "audit:read"
)

var rolePermissions = map[string]map[string]bool{
	RoleInstanceOwner: {
		PermOrgApprove: true,
		PermAuditRead:  true,
	},
	RoleInstanceAdmin: {
		PermOrgApprove: true,
		PermOrgManage:  true,
		PermAuditRead:  true,
	},
	RoleOrgOwner: {
		PermOrgManage:         true,
		PermMembershipInv:     true,
		PermMembershipRev:     true,
		PermUnitManage:        true,
		PermAreaManage:        true,
		PermCustomerManage:    true,
		PermCollectionAssign:  true,
		PermCollectionExecute: true,
		PermTaskRead:          true,
		PermBillingManage:     true,
		PermPaymentRecord:     true,
		PermPaymentVerify:     true,
		PermPaymentReverse:    true,
		PermFinanceRead:       true,
		PermAuditRead:         true,
	},
	RoleOrgAdmin: {
		PermOrgManage:        true,
		PermMembershipInv:    true,
		PermMembershipRev:    true,
		PermUnitManage:       true,
		PermAreaManage:       true,
		PermCustomerManage:   true,
		PermCollectionAssign: true,
		PermTaskRead:         true,
	},
	RoleTreasurer: {
		PermPaymentVerify:  true,
		PermPaymentReverse: true,
		PermFinanceRead:    true,
		PermAuditRead:      true,
	},
	RoleBillingOfficer: {
		PermBillingManage: true,
		PermFinanceRead:   true,
	},
	RoleCashier: {
		PermPaymentRecord: true,
		PermFinanceRead:   true,
	},
	RoleOpsManager: {
		PermUnitManage:       true,
		PermAreaManage:       true,
		PermCollectionAssign: true,
		PermTaskRead:         true,
	},
	RoleCollector: {
		PermCollectionExecute: true,
		PermTaskRead:          true,
	},
}

// RoleGrant represents a user's scoped role within an organization.
type RoleGrant struct {
	RoleCode      string
	ServiceAreaID string // empty means org-wide
	ExpiresAt     *time.Time
}

// MembershipContext represents the server-resolved membership for a single organization.
type MembershipContext struct {
	UserRefID      string
	OrganizationID string
	Status         string // invited, active, suspended, revoked
	ExpiresAt      *time.Time
	Roles          []RoleGrant
}

// Authorize evaluates RBAC + ABAC (organization boundary, membership status, expiration, service area)
// using deny-by-default semantics.
func Authorize(m *MembershipContext, targetOrgID, targetServiceAreaID, permission string, now time.Time) error {
	if m == nil {
		return ErrForbidden
	}
	if m.OrganizationID != targetOrgID {
		return ErrCrossOrgAccess
	}
	if m.Status != "active" {
		return ErrMembershipInactive
	}
	if m.ExpiresAt != nil && !m.ExpiresAt.After(now) {
		return ErrMembershipInactive
	}

	for _, r := range m.Roles {
		if r.ExpiresAt != nil && !r.ExpiresAt.After(now) {
			continue
		}
		if r.ServiceAreaID != "" && targetServiceAreaID != "" && r.ServiceAreaID != targetServiceAreaID {
			continue
		}
		if perms, ok := rolePermissions[r.RoleCode]; ok && perms[permission] {
			return nil
		}
	}
	return ErrForbidden
}

// CheckMakerChecker enforces separation of duties: initiator cannot approve their own request.
func CheckMakerChecker(initiatorUserID, approverUserID string) error {
	if initiatorUserID == "" || approverUserID == "" || initiatorUserID == approverUserID {
		return ErrSelfApproval
	}
	return nil
}
