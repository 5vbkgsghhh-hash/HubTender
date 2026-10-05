package mcpserver

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/services"
)

func TestToolCatalogIsCompleteAndAnnotated(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{}})
	registerTools(server, &services.PricingService{}, pricing.Principal{})
	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 15 {
		t.Fatalf("got %d tools, want 15", len(result.Tools))
	}
	names := map[string]bool{}
	for _, tool := range result.Tools {
		names[tool.Name] = true
		if tool.Description == "" || tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil {
			t.Fatalf("tool %s missing schema/description/annotations", tool.Name)
		}
	}
	for _, want := range []string{"tenderhub_search_archive_prices", "tenderhub_list_cost_categories", "tenderhub_list_boq_items", "tenderhub_get_boq_item", "tenderhub_get_direct_pricing_receipt", "tenderhub_price_boq_item", "tenderhub_get_pricing_qa_report"} {
		if !names[want] {
			t.Errorf("missing %s", want)
		}
	}
	for _, tool := range result.Tools {
		if tool.Name == "tenderhub_price_boq_item" && (!tool.Annotations.IdempotentHint || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint) {
			t.Fatal("direct pricing tool annotations are unsafe")
		}
	}
	for _, legacy := range []string{"tenderhub_create_pricing_draft", "tenderhub_apply_pricing_draft", "tenderhub_get_pricing_draft"} {
		if names[legacy] {
			t.Errorf("legacy draft tool %s is still advertised", legacy)
		}
	}
}

func TestDirectPricingRequestsConfirmationBeforeServiceCall(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	registerTools(server, &services.PricingService{}, pricing.Principal{})
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	confirmationAsked := false
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, _ *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			confirmationAsked = true
			return &mcp.ElicitResult{Action: "decline"}, nil
		},
	})
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "tenderhub_price_boq_item",
		Arguments: map[string]any{
			"tender_id": "tender", "target_position_id": "position",
			"source_kind": "archive", "source_id": "source", "expected_source_rate": 100,
			"expected_revision": 0, "request_key": "test-direct-request-001",
		},
	})
	if !confirmationAsked {
		t.Fatal("direct pricing did not request confirmation")
	}
	if err == nil && (result == nil || !result.IsError) {
		t.Fatalf("declined direct pricing was not rejected: %+v", result)
	}
}

func TestDirectPricingRejectsAgentControlledConsumption(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	registerTools(server, &services.PricingService{}, pricing.Principal{})
	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	asked := false
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			asked = true
			return &mcp.ElicitResult{Action: "decline"}, nil
		},
	})
	session, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "tenderhub_price_boq_item", Arguments: map[string]any{
		"tender_id": "t", "target_position_id": "p", "source_kind": "current", "target_item_id": "i",
		"conversion_coefficient": 1, "consumption_coefficient": 999, "expected_revision": 0, "request_key": "consumption-reject-001",
	}})
	if asked || (err == nil && !result.IsError) {
		t.Fatalf("unknown consumption reached handler: asked=%t result=%+v err=%v", asked, result, err)
	}
}

// The old draft tool committed successfully but Codex rejected its output
// schema afterwards. Keep a real SDK round trip for the replacement receipt.
func TestDirectPricingResultPassesSDKOutputValidation(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	mcp.AddTool(server, readTool("direct_result_contract", "Result contract", "Check direct result schema."),
		func(context.Context, *mcp.CallToolRequest, emptyInput) (*mcp.CallToolResult, pricing.DirectPricingResult, error) {
			return nil, pricing.DirectPricingResult{
				RequestKey: "contract-test-request", TenderID: "t", PositionID: "p",
				ItemID: "i", Action: "create_item", SourceKind: "archive",
				SourceID: "s", UnitRate: 100, Currency: "RUB",
				TotalAmount: 200, ETag: "\"etag\"", FinancialInputRevision: 1,
				Warnings:        []string{},
				LinkedMaterials: []pricing.LinkedMaterialResult{},
			}, nil
		})
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "direct_result_contract", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("direct result schema rejected: result=%+v err=%v", result, err)
	}
}
