package services

import (
	"context"
	"errors"
	"testing"

	"github.com/su10/hubtender/backend/internal/pricing"
)

func TestDirectPricingDoesNotUpgradeRetiredOrReadScopes(t *testing.T) {
	svc := &PricingService{}
	for _, scope := range []string{"pricing:draft", "pricing:apply", "tenders:read"} {
		_, err := svc.ApplyDirectPrice(context.Background(), pricing.Principal{UserID: "user", Scopes: []string{scope}}, DirectPricingInput{})
		if !errors.Is(err, ErrPricingForbidden) {
			t.Fatalf("scope %s granted direct write: %v", scope, err)
		}
	}
}
