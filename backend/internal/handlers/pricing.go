package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/su10/hubtender/backend/internal/mcpauth"
	"github.com/su10/hubtender/backend/internal/middleware"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/services"
	"github.com/su10/hubtender/backend/pkg/apierr"
)

type PricingHandler struct{ svc *services.PricingService }

func NewPricingHandler(svc *services.PricingService) *PricingHandler {
	return &PricingHandler{svc: svc}
}

func (h *PricingHandler) GetState(w http.ResponseWriter, r *http.Request) {
	p, ok := portalPrincipal(r)
	if !ok {
		apierr.Unauthorized("authentication required").Render(w)
		return
	}
	limit := parseLimitParam(r.URL.Query().Get("limit"), 50)
	offset := parseOffset(r.URL.Query().Get("offset"))
	out, err := h.svc.GetPricingState(r.Context(), p, chi.URLParam(r, "id"), limit, offset)
	if err != nil {
		apierr.InternalFromErr(w, r, err, "failed to load pricing state")
		return
	}
	renderJSON(w, r, http.StatusOK, dataEnvelope{Data: out})
}

func (h *PricingHandler) QAReport(w http.ResponseWriter, r *http.Request) {
	p, ok := portalPrincipal(r)
	if !ok {
		apierr.Unauthorized("authentication required").Render(w)
		return
	}
	out, err := h.svc.QAReport(r.Context(), p, chi.URLParam(r, "id"))
	if err != nil {
		renderPricingError(w, r, err)
		return
	}
	renderJSON(w, r, http.StatusOK, dataEnvelope{Data: out})
}

func portalPrincipal(r *http.Request) (pricing.Principal, bool) {
	u := middleware.UserFromContext(r.Context())
	if u == nil {
		return pricing.Principal{}, false
	}
	return pricing.Principal{UserID: u.ID, Email: u.Email, RoleCode: u.Role, Scopes: mcpauth.AllScopes}, true
}
func parseOffset(raw string) int {
	n, _ := strconv.Atoi(raw)
	if n < 0 {
		return 0
	}
	return n
}
func renderPricingError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, services.ErrPricingForbidden):
		apierr.Forbidden("pricing access denied").Render(w)
	case errors.Is(err, services.ErrPricingDisabled):
		(&apierr.Problem{Status: http.StatusServiceUnavailable, Title: "Pricing writes disabled", Detail: "Enable MCP_WRITE_ENABLED only after staging verification"}).Render(w)
	case errors.Is(err, services.ErrInvalidPricingInput):
		apierr.BadRequest(err.Error()).Render(w)
	default:
		apierr.InternalFromErr(w, r, err, "pricing operation failed")
	}
}
