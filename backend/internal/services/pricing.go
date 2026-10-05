package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/su10/hubtender/backend/internal/access"
	"github.com/su10/hubtender/backend/internal/cache"
	"github.com/su10/hubtender/backend/internal/domain/user"
	"github.com/su10/hubtender/backend/internal/mcpauth"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/repository"
)

var (
	ErrPricingForbidden    = errors.New("pricing operation is not permitted")
	ErrPricingDisabled     = errors.New("pricing writes are disabled")
	ErrInvalidPricingInput = errors.New("invalid pricing input")
)

type PricingFeatures struct {
	WriteEnabled         bool
	TemplateWriteEnabled bool
}

type PricingService struct {
	repo    *repository.PricingRepo
	users   *repository.UserRepo
	library *repository.LibraryRepo
	grants  interface {
		IsGrantActive(context.Context, string, string) (bool, error)
	}
	recalcQueue *RecalcQueue
	cache       *cache.InMem
	features    PricingFeatures
}

func (s *PricingService) WithRecalcQueue(q *RecalcQueue) *PricingService {
	s.recalcQueue = q
	return s
}

func (s *PricingService) WithCache(c *cache.InMem) *PricingService {
	s.cache = c
	return s
}

func NewPricingService(repo *repository.PricingRepo, users *repository.UserRepo, library *repository.LibraryRepo, grants interface {
	IsGrantActive(context.Context, string, string) (bool, error)
}, features PricingFeatures) *PricingService {
	return &PricingService{repo: repo, users: users, library: library, grants: grants, features: features}
}

func (s *PricingService) Authorize(ctx context.Context, p pricing.Principal, scope, page string) (*user.User, error) {
	if p.UserID == "" || !slices.Contains(p.Scopes, scope) {
		return nil, ErrPricingForbidden
	}
	u, err := s.users.GetByID(ctx, p.UserID)
	if err != nil || u == nil || !u.AccessEnabled || !strings.EqualFold(u.AccessStatus, "approved") {
		return nil, ErrPricingForbidden
	}
	if page != "" && !access.HasAccess(u.AllowedPages, page) {
		return nil, ErrPricingForbidden
	}
	if p.ClientID != "" {
		if s.grants == nil {
			return nil, ErrPricingForbidden
		}
		active, err := s.grants.IsGrantActive(ctx, p.UserID, p.ClientID)
		if err != nil || !active {
			return nil, ErrPricingForbidden
		}
	}
	return u, nil
}

func (s *PricingService) ListTenders(ctx context.Context, p pricing.Principal, search string, archived *bool, limit, offset int) ([]pricing.TenderSummary, pricing.Page, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopeTendersRead, "/positions"); err != nil {
		return nil, pricing.Page{}, err
	}
	limit, offset = normalizePage(limit, offset, 50)
	rows, total, err := s.repo.ListPricingTenders(ctx, search, archived, limit, offset)
	page := buildPage(limit, offset, total, len(rows))
	return rows, page, err
}

func (s *PricingService) GetPricingState(ctx context.Context, p pricing.Principal, tenderID string, limit, offset int) (*pricing.PricingState, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopeTendersRead, "/positions"); err != nil {
		return nil, err
	}
	limit, offset = normalizePage(limit, offset, 50)
	return s.repo.GetPricingState(ctx, tenderID, limit, offset)
}

