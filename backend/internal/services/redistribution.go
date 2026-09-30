package services

import (
	"context"
	"fmt"

	"github.com/su10/hubtender/backend/internal/cache"
	"github.com/su10/hubtender/backend/internal/calc"
	"github.com/su10/hubtender/backend/internal/repository"
)

type redistributionRepoer interface {
	SaveAuthoritative(
		ctx context.Context,
		tenderID, tacticID string,
		rules calc.RedistributionRulesInput,
		createdBy string,
	) (*repository.RedistributionSaveOutput, error)
	ClearAuthoritative(ctx context.Context, tenderID, tacticID string) (int, error)
	LoadResults(ctx context.Context, tenderID, tacticID string) (*repository.RedistributionLoad, error)
}

// RedistributionService wraps the repo with cache invalidation.
type RedistributionService struct {
	repo  redistributionRepoer
	cache *cache.InMem
}

// NewRedistributionService creates a RedistributionService.
func NewRedistributionService(repo *repository.RedistributionRepo, c *cache.InMem) *RedistributionService {
	return &RedistributionService{repo: repo, cache: c}
}

// Save recalculates and persists the redistribution snapshot server-side (the
// client contributes ONLY rules) and invalidates caches that depend on
// redistributed work costs — only AFTER the repository committed. On any error
// there are NO side effects. pg_notify triggers on cost_redistribution_results
// already broadcast tender:<id> to WebSocket subscribers.
func (s *RedistributionService) Save(
	ctx context.Context,
	tenderID, tacticID string,
	rules calc.RedistributionRulesInput,
	createdBy string,
) (*repository.RedistributionSaveOutput, error) {
	out, err := s.repo.SaveAuthoritative(ctx, tenderID, tacticID, rules, createdBy)
	if err != nil {
		return nil, fmt.Errorf("redistributionService.Save: %w", err)
	}

	s.cache.Delete("tender:overview:" + tenderID)
	s.cache.Delete("positions:with_costs:" + tenderID)
	s.cache.DeleteByPrefix(tenderListKeyPrefix) // grand total may have changed

	return out, nil
}

// Clear deletes the redistribution snapshot (every rule removed on the page).
// Caches are invalidated only when something was actually deleted and
// committed; a no-op clear leaves them intact.
func (s *RedistributionService) Clear(ctx context.Context, tenderID, tacticID string) (int, error) {
	deleted, err := s.repo.ClearAuthoritative(ctx, tenderID, tacticID)
	if err != nil {
		return 0, fmt.Errorf("redistributionService.Clear: %w", err)
	}
	if deleted > 0 {
		s.cache.Delete("tender:overview:" + tenderID)
		s.cache.Delete("positions:with_costs:" + tenderID)
		s.cache.DeleteByPrefix(tenderListKeyPrefix)
	}
	return deleted, nil
}

// LoadResults returns the saved redistribution snapshot for (tenderID,
// tacticID) with its status. Read-only — no caching.
func (s *RedistributionService) LoadResults(
	ctx context.Context,
	tenderID, tacticID string,
) (*repository.RedistributionLoad, error) {
	out, err := s.repo.LoadResults(ctx, tenderID, tacticID)
	if err != nil {
		return nil, fmt.Errorf("redistributionService.LoadResults: %w", err)
	}
	return out, nil
}
