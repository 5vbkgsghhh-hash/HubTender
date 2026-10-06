package pricing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// A price includes units, currency, delivery, consumption and quote evidence.
// Search-specific scores and historical display conversions are excluded.
func ArchiveSourceVersion(c ArchiveCandidate) string {
	c.SourceVersion, c.MatchLevel, c.Rationale = "", "", ""
	c.RawSimilarity, c.Confidence = 0, 0
	c.Warnings, c.Quantity, c.HistoricalRUBUnitRate = nil, nil, nil
	return pricingSourceVersion(c)
}

func LibrarySourceVersion(c LibraryCandidate) string {
	c.SourceVersion, c.Confidence = "", 0
	return pricingSourceVersion(c)
}

func pricingSourceVersion(v any) string {
	encoded, err := json.Marshal(v)
	if err != nil {
		return ""
	} // invalid numeric source cannot acquire a version
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
