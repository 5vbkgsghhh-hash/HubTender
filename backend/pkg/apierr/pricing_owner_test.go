package apierr

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPricingOwnerErrorsRenderForbidden(t *testing.T) {
	for _, message := range []string{"PRICING_OWNER_MISMATCH", "PRICING_OWNER_UNASSIGNED", "PRICING_ACTOR_REQUIRED"} {
		t.Run(message, func(t *testing.T) {
			w := httptest.NewRecorder()
			InternalFromErr(w, httptest.NewRequest("DELETE", "/items/test", nil), fmt.Errorf("repository: %w", &pgconn.PgError{Code: "42501", Message: message}), "failed")
			if w.Code != 403 || strings.Contains(w.Body.String(), message) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
