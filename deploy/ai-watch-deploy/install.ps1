<#
.SYNOPSIS
  AI Watch Server standalone installer for Windows (Docker Desktop). Works in Windows PowerShell 5.1 and PowerShell 7.
  Needs only Docker with Compose: no git, Go or Python.

.EXAMPLE
  .\install.ps1                          # install or verify the newest stable release (image :latest)
  .\install.ps1 -Version v1.2.0          # install a specific immutable version
  .\install.ps1 -Update                  # pull the current tag again and recreate the container
  .\install.ps1 -Rollback previous       # go back to the previously deployed image
  .\install.ps1 -Rollback v1.1.0         # go back to a specific version

  Options: -Dir DIR  -Bind IP  -Port N  -NoPull  -NoCheck  -Force  -Repo owner/name

  An existing config.local.json and .env are never overwritten. A running deployment is left
  untouched unless you pass -Update, -Version, -Rollback or -Force.
#>
[CmdletBinding()]
param(
    [string]$Dir = '',
    [string]$Version = '',
    [string]$Rollback = '',
    [switch]$Update,
    [string]$Bind = '',
    [int]$Port = 0,
    [string]$Repo = $(if ($env:AI_WATCH_REPO) { $env:AI_WATCH_REPO } else { 'athegreat90/ai-watch-server' }),
    [switch]$NoPull,
    [switch]$NoCheck,
    [switch]$Force
)
$ErrorActionPreference = 'Stop'
if (-not $Dir) { $Dir = Split-Path -Parent $MyInvocation.MyCommand.Path }
try { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 } catch { }

function Note([string]$Message) { Write-Host "==> $Message" }
function Fail([string]$Message) { Write-Error $Message; exit 1 }
# Optional queries: a failure must not abort the script (Windows PowerShell 5.1 turns native stderr into errors).
function Query-Docker { $old = $ErrorActionPreference; $ErrorActionPreference = 'Continue'; try { & docker @args 2>$null } finally { $ErrorActionPreference = $old } }
function Invoke-Docker { & docker @args; if ($LASTEXITCODE -ne 0) { throw "docker $($args -join ' ') failed (exit $LASTEXITCODE)" } }

$tagPattern = '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$'
if ($Version -and $Version -ne 'latest' -and $Version -notmatch $tagPattern) { Fail "-Version must be 'latest' or vX.Y.Z" }
if ($Rollback -and $Rollback -ne 'previous' -and $Rollback -notmatch $tagPattern) { Fail "-Rollback needs 'previous' or vX.Y.Z" }

$imageRepo = if ($env:AI_WATCH_IMAGE_REPO) { $env:AI_WATCH_IMAGE_REPO } else { "ghcr.io/$($Repo.ToLower())" }
$releaseApi = if ($env:AI_WATCH_RELEASE_API) { $env:AI_WATCH_RELEASE_API } else { "https://api.github.com/repos/$Repo" }
$releaseBase = if ($env:AI_WATCH_RELEASE_BASE) { $env:AI_WATCH_RELEASE_BASE } else { "https://github.com/$Repo/releases/download" }

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Fail 'Docker is not installed or not on PATH. Install Docker Desktop first.' }
& docker compose version *> $null
if ($LASTEXITCODE -ne 0) { Fail "The Docker Compose plugin is required ('docker compose')." }
& docker info *> $null
if ($LASTEXITCODE -ne 0) { Fail 'The Docker daemon is not reachable. Start Docker Desktop and retry.' }

# Download over HTTPS, or copy from a file:// URL (used by tests). Returns nothing; throws on failure.
function Get-File([string]$Url, [string]$Destination) {
    if ($Url -like 'file://*') { Copy-Item -LiteralPath ([Uri]$Url).LocalPath -Destination $Destination -Force; return }
    Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Destination
}

