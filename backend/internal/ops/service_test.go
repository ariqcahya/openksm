package ops

import "testing"

func TestCheckUpgradePreflight_BlocksUnsafeUpgrades(t *testing.T) {
	// Unknown version must be blocked (NFR-REL-03)
	res := CheckUpgradePreflight(UpgradePreflightInput{
		TargetIDNBaseVersion: "9.9.9",
		HasVerifiedBackup:    true,
		DigestPinned:         true,
		SchemaCompatible:     true,
		HealthOK:             true,
	})
	if res.Allowed || len(res.Reasons) == 0 {
		t.Fatal("expected unverified IDNBase version to be blocked")
	}

	// Missing backup must be blocked
	resNoBackup := CheckUpgradePreflight(UpgradePreflightInput{
		TargetIDNBaseVersion: "1.2.1",
		HasVerifiedBackup:    false,
		DigestPinned:         true,
		SchemaCompatible:     true,
		HealthOK:             true,
	})
	if resNoBackup.Allowed {
		t.Fatal("expected missing backup to block upgrade")
	}

	// All gates satisfied -> allowed
	okRes := CheckUpgradePreflight(UpgradePreflightInput{
		TargetIDNBaseVersion: "1.2.1",
		HasVerifiedBackup:    true,
		DigestPinned:         true,
		SchemaCompatible:     true,
		HealthOK:             true,
	})
	if !okRes.Allowed {
		t.Fatalf("expected valid upgrade preflight to be allowed, got %v", okRes.Reasons)
	}
}
