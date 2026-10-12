package mcpserver

import (
	"context"
	"fmt"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/services"
)

type catalogSearchInput struct {
	Kind     string `json:"kind" jsonschema:"work or material"`
	Query    string `json:"query,omitempty"`
	UnitCode string `json:"unit_code,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}
type catalogSearchOutput struct {
	Items      []pricing.CatalogName `json:"items"`
	Pagination pricing.Page          `json:"pagination"`
}
type catalogUnitsInput struct {
	Query           string `json:"query,omitempty"`
	IncludeInactive bool   `json:"include_inactive,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	Offset          int    `json:"offset,omitempty"`
}
type catalogUnitsOutput struct {
	Items      []pricing.CatalogUnit `json:"items"`
	Pagination pricing.Page          `json:"pagination"`
}
type createCatalogUnitInput struct {
	Code       string `json:"code" jsonschema:"Exact unit code; case matters, e.g. SI milli and mega prefixes are different"`
	Name       string `json:"name"`
	RequestKey string `json:"request_key" jsonschema:"Unique 16-80 character key; reuse only to retry identical inputs"`
}
type createCatalogNameInput struct {
	Kind       string `json:"kind" jsonschema:"work or material"`
	Name       string `json:"name"`
	UnitCode   string `json:"unit_code" jsonschema:"Existing active code from tenderhub_list_units"`
	RequestKey string `json:"request_key"`
}
type createCatalogLibraryInput struct {
	Kind                   string   `json:"kind" jsonschema:"work or material"`
	NameID                 string   `json:"name_id" jsonschema:"Existing nomenclature UUID from search or creation"`
	ExpectedNameVersion    string   `json:"expected_name_version" jsonschema:"version from the chosen nomenclature item; prevents a price being assigned to a changed unit/name"`
	ItemType               string   `json:"item_type,omitempty" jsonschema:"Work: раб/суб-раб/раб-комп. (default раб); material: мат/суб-мат/мат-комп. (default мат)"`
	MaterialType           string   `json:"material_type,omitempty" jsonschema:"For material only: основн. or вспомогат.; default основн."`
	UnitRate               float64  `json:"unit_rate" jsonschema:"Positive rate explicitly supplied by the user or quote; never invent it"`
	Currency               string   `json:"currency" jsonschema:"Explicit RUB, USD, EUR or CNY"`
	ConsumptionCoefficient *float64 `json:"consumption_coefficient,omitempty" jsonschema:"New material recipe only; positive coefficient, default 1. This does not edit any existing linked BOQ row"`
	DeliveryPriceType      string   `json:"delivery_price_type,omitempty" jsonschema:"New material only: в цене (default), не в цене, or суммой"`
	DeliveryAmount         *float64 `json:"delivery_amount,omitempty" jsonschema:"Nonnegative RUB delivery cost per unit; only nonzero for суммой"`
	PriceSource            string   `json:"price_source" jsonschema:"Quote identifier/link or reference to the user's explicit price; stored with the confirmed creation for audit"`
	RequestKey             string   `json:"request_key"`
}

