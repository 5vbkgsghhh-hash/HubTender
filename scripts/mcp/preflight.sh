#!/usr/bin/env bash
set -euo pipefail
EXPECTED_BASE="${EXPECTED_BASE_COMMIT:-0611f20bcfc9f842263323208ffa715bd98f8387}"
ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"
git cat-file -e "${EXPECTED_BASE}^{commit}"
git merge-base --is-ancestor "$EXPECTED_BASE" HEAD
if [[ -n "$(git status --porcelain)" && "${ALLOW_DIRTY:-0}" != "1" ]]; then
  echo "FAIL: worktree is dirty" >&2
  git status --short >&2
  exit 1
fi
for path in backend/cmd/server/routes.go backend/cmd/server/wire.go backend/internal/calc/boq_amount.go db/yandex/incremental; do
  [[ -e "$path" ]] || { echo "FAIL: missing $path" >&2; exit 1; }
done
go_minor="$(go version | sed -nE 's/.*go1\.([0-9]+).*/\1/p')"
[[ -n "$go_minor" && "$go_minor" -ge 25 ]] || { echo "FAIL: Go 1.25+ required" >&2; exit 1; }
node --version >/dev/null
npm --version >/dev/null
if git ls-files | grep -Eq '(^|/)(\.env|\.env\.prod|\.env\.production|.*private.*\.pem|.*\.p12|.*\.pfx)$'; then
  echo "FAIL: tracked secret-like file detected" >&2
  exit 1
fi
printf 'PASS repo=%s head=%s base=%s go=%s node=%s npm=%s\n' \
  "$ROOT" "$(git rev-parse HEAD)" "$EXPECTED_BASE" "$(go version)" "$(node --version)" "$(npm --version)"

