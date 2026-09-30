package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/su10/hubtender/backend/internal/cache"
	"github.com/su10/hubtender/backend/internal/calc"
	"github.com/su10/hubtender/backend/internal/repository"
)

// Stage 0.1.2.3a (§17.10-14): the redistribution service has NO side effects on
// error — cache stays intact, no success response — and exactly ONE batched
// repository call per save (no per-row calls).

type fakeRedistributionRepo struct {
	out     *repository.RedistributionSaveOutput
	cleared int
	err     error
	calls   int
}

func (f *fakeRedistributionRepo) SaveAuthoritative(
	context.Context, string, string, calc.RedistributionRulesInput, string,
) (*repository.RedistributionSaveOutput, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

func (f *fakeRedistributionRepo) ClearAuthoritative(context.Context, string, string) (int, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	return f.cleared, nil
}

func (f *fakeRedistributionRepo) LoadResults(context.Context, string, string) (*repository.RedistributionLoad, error) {
	panic("not used")
}

func TestRedistributionSave_ErrorHasNoSideEffects(t *testing.T) {
	const tenderID = "tender-1"
	domainErrs := []error{
		&calc.InvalidRedistributionRulesError{Issues: []calc.RuleIssue{{Code: "X"}}},
		&calc.RedistributionTacticMismatchError{TenderID: tenderID},
		&calc.UnbalancedRedistributionError{TotalDeducted: 1, TotalAdded: 2},
		&calc.RedistributionNoBoqItemsError{TenderID: tenderID},
		&calc.MissingFXRateError{Currency: "USD"},
		// Stage 0.1.2.3b: prepared-pipeline failures are equally side-effect-free.
		&calc.InvalidInsuranceConfigurationError{Field: "judicial_pct", Reason: "negative value"},
		&calc.InvalidPreparedRedistributionResultError{Field: "summary.final_total", Reason: "mismatch"},
	}
	for _, dErr := range domainErrs {
		t.Run(dErr.Error(), func(t *testing.T) {
			c := cache.New()
			c.Set("tender:overview:"+tenderID, "cached", time.Minute)
			c.Set("positions:with_costs:"+tenderID, "cached", time.Minute)
			c.Set(tenderListKeyPrefix+"all", "cached", time.Minute)

			repo := &fakeRedistributionRepo{err: dErr}
			svc := &RedistributionService{repo: repo, cache: c}

			out, err := svc.Save(context.Background(), tenderID, "tactic-1", calc.RedistributionRulesInput{}, "user")
			if out != nil {
				t.Fatalf("result must be nil on error, got %+v", out)
			}
			if !errors.Is(err, dErr) {
				t.Fatalf("domain error lost through %%w: %v", err)
			}
			for _, key := range []string{"tender:overview:" + tenderID, "positions:with_costs:" + tenderID, tenderListKeyPrefix + "all"} {
				if _, ok := c.Get(key); !ok {
					t.Fatalf("cache key %q must NOT be invalidated on error", key)
				}
			}
		})
	}
}

func TestRedistributionSave_SuccessSingleBatchedCall(t *testing.T) {
	const tenderID = "tender-1"
	c := cache.New()
	c.Set("tender:overview:"+tenderID, "cached", time.Minute)
	c.Set("positions:with_costs:"+tenderID, "cached", time.Minute)
	c.Set(tenderListKeyPrefix+"all", "cached", time.Minute)

	repo := &fakeRedistributionRepo{out: &repository.RedistributionSaveOutput{
		SavedCount: 125, TenderID: tenderID,
		Results: make([]repository.RedistributionRecord, 125),
	}}
	svc := &RedistributionService{repo: repo, cache: c}

	out, err := svc.Save(context.Background(), tenderID, "tactic-1", calc.RedistributionRulesInput{}, "user")
	if err != nil || out == nil {
		t.Fatalf("unexpected failure: %v", err)
	}
	// §17.13/14 — exactly ONE repository call for the whole 125-row batch.
	if repo.calls != 1 {
		t.Fatalf("repo called %d times, want 1 (batched writer)", repo.calls)
	}
	for _, key := range []string{"tender:overview:" + tenderID, "positions:with_costs:" + tenderID, tenderListKeyPrefix + "all"} {
		if _, ok := c.Get(key); ok {
			t.Fatalf("cache key %q must be invalidated on success", key)
		}
	}
}

func TestRedistributionClear_InvalidatesCacheOnlyWhenDeleted(t *testing.T) {
	const tenderID = "tender-1"
	keys := []string{"tender:overview:" + tenderID, "positions:with_costs:" + tenderID, tenderListKeyPrefix + "all"}
	for _, tc := range []struct {
		name        string
		cleared     int
		err         error
		invalidated bool
	}{
		{name: "deleted", cleared: 5, invalidated: true},
		{name: "noop", cleared: 0, invalidated: false},
		{name: "error", err: &calc.MissingFXRateError{Currency: "USD"}, invalidated: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cache.New()
			for _, key := range keys {
				c.Set(key, "cached", time.Minute)
			}
			repo := &fakeRedistributionRepo{cleared: tc.cleared, err: tc.err}
			svc := &RedistributionService{repo: repo, cache: c}

			n, err := svc.Clear(context.Background(), tenderID, "tactic-1")
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("domain error lost through %%w: %v", err)
				}
			} else if err != nil || n != tc.cleared {
				t.Fatalf("n=%d err=%v, want %d", n, err, tc.cleared)
			}
			for _, key := range keys {
				if _, ok := c.Get(key); ok == tc.invalidated {
					t.Fatalf("cache key %q: present=%v, want invalidated=%v", key, ok, tc.invalidated)
				}
			}
		})
	}
}
