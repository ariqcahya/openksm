package org

import "testing"

func TestSlugValidation_RegionAgnostic(t *testing.T) {
	valid := []string{"ksm-sleman-01", "tps3r-makassar", "bank-sampah-medan"}
	for _, s := range valid {
		if !slugRegex.MatchString(s) {
			t.Errorf("expected slug %q to be valid", s)
		}
	}
	invalid := []string{"ab", "-bad", "BadUpper", "no spaces"}
	for _, s := range invalid {
		if slugRegex.MatchString(s) {
			t.Errorf("expected slug %q to be invalid", s)
		}
	}
}
