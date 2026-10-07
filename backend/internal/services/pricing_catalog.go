package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/su10/hubtender/backend/internal/calc"
	"github.com/su10/hubtender/backend/internal/mcpauth"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/repository"
)

var catalogUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (s *PricingService) SearchCatalogNames(ctx context.Context, p pricing.Principal, kind, query, unit string, limit, offset int) ([]pricing.CatalogName, pricing.Page, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopeLibraryRead, "/library"); err != nil {
		return nil, pricing.Page{}, err
	}
	if kind != "work" && kind != "material" {
		return nil, pricing.Page{}, fmt.Errorf("%w: kind must be work or material", ErrInvalidPricingInput)
	}
	limit, offset = normalizePage(limit, offset, 20)
	rows, total, err := s.repo.SearchCatalogNames(ctx, kind, strings.TrimSpace(query), strings.TrimSpace(unit), limit, offset)
	return rows, buildPage(limit, offset, total, len(rows)), err
}

func (s *PricingService) ListCatalogUnits(ctx context.Context, p pricing.Principal, query string, includeInactive bool, limit, offset int) ([]pricing.CatalogUnit, pricing.Page, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopeLibraryRead, "/library"); err != nil {
		return nil, pricing.Page{}, err
	}
	limit, offset = normalizePage(limit, offset, 50)
	rows, total, err := s.repo.ListCatalogUnits(ctx, strings.TrimSpace(query), includeInactive, limit, offset)
	return rows, buildPage(limit, offset, total, len(rows)), err
}

func (s *PricingService) GetCatalogCreationReceipt(ctx context.Context, p pricing.Principal, key string) (*pricing.CatalogCreationResult, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopeLibraryRead, "/library"); err != nil {
		return nil, err
	}
	if !directRequestKeyPattern.MatchString(key) {
		return nil, ErrInvalidPricingInput
	}
	out, _, err := s.repo.GetCatalogCreationReceipt(ctx, p.UserID, key)
	return out, err
}

// Normalize once before consent and again before execution, so confirmation,
// deduplication and receipt hashing use the same effective business values.
func NormalizeCatalogCreation(in pricing.CatalogCreationInput) (pricing.CatalogCreationInput, error) {
	bad := func(message string) (pricing.CatalogCreationInput, error) {
		return in, fmt.Errorf("%w: %s", ErrInvalidPricingInput, message)
	}
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	in.UnitCode = strings.TrimSpace(in.UnitCode)
	in.NameID = strings.ToLower(strings.TrimSpace(in.NameID))
	in.PriceSource = strings.TrimSpace(in.PriceSource)
	if !directRequestKeyPattern.MatchString(in.RequestKey) {
		return bad("request_key must contain 16-80 letters, digits, underscores or hyphens")
	}
	if in.EntityType != "library" {
		if in.EntityType != "unit" && in.EntityType != "nomenclature" {
			return bad("unsupported catalog entity")
		}
		if in.Name == "" || utf8.RuneCountInString(in.Name) > 500 || in.UnitCode == "" || utf8.RuneCountInString(in.UnitCode) > 64 {
			return bad("name and unit code are required and must be within length limits")
		}
		if in.ExpectedNameVersion != "" || in.NameID != "" || in.ItemType != "" || in.MaterialType != "" || in.UnitRate != 0 || in.Currency != "" || in.ConsumptionCoefficient != nil || in.DeliveryPriceType != "" || in.DeliveryAmount != nil || in.PriceSource != "" {
			return bad("unit/nomenclature creation accepts only name, unit code and kind")
		}
		if in.EntityType == "unit" && in.Kind != "" {
			return bad("a unit has no work/material kind")
		}
		if in.EntityType == "nomenclature" && in.Kind != "work" && in.Kind != "material" {
			return bad("kind must be work or material")
		}
		return in, nil
	}
	if in.Name != "" || in.UnitCode != "" {
		return bad("library creation takes an existing name_id; name and unit come from nomenclature")
	}
	if in.Kind != "work" && in.Kind != "material" {
		return bad("kind must be work or material")
	}
	if !catalogUUIDPattern.MatchString(in.NameID) {
		return bad("name_id must be a nomenclature UUID")
	}
	if !sourceVersionPattern.MatchString(in.ExpectedNameVersion) {
		return bad("expected_name_version from a nomenclature search/creation is required")
	}
	if !positiveFinite(in.UnitRate) || !slices.Contains([]string{"RUB", "USD", "EUR", "CNY"}, in.Currency) {
		return bad("a positive finite unit_rate and explicit currency RUB/USD/EUR/CNY are required")
	}
	if in.PriceSource == "" || utf8.RuneCountInString(in.PriceSource) > 2000 {
		return bad("price_source must identify the user-provided rate or quote; do not invent a price")
	}
	if in.Kind == "work" {
		if in.ItemType == "" {
			in.ItemType = calc.BoqRab
		}
		if !calc.IsWorkBoqType(in.ItemType) || in.MaterialType != "" || in.ConsumptionCoefficient != nil || in.DeliveryPriceType != "" || in.DeliveryAmount != nil {
			return bad("work creation accepts a work item type and no material recipe/delivery fields")
		}
	} else {
		if in.ItemType == "" {
			in.ItemType = calc.BoqMat
		}
		if in.MaterialType == "" {
			in.MaterialType = "основн."
		}
		if in.DeliveryPriceType == "" {
			in.DeliveryPriceType = calc.DeliveryInPrice
		}
		if in.ConsumptionCoefficient == nil {
			one := 1.0
			in.ConsumptionCoefficient = &one
		}
		if in.DeliveryAmount == nil {
			zero := 0.0
			in.DeliveryAmount = &zero
		}
		if !calc.IsMaterialBoqType(in.ItemType) || !slices.Contains([]string{"основн.", "вспомогат."}, in.MaterialType) || !positiveFinite(*in.ConsumptionCoefficient) {
			return bad("valid material type and positive finite consumption are required")
		}
		if !slices.Contains([]string{calc.DeliveryInPrice, calc.DeliveryNotInPrice, calc.DeliveryAmount}, in.DeliveryPriceType) || *in.DeliveryAmount < 0 || math.IsNaN(*in.DeliveryAmount) || math.IsInf(*in.DeliveryAmount, 0) {
			return bad("invalid delivery type or amount")
		}
		if in.DeliveryPriceType != calc.DeliveryAmount && *in.DeliveryAmount != 0 {
			return bad("delivery_amount is only allowed for delivery_price_type=суммой")
		}
	}
	return in, nil
}

