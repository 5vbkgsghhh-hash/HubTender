package mcpserver

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

func TestConfirmationFallbackRequiresMatchingExplicitAcknowledgement(t *testing.T) {
	result, err := requireConfirmation(nil, "R: work quantity is 1; approve?", "findings-1", confirmationAck{})
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "CONFIRMATION_REQUIRED") || !strings.Contains(text, "confirmation_state=findings-1") {
		t.Fatal(text)
	}
	if _, err := requireConfirmation(nil, "Approve?", "findings-1", confirmationAck{ConfirmationState: "wrong", UserConfirmed: true}); err == nil {
		t.Fatal("wrong state accepted")
	}
	if pending, err := requireConfirmation(nil, "Approve?", "findings-1", confirmationAck{ConfirmationState: "findings-1", UserConfirmed: true}); err != nil || pending != nil {
		t.Fatalf("valid ack: %+v %v", pending, err)
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
