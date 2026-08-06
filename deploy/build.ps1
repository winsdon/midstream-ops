# =============================================================================
# Build / push image
# =============================================================================
# Usage:
#   .\deploy\build.ps1                                    # build dev image only
#   .\deploy\build.ps1 -Version 0.1.0 -User winsdon8      # build + tag release
#   .\deploy\build.ps1 -Version 0.1.0 -User winsdon8 -Push # build + push
#
# Login first (use an access token from hub.docker.com, not your password):
#   docker login docker.io -u <username>
#
# Uses docker when available, otherwise falls back to podman.
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
    [switch]$Push,
    # Proxy the podman machine VM can reach, e.g. http://172.31.32.1:7897.
    # Only needed when pushing through podman behind a host proxy bound to
    # 127.0.0.1 -- see the note above the push block.
    [string]$PushProxy = $env:PODMAN_PUSH_PROXY
)

$ErrorActionPreference = 'Stop'

# Prefer docker; fall back to podman (which installs under LOCALAPPDATA and is
# not on PATH by default).
$PodmanFallback = "$env:LOCALAPPDATA\Programs\Podman\podman.exe"
if (Get-Command docker -ErrorAction SilentlyContinue) {
    $Engine = 'docker'
    $IsPodman = $false
} elseif (Get-Command podman -ErrorAction SilentlyContinue) {
    $Engine = 'podman'
    $IsPodman = $true
} elseif (Test-Path $PodmanFallback) {
    $Engine = $PodmanFallback
    $IsPodman = $true
} else {
    throw "no container engine found: install docker, or podman at $PodmanFallback"
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

Write-Host "==> Building image (linux/amd64) with $Engine" -ForegroundColor Cyan
$tags | ForEach-Object { Write-Host "    $_" }

# podman defaults to OCI format, which has no HEALTHCHECK field, so the
# Dockerfile's HEALTHCHECK is silently dropped (build only prints a one-line
# warning). --format docker preserves it, and both `podman ps` health status
# and compose depends_on:healthy rely on it. docker always uses that format
# and rejects the flag, so only pass it for podman.
#
# --http-proxy=false: podman injects the host's http_proxy/https_proxy into
# every build container. When the host proxy listens on 127.0.0.1 (Clash and
# friends), that address inside the container points at the container itself,
# so `apk add` dies with "could not connect to server". Cached layers hide it
# until a clean build. docker does not inject host proxy vars and has no such
# flag, so this is podman-only too.
$formatArgs = if ($IsPodman) { @('--format', 'docker', '--http-proxy=false') } else { @() }

& $Engine build @formatArgs --platform linux/amd64 @tagArgs -f "$RepoRoot\Dockerfile" $RepoRoot
if ($LASTEXITCODE -ne 0) { throw "build failed" }

Write-Host "==> Build complete" -ForegroundColor Green
& $Engine images --filter reference=*midstream-ops --format "table {{.Repository}}:{{.Tag}}`t{{.Size}}"

if ($Push) {
    # podman push runs inside the machine VM, whose proxy env is baked in at VM
    # start from the host's http_proxy. A host proxy on 127.0.0.1 is unreachable
    # from the VM (that address is the VM itself), so push dies with
    # "proxyconnect tcp: dial tcp 127.0.0.1:7897: connect: connection refused".
    # Overriding the vars on the Windows side does not help -- the VM keeps its
    # own copy. Run push inside the VM instead, exporting a proxy address the VM
    # can actually reach and pointing at the Windows credential file (the VM has
    # no auth.json of its own; podman machine mounts C: under /mnt/c).
    #
    # PushProxy example: http://172.31.32.1:7897 -- the host's WSL vEthernet
    # address, from `Get-NetIPAddress -InterfaceAlias 'vEthernet (WSL*'`.
    # Leave it empty when the VM reaches the registry directly.
    foreach ($t in $tags | Where-Object { $_ -like 'docker.io/*' }) {
        Write-Host "==> Pushing $t" -ForegroundColor Cyan
        # podman writes copy progress to stderr. Under $ErrorActionPreference =
        # 'Stop' PowerShell 5.1 turns any native stderr line into a terminating
        # NativeCommandError, so a perfectly good push aborts the script.
        # Relax it around the native call and judge by $LASTEXITCODE alone.
        # (Do NOT "fix" this with 2>&1 -- that wraps each stderr line in an
        # ErrorRecord and makes it worse.)
        $prevEap = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        try {
            if ($IsPodman -and $PushProxy) {
                $authPath = "/mnt/c" + ($env:USERPROFILE -replace '^[A-Za-z]:', '' -replace '\\', '/') +
                            "/.config/containers/auth.json"
                $remote = "export http_proxy=$PushProxy https_proxy=$PushProxy " +
                          "no_proxy=localhost,127.0.0.1 REGISTRY_AUTH_FILE=$authPath; podman push $t"
                & $Engine machine ssh $remote
            } else {
                & $Engine push $t
            }
        } finally {
            $ErrorActionPreference = $prevEap
        }
        if ($LASTEXITCODE -ne 0) { throw "push failed: $t" }
    }
    Write-Host "==> Push complete" -ForegroundColor Green
}
