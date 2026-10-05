package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/su10/hubtender/backend/internal/mcpauth"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/repository"
)

var directRequestKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,80}$`)

// DirectPricingInput commits exactly one source-backed BOQ item to the VOR.
// Updates preserve the item's name, category, quantity and parent link.
type DirectPricingInput struct {
	ConversionCoefficient *float64 `json:"conversion_coefficient,omitempty"`
	TenderID              string   `json:"tender_id"`
	TargetPositionID      string   `json:"target_position_id"`
	TargetItemID          *string  `json:"target_item_id,omitempty"`
	ParentWorkItemID      *string  `json:"parent_work_item_id,omitempty"`
	DetailCostCategoryID  *string  `json:"detail_cost_category_id,omitempty"`
	SourceKind            string   `json:"source_kind"` // archive | library
	SourceID              string   `json:"source_id"`
	ExpectedSourceRate    float64  `json:"expected_source_rate"`
	LibraryKind           string   `json:"library_kind,omitempty"` // work | material
	Quantity              *float64 `json:"quantity,omitempty"`
	ExpectedETag          *string  `json:"expected_etag,omitempty"`
	ExpectedRevision      int64    `json:"expected_revision"`
	RequestKey            string   `json:"request_key"`
	Rationale             string   `json:"rationale,omitempty"`
	Confirm               bool     `json:"confirm"`
}

func (s *PricingService) GetDirectBoqItem(ctx context.Context, p pricing.Principal, id string) (*repository.BoqItemRow, string, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopeTendersRead, "/positions"); err != nil {
		return nil, "", err
	}
	return s.repo.GetCurrentBoqItem(ctx, id)
}

func (s *PricingService) ListDirectBoqItems(ctx context.Context, p pricing.Principal, tenderID, positionID string, limit, offset int) ([]repository.BoqItemRow, pricing.Page, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopeTendersRead, "/positions"); err != nil {
		return nil, pricing.Page{}, err
	}
	position, err := s.repo.GetTargetPosition(ctx, positionID)
	if err != nil {
		return nil, pricing.Page{}, err
	}
	if position.TenderID != tenderID {
		return nil, pricing.Page{}, ErrPricingForbidden
	}
	limit, offset = normalizePage(limit, offset, 100)
	items, total, err := s.repo.ListDirectBoqItems(ctx, tenderID, positionID, limit, offset)
	return items, buildPage(limit, offset, total, len(items)), err
}

func (s *PricingService) ListDirectCostCategories(ctx context.Context, p pricing.Principal, search string, limit, offset int) ([]pricing.CostCategory, pricing.Page, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopeLibraryRead, "/positions"); err != nil {
		return nil, pricing.Page{}, err
	}
	limit, offset = normalizePage(limit, offset, 100)
	items, total, err := s.repo.ListDirectCostCategories(ctx, strings.TrimSpace(search), limit, offset)
	return items, buildPage(limit, offset, total, len(items)), err
}

func (s *PricingService) GetDirectPricingReceipt(ctx context.Context, p pricing.Principal, requestKey string) (*pricing.DirectPricingResult, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopeTendersRead, "/positions"); err != nil {
		return nil, err
	}
	if !directRequestKeyPattern.MatchString(requestKey) {
		return nil, fmt.Errorf("%w: invalid request_key", ErrInvalidPricingInput)
	}
	result, _, err := s.repo.GetDirectPricingReceipt(ctx, p.UserID, requestKey)
	return result, err
}

func (s *PricingService) ApplyDirectPrice(ctx context.Context, p pricing.Principal, in DirectPricingInput) (*pricing.DirectPricingResult, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopePricingWrite, "/positions"); err != nil {
		return nil, err
	}
	if !in.Confirm || in.TenderID == "" || in.TargetPositionID == "" || !directRequestKeyPattern.MatchString(in.RequestKey) || in.ExpectedRevision < 0 {
		return nil, fmt.Errorf("%w: confirm, IDs, request_key and expected_revision are required", ErrInvalidPricingInput)
	}
	if in.SourceKind != "current" && (in.SourceID == "" || !positiveFinite(in.ExpectedSourceRate)) {
		return nil, fmt.Errorf("%w: source_id and positive expected_source_rate are required", ErrInvalidPricingInput)
	}
	if (in.Quantity != nil && !positiveFinite(*in.Quantity)) || (in.ConversionCoefficient != nil && !positiveFinite(*in.ConversionCoefficient)) {
		return nil, fmt.Errorf("%w: quantity and conversion_coefficient must be finite and positive", ErrInvalidPricingInput)
	}
	if in.ParentWorkItemID != nil && in.Quantity != nil {
		return nil, repository.ErrLinkedMaterialQuantity
	}
	encoded, err := json.Marshal(struct {
		ClientID string             `json:"client_id"`
		Input    DirectPricingInput `json:"input"`
	}{ClientID: p.ClientID, Input: in})
	if err != nil {
		return nil, err
	}
	hashBytes := sha256.Sum256(encoded)
	hash := hex.EncodeToString(hashBytes[:])
	prior, priorHash, err := s.repo.GetDirectPricingReceipt(ctx, p.UserID, in.RequestKey)
	if err != nil {
		return nil, err
	}
	if prior != nil {
		if priorHash != hash {
			return nil, repository.ErrDirectRequestKeyReused
		}
		prior.Replayed = true
		return prior, nil
	}
	if !s.features.WriteEnabled {
		return nil, ErrPricingDisabled
	}
	if in.SourceKind == "archive" {
		if _, err := s.Authorize(ctx, p, mcpauth.ScopeArchiveRead, "/positions"); err != nil {
			return nil, err
		}
	} else if in.SourceKind == "library" {
		if _, err := s.Authorize(ctx, p, mcpauth.ScopeLibraryRead, "/library"); err != nil {
			return nil, err
		}
	}
	if in.TargetItemID == nil && in.ParentWorkItemID == nil && in.Quantity == nil {
		return nil, fmt.Errorf("%w: positive quantity is required for a new work or standalone material", ErrInvalidPricingInput)
	}
	if in.TargetItemID != nil && (in.ExpectedETag == nil || *in.ExpectedETag == "") {
		return nil, fmt.Errorf("%w: updates require expected_etag", ErrInvalidPricingInput)
	}
	if in.TargetItemID != nil && (in.ParentWorkItemID != nil || in.DetailCostCategoryID != nil) {
		return nil, fmt.Errorf("%w: updates preserve parent work and cost category", ErrInvalidPricingInput)
	}
	position, err := s.repo.GetTargetPosition(ctx, in.TargetPositionID)
	if err != nil {
		return nil, err
	}
	if position.TenderID != in.TenderID {
		return nil, repository.ErrDirectTargetInvalid
	}
	var target *repository.BoqItemRow
	if in.TargetItemID != nil {
		var etag string
		target, etag, err = s.repo.GetCurrentBoqItem(ctx, *in.TargetItemID)
		if err != nil {
			return nil, err
		}
		if target.TenderID != in.TenderID || target.ClientPositionID != position.ID || etag != *in.ExpectedETag {
			return nil, repository.ErrDirectPricingStale
		}
		if target.ParentWorkItemID != nil && in.Quantity != nil {
			return nil, repository.ErrLinkedMaterialQuantity
		}
	}
	op := pricing.DirectOperation{
		Action: "create_item", TargetPositionID: position.ID,
		TargetItemID: in.TargetItemID, ExpectedETag: in.ExpectedETag,
		SourceKind: in.SourceKind, Warnings: []string{}, PositionOrder: position.MaxSort + 1,
	}
	if target != nil {
		op.Action = "update_item"
	}
	switch in.SourceKind {
	case "archive":
		err = s.buildDirectArchive(ctx, in, position, target, &op)
	case "library":
		err = s.buildDirectLibrary(ctx, in, position, target, &op)
	case "current":
		if target == nil || (in.Quantity == nil && in.ConversionCoefficient == nil) || in.SourceID != "" || in.ExpectedSourceRate != 0 || in.LibraryKind != "" {
			return nil, fmt.Errorf("%w: current updates only quantity or conversion_coefficient on an existing row; omit source_id, expected_source_rate and library_kind", ErrInvalidPricingInput)
		}
		op.ProposedPayload = proposedFromExisting(target)
		op.MatchLevel, op.Confidence = "manual", 1
	default:
		err = fmt.Errorf("%w: source_kind must be archive, library or current", ErrInvalidPricingInput)
	}
	if err != nil {
		return nil, err
	}
	payload := &op.ProposedPayload
	parentID := in.ParentWorkItemID
	if target != nil {
		parentID = target.ParentWorkItemID
	}
	if in.SourceKind != "current" && (payload.UnitRate == nil || !positiveFinite(*payload.UnitRate)) {
		return nil, fmt.Errorf("%w: selected source has no positive unit rate", ErrInvalidPricingInput)
	}
	if parentID != nil && !samePricingFamily(payload.BoqItemType, "material") {
		return nil, repository.ErrDirectParentInvalid
	}
	if in.ConversionCoefficient != nil && parentID == nil {
		return nil, fmt.Errorf("%w: conversion_coefficient is editable only for a linked material", ErrInvalidPricingInput)
	}
	if in.Quantity != nil {
		payload.Quantity = in.Quantity
		if samePricingFamily(payload.BoqItemType, "material") {
			payload.BaseQuantity = in.Quantity
		}
	}
	if in.ConversionCoefficient != nil {
		payload.ConversionCoefficient = in.ConversionCoefficient
	}
	if parentID != nil {
		payload.BaseQuantity = nil
		// Quantity is never an agent-controlled input for a linked material.
		// Derive it from the locked parent in the commit transaction.
		payload.Quantity = nil
		if target == nil && payload.ConversionCoefficient == nil {
			parent, _, err := s.repo.GetCurrentBoqItem(ctx, *parentID)
			if err != nil {
				return nil, err
			}
			if !sameUnit(parent.UnitCode, payload.UnitCode) {
				return nil, fmt.Errorf("%w: explicit conversion_coefficient is required when material and work units differ", ErrInvalidPricingInput)
			}
			one := 1.0
			payload.ConversionCoefficient = &one
		}
	} else if payload.Quantity == nil || !positiveFinite(*payload.Quantity) {
		return nil, fmt.Errorf("%w: item quantity is missing", ErrInvalidPricingInput)
	}
	if payload.DetailCostCategoryID == nil && in.SourceKind != "current" {
		return nil, fmt.Errorf("%w: detail_cost_category_id is required", ErrInvalidPricingInput)
	}
	result, err := s.repo.ApplyDirectPricing(ctx, repository.DirectPricingCommit{
		TenderID: in.TenderID, ActorID: p.UserID, RequestKey: in.RequestKey,
		RequestHash: hash, ExpectedRevision: in.ExpectedRevision, Operation: op,
		ParentWorkItemID: parentID, RequestedQuantity: in.Quantity,
	})
	if err != nil {
		return nil, err
	}
	if !result.Replayed {
		if s.cache != nil {
			s.cache.Delete("tender:overview:" + in.TenderID)
			s.cache.Delete("positions:with_costs:" + in.TenderID)
			s.cache.DeleteByPrefix(tenderListKeyPrefix)
		}
		if s.recalcQueue != nil {
			s.recalcQueue.Enqueue(in.TenderID)
		}
	}
	return result, nil
}

func positiveFinite(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

func (s *PricingService) buildDirectArchive(ctx context.Context, in DirectPricingInput, position *repository.TargetPosition, target *repository.BoqItemRow, op *pricing.DirectOperation) error {
	source, err := s.repo.GetArchiveItem(ctx, in.SourceID)
	if err != nil {
		return err
	}
	if source == nil {
		return fmt.Errorf("%w: archive item not found", ErrInvalidPricingInput)
	}
	if source.UnitRate == nil || math.Abs(*source.UnitRate-in.ExpectedSourceRate) > 0.000001 {
		return fmt.Errorf("%w: archive source rate changed; search again", repository.ErrDirectPricingStale)
	}
	if target != nil && (!samePricingFamily(target.BoqItemType, source.ItemKind) || !sameUnit(target.UnitCode, source.UnitCode)) {
		return fmt.Errorf("%w: source family or unit differs from the target BOQ item", ErrInvalidPricingInput)
	}
	if target == nil && source.ItemKind == "work" && !sameUnit(position.UnitCode, source.UnitCode) {
		return fmt.Errorf("%w: work unit differs from the VOR position", ErrInvalidPricingInput)
	}
	if target != nil || source.ItemKind == "work" {
		name, unit, category := position.WorkName, value(position.UnitCode), ""
		if target != nil {
			unit = value(target.UnitCode)
			category = value(target.DetailCostCategoryID)
			current, err := s.repo.GetArchiveItem(ctx, target.ID)
			if err != nil {
				return err
			}
			if current == nil {
				return fmt.Errorf("%w: target BOQ name is unavailable", ErrInvalidPricingInput)
			}
			name = current.ItemName
		}
		score, level, warnings, ok := pricing.ScoreCandidate(name, unit, category, "", "", *source, time.Now().UTC())
		if !ok {
			return fmt.Errorf("%w: archive source is below the safety threshold or incompatible", ErrInvalidPricingInput)
		}
		op.MatchLevel, op.Confidence = level, score
		op.Warnings = append(op.Warnings, warnings...)
	}
	if target != nil {
		p := proposedFromExisting(target)
		p.UnitRate, p.CurrencyType = source.UnitRate, source.CurrencyType
		p.DeliveryPriceType, p.DeliveryAmount = source.DeliveryPriceType, source.DeliveryAmount
		if p.DetailCostCategoryID == nil {
			p.DetailCostCategoryID = source.DetailCostCategoryID
		}
		if source.QuoteLink != nil {
			p.QuoteLink = source.QuoteLink
		}
		if source.QuotePriceDate != nil {
			p.QuotePriceDate = source.QuotePriceDate
		}
		if source.QuoteValidUntil != nil {
			p.QuoteValidUntil = source.QuoteValidUntil
		}
		op.ProposedPayload = p
	} else {
		sortNum := op.PositionOrder
		op.ProposedPayload = pricing.ProposedItem{
			BoqItemType: source.BoqItemType, MaterialType: source.MaterialType,
			Description: source.Description, UnitCode: source.UnitCode, Quantity: in.Quantity,
			UnitRate: source.UnitRate, CurrencyType: source.CurrencyType,
			DeliveryPriceType: source.DeliveryPriceType, DeliveryAmount: source.DeliveryAmount,
			ConsumptionCoefficient: source.ConsumptionCoefficient,
			DetailCostCategoryID:   source.DetailCostCategoryID,
			MaterialNameID:         source.MaterialNameID, WorkNameID: source.WorkNameID,
			QuoteLink: source.QuoteLink, QuotePriceDate: source.QuotePriceDate,
			QuoteValidUntil: source.QuoteValidUntil, SortNumber: &sortNum,
		}
		if in.DetailCostCategoryID != nil {
			op.ProposedPayload.DetailCostCategoryID = in.DetailCostCategoryID
		}
		if source.ItemKind == "material" {
			op.Warnings = append(op.Warnings, "Материал добавляется по явно выбранному источнику; проверьте расход на позицию")
			if source.TenderDate.Before(time.Now().UTC().AddDate(0, 0, -180)) {
				op.Warnings = append(op.Warnings, "Архивная цена старше 180 дней")
			}
			if in.ParentWorkItemID == nil {
				op.Warnings = append(op.Warnings, "Материал добавлен без связи с работой")
			}
		}
	}
	if source.TenderID == in.TenderID {
		op.Warnings = append(op.Warnings, "Источник находится в том же тендере")
	}
	op.SourceRef = pricing.SourceRef{TenderID: &source.TenderID, ItemID: &source.ItemID,
		Rate: source.UnitRate, Currency: source.CurrencyType, Date: &source.TenderDate}
	if op.MatchLevel == "" {
		op.MatchLevel, op.Confidence = "manual", 1
	}
	op.Rationale = stringPtr(defaultText(in.Rationale, "Прямая расценка ВОР по архивному источнику"))
	return nil
}

func (s *PricingService) buildDirectLibrary(ctx context.Context, in DirectPricingInput, position *repository.TargetPosition, target *repository.BoqItemRow, op *pricing.DirectOperation) error {
	if in.LibraryKind != "work" && in.LibraryKind != "material" {
		return fmt.Errorf("%w: library_kind must be work or material", ErrInvalidPricingInput)
	}
	item, err := s.repo.GetLibraryItem(ctx, in.SourceID, in.LibraryKind)
	if err != nil {
		return err
	}
	if item == nil || item.Kind != in.LibraryKind {
		return fmt.Errorf("%w: library item not found", ErrInvalidPricingInput)
	}
	unit, currency, rate := item.UnitCode, item.CurrencyType, item.UnitRate
	if math.Abs(rate-in.ExpectedSourceRate) > 0.000001 {
		return fmt.Errorf("%w: library source rate changed; search again", repository.ErrDirectPricingStale)
	}
	if target != nil && (!samePricingFamily(target.BoqItemType, item.Kind) || !sameUnit(target.UnitCode, &unit)) {
		return fmt.Errorf("%w: library family or unit differs from the target BOQ item", ErrInvalidPricingInput)
	}
	if target == nil && item.Kind == "work" && !sameUnit(position.UnitCode, &unit) {
		return fmt.Errorf("%w: work unit differs from the VOR position", ErrInvalidPricingInput)
	}
	if target != nil {
		p := proposedFromExisting(target)
		p.UnitRate, p.CurrencyType = &rate, &currency
		p.DeliveryPriceType, p.DeliveryAmount = item.DeliveryPriceType, item.DeliveryAmount
		op.ProposedPayload = p
	} else {
		sortNum := op.PositionOrder
		p := pricing.ProposedItem{
			BoqItemType: item.ItemType, UnitCode: &unit, Quantity: in.Quantity,
			UnitRate: &rate, CurrencyType: &currency,
			MaterialType: item.MaterialType, DeliveryPriceType: item.DeliveryPriceType,
			DeliveryAmount: item.DeliveryAmount, ConsumptionCoefficient: item.ConsumptionCoefficient,
			SortNumber:           &sortNum,
			DetailCostCategoryID: in.DetailCostCategoryID,
		}
		if item.Kind == "work" {
			p.WorkNameID = &item.NameID
		} else {
			p.MaterialNameID = &item.NameID
			op.Warnings = append(op.Warnings, "Проверьте расход материала и категорию затрат")
			if in.ParentWorkItemID == nil {
				op.Warnings = append(op.Warnings, "Материал добавлен без связи с работой")
			}
		}
		op.ProposedPayload = p
	}
	op.SourceRef = pricing.SourceRef{LibraryID: &item.ID, Rate: &rate, Currency: &currency}
	op.MatchLevel, op.Confidence = "manual", 1
	op.Rationale = stringPtr(defaultText(in.Rationale, "Прямая расценка ВОР из библиотеки TenderHUB"))
	return nil
}

func samePricingFamily(boqType, sourceKind string) bool {
	if sourceKind == "work" {
		return strings.Contains(boqType, "раб")
	}
	return sourceKind == "material" && strings.Contains(boqType, "мат")
}

func sameUnit(a, b *string) bool {
	return a != nil && b != nil && strings.EqualFold(strings.TrimSpace(*a), strings.TrimSpace(*b))
}