# Download a release asset and verify it against the release's SHA256SUMS. Returns $false if it cannot be fetched.
function Get-VerifiedAsset([string]$Tag, [string]$Asset, [string]$Destination) {
    $sums = [IO.Path]::GetTempFileName()
    try {
        Get-File "$releaseBase/$Tag/SHA256SUMS" $sums
        Get-File "$releaseBase/$Tag/$Asset" $Destination
    } catch { Remove-Item $sums, $Destination -Force -ErrorAction SilentlyContinue; return $false }
    $line = Get-Content $sums | Where-Object { $_ -match "^[0-9a-f]{64} [ *]?$([regex]::Escape($Asset))$" } | Select-Object -First 1
    Remove-Item $sums -Force
    $expected = if ($line) { $line.Substring(0, 64) } else { '' }
    $stream = [IO.File]::OpenRead($Destination)
    try { $actual = ([BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash($stream)) -replace "-", "").ToLower() } finally { $stream.Dispose() }
    if (-not $expected -or $actual -ne $expected) { Remove-Item $Destination -Force; Fail "Checksum mismatch for ${Asset}: refusing to use it." }
    return $true
}

New-Item -ItemType Directory -Force -Path $Dir | Out-Null
Set-Location $Dir
if (-not (Test-Path compose.yaml)) {
    Note 'compose.yaml not found next to the installer; downloading the deployment bundle'
    $bundleTag = $Version
    if (-not $bundleTag -or $bundleTag -eq 'latest') {
        $tmp = [IO.Path]::GetTempFileName()
        Get-File "$releaseApi/releases/latest" $tmp
        $bundleTag = ([regex]::Match((Get-Content $tmp -Raw), '"tag_name":\s*"([^"]+)"')).Groups[1].Value
        Remove-Item $tmp -Force
    }
    if (-not $bundleTag) { Fail 'Could not determine the latest release; pass -Version vX.Y.Z' }
    $zip = [IO.Path]::GetTempFileName() + '.zip'
    if (-not (Get-VerifiedAsset $bundleTag "ai-watch-deploy-$bundleTag.zip" $zip)) { Fail "Could not download the deployment bundle for $bundleTag" }
    $extract = Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [IO.Compression.ZipFile]::ExtractToDirectory($zip, $extract)
    Get-ChildItem (Join-Path $extract 'ai-watch-deploy') -Force | ForEach-Object {
        if (-not (Test-Path $_.Name)) { Copy-Item $_.FullName -Destination . -Recurse }
    }
    Remove-Item $zip, $extract -Recurse -Force
}

if (-not (Test-Path .env)) { if (Test-Path .env.example) { Copy-Item .env.example .env } else { New-Item .env -ItemType File | Out-Null } }
function Get-EnvValue([string]$Key) {
    $m = Select-String -Path .env -Pattern "^$Key=(.*)$" | Select-Object -Last 1
    if ($m) { return $m.Matches[0].Groups[1].Value.Trim() } else { return '' }
}
function Set-EnvValue([string]$Key, [string]$Value) {
    $lines = @(Get-Content .env)
    if ($lines -match "^$Key=") { $lines = $lines -replace "^$Key=.*$", "$Key=$Value" } else { $lines += "$Key=$Value" }
    [IO.File]::WriteAllLines((Join-Path (Get-Location) '.env'), [string[]]$lines)   # UTF-8 without BOM, which Compose requires
}

$history = '.deploy-history'
$explicit = $false
if ($Rollback) {
    if ($Rollback -eq 'previous') {
        $last = if (Test-Path $history) { Get-Content $history | Select-Object -Last 1 } else { '' }
        $target = if ($last) { ($last -split ' ')[1] } else { '' }
        if (-not $target) { Fail "No previous deployment recorded in $history; pass -Rollback vX.Y.Z" }
    } else { $target = "${imageRepo}:$Rollback" }
    Set-EnvValue AI_WATCH_SERVER_IMAGE $target; $explicit = $true
} elseif ($Version) {
    Set-EnvValue AI_WATCH_SERVER_IMAGE "${imageRepo}:$Version"; $explicit = $true
} elseif (-not (Get-EnvValue AI_WATCH_SERVER_IMAGE)) {
    Set-EnvValue AI_WATCH_SERVER_IMAGE "${imageRepo}:latest"
}
if ($Bind) { Set-EnvValue MONITOR_HOST_BIND $Bind; $explicit = $true }
if ($Port -gt 0) { Set-EnvValue MONITOR_HOST_PORT "$Port"; $explicit = $true }

$image = Get-EnvValue AI_WATCH_SERVER_IMAGE
$hostBind = Get-EnvValue MONITOR_HOST_BIND; if (-not $hostBind) { $hostBind = '127.0.0.1' }
$hostPort = Get-EnvValue MONITOR_HOST_PORT; if (-not $hostPort) { $hostPort = '8765' }
$checkHost = if ($hostBind -eq '0.0.0.0') { '127.0.0.1' } else { $hostBind }

