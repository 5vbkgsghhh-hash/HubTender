package repository

import (
	"github.com/su10/hubtender/backend/internal/quality"
	"testing"
)

func TestMCPPricingPolicyUsesExactlySelectedActiveRules(t *testing.T) {
	want := map[string]bool{"A": true, "AB": true, "F": true, "B": true, "M": true, "R": true, "S": true, "U": true, "H": true, "YD": true, "Y": true, "I": true, "J": true, "L": true, "QA": true, "AA": true, "N": true}
	active := map[string]bool{}
	for _, rule := range quality.Active() {
		active[rule.Code] = true
	}
	for _, code := range directPricingRuleCodes {
		if !want[code] || !active[code] {
			t.Fatalf("unexpected/inactive rule %s", code)
		}
		delete(want, code)
	}
	if len(want) != 0 {
		t.Fatalf("missing rules: %v", want)
	}
}
