package pricing

type CatalogName struct {
	Version  string `json:"version"`
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	UnitCode string `json:"unit_code"`
}

type CatalogUnit struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// A creation/reuse receipt records the confirmed input and result atomically.
// It is not a draft and never edits an existing catalog record.
type CatalogCreationResult struct {
	NomenclatureItem *CatalogName      `json:"nomenclature_item,omitempty"`
	RequestKey       string            `json:"request_key"`
	EntityType       string            `json:"entity_type"`
	EntityID         string            `json:"entity_id"`
	Kind             string            `json:"kind"`
	Name             string            `json:"name"`
	UnitCode         string            `json:"unit_code"`
	Created          bool              `json:"created"`
	Replayed         bool              `json:"replayed"`
	LibraryItem      *LibraryCandidate `json:"library_item,omitempty"`
}

type CatalogCreationInput struct {
	EntityType             string   `json:"entity_type"`
	Kind                   string   `json:"kind,omitempty"`
	Name                   string   `json:"name,omitempty"`
	UnitCode               string   `json:"unit_code,omitempty"`
	NameID                 string   `json:"name_id,omitempty"`
	ExpectedNameVersion    string   `json:"expected_name_version,omitempty"`
	ItemType               string   `json:"item_type,omitempty"`
	MaterialType           string   `json:"material_type,omitempty"`
	UnitRate               float64  `json:"unit_rate,omitempty"`
	Currency               string   `json:"currency,omitempty"`
	ConsumptionCoefficient *float64 `json:"consumption_coefficient,omitempty"`
	DeliveryPriceType      string   `json:"delivery_price_type,omitempty"`
	DeliveryAmount         *float64 `json:"delivery_amount,omitempty"`
	PriceSource            string   `json:"price_source,omitempty"`
	RequestKey             string   `json:"request_key"`
	Confirm                bool     `json:"confirm"`
}