func (s *PricingService) DescribeCatalogName(ctx context.Context, p pricing.Principal, in pricing.CatalogCreationInput) (*pricing.CatalogName, error) {
	u, err := s.Authorize(ctx, p, mcpauth.ScopeLibraryCreate, "/library")
	if err != nil {
		return nil, err
	}
	if !slices.Contains([]string{"engineer", "veduschiy_inzhener", "administrator", "developer"}, u.RoleCode) {
		return nil, ErrPricingForbidden
	}
	// A retry describes the already-confirmed snapshot, even if the underlying
	// nomenclature was subsequently renamed/deleted. The hash includes client
	// and every normalized input; a changed request cannot borrow that consent.
	normalized, err := NormalizeCatalogCreation(in)
	if err != nil {
		return nil, err
	}
	_, hash, err := catalogCreationPayload(p.ClientID, normalized)
	if err != nil {
		return nil, err
	}
	prior, priorHash, err := s.repo.GetCatalogCreationReceipt(ctx, p.UserID, normalized.RequestKey)
	if err != nil {
		return nil, err
	}
	if prior != nil {
		if priorHash != hash {
			return nil, repository.ErrCatalogKeyReused
		}
		return &pricing.CatalogName{ID: normalized.NameID, Kind: normalized.Kind, Name: prior.Name, UnitCode: prior.UnitCode, Version: normalized.ExpectedNameVersion}, nil
	}
	n, err := s.repo.GetCatalogName(ctx, in.Kind, in.NameID)
	if err != nil {
		return nil, err
	}
	if n.Version != in.ExpectedNameVersion {
		return nil, repository.ErrCatalogNameStale
	}
	return n, nil
}

func (s *PricingService) CreateCatalogEntity(ctx context.Context, p pricing.Principal, in pricing.CatalogCreationInput) (*pricing.CatalogCreationResult, error) {
	scope := mcpauth.ScopeNomenclatureCreate
	if in.EntityType == "library" {
		scope = mcpauth.ScopeLibraryCreate
	}
	u, err := s.Authorize(ctx, p, scope, "/library")
	if err != nil {
		return nil, err
	}
	// Explicit user-approved policy: engineers can create shared records through
	// these tools. Existing portal edit/delete/template role gates are unchanged.
	if !slices.Contains([]string{"engineer", "veduschiy_inzhener", "administrator", "developer"}, u.RoleCode) {
		return nil, ErrPricingForbidden
	}
	if !in.Confirm {
		return nil, fmt.Errorf("%w: explicit creation confirmation is required", ErrInvalidPricingInput)
	}
	in, err = NormalizeCatalogCreation(in)
	if err != nil {
		return nil, err
	}
	encoded, hash, err := catalogCreationPayload(p.ClientID, in)
	if err != nil {
		return nil, err
	}
	prior, priorHash, err := s.repo.GetCatalogCreationReceipt(ctx, p.UserID, in.RequestKey)
	if err != nil {
		return nil, err
	}
	if prior != nil {
		if priorHash != hash {
			return nil, repository.ErrCatalogKeyReused
		}
		prior.Replayed = true
		s.invalidateCatalogCaches()
		return prior, nil
	}
	if !s.features.WriteEnabled || !s.features.CatalogWriteEnabled {
		return nil, ErrPricingDisabled
	}
	out, err := s.repo.CreateCatalogEntity(ctx, p.UserID, in.RequestKey, hash, encoded, in)
	if err == nil {
		s.invalidateCatalogCaches()
	}
	return out, err
}

func catalogCreationPayload(clientID string, in pricing.CatalogCreationInput) ([]byte, string, error) {
	// Preview and execution share the fingerprint of the confirmed command.
	in.Confirm = true
	encoded, err := json.Marshal(struct {
		ClientID string                       `json:"client_id"`
		Input    pricing.CatalogCreationInput `json:"input"`
	}{clientID, in})
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(sum[:]), nil
}

func (s *PricingService) invalidateCatalogCaches() {
	if s.cache == nil {
		return
	}
	for _, prefix := range []string{"ref:units", "ref:material_names", "ref:work_names"} {
		s.cache.DeleteByPrefix(prefix)
	}
	s.cache.Delete("works-library:all")
	s.cache.Delete("materials-library:all")
}
