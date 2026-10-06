package pricing

import (
	"time"
)

type Principal struct {
	UserID   string
	Email    string
	RoleCode string
	Scopes   []string
	ClientID string
}

type Page struct {
	Limit      int  `json:"limit"`
	Offset     int  `json:"offset"`
	TotalCount int  `json:"total_count"`
	HasMore    bool `json:"has_more"`
	NextOffset *int `json:"next_offset,omitempty"`
}

type TenderSummary struct {
	ID                 string     `json:"id"`
	TenderNumber       string     `json:"tender_number"`
	Title              string     `json:"title"`
	ClientName         string     `json:"client_name"`
	Version            *int64     `json:"version,omitempty"`
	IsArchived         bool       `json:"is_archived"`
	HousingClass       *string    `json:"housing_class,omitempty"`
	ConstructionScope  *string    `json:"construction_scope,omitempty"`
	SubmissionDeadline *time.Time `json:"submission_deadline,omitempty"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type PositionPricingState struct {
	ID                string   `json:"id"`
	PositionNumber    float64  `json:"position_number"`
	WorkName          string   `json:"work_name"`
	UnitCode          *string  `json:"unit_code,omitempty"`
	Volume            *float64 `json:"volume,omitempty"`
	ManualVolume      *float64 `json:"manual_volume,omitempty"`
	ItemsCount        int      `json:"items_count"`
	PricedItems       int      `json:"priced_items"`
	MissingRates      int      `json:"missing_rates"`
	MissingCategories int      `json:"missing_categories"`
	DirectTotal       float64  `json:"direct_total"`
}

type PricingState struct {
	Tender                 TenderSummary          `json:"tender"`
	FinancialInputRevision int64                  `json:"financial_input_revision"`
	PositionCount          int                    `json:"position_count"`
	ItemCount              int                    `json:"item_count"`
	PricedItemCount        int                    `json:"priced_item_count"`
	MissingRateCount       int                    `json:"missing_rate_count"`
	DirectTotal            float64                `json:"direct_total"`
	Positions              []PositionPricingState `json:"positions"`
	Pagination             Page                   `json:"pagination"`
}

// DirectPricingResult is a committed BOQ change. It has no draft lifecycle.
type DirectPricingResult struct {
	SourceVersion          string                 `json:"source_version,omitempty"`
	Quantity               float64                `json:"quantity"`
	ParentWorkItemID       *string                `json:"parent_work_item_id,omitempty"`
	ConversionCoefficient  *float64               `json:"conversion_coefficient,omitempty"`
	ConsumptionCoefficient *float64               `json:"consumption_coefficient,omitempty"`
	LinkedMaterials        []LinkedMaterialResult `json:"linked_materials"`
	RequestKey             string                 `json:"request_key"`
	TenderID               string                 `json:"tender_id"`
	PositionID             string                 `json:"position_id"`
	ItemID                 string                 `json:"item_id"`
	Action                 string                 `json:"action"`
	SourceKind             string                 `json:"source_kind"`
	SourceID               string                 `json:"source_id"`
	UnitRate               float64                `json:"unit_rate"`
	Currency               string                 `json:"currency"`
	TotalAmount            float64                `json:"total_amount"`
	ETag                   string                 `json:"etag"`
	FinancialInputRevision int64                  `json:"financial_input_revision"`
	Warnings               []string               `json:"warnings"`
	Replayed               bool                   `json:"replayed"`
}

type LinkedMaterialResult struct {
	ItemID      string  `json:"item_id"`
	Quantity    float64 `json:"quantity"`
	TotalAmount float64 `json:"total_amount"`
	ETag        string  `json:"etag"`
}

type CostCategory struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Unit       string `json:"unit"`
	Location   string `json:"location"`
	CategoryID string `json:"cost_category_id"`
}

type ArchiveSearchInput struct {
	Query                string
	Kind                 string
	UnitCode             string
	DetailCostCategoryID string
	HousingClass         string
	ConstructionScope    string
	IncludeActive        bool
	Limit                int
	Offset               int
}

type ArchiveCandidate struct {
	SourceVersion          string    `json:"source_version"`
	ItemID                 string    `json:"item_id"`
	TenderID               string    `json:"tender_id"`
	TenderTitle            string    `json:"tender_title"`
	TenderNumber           string    `json:"tender_number"`
	TenderVersion          *int64    `json:"tender_version,omitempty"`
	TenderArchived         bool      `json:"tender_archived"`
	TenderDate             time.Time `json:"tender_date"`
	HousingClass           *string   `json:"housing_class,omitempty"`
	ConstructionScope      *string   `json:"construction_scope,omitempty"`
	PositionID             string    `json:"position_id"`
	PositionName           string    `json:"position_name"`
	ItemName               string    `json:"item_name"`
	ItemKind               string    `json:"item_kind"`
	BoqItemType            string    `json:"boq_item_type"`
	UnitCode               *string   `json:"unit_code,omitempty"`
	Quantity               *float64  `json:"quantity,omitempty"`
	UnitRate               *float64  `json:"unit_rate,omitempty"`
	CurrencyType           *string   `json:"currency_type,omitempty"`
	DeliveryPriceType      *string   `json:"delivery_price_type,omitempty"`
	DeliveryAmount         *float64  `json:"delivery_amount,omitempty"`
	ConsumptionCoefficient *float64  `json:"consumption_coefficient,omitempty"`
	DetailCostCategoryID   *string   `json:"detail_cost_category_id,omitempty"`
	DetailCostCategoryName *string   `json:"detail_cost_category_name,omitempty"`
	MaterialNameID         *string   `json:"material_name_id,omitempty"`
	WorkNameID             *string   `json:"work_name_id,omitempty"`
	MaterialType           *string   `json:"material_type,omitempty"`
	Description            *string   `json:"description,omitempty"`
	QuoteLink              *string   `json:"quote_link,omitempty"`
	QuotePriceDate         *string   `json:"quote_price_date,omitempty"`
	QuoteValidUntil        *string   `json:"quote_valid_until,omitempty"`
	HistoricalRUBUnitRate  *float64  `json:"historical_rub_unit_rate,omitempty"`
	RawSimilarity          float64   `json:"raw_similarity"`
	Confidence             float64   `json:"confidence"`
	MatchLevel             string    `json:"match_level"`
	Warnings               []string  `json:"warnings"`
	Rationale              string    `json:"rationale"`
}

type LibraryCandidate struct {
	SourceVersion          string   `json:"source_version"`
	ID                     string   `json:"id"`
	Kind                   string   `json:"kind"`
	Name                   string   `json:"name"`
	NameID                 string   `json:"name_id"`
	UnitCode               string   `json:"unit_code"`
	ItemType               string   `json:"item_type"`
	MaterialType           *string  `json:"material_type,omitempty"`
	UnitRate               float64  `json:"unit_rate"`
	CurrencyType           string   `json:"currency_type"`
	DeliveryPriceType      *string  `json:"delivery_price_type,omitempty"`
	DeliveryAmount         *float64 `json:"delivery_amount,omitempty"`
	ConsumptionCoefficient *float64 `json:"consumption_coefficient,omitempty"`
	Confidence             float64  `json:"confidence"`
}

type TemplateSummary struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	DetailCostCategoryID *string   `json:"detail_cost_category_id,omitempty"`
	FolderID             *string   `json:"folder_id,omitempty"`
	ItemsCount           int       `json:"items_count"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type TemplateItem struct {
	ID                     string   `json:"id"`
	Kind                   string   `json:"kind"`
	Position               int      `json:"position"`
	ParentTemplateItemID   *string  `json:"parent_template_item_id,omitempty"`
	ItemType               string   `json:"item_type"`
	Name                   string   `json:"name"`
	NameID                 string   `json:"name_id"`
	UnitCode               string   `json:"unit_code"`
	UnitRate               float64  `json:"unit_rate"`
	CurrencyType           string   `json:"currency_type"`
	MaterialType           *string  `json:"material_type,omitempty"`
	DeliveryPriceType      *string  `json:"delivery_price_type,omitempty"`
	DeliveryAmount         *float64 `json:"delivery_amount,omitempty"`
	ConsumptionCoefficient *float64 `json:"consumption_coefficient,omitempty"`
	ConversionCoefficient  *float64 `json:"conversion_coefficient,omitempty"`
	DetailCostCategoryID   *string  `json:"detail_cost_category_id,omitempty"`
	Note                   *string  `json:"note,omitempty"`
}

type TemplateDetail struct {
	TemplateSummary
	Items []TemplateItem `json:"items"`
}

type ProposedItem struct {
	BoqItemType            string   `json:"boq_item_type"`
	MaterialType           *string  `json:"material_type,omitempty"`
	Description            *string  `json:"description,omitempty"`
	UnitCode               *string  `json:"unit_code,omitempty"`
	Quantity               *float64 `json:"quantity,omitempty"`
	BaseQuantity           *float64 `json:"base_quantity,omitempty"`
	ConversionCoefficient  *float64 `json:"conversion_coefficient,omitempty"`
	UnitRate               *float64 `json:"unit_rate,omitempty"`
	CurrencyType           *string  `json:"currency_type,omitempty"`
	DeliveryPriceType      *string  `json:"delivery_price_type,omitempty"`
	DeliveryAmount         *float64 `json:"delivery_amount,omitempty"`
	ConsumptionCoefficient *float64 `json:"consumption_coefficient,omitempty"`
	DetailCostCategoryID   *string  `json:"detail_cost_category_id,omitempty"`
	MaterialNameID         *string  `json:"material_name_id,omitempty"`
	WorkNameID             *string  `json:"work_name_id,omitempty"`
	QuoteLink              *string  `json:"quote_link,omitempty"`
	QuotePriceDate         *string  `json:"quote_price_date,omitempty"`
	QuoteValidUntil        *string  `json:"quote_valid_until,omitempty"`
	SortNumber             *int     `json:"sort_number,omitempty"`
}

type SourceRef struct {
	Version    string     `json:"version,omitempty"`
	TenderID   *string    `json:"tender_id,omitempty"`
	ItemID     *string    `json:"item_id,omitempty"`
	TemplateID *string    `json:"template_id,omitempty"`
	LibraryID  *string    `json:"library_id,omitempty"`
	Rate       *float64   `json:"rate,omitempty"`
	Currency   *string    `json:"currency,omitempty"`
	Date       *time.Time `json:"date,omitempty"`
}

type DirectOperation struct {
	ID               string       `json:"id"`
	Action           string       `json:"action"`
	TargetPositionID string       `json:"target_position_id"`
	TargetItemID     *string      `json:"target_item_id,omitempty"`
	ExpectedETag     *string      `json:"expected_etag,omitempty"`
	ProposedPayload  ProposedItem `json:"proposed_payload"`
	SourceKind       string       `json:"source_kind"`
	SourceRef        SourceRef    `json:"source_ref"`
	MatchLevel       string       `json:"match_level"`
	Confidence       float64      `json:"confidence"`
	Rationale        *string      `json:"rationale,omitempty"`
	Warnings         []string     `json:"warnings"`
	PositionOrder    int          `json:"position_order"`
}

type QAReport struct {
	TenderID               string    `json:"tender_id"`
	PositionCount          int       `json:"position_count"`
	LeafPositionCount      int       `json:"leaf_position_count"`
	UnpricedPositionCount  int       `json:"unpriced_position_count"`
	ItemCount              int       `json:"item_count"`
	MissingRateCount       int       `json:"missing_rate_count"`
	MissingQuantityCount   int       `json:"missing_quantity_count"`
	MissingCategoryCount   int       `json:"missing_category_count"`
	MissingFXCurrencies    []string  `json:"missing_fx_currencies"`
	WeakSourceCount        int       `json:"weak_source_count"`
	StaleSourceCount       int       `json:"stale_source_count"`
	ItemsWithoutProvenance int       `json:"items_without_provenance"`
	DirectTotal            float64   `json:"direct_total"`
	ReadyForReview         bool      `json:"ready_for_review"`
	BlockingIssues         []string  `json:"blocking_issues"`
	GeneratedAt            time.Time `json:"generated_at"`
}
