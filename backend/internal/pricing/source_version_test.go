package pricing

import "testing"

func TestSourceVersionTracksEconomicsButIgnoresSearchDisplay(t *testing.T) {
	rate, cons, delivery := 100.0, 1.2, 15.0
	currency, unit, quote := "RUB", "шт", "КП-1"
	c := ArchiveCandidate{UnitRate: &rate, CurrencyType: &currency, UnitCode: &unit, ConsumptionCoefficient: &cons, DeliveryAmount: &delivery, QuoteLink: &quote}
	version := ArchiveSourceVersion(c)
	if len(version) != 64 {
		t.Fatalf("invalid source version %s", version)
	}
	display := c
	display.Confidence, display.RawSimilarity, display.MatchLevel = 1, .99, "exact"
	display.Warnings, display.Rationale = []string{"warning"}, "display"
	if ArchiveSourceVersion(display) != version {
		t.Fatal("search-only display changed the price snapshot")
	}
	usd, newUnit, newQuote, newCons, newDelivery := "USD", "м3", "КП-2", 2.0, 20.0
	for _, mutate := range []func(*ArchiveCandidate){
		func(c *ArchiveCandidate) { c.CurrencyType = &usd },
		func(c *ArchiveCandidate) { c.UnitCode = &newUnit },
		func(c *ArchiveCandidate) { c.QuoteLink = &newQuote },
		func(c *ArchiveCandidate) { c.ConsumptionCoefficient = &newCons },
		func(c *ArchiveCandidate) { c.DeliveryAmount = &newDelivery },
	} {
		changed := c
		mutate(&changed)
		if ArchiveSourceVersion(changed) == version {
			t.Fatal("changed source economics retained old version")
		}
	}
}
