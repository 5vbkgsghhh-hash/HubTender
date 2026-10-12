$ErrorActionPreference = 'Stop'

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Split-Path -Parent $ScriptDir

function Import-DotEnv {
  param([Parameter(Mandatory = $true)][string]$Path)

  if (-not (Test-Path $Path)) {
    Write-Warning "[dev-backend] Env file not found: $Path"
    return
  }

  Get-Content $Path | ForEach-Object {
    if ($_ -match '^\s*([^#][A-Za-z0-9_]+)=(.*)$') {
      $name = $matches[1].Trim()
      $value = $matches[2].Trim()

      if (($value.StartsWith('"') -and $value.EndsWith('"')) -or
          ($value.StartsWith("'") -and $value.EndsWith("'"))) {
        $value = $value.Substring(1, $value.Length - 2)
      }

      Set-Item -Path "env:$name" -Value $value
    }
  }
}

function Set-DatabaseSSLRootCert {
  param(
    [Parameter(Mandatory = $true)][string]$DatabaseURL,
    [Parameter(Mandatory = $true)][string]$SSLRootCert
  )

  if ($DatabaseURL -match 'sslrootcert=') {
    return [regex]::Replace($DatabaseURL, 'sslrootcert=[^&]+', "sslrootcert=$SSLRootCert")
  }

  $separator = if ($DatabaseURL.Contains('?')) { '&' } else { '?' }
  return "$DatabaseURL$separator" + "sslrootcert=$SSLRootCert"
}

function Resolve-GoExecutable {
  $fromPath = Get-Command go.exe -ErrorAction SilentlyContinue
  if ($fromPath) {
    return $fromPath.Source
  }

  $candidates = @(@(
    (Join-Path ${env:ProgramFiles} 'Go\bin\go.exe'),
    (Join-Path ${env:ProgramFiles(x86)} 'Go\bin\go.exe'),
    'C:\Go\bin\go.exe'
  ) | Where-Object { $_ -and (Test-Path $_) })

  if ($candidates.Count -gt 0) {
    $goBin = Split-Path -Parent $candidates[0]
    $env:Path = "$goBin;$env:Path"
    return $candidates[0]
  }

  return $null
}

function Start-GoBackend {
  param([Parameter(Mandatory = $true)][string]$GoExe)

  Write-Host "[dev-backend] Starting Go BFF on http://$($env:BIND_HOST):$($env:PORT)"
  Write-Host '[dev-backend] Press Ctrl+C to stop.'

  Push-Location (Join-Path $RepoRoot 'backend')
  try {
    & $GoExe run ./cmd/server
    exit $LASTEXITCODE
  } finally {
    Pop-Location
  }
}

