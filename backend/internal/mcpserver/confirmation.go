package mcpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP protocol version that returns InputRequests to the client (SEP-2322)
// instead of fulfilling them via server→client elicitation/create.
const protocolMultiRoundTrip = "2026-07-28"

// confirmationAck is the argument-based confirmation path for clients that do
// not advertise form elicitation (or that cannot complete server→client
// elicitation under Stateless Streamable HTTP).
type confirmationAck struct {
	ConfirmationState string `json:"confirmation_state,omitempty" jsonschema:"Echo confirmation_state from a previous needs_confirmation tool result after a human approved the write"`
	UserConfirmed     bool   `json:"user_confirmed,omitempty" jsonschema:"Set true only after a human approved the proposed write; must be paired with confirmation_state"`
}

func confirmationState(v any) string {
	raw, _ := json.Marshal(v)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil && m != nil {
		delete(m, "confirmation_state")
		delete(m, "user_confirmed")
		raw, _ = json.Marshal(m)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func supportsFormElicitation(req *mcp.CallToolRequest) bool {
	if req == nil {
		return false
	}
	caps := req.ClientCapabilities()
	if caps == nil || caps.Elicitation == nil {
		return false
	}
	// Match go-sdk ServerSession.Elicit: Form==nil && URL!=nil means form is
	// unsupported; both nil means form is assumed for backward compatibility.
	if caps.Elicitation.Form == nil && caps.Elicitation.URL != nil {
		return false
	}
	return true
}

// useElicitationInputRequests is true only when the client both advertises form
// elicitation and speaks a protocol that handles InputRequests itself. Older
// clients trigger go-sdk middleware that calls ServerSession.Elicit; that fails
// on Stateless HTTP ("client does not support elicitation" when ephemeral
// InitializeParams omit Capabilities.Elicitation).
func useElicitationInputRequests(req *mcp.CallToolRequest) bool {
	if !supportsFormElicitation(req) {
		return false
	}
	return req.ProtocolVersion() >= protocolMultiRoundTrip
}

func confirmSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"confirm": {Type: "boolean", Description: "Set true only after reviewing the proposed change."},
		},
		Required: []string{"confirm"},
	}
}

func confirmationRequest(req *mcp.CallToolRequest, message, state string) *mcp.CallToolResult {
	if useElicitationInputRequests(req) {
		return &mcp.CallToolResult{
			InputRequests: mcp.InputRequestMap{
				"confirm": &mcp.ElicitParams{
					Mode:            "form",
					Message:         message,
					RequestedSchema: confirmSchema(),
				},
			},
			RequestState: state,
		}
	}
	text := fmt.Sprintf(
		"CONFIRMATION_REQUIRED\n\n%s\n\nconfirmation_state=%s\n\nAsk the human to approve this write, then retry the identical tool call with confirmation_state set to the value above and user_confirmed=true. Do not invent approval.",
		message, state,
	)
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func confirmed(req *mcp.CallToolRequest, state string, ack confirmationAck) bool {
	if req != nil {
		if r, ok := req.Params.InputResponses["confirm"].(*mcp.ElicitResult); ok {
			return req.Params.RequestState == state && r.Action == "accept" && r.Content["confirm"] == true
		}
	}
	return ack.UserConfirmed && ack.ConfirmationState != "" && ack.ConfirmationState == state
}

func confirmationAttempted(req *mcp.CallToolRequest, ack confirmationAck) bool {
	if ack.UserConfirmed || ack.ConfirmationState != "" {
		return true
	}
	return req != nil && len(req.Params.InputResponses) > 0
}

func requireConfirmation(req *mcp.CallToolRequest, message, state string, ack confirmationAck) (*mcp.CallToolResult, error) {
	if confirmed(req, state, ack) {
		return nil, nil
	}
	if confirmationAttempted(req, ack) {
		return nil, fmt.Errorf("write was not confirmed")
	}
	return confirmationRequest(req, message, state), nil
}
