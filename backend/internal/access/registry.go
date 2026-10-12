package access

import "encoding/json"

// FullAccessRoles contains role codes whose empty allowed_pages array in the DB
// means "no restriction" (i.e., they see all pages). Any role not listed here
// that has an empty allowed_pages is treated the same — full access — which
// mirrors the frontend hasPageAccess() behaviour: empty array = all pages.
//
// Listing them explicitly makes the intent clear for new developers.
var FullAccessRoles = map[string]bool{
	"administrator":      true,
	"developer":          true,
	"director":           true,
	"veduschiy_inzhener": true,
}

// AllPages is the complete ordered list of page paths registered in the app.
// Keep this in sync with src/lib/supabase/types.ts ALL_PAGES.
var AllPages = []string{
	"/dashboard",
	"/tenders",
	"/positions",
	"/library",
	"/library/templates",
	"/bsm",
	"/costs",
	"/analytics/comparison",
	"/financial-indicators",
	"/commerce/proposal",
	"/admin/nomenclatures",
	"/admin/construction_cost",
	"/admin/insurance",
	"/admin/import-log",
	"/admin/users",
	"/admin/roles",
	"/admin/markup",
	"/admin/ai-settings",
	"/tasks",
	"/tender-timeline",
	"/projects",
	"/settings/agents",
}

// GetAllowedPages computes the set of pages a user may visit.
//
// Logic (mirrors frontend hasPageAccess):
//  1. If allowedPagesJSON is null/empty JSON array → return AllPages (no
//     restriction regardless of role).
//  2. Otherwise return the pages listed in allowedPagesJSON.
//
// The roleCode parameter is kept for forward compatibility (e.g., future role-
// level default page sets) but is not currently used for filtering.
func GetAllowedPages(roleCode string, allowedPagesJSON []byte) []string {
	// Treat nil or empty bytes as "no restriction".
	if len(allowedPagesJSON) == 0 {
		return AllPages
	}

	var pages []string
	if err := json.Unmarshal(allowedPagesJSON, &pages); err != nil {
		// Corrupt JSON — fail safe by returning full access.
		return AllPages
	}

	// Empty JSON array ("[]") also means full access.
	if len(pages) == 0 {
		return AllPages
	}

	return pages
}

// HasAccess reports whether the given list of allowed pages includes path.
// An empty allowedPages slice is treated as full access.
func HasAccess(allowedPages []string, path string) bool {
	if len(allowedPages) == 0 {
		return true
	}
	for _, p := range allowedPages {
		if p == path {
			return true
		}
	}
	return false
}
