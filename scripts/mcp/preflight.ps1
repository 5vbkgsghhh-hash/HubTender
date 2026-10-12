param(
  [string]$ExpectedBaseCommit = '2b2e8674b90e7d3f522db6f5d608ecc630360ffe',
  [switch]$AllowDirty
)
$ErrorActionPreference = 'Stop'
$root = (& git rev-parse --show-toplevel).Trim()
if (-not $root) { throw 'Not inside a Git checkout.' }
Push-Location $root
try {
  & git cat-file -e "$ExpectedBaseCommit`^{commit}" 2>$null
  if ($LASTEXITCODE -ne 0) { throw "Expected base commit is absent: $ExpectedBaseCommit" }
  & git merge-base --is-ancestor $ExpectedBaseCommit HEAD
  if ($LASTEXITCODE -ne 0) { throw "Expected base is not an ancestor of HEAD: $ExpectedBaseCommit" }
  $dirty = & git status --porcelain
  if ($dirty -and -not $AllowDirty) { throw "Worktree is dirty. Commit/stash unrelated work first.`n$dirty" }

  $required = @(
    'backend/cmd/server/routes.go', 'backend/cmd/server/wire.go',
    'backend/internal/calc/boq_amount.go', 'db/yandex/incremental'
  )
  foreach ($path in $required) { if (-not (Test-Path -LiteralPath $path)) { throw "Required path missing: $path" } }

  $goVersion = (& go version)
  if ($goVersion -notmatch 'go1\.(\d+)') { throw "Cannot parse Go version: $goVersion" }
  if ([int]$Matches[1] -lt 25) { throw "Go 1.25+ is required: $goVersion" }
  & node --version | Out-Null
  & npm --version | Out-Null

  $tracked = & git ls-files
  $bad = $tracked | Where-Object { $_ -match '(^|/)(\.env|\.env\.prod|\.env\.production|.*private.*\.pem|.*\.p12|.*\.pfx)$' }
  if ($bad) { throw "Tracked secret-like files detected:`n$($bad -join "`n")" }

  [pscustomobject]@{
    status = 'PASS'
    repository = $root
    head = (& git rev-parse HEAD).Trim()
    expected_base = $ExpectedBaseCommit
    go = $goVersion
    node = (& node --version)
    npm = (& npm --version)
  } | ConvertTo-Json -Depth 3
} finally { Pop-Location }

