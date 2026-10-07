package services

import (
	"github.com/su10/hubtender/backend/internal/pricing"
	"math"
	"strings"
	"testing"
)

func TestCatalogDefaultsAndInvalidBusinessInputs(t *testing.T) {
	base := pricing.CatalogCreationInput{EntityType: "library", Kind: "material", NameID: "eeeeeeee-1000-0000-0000-000000000001", ExpectedNameVersion: strings.Repeat("a", 64), UnitRate: 10, Currency: "RUB", PriceSource: "КП №1", RequestKey: "unit-test-catalog-create"}
	in, err := NormalizeCatalogCreation(base)
	if err != nil || in.ItemType != "мат" || in.MaterialType != "основн." || *in.ConsumptionCoefficient != 1 || *in.DeliveryAmount != 0 || in.DeliveryPriceType != "в цене" {
		t.Fatalf("defaults=%+v err=%v", in, err)
	}
	for _, change := range []func(*pricing.CatalogCreationInput){
		func(i *pricing.CatalogCreationInput) { i.UnitRate = math.NaN() },
		func(i *pricing.CatalogCreationInput) { i.Currency = "" },
		func(i *pricing.CatalogCreationInput) { i.PriceSource = "" },
		func(i *pricing.CatalogCreationInput) { i.Kind = "work"; i.ConsumptionCoefficient = new(float64) },
		func(i *pricing.CatalogCreationInput) { a := 1.0; i.DeliveryAmount = &a },
		func(i *pricing.CatalogCreationInput) { i.ExpectedNameVersion = "" },
	} {
		bad := base
		change(&bad)
		if _, err := NormalizeCatalogCreation(bad); err == nil {
			t.Fatal("invalid catalog input accepted")
		}
	}
}