function Start-DockerBackend {
  $docker = Get-Command docker.exe -ErrorAction SilentlyContinue
  if (-not $docker) {
    throw '[dev-backend] go.exe not found and docker.exe is not available. Install Go 1.23+ or Docker Desktop.'
  }

  $dockerDatabaseURL = Set-DatabaseSSLRootCert -DatabaseURL $env:DATABASE_URL -SSLRootCert '/certs/yandex-ca.pem'
  $certMountSource = (Resolve-Path $certDir).Path
  $backendDir = Join-Path $RepoRoot 'backend'
  $imageTag = 'hubtender-api:dev'
  $containerName = 'hubtender-dev-api'

  $existing = & docker.exe ps -aq --filter "name=^$containerName$"
  if ($LASTEXITCODE -ne 0) {
    throw '[dev-backend] Failed to inspect existing Docker containers.'
  }
  if ($existing) {
    throw "[dev-backend] Docker container '$containerName' already exists. Remove it with: docker rm -f $containerName"
  }

  Write-Host '[dev-backend] go.exe not found; falling back to Docker.'
  & docker.exe build -t $imageTag $backendDir
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }

  $dockerArgs = @(
    'run',
    '--rm',
    '--name', $containerName,
    '-p', "$($env:PORT):3005",
    '--mount', "type=bind,source=$certMountSource,target=/certs,readonly",
    '-e', "DATABASE_URL=$dockerDatabaseURL",
    '-e', "APP_JWT_ISSUER=$($env:APP_JWT_ISSUER)",
    '-e', "APP_JWT_AUDIENCE=$($env:APP_JWT_AUDIENCE)",
    '-e', "APP_JWT_KEY_ID=$($env:APP_JWT_KEY_ID)",
    '-e', "APP_JWT_PRIVATE_KEY_PATH=",
    '-e', "APP_JWT_PRIVATE_KEY_B64=$($env:APP_JWT_PRIVATE_KEY_B64)",
    '-e', "APP_ACCESS_TOKEN_TTL_MINUTES=$($env:APP_ACCESS_TOKEN_TTL_MINUTES)",
    '-e', "APP_REFRESH_TOKEN_TTL_DAYS=$($env:APP_REFRESH_TOKEN_TTL_DAYS)",
    '-e', "LOG_LEVEL=$($env:LOG_LEVEL)",
    '-e', "CORS_ORIGINS=$($env:CORS_ORIGINS)",
    '-e', "DB_MAX_CONNS=$($env:DB_MAX_CONNS)",
    '-e', "DB_MIN_CONNS=$($env:DB_MIN_CONNS)",
    '-e', "DB_MAX_CONN_IDLE_TIME=$($env:DB_MAX_CONN_IDLE_TIME)",
    '-e', "SENTRY_DSN=$($env:SENTRY_DSN)",
    '-e', "SENTRY_ENVIRONMENT=$($env:SENTRY_ENVIRONMENT)",
    '-e', "SENTRY_RELEASE=$($env:SENTRY_RELEASE)",
    '-e', "APP_ENV=$($env:APP_ENV)",
    '-e', "APP_BASE_URL=$($env:APP_BASE_URL)",
    '-e', "SMTP_HOST=$($env:SMTP_HOST)",
    '-e', "SMTP_PORT=$($env:SMTP_PORT)",
    '-e', "SMTP_USER=$($env:SMTP_USER)",
    '-e', "SMTP_PASSWORD=$($env:SMTP_PASSWORD)",
    '-e', "SMTP_FROM=$($env:SMTP_FROM)",
    '-e', "CBR_BASE_URL=$($env:CBR_BASE_URL)",
    '-e', 'BIND_HOST=0.0.0.0',
    '-e', 'PORT=3005',
    $imageTag
  )

  Write-Host '[dev-backend] Starting Dockerized Go BFF on http://127.0.0.1:3005'
  Write-Host '[dev-backend] Press Ctrl+C to stop.'
  & docker.exe @dockerArgs
  exit $LASTEXITCODE
}

Import-DotEnv (Join-Path $RepoRoot '.env')
# Один env-файл — .env; устаревший .env.prod читается, только если ещё лежит рядом.
$legacyEnv = Join-Path $RepoRoot '.env.prod'
if (Test-Path $legacyEnv) { Import-DotEnv $legacyEnv }

$required = @('DATABASE_URL', 'APP_JWT_ISSUER')
foreach ($name in $required) {
  if (-not (Get-Item -Path "env:$name" -ErrorAction SilentlyContinue)) {
    throw "[dev-backend] Required env var is missing: $name"
  }
}

if (-not $env:APP_JWT_PRIVATE_KEY_PATH -and -not $env:APP_JWT_PRIVATE_KEY_B64) {
  throw '[dev-backend] Required env var is missing: APP_JWT_PRIVATE_KEY_PATH or APP_JWT_PRIVATE_KEY_B64'
}

$certDir = Join-Path $RepoRoot 'certs'
$caPath = Join-Path $certDir 'yandex-ca.pem'
if (-not (Test-Path $caPath)) {
  New-Item -ItemType Directory -Force $certDir | Out-Null
  Write-Host '[dev-backend] Downloading public Yandex CA certificate...'
  & curl.exe -fsSL 'https://storage.yandexcloud.net/cloud-certs/CA.pem' -o $caPath
  if ($LASTEXITCODE -ne 0) {
    throw '[dev-backend] Failed to download Yandex CA certificate.'
  }
}

$localCertPath = [System.Uri]::EscapeDataString((Resolve-Path $caPath).Path.Replace('\', '/'))
$env:DATABASE_URL = Set-DatabaseSSLRootCert -DatabaseURL $env:DATABASE_URL -SSLRootCert $localCertPath

$env:CORS_ORIGINS = if ($env:DEV_CORS_ORIGINS) { $env:DEV_CORS_ORIGINS } else { 'http://localhost:5185,http://127.0.0.1:5185' }
$env:APP_BASE_URL = if ($env:DEV_APP_BASE_URL) { $env:DEV_APP_BASE_URL } else { 'http://localhost:5185' }
$env:APP_ENV = 'development'
$env:SENTRY_DSN = ''
$env:SENTRY_ENVIRONMENT = 'development'
$env:BIND_HOST = '127.0.0.1'
$env:PORT = if ($env:DEV_BACKEND_PORT) { $env:DEV_BACKEND_PORT } else { '3005' }

$go = Resolve-GoExecutable
if ($go) {
  Start-GoBackend -GoExe $go
} else {
  Start-DockerBackend
}
