package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/su10/hubtender/backend/internal/middleware"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/repository"
	"github.com/su10/hubtender/backend/internal/services"
)

const serverInstructions = `TenderHUB MCP works only with already-created and imported tenders. Search archive/library sources, copy the selected source_version into expected_source_version, inspect the target BOQ item and financial_input_revision, then use tenderhub_price_boq_item to write directly to the VOR. The source version covers currency, units, delivery, consumption and quote evidence as well as rate. Use source_kind=current to change an existing work/standalone quantity or a linked material conversion coefficient while preserving its price. A linked material quantity is read-only: the server derives it as parent work quantity * conversion_coefficient * stored consumption_coefficient. Never send quantity for a linked material or change its consumption through MCP. Work changes atomically recalculate every linked material and position totals. Writes require confirmation and a request_key for safe retries. Read the item and tender back after every write. Never invent a rate or silently replace an unmatched item.`

type Config struct {
	MaxRequestBodyBytes int64
	Logger              zerolog.Logger
}

func NewHTTPHandler(svc *services.PricingService, cfg Config) http.Handler {
	cache := mcp.NewSchemaCache()
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			return nil
		}
		principal := pricing.Principal{UserID: u.ID, Email: u.Email, RoleCode: u.Role, Scopes: u.Scopes, ClientID: u.ClientID}
		server := mcp.NewServer(&mcp.Implementation{
			Name: "tenderhub-mcp-server", Title: "TenderHUB MCP", Version: "2.0.0",
			Description: "Source-backed direct VOR pricing with audit and retry-safe receipts",
		}, &mcp.ServerOptions{Instructions: serverInstructions, SchemaCache: cache, Capabilities: &mcp.ServerCapabilities{}})
		registerTools(server, svc, principal)
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: cfg.MaxRequestBodyBytes,
		PropagateRequestCancellation: true,
	})
	return auditHTTP(h, cfg.Logger)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }
func auditHTTP(next http.Handler, logger zerolog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		u := middleware.UserFromContext(r.Context())
		event := logger.Info()
		if rec.status >= 400 {
			event = logger.Warn()
		}
		if u != nil {
			event = event.Str("user_id", u.ID).Str("oauth_client_id", u.ClientID)
		}
		event.Str("mcp_method", r.Header.Get("Mcp-Method")).Str("mcp_name", r.Header.Get("Mcp-Name")).Int("status", rec.status).Dur("duration", time.Since(start)).Msg("mcp request")
	})
}

type emptyInput struct{}
type whoamiOutput struct {
	UserID   string   `json:"user_id"`
	Email    string   `json:"email"`
	RoleCode string   `json:"role_code"`
	Scopes   []string `json:"scopes"`
}
type paginationInput struct {
	Limit  int `json:"limit,omitempty" jsonschema:"Maximum results from 1 to 100"`
	Offset int `json:"offset,omitempty" jsonschema:"Zero-based result offset"`
}
type listTendersInput struct {
	Search     string `json:"search,omitempty"`
	IsArchived *bool  `json:"is_archived,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Offset     int    `json:"offset,omitempty"`
}
type listTendersOutput struct {
	Tenders    []pricing.TenderSummary `json:"tenders"`
	Pagination pricing.Page            `json:"pagination"`
}
type pricingStateInput struct {
	TenderID string `json:"tender_id" jsonschema:"UUID of an already imported tender"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}