func (s *PricingService) SearchArchive(ctx context.Context, p pricing.Principal, in pricing.ArchiveSearchInput) ([]pricing.ArchiveCandidate, pricing.Page, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopeArchiveRead, "/positions"); err != nil {
		return nil, pricing.Page{}, err
	}
	in.Query = strings.TrimSpace(in.Query)
	if len([]rune(in.Query)) < 2 || len([]rune(in.Query)) > 300 {
		return nil, pricing.Page{}, fmt.Errorf("%w: query must contain 2-300 characters", ErrInvalidPricingInput)
	}
	in.Limit, in.Offset = normalizePage(in.Limit, in.Offset, 20)
	raw, err := s.repo.RawArchiveCandidates(ctx, in, 500)
	if err != nil {
		return nil, pricing.Page{}, err
	}
	matched := make([]pricing.ArchiveCandidate, 0, len(raw))
	for _, c := range raw {
		score, level, warnings, ok := pricing.ScoreCandidate(in.Query, in.UnitCode, in.DetailCostCategoryID, in.HousingClass, in.ConstructionScope, c, time.Now().UTC())
		if !ok {
			continue
		}
		c.Confidence, c.MatchLevel, c.Warnings = score, level, warnings
		c.Rationale = fmt.Sprintf("детерминированная оценка %.0f%%: название/семейство, единица, категория, давность и контекст", score*100)
		matched = append(matched, c)
	}
	sort.SliceStable(matched, func(i, j int) bool { return matched[i].Confidence > matched[j].Confidence })
	matched = pricing.ApplyOutlierWarnings(matched)
	total := len(matched)
	start := min(in.Offset, total)
	end := min(start+in.Limit, total)
	return matched[start:end], buildPage(in.Limit, in.Offset, total, end-start), nil
}

func (s *PricingService) SearchLibrary(ctx context.Context, p pricing.Principal, query, kind, unit string, limit, offset int) ([]pricing.LibraryCandidate, pricing.Page, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopeLibraryRead, "/library"); err != nil {
		return nil, pricing.Page{}, err
	}
	if len([]rune(strings.TrimSpace(query))) < 2 {
		return nil, pricing.Page{}, fmt.Errorf("%w: query must contain at least 2 characters", ErrInvalidPricingInput)
	}
	limit, offset = normalizePage(limit, offset, 20)
	rows, total, err := s.repo.SearchLibrary(ctx, query, kind, unit, limit, offset)
	return rows, buildPage(limit, offset, total, len(rows)), err
}

func (s *PricingService) ListTemplates(ctx context.Context, p pricing.Principal, search string, limit, offset int) ([]pricing.TemplateSummary, pricing.Page, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopeLibraryRead, "/library/templates"); err != nil {
		return nil, pricing.Page{}, err
	}
	limit, offset = normalizePage(limit, offset, 20)
	rows, total, err := s.repo.ListTemplatesForPricing(ctx, search, limit, offset)
	return rows, buildPage(limit, offset, total, len(rows)), err
}

func (s *PricingService) GetTemplate(ctx context.Context, p pricing.Principal, id string) (*pricing.TemplateDetail, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopeLibraryRead, "/library/templates"); err != nil {
		return nil, err
	}
	return s.repo.GetTemplateForPricing(ctx, id)
}

func (s *PricingService) QAReport(ctx context.Context, p pricing.Principal, tenderID string) (*pricing.QAReport, error) {
	if _, err := s.Authorize(ctx, p, mcpauth.ScopeTendersRead, "/positions"); err != nil {
		return nil, err
	}
	return s.repo.GetPricingQA(ctx, tenderID)
}

func (s *PricingService) CreateTemplate(ctx context.Context, p pricing.Principal, in repository.CreateTemplateInput) (string, error) {
	u, err := s.Authorize(ctx, p, mcpauth.ScopeTemplatesWrite, "/library/templates")
	if err != nil {
		return "", err
	}
	if !s.features.TemplateWriteEnabled || !slices.Contains([]string{"veduschiy_inzhener", "administrator", "developer"}, u.RoleCode) {
		return "", ErrPricingDisabled
	}
	return s.library.CreateTemplate(ctx, in)
}

func (s *PricingService) UpdateTemplate(ctx context.Context, p pricing.Principal, id string, in repository.UpdateTemplateInput) error {
	u, err := s.Authorize(ctx, p, mcpauth.ScopeTemplatesWrite, "/library/templates")
	if err != nil {
		return err
	}
	if !s.features.TemplateWriteEnabled || !slices.Contains([]string{"veduschiy_inzhener", "administrator", "developer"}, u.RoleCode) {
		return ErrPricingDisabled
	}
	return s.library.UpdateTemplate(ctx, id, in)
}