func registerCatalogTools(server *mcp.Server, svc *services.PricingService, p pricing.Principal) {
	mcp.AddTool(server, readTool("tenderhub_list_units", "List units of measurement", "Read canonical active unit codes before creating nomenclature. Unit codes are case-sensitive."), func(ctx context.Context, _ *mcp.CallToolRequest, in catalogUnitsInput) (*mcp.CallToolResult, catalogUnitsOutput, error) {
		items, page, err := svc.ListCatalogUnits(ctx, p, in.Query, in.IncludeInactive, in.Limit, in.Offset)
		return md(fmt.Sprintf("Found %d units.", page.TotalCount)), catalogUnitsOutput{items, page}, err
	})
	mcp.AddTool(server, readTool("tenderhub_search_nomenclature", "Search work/material names", "Search shared nomenclature even when it has no library price. Use IDs and versions rather than creating duplicates."), func(ctx context.Context, _ *mcp.CallToolRequest, in catalogSearchInput) (*mcp.CallToolResult, catalogSearchOutput, error) {
		items, page, err := svc.SearchCatalogNames(ctx, p, in.Kind, in.Query, in.UnitCode, in.Limit, in.Offset)
		return md(fmt.Sprintf("Found %d nomenclature items.", page.TotalCount)), catalogSearchOutput{items, page}, err
	})
	mcp.AddTool(server, readTool("tenderhub_get_catalog_creation_receipt", "Recover a catalog creation result", "Read your committed unit/nomenclature/library creation by request_key after a lost response. Never creates a new record."), func(ctx context.Context, _ *mcp.CallToolRequest, in directReceiptInput) (*mcp.CallToolResult, pricing.CatalogCreationResult, error) {
		out, err := svc.GetCatalogCreationReceipt(ctx, p, in.RequestKey)
		if out == nil {
			if err == nil {
				err = fmt.Errorf("catalog creation receipt was not found")
			}
			return nil, pricing.CatalogCreationResult{}, err
		}
		return md("Committed catalog creation `" + in.RequestKey + "`."), *out, nil
	})
	mcp.AddTool(server, catalogCreateTool("tenderhub_create_unit", "Create a unit of measurement", "Create an active shared unit, or return the existing active code unchanged. Requires nomenclature:create, catalog write gate and confirmation. No update/delete."), func(ctx context.Context, req *mcp.CallToolRequest, in createCatalogUnitInput) (*mcp.CallToolResult, pricing.CatalogCreationResult, error) {
		return confirmCatalogCreation(ctx, req, svc, p, pricing.CatalogCreationInput{EntityType: "unit", Name: in.Name, UnitCode: in.Code, RequestKey: in.RequestKey})
	})
	mcp.AddTool(server, catalogCreateTool("tenderhub_create_nomenclature_item", "Create a work/material name", "Create a shared name in an active unit, or return one exact existing name/unit match. Multiple historical matches require choosing an ID. Requires nomenclature:create and confirmation. Never merges or edits existing names."), func(ctx context.Context, req *mcp.CallToolRequest, in createCatalogNameInput) (*mcp.CallToolResult, pricing.CatalogCreationResult, error) {
		return confirmCatalogCreation(ctx, req, svc, p, pricing.CatalogCreationInput{EntityType: "nomenclature", Kind: in.Kind, Name: in.Name, UnitCode: in.UnitCode, RequestKey: in.RequestKey})
	})
	mcp.AddTool(server, catalogCreateTool("tenderhub_create_library_item", "Create a priced work/material library card", "Create a card for existing nomenclature using a user/quote-supplied rate, currency and material recipe/delivery. Requires library:create and confirmation. Exact existing variants are reused unchanged. Result includes source_version for direct VOR insertion; linked quantity stays server-derived."), func(ctx context.Context, req *mcp.CallToolRequest, in createCatalogLibraryInput) (*mcp.CallToolResult, pricing.CatalogCreationResult, error) {
		return confirmCatalogCreation(ctx, req, svc, p, pricing.CatalogCreationInput{EntityType: "library", Kind: in.Kind, NameID: in.NameID, ExpectedNameVersion: in.ExpectedNameVersion, ItemType: in.ItemType, MaterialType: in.MaterialType, UnitRate: in.UnitRate, Currency: in.Currency, ConsumptionCoefficient: in.ConsumptionCoefficient, DeliveryPriceType: in.DeliveryPriceType, DeliveryAmount: in.DeliveryAmount, PriceSource: in.PriceSource, RequestKey: in.RequestKey})
	})
}

func catalogCreateTool(name, title, description string) *mcp.Tool {
	t := additiveTool(name, title, description)
	t.Annotations.IdempotentHint = true
	return t
}

func confirmCatalogCreation(ctx context.Context, req *mcp.CallToolRequest, svc *services.PricingService, p pricing.Principal, in pricing.CatalogCreationInput) (*mcp.CallToolResult, pricing.CatalogCreationResult, error) {
	normalized, err := services.NormalizeCatalogCreation(in)
	if err != nil {
		return nil, pricing.CatalogCreationResult{}, err
	}
	state := confirmationState(normalized)
	if len(req.Params.InputResponses) == 0 {
		message := fmt.Sprintf("Создать в общем справочнике %s: «%s», единица «%s», вид %s? Точное совпадение будет использовано без изменения существующей записи.", normalized.EntityType, normalized.Name, normalized.UnitCode, normalized.Kind)
		if normalized.EntityType == "library" {
			n, err := svc.DescribeCatalogName(ctx, p, normalized)
			if err != nil {
				return nil, pricing.CatalogCreationResult{}, err
			}
			message = fmt.Sprintf("Создать общую карточку %s «%s», единица %s: цена %s %s, тип %s, материал %s, расход %v, доставка %s/%v? Источник цены: %s. Существующие карточки и строки ВОР не изменяются.", normalized.Kind, n.Name, n.UnitCode, strconv.FormatFloat(normalized.UnitRate, 'f', -1, 64), normalized.Currency, normalized.ItemType, normalized.MaterialType, valueOrNone(normalized.ConsumptionCoefficient), normalized.DeliveryPriceType, valueOrNone(normalized.DeliveryAmount), normalized.PriceSource)
		}
		return confirmationRequest(message, state), pricing.CatalogCreationResult{}, nil
	}
	if !confirmed(req, state) {
		return nil, pricing.CatalogCreationResult{}, fmt.Errorf("catalog creation was not confirmed")
	}
	normalized.Confirm = true
	out, err := svc.CreateCatalogEntity(ctx, p, normalized)
	if out == nil {
		return nil, pricing.CatalogCreationResult{}, err
	}
	action := "Reused existing"
	if out.Created {
		action = "Created"
	}
	if out.Replayed {
		action = "Recovered committed"
	}
	return md(fmt.Sprintf("%s %s `%s`: %s (%s).", action, out.EntityType, out.EntityID, out.Name, out.UnitCode)), *out, nil
}
