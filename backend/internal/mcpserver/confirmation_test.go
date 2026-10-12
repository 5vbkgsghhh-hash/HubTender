package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/services"
)

func TestConfirmationStateIgnoresAckFields(t *testing.T) {
	base := directPriceInput{
		TenderID: "t", TargetPositionID: "p", SourceKind: "archive", SourceID: "s",
		ExpectedSourceRate: 10, ExpectedRevision: 1, RequestKey: "request-key-abcdef",
	}
	withAck := base
	withAck.ConfirmationState = "abc"
	withAck.UserConfirmed = true
	if confirmationState(base) != confirmationState(withAck) {
		t.Fatal("confirmation_state/user_confirmed must not change the hash")
	}
}

func TestDirectPricingFallsBackWithoutElicitationCapability(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	registerTools(server, &services.PricingService{}, pricing.Principal{})
	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	// No ElicitationHandler → client initialize omits capabilities.elicitation.
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "tenderhub_price_boq_item",
		Arguments: map[string]any{
			"tender_id": "tender", "target_position_id": "position",
			"source_kind": "archive", "source_id": "source", "expected_source_rate": 100,
			"expected_revision": 0, "request_key": "fallback-confirm-request-01",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected CONFIRMATION_REQUIRED tool error, got %+v", result)
	}
	text := ""
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "CONFIRMATION_REQUIRED") || !strings.Contains(text, "confirmation_state=") {
		t.Fatalf("fallback text missing confirmation_state: %q", text)
	}
	if strings.Contains(text, "client does not support elicitation") {
		t.Fatal("server attempted elicitation against a non-eliciting client")
	}
	if result.InputRequests != nil {
		t.Fatal("InputRequests must not be set when client lacks elicitation")
	}

	state := confirmationState(directPriceInput{
		TenderID: "tender", TargetPositionID: "position",
		SourceKind: "archive", SourceID: "source", ExpectedSourceRate: 100,
		ExpectedRevision: 0, RequestKey: "fallback-confirm-request-01",
	})
	result2, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "tenderhub_price_boq_item",
		Arguments: map[string]any{
			"tender_id": "tender", "target_position_id": "position",
			"source_kind": "archive", "source_id": "source", "expected_source_rate": 100,
			"expected_revision": 0, "request_key": "fallback-confirm-request-01",
			"confirmation_state": "wrong-state", "user_confirmed": true,
		},
	})
	if err == nil && (result2 == nil || !result2.IsError) {
		t.Fatalf("wrong confirmation_state must not proceed: %+v", result2)
	}
	text2 := ""
	if result2 != nil {
		for _, c := range result2.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text2 += tc.Text
			}
		}
	}
	if strings.Contains(text2, "CONFIRMATION_REQUIRED") {
		t.Fatal("failed ack must reject, not re-prompt")
	}

	result3, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "tenderhub_price_boq_item",
		Arguments: map[string]any{
			"tender_id": "tender", "target_position_id": "position",
			"source_kind": "archive", "source_id": "source", "expected_source_rate": 100,
			"expected_revision": 0, "request_key": "fallback-confirm-request-01",
			"confirmation_state": state, "user_confirmed": true,
		},
	})
	text3 := ""
	if result3 != nil {
		for _, c := range result3.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text3 += tc.Text
			}
		}
	}
	if strings.Contains(text3, "CONFIRMATION_REQUIRED") {
		t.Fatalf("matching confirmation_state must pass confirmation gate: err=%v text=%q", err, text3)
	}
}

func TestConfirmationRequestNilReqUsesFallback(t *testing.T) {
	res := confirmationRequest(nil, "Approve?", "state-1")
	if res.InputRequests != nil {
		t.Fatal("nil request must use argument fallback, not InputRequests")
	}
	if !res.IsError {
		t.Fatal("fallback confirmation must be IsError")
	}
}
