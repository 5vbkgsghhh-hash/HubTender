package services

import (
	"context"
	"fmt"

	"github.com/su10/hubtender/backend/internal/cache"
	"github.com/su10/hubtender/backend/internal/repository"
)

// ImportLogService is a thin wrapper around the import-log repo.
type ImportLogService struct {
	repo   *repository.ImportLogRepo
	cache  *cache.InMem
	recalc Enqueuer
}

// NewImportLogService creates an ImportLogService.
func NewImportLogService(repo *repository.ImportLogRepo, c *cache.InMem) *ImportLogService {
	return &ImportLogService{repo: repo, cache: c}
}

// WithRecalcQueue wires the commercial-recalc queue so a cancelled import
// re-triggers the server-side recalc of the tender (as the import itself does).
func (s *ImportLogService) WithRecalcQueue(q Enqueuer) *ImportLogService {
	s.recalc = q
	return s
}

func (s *ImportLogService) ListSessions(ctx context.Context, tenderID, restrictUserID string) ([]repository.ImportSessionRow, error) {
	return s.repo.ListSessions(ctx, tenderID, restrictUserID)
}
func (s *ImportLogService) UsersByIDs(ctx context.Context, ids []string) ([]repository.ImportLogUserRow, error) {
	return s.repo.UsersByIDs(ctx, ids)
}
func (s *ImportLogService) TendersByIDs(ctx context.Context, ids []string) ([]repository.TenderShort, error) {
	return s.repo.TendersByIDs(ctx, ids)
}
func (s *ImportLogService) ListAllTendersForFilter(ctx context.Context) ([]repository.TenderShort, error) {
	return s.repo.ListAllTendersForFilter(ctx)
}

// CancelSession undoes the import and, after the commit, evicts the same
// tender caches as ImportBoqService.BulkImport and enqueues the recalc —
// иначе страница позиций и коммерция показывали бы удалённые строки.
func (s *ImportLogService) CancelSession(ctx context.Context, sessionID, cancelledBy string, requireOwnership bool) (*repository.CancelResult, error) {
	res, err := s.repo.CancelSession(ctx, sessionID, cancelledBy, requireOwnership)
	if err != nil {
		return nil, fmt.Errorf("importLogService.CancelSession: %w", err)
	}
	if res.TenderID != "" {
		if s.cache != nil {
			s.cache.Delete("tender:overview:" + res.TenderID)
			s.cache.Delete("positions:with_costs:" + res.TenderID)
			s.cache.DeleteByPrefix(tenderListKeyPrefix)
		}
		if s.recalc != nil {
			s.recalc.Enqueue(res.TenderID)
		}
	}
	return res, nil
}
