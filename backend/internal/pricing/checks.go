package pricing

// PricingCheck is a finding from the existing server quality catalogue. A write
// checks the complete target position; unrelated positions do not block it.
type PricingCheck struct {
	RuleCode    string   `json:"rule_code"`
	Severity    string   `json:"severity"`
	EntityID    string   `json:"entity_id"`
	Fingerprint string   `json:"fingerprint"`
	Detail      string   `json:"detail"`
	MoneyDelta  *float64 `json:"money_delta,omitempty"`
}

type PricingChecks struct {
	Rules       []string       `json:"rules"`
	Findings    []PricingCheck `json:"findings"`
	Fingerprint string         `json:"fingerprint"`
	Blocking    bool           `json:"blocking"`
}
