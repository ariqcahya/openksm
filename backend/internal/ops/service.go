package ops

import (
	"fmt"
	"sync/atomic"
)

// VerifiedIDNBaseVersions lists vendor versions that passed IDC-01..12 compatibility gates.
var VerifiedIDNBaseVersions = map[string]bool{
	"1.2.1": true,
}

type UpgradePreflightInput struct {
	TargetIDNBaseVersion string `json:"target_idnbase_version"`
	HasVerifiedBackup    bool   `json:"has_verified_backup"`
	DigestPinned         bool   `json:"digest_pinned"`
	SchemaCompatible     bool   `json:"schema_compatible"`
	HealthOK             bool   `json:"health_ok"`
}

type UpgradePreflightResult struct {
	Allowed bool     `json:"allowed"`
	Reasons []string `json:"reasons,omitempty"`
}

// CheckUpgradePreflight enforces NFR-REL-03 & FR-026: block upgrade if any safety gate fails.
func CheckUpgradePreflight(in UpgradePreflightInput) UpgradePreflightResult {
	var reasons []string
	if !VerifiedIDNBaseVersions[in.TargetIDNBaseVersion] {
		reasons = append(reasons, fmt.Sprintf("unverified target IDNBase version %q (UNKNOWN)", in.TargetIDNBaseVersion))
	}
	if !in.HasVerifiedBackup {
		reasons = append(reasons, "verified backup required before upgrade")
	}
	if !in.DigestPinned {
		reasons = append(reasons, "signed/pinned image digest required")
	}
	if !in.SchemaCompatible {
		reasons = append(reasons, "schema compatibility gate failed")
	}
	if !in.HealthOK {
		reasons = append(reasons, "system health check must pass")
	}
	return UpgradePreflightResult{
		Allowed: len(reasons) == 0,
		Reasons: reasons,
	}
}

var RequestCounter atomic.Int64