type directItemsInput struct {
	TenderID   string `json:"tender_id"`
	PositionID string `json:"position_id"`
	Limit      int    `json:"limit,omitempty"`
	Offset     int    `json:"offset,omitempty"`
}
type directItemsOutput struct {
	Items      []repository.BoqItemRow `json:"items"`
	Pagination pricing.Page            `json:"pagination"`
}
type directItemOutput struct {
	Item repository.BoqItemRow `json:"item"`
	ETag string                `json:"etag"`
}
type directReceiptInput struct {
	RequestKey string `json:"request_key"`
}
type directPriceInput struct {
	ExpectedSourceVersion string   `json:"expected_source_version,omitempty" jsonschema:"Required source_version from the selected archive/library search result. Covers currency, units, delivery, consumption and quote evidence. Omit for current"`
	DetailCostCategoryID  *string  `json:"detail_cost_category_id,omitempty" jsonschema:"Required for a new library item; use tenderhub_list_cost_categories"`
	TenderID              string   `json:"tender_id"`
	TargetPositionID      string   `json:"target_position_id"`
	TargetItemID          *string  `json:"target_item_id,omitempty"`
	ParentWorkItemID      *string  `json:"parent_work_item_id,omitempty" jsonschema:"Optional existing work row in the same VOR position for a new material"`
	SourceKind            string   `json:"source_kind" jsonschema:"archive or library for source-backed pricing; current to edit only quantity or conversion_coefficient of an existing row"`
	SourceID              string   `json:"source_id,omitempty" jsonschema:"Required for archive/library; omit for current"`
	ExpectedSourceRate    float64  `json:"expected_source_rate,omitempty" jsonschema:"Required positive rate from archive/library; omit for current"`
	LibraryKind           string   `json:"library_kind,omitempty" jsonschema:"work or material for a library source"`
	Quantity              *float64 `json:"quantity,omitempty" jsonschema:"Positive quantity for work or standalone material only. Forbidden for a linked material: derived from the parent work on the server"`
	ConversionCoefficient *float64 `json:"conversion_coefficient,omitempty" jsonschema:"Positive unit conversion multiplier for a linked material only. Required on creation when work and material units differ. Stored consumption cannot be edited through MCP"`
	ExpectedETag          *string  `json:"expected_etag,omitempty" jsonschema:"Required ETag from tenderhub_get_boq_item when updating"`
	ExpectedRevision      int64    `json:"expected_revision" jsonschema:"financial_input_revision from tenderhub_get_pricing_state"`
	RequestKey            string   `json:"request_key" jsonschema:"Unique 16-80 character idempotency key; reuse only for an identical retry"`
	Rationale             string   `json:"rationale,omitempty"`
}
type archiveSearchToolInput struct {
	Query                string `json:"query" jsonschema:"Russian item or work description to match"`
	Kind                 string `json:"kind,omitempty" jsonschema:"Optional work or material filter"`
	UnitCode             string `json:"unit_code,omitempty"`
	DetailCostCategoryID string `json:"detail_cost_category_id,omitempty"`
	HousingClass         string `json:"housing_class,omitempty"`
	ConstructionScope    string `json:"construction_scope,omitempty"`
	IncludeActive        bool   `json:"include_active,omitempty"`
	Limit                int    `json:"limit,omitempty"`
	Offset               int    `json:"offset,omitempty"`
}
type archiveSearchOutput struct {
	Candidates []pricing.ArchiveCandidate `json:"candidates"`
	Pagination pricing.Page               `json:"pagination"`
}
type librarySearchInput struct {
	Query    string `json:"query"`
	Kind     string `json:"kind,omitempty"`
	UnitCode string `json:"unit_code,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}
type librarySearchOutput struct {
	Items      []pricing.LibraryCandidate `json:"items"`
	Pagination pricing.Page               `json:"pagination"`
}
type listTemplatesInput struct {
	Search string `json:"search,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}
type listTemplatesOutput struct {
	Templates  []pricing.TemplateSummary `json:"templates"`
	Pagination pricing.Page              `json:"pagination"`
}
type listCostCategoriesInput struct {
	Search string `json:"search,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}
type listCostCategoriesOutput struct {
	Items      []pricing.CostCategory `json:"items"`
	Pagination pricing.Page           `json:"pagination"`
}
type idInput struct {
	ID string `json:"id"`
}
type qaInput struct {
	TenderID string `json:"tender_id"`
}
type statusOutput struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}
type createTemplateInput struct {
	Name                 string                             `json:"name"`
	DetailCostCategoryID string                             `json:"detail_cost_category_id"`
	Works                []repository.TemplateWorkInput     `json:"works"`
	Materials            []repository.TemplateMaterialInput `json:"materials"`
}
type createTemplateOutput struct {
	ID string `json:"id"`
}
type updateTemplateInput struct {
	ID                   string                         `json:"id"`
	Name                 string                         `json:"name"`
	DetailCostCategoryID string                         `json:"detail_cost_category_id"`
	Items                []repository.TemplateItemPatch `json:"items"`
}

func registerTools(server *mcp.Server, svc *services.PricingService, principal pricing.Principal) {
	mcp.AddTool(server, readTool("tenderhub_whoami", "Show current TenderHUB OAuth identity and scopes", "Current TenderHUB identity and effective OAuth scopes."), func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, whoamiOutput, error) {
		if _, err := svc.Authorize(ctx, principal, "tenders:read", "/positions"); err != nil {
			return nil, whoamiOutput{}, err
		}
		return md("# TenderHUB identity\n\nAuthenticated as **" + principal.Email + "** (`" + principal.RoleCode + "`)."), whoamiOutput{principal.UserID, principal.Email, principal.RoleCode, principal.Scopes}, nil
	})
	mcp.AddTool(server, readTool("tenderhub_list_tenders", "List active or archived tenders", "List tenders with search, archive filter and pagination. Does not modify TenderHUB."), func(ctx context.Context, _ *mcp.CallToolRequest, in listTendersInput) (*mcp.CallToolResult, listTendersOutput, error) {
		rows, page, err := svc.ListTenders(ctx, principal, in.Search, in.IsArchived, in.Limit, in.Offset)
		return md(fmt.Sprintf("Found %d tenders; returning %d.", page.TotalCount, len(rows))), listTendersOutput{rows, page}, err
	})
	mcp.AddTool(server, readTool("tenderhub_get_pricing_state", "Inspect tender pricing completeness", "Return direct-cost completeness and paginated positions for an imported tender."), func(ctx context.Context, _ *mcp.CallToolRequest, in pricingStateInput) (*mcp.CallToolResult, pricing.PricingState, error) {
		out, err := svc.GetPricingState(ctx, principal, in.TenderID, in.Limit, in.Offset)
		if out == nil {
			return nil, pricing.PricingState{}, err
		}
		return md(fmt.Sprintf("# %s\n\nPositions: %d; BOQ items: %d; missing rates: %d; direct total: %.2f RUB.", out.Tender.Title, out.PositionCount, out.ItemCount, out.MissingRateCount, out.DirectTotal)), *out, err
	})
	mcp.AddTool(server, readTool("tenderhub_search_archive_prices", "Search historical tender rates", "Search archived BOQ items using deterministic name/family, unit, category, recency and project-context ranking. Results below the safety threshold are omitted."), func(ctx context.Context, _ *mcp.CallToolRequest, in archiveSearchToolInput) (*mcp.CallToolResult, archiveSearchOutput, error) {
		rows, page, err := svc.SearchArchive(ctx, principal, pricing.ArchiveSearchInput{Query: in.Query, Kind: in.Kind, UnitCode: in.UnitCode, DetailCostCategoryID: in.DetailCostCategoryID, HousingClass: in.HousingClass, ConstructionScope: in.ConstructionScope, IncludeActive: in.IncludeActive, Limit: in.Limit, Offset: in.Offset})
		return md(fmt.Sprintf("Found %d safe archive candidates; returning %d. Review every warning and source date.", page.TotalCount, len(rows))), archiveSearchOutput{rows, page}, err
	})
	mcp.AddTool(server, readTool("tenderhub_search_library", "Search the managed work/material library", "Search managed TenderHUB work and material rates with pagination."), func(ctx context.Context, _ *mcp.CallToolRequest, in librarySearchInput) (*mcp.CallToolResult, librarySearchOutput, error) {
		rows, page, err := svc.SearchLibrary(ctx, principal, in.Query, in.Kind, in.UnitCode, in.Limit, in.Offset)
		return md(fmt.Sprintf("Found %d library items; returning %d.", page.TotalCount, len(rows))), librarySearchOutput{rows, page}, err
	})
	mcp.AddTool(server, readTool("tenderhub_list_templates", "List pricing templates", "List reusable managed pricing templates."), func(ctx context.Context, _ *mcp.CallToolRequest, in listTemplatesInput) (*mcp.CallToolResult, listTemplatesOutput, error) {
		rows, page, err := svc.ListTemplates(ctx, principal, in.Search, in.Limit, in.Offset)
		return md(fmt.Sprintf("Found %d templates; returning %d.", page.TotalCount, len(rows))), listTemplatesOutput{rows, page}, err
	})
	mcp.AddTool(server, readTool("tenderhub_get_template", "Inspect a pricing template", "Return all work/material template items and parent relationships."), func(ctx context.Context, _ *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, pricing.TemplateDetail, error) {
		out, err := svc.GetTemplate(ctx, principal, in.ID)
		if out == nil {
			return nil, pricing.TemplateDetail{}, err
		}
		return md(fmt.Sprintf("Template **%s** contains %d items.", out.Name, len(out.Items))), *out, err
	})

	mcp.AddTool(server, readTool("tenderhub_list_cost_categories", "List direct-cost categories", "Find a category ID for a newly created BOQ row in the VOR."), func(ctx context.Context, _ *mcp.CallToolRequest, in listCostCategoriesInput) (*mcp.CallToolResult, listCostCategoriesOutput, error) {
		items, page, err := svc.ListDirectCostCategories(ctx, principal, in.Search, in.Limit, in.Offset)
		return md(fmt.Sprintf("Found %d cost categories; returning %d.", page.TotalCount, len(items))), listCostCategoriesOutput{Items: items, Pagination: page}, err
	})
	mcp.AddTool(server, readTool("tenderhub_list_boq_items", "List BOQ rows in one VOR position", "Read existing work/material rows and IDs before a direct pricing change."), func(ctx context.Context, _ *mcp.CallToolRequest, in directItemsInput) (*mcp.CallToolResult, directItemsOutput, error) {
		items, page, err := svc.ListDirectBoqItems(ctx, principal, in.TenderID, in.PositionID, in.Limit, in.Offset)
		return md(fmt.Sprintf("Position has %d BOQ items; returning %d.", page.TotalCount, len(items))), directItemsOutput{Items: items, Pagination: page}, err
	})
	mcp.AddTool(server, readTool("tenderhub_get_boq_item", "Read a BOQ row and ETag", "Return the current BOQ inputs and ETag required for a safe direct update."), func(ctx context.Context, _ *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, directItemOutput, error) {
		item, etag, err := svc.GetDirectBoqItem(ctx, principal, in.ID)
		if item == nil {
			return nil, directItemOutput{}, err
		}
		return md("BOQ item `" + item.ID + "` loaded with ETag."), directItemOutput{Item: *item, ETag: etag}, err
	})
	mcp.AddTool(server, readTool("tenderhub_get_direct_pricing_receipt", "Read a committed pricing receipt", "Recover the result of a direct BOQ write by request_key after a lost response. No new write is made."), func(ctx context.Context, _ *mcp.CallToolRequest, in directReceiptInput) (*mcp.CallToolResult, pricing.DirectPricingResult, error) {
		result, err := svc.GetDirectPricingReceipt(ctx, principal, in.RequestKey)
		if result == nil {
			if err == nil {
				err = fmt.Errorf("direct pricing request %s was not found", in.RequestKey)
			}
			return nil, pricing.DirectPricingResult{}, err
		}
		return md("Committed direct pricing request `" + in.RequestKey + "`."), *result, nil
	})
	mcp.AddTool(server, destructiveTool("tenderhub_price_boq_item", "Write one BOQ row directly in the VOR", "Create/update directly from archive/library, or use source_kind=current to edit work/standalone quantity or linked material conversion. Linked material quantity is server-derived and cannot be set. Work updates atomically recalculate all linked materials. Requires pricing:write, current revision/ETag and confirmation. Reuse request_key only for identical retry; read back afterwards."), func(ctx context.Context, req *mcp.CallToolRequest, in directPriceInput) (*mcp.CallToolResult, pricing.DirectPricingResult, error) {
		state := confirmationState(in)
		if len(req.Params.InputResponses) == 0 {
			action := "Create a BOQ row"
			if in.TargetItemID != nil {
				action = "Update an existing BOQ row"
			}
			return confirmationRequest(fmt.Sprintf("%s in TenderHUB VOR, tender %s, position %s, item %v, source %s/%s, source rate %.2f, requested quantity %v, conversion %v? Linked quantities are calculated from the parent; a work update recalculates every linked material. This changes direct costs immediately.", action, in.TenderID, in.TargetPositionID, valueOrNone(in.TargetItemID), in.SourceKind, in.SourceID, in.ExpectedSourceRate, valueOrNone(in.Quantity), valueOrNone(in.ConversionCoefficient)), state), pricing.DirectPricingResult{}, nil
		}
		if !confirmed(req, state) {
			return nil, pricing.DirectPricingResult{}, fmt.Errorf("direct BOQ pricing was not confirmed")
		}
		out, err := svc.ApplyDirectPrice(ctx, principal, services.DirectPricingInput{
			TenderID: in.TenderID, TargetPositionID: in.TargetPositionID, TargetItemID: in.TargetItemID,
			ParentWorkItemID:     in.ParentWorkItemID,
			DetailCostCategoryID: in.DetailCostCategoryID,
			SourceKind:           in.SourceKind, SourceID: in.SourceID, LibraryKind: in.LibraryKind,
			ExpectedSourceRate: in.ExpectedSourceRate, ExpectedSourceVersion: in.ExpectedSourceVersion,
			ConversionCoefficient: in.ConversionCoefficient,
			Quantity:              in.Quantity, ExpectedETag: in.ExpectedETag, ExpectedRevision: in.ExpectedRevision,
			RequestKey: in.RequestKey, Rationale: in.Rationale, Confirm: true,
		})
		if out == nil {
			return nil, pricing.DirectPricingResult{}, err
		}
		return md(fmt.Sprintf("%s BOQ item `%s` directly. Revision %d; rate %.2f %s. Read back before continuing.", out.Action, out.ItemID, out.FinancialInputRevision, out.UnitRate, out.Currency)), *out, err
	})

	mcp.AddTool(server, readTool("tenderhub_get_pricing_qa_report", "Generate a direct-price QA report", "Check leaf-position coverage, missing rates/quantities/categories/FX, weak or stale sources, provenance and direct total. Does not modify data."), func(ctx context.Context, _ *mcp.CallToolRequest, in qaInput) (*mcp.CallToolResult, pricing.QAReport, error) {
		out, err := svc.QAReport(ctx, principal, in.TenderID)
		if out == nil {
			return nil, pricing.QAReport{}, err
		}
		return md(fmt.Sprintf("QA ready_for_review=%t; blocking issues=%d; direct total=%.2f RUB.", out.ReadyForReview, len(out.BlockingIssues), out.DirectTotal)), *out, err
	})

	mcp.AddTool(server, additiveTool("tenderhub_create_template", "Create a managed pricing template", "Create a global template. Requires templates:write and a leading-engineer/administrator/developer role."), func(ctx context.Context, req *mcp.CallToolRequest, in createTemplateInput) (*mcp.CallToolResult, createTemplateOutput, error) {
		state := confirmationState(in)
		if len(req.Params.InputResponses) == 0 {
			return confirmationRequest("Create global pricing template "+in.Name+"?", state), createTemplateOutput{}, nil
		}
		if !confirmed(req, state) {
			return nil, createTemplateOutput{}, fmt.Errorf("template creation was not confirmed")
		}
		id, err := svc.CreateTemplate(ctx, principal, repository.CreateTemplateInput{Name: in.Name, DetailCostCategoryID: in.DetailCostCategoryID, Works: in.Works, Materials: in.Materials})
		return md("Created managed template `" + id + "`."), createTemplateOutput{id}, err
	})
	mcp.AddTool(server, destructiveTool("tenderhub_update_template", "Update a managed pricing template", "Update global template metadata and relationships. Requires templates:write and an allowed senior role."), func(ctx context.Context, req *mcp.CallToolRequest, in updateTemplateInput) (*mcp.CallToolResult, statusOutput, error) {
		state := confirmationState(in)
		if len(req.Params.InputResponses) == 0 {
			return confirmationRequest("Update global pricing template "+in.Name+"?", state), statusOutput{}, nil
		}
		if !confirmed(req, state) {
			return nil, statusOutput{}, fmt.Errorf("template update was not confirmed")
		}
		err := svc.UpdateTemplate(ctx, principal, in.ID, repository.UpdateTemplateInput{Name: in.Name, DetailCostCategoryID: in.DetailCostCategoryID, Items: in.Items})
		return md("Updated managed template `" + in.ID + "`."), statusOutput{err == nil, "template updated"}, err
	})
}

func readTool(name, title, description string) *mcp.Tool {
	return &mcp.Tool{Name: name, Title: title, Description: description, Annotations: &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPtr(false), DestructiveHint: boolPtr(false)}}
}
func additiveTool(name, title, description string) *mcp.Tool {
	return &mcp.Tool{Name: name, Title: title, Description: description, Annotations: &mcp.ToolAnnotations{Title: title, ReadOnlyHint: false, IdempotentHint: false, OpenWorldHint: boolPtr(false), DestructiveHint: boolPtr(false)}}
}
func destructiveTool(name, title, description string) *mcp.Tool {
	return &mcp.Tool{Name: name, Title: title, Description: description, Annotations: &mcp.ToolAnnotations{Title: title, ReadOnlyHint: false, IdempotentHint: true, OpenWorldHint: boolPtr(false), DestructiveHint: boolPtr(true)}}
}
func boolPtr(v bool) *bool { return &v }
func valueOrNone[T any](p *T) any {
	if p == nil {
		return "omitted"
	}
	return *p
}
func md(text string) *mcp.CallToolResult {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}
func confirmationState(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func confirmationRequest(message, state string) *mcp.CallToolResult {
	return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{"confirm": &mcp.ElicitParams{Message: message, RequestedSchema: &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"confirm": {Type: "boolean", Description: "Set true only after reviewing the proposed change."}}, Required: []string{"confirm"}}}}, RequestState: state}
}
func confirmed(req *mcp.CallToolRequest, state string) bool {
	if req.Params.RequestState != state {
		return false
	}
	r, ok := req.Params.InputResponses["confirm"].(*mcp.ElicitResult)
	return ok && r.Action == "accept" && r.Content["confirm"] == true
}