$running = (Query-Docker compose ps -q ai-watch-server | Select-Object -First 1)
if ($running -and -not $Update -and -not $explicit -and -not $Force) {
    Note 'AI Watch Server is already running; leaving it untouched (use -Update, -Version or -Rollback to change it).'
} else {
    if ($running) {
        $prevRef = (& docker inspect --format '{{.Config.Image}}' $running)
        $prevId = (& docker inspect --format '{{.Image}}' $running)
        $prevDigest = (Query-Docker image inspect --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' $prevId)
        if (-not $prevDigest) { $prevDigest = 'unknown' }
        Add-Content -Path $history -Value "$([DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')) $prevRef $prevDigest"
    }
    if (-not $NoPull) { Note "Pulling $image"; Invoke-Docker compose pull }
    if (-not (Test-Path config.local.json)) {
        Note "Generating config.local.json with a new random token (the image's own init-config)"
        Invoke-Docker run --rm --entrypoint /app/ai-watch-server -v "$((Get-Location).Path):/cfg" $image init-config --config /cfg/config.local.json
        try { & icacls config.local.json /inheritance:r /grant:r "$($env:USERNAME):(R,W)" *> $null } catch { }
    } else { Note 'Keeping the existing config.local.json and its token' }
    Note 'Starting the container'
    $up = @('compose', 'up', '-d', '--wait', '--wait-timeout', '90'); if ($Force) { $up += '--force-recreate' }
    & docker @up
    if ($LASTEXITCODE -ne 0) { & docker compose logs --tail 30 ai-watch-server; Fail 'The container did not become healthy' }
}

Note "Waiting for the health endpoint on ${checkHost}:$hostPort"
$healthy = $false
for ($i = 0; $i -lt 30 -and -not $healthy; $i++) {
    try { $healthy = (Invoke-WebRequest -UseBasicParsing -Uri "http://${checkHost}:$hostPort/health" -TimeoutSec 2).StatusCode -eq 200 } catch { Start-Sleep -Seconds 1 }
}
if (-not $healthy) { Fail "http://${checkHost}:$hostPort/health did not answer" }

$cid = (& docker compose ps -q ai-watch-server | Select-Object -First 1)
$imageId = (& docker inspect --format '{{.Image}}' $cid)
$resolvedVersion = (Query-Docker image inspect --format '{{index .Config.Labels `org.opencontainers.image.version`}}' $imageId)
$resolvedDigest = (Query-Docker image inspect --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' $imageId)

if (-not $NoCheck) {
    Note 'Running the standalone API checker'
    New-Item -ItemType Directory -Force -Path bin | Out-Null
    $checker = Join-Path bin 'ai-watch-check-api.exe'
    $ok = $false
    if ($resolvedVersion -match $tagPattern) {
        $current = if (Test-Path $checker) { (& $checker --version) } else { '' }
        $ok = ($current -eq $resolvedVersion) -or (Get-VerifiedAsset $resolvedVersion "ai-watch-check-api_${resolvedVersion}_windows_amd64.exe" $checker)
    }
    if ($ok) {
        & $checker --url "http://${checkHost}:$hostPort" --token-file config.local.json
        if ($LASTEXITCODE -ne 0) { Fail "API check failed (exit $LASTEXITCODE)" }
    } else {
        Note 'Standalone checker not available for this release; using the one inside the image'
        Invoke-Docker compose exec -T ai-watch-server /app/ai-watch-check-api --url http://127.0.0.1:8765 --token-file /app/config.local.json
    }
}

if (-not $resolvedVersion) { $resolvedVersion = 'unknown' }
if (-not $resolvedDigest) { $resolvedDigest = 'not available (local image)' }
Write-Host @"

AI Watch Server is running.
  requested image : $image
  deployed version: $resolvedVersion
  image digest    : $resolvedDigest
  URL             : http://${hostBind}:$hostPort   (token: $((Get-Location).Path)\config.local.json)
  update          : .\install.ps1 -Update       roll back: .\install.ps1 -Rollback previous
"@
if ($hostBind -eq '127.0.0.1') { Write-Host "  Note: it listens on this machine only. For the watch/hooks on other machines run: .\install.ps1 -Bind <this PC's LAN IP>" }
