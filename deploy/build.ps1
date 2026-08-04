# =============================================================================
# Build / push image with podman
# =============================================================================
# Usage:
#   .\deploy\build.ps1                                    # build dev image only
#   .\deploy\build.ps1 -Version 0.1.0 -User winsdon8      # build + tag release
#   .\deploy\build.ps1 -Version 0.1.0 -User winsdon8 -Push # build + push
#
# Login first (use an access token from hub.docker.com, not your password):
#   podman login docker.io -u <username>
#
# NOTE: keep this file ASCII-only. Windows PowerShell 5.1 reads scripts using
# the system ANSI codepage (GBK on zh-CN), so UTF-8 comments without a BOM get
# mis-decoded and break parsing. Chinese docs live in deploy/DOCKER.md instead.
# =============================================================================

[CmdletBinding()]
param(
    # Release version, e.g. 0.1.0. Omit to build the dev tag only.
    [string]$Version,
    # Docker Hub username
    [string]$User,
    # Push to Docker Hub after building
    [switch]$Push
)

$ErrorActionPreference = 'Stop'

# podman installs under LOCALAPPDATA and is not on PATH by default
$Podman = if (Get-Command podman -ErrorAction SilentlyContinue) {
    'podman'
} else {
    "$env:LOCALAPPDATA\Programs\Podman\podman.exe"
}
if ($Podman -ne 'podman' -and -not (Test-Path $Podman)) {
    throw "podman not found at: $Podman"
}

$RepoRoot = Split-Path -Parent $PSScriptRoot

if ($Push -and (-not $Version -or -not $User)) {
    throw "-Push requires both -Version and -User"
}

$tags = @('midstream-ops:dev')
if ($Version -and $User) {
    $tags += "docker.io/$User/midstream-ops:$Version"
    $tags += "docker.io/$User/midstream-ops:latest"
}

$tagArgs = $tags | ForEach-Object { '-t', $_ }

Write-Host "==> Building image (linux/amd64)" -ForegroundColor Cyan
$tags | ForEach-Object { Write-Host "    $_" }

# --format docker is required: podman defaults to OCI format, which has no
# HEALTHCHECK field, so the Dockerfile's HEALTHCHECK is silently dropped
# (build only prints a one-line warning). Docker format preserves it, and
# both `docker ps` health status and compose depends_on:healthy rely on it.
& $Podman build --format docker --platform linux/amd64 @tagArgs -f "$RepoRoot\Dockerfile" $RepoRoot
if ($LASTEXITCODE -ne 0) { throw "build failed" }

Write-Host "==> Build complete" -ForegroundColor Green
& $Podman images --filter reference=*midstream-ops --format "table {{.Repository}}:{{.Tag}}`t{{.Size}}"

if ($Push) {
    foreach ($t in $tags | Where-Object { $_ -like 'docker.io/*' }) {
        Write-Host "==> Pushing $t" -ForegroundColor Cyan
        & $Podman push $t
        if ($LASTEXITCODE -ne 0) { throw "push failed: $t" }
    }
    Write-Host "==> Push complete" -ForegroundColor Green
}
