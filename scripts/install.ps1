[CmdletBinding()]
param(
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'

$repo = 'Rethunk-Tech/rethunk-git-cli'
$prefix = if ($env:PREFIX) { $env:PREFIX } else { Join-Path $HOME '.local/bin' }
$version = if ($env:VERSION) { $env:VERSION } else { 'latest' }

if ($version -eq 'latest' -and -not $DryRun) {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest"
    $tag = $release.tag_name
    if ([string]::IsNullOrWhiteSpace($tag)) {
        throw 'install.ps1: could not determine the latest release tag'
    }
} else {
    $tag = $version
}

$asset = "rgit-$tag-windows-amd64.exe"
$baseUrl = "https://github.com/$repo/releases/download/$tag"
$assetUrl = "$baseUrl/$asset"
$checksumsUrl = "$baseUrl/SHA256SUMS"
$installPath = Join-Path $prefix 'rgit.exe'

if ($DryRun) {
    Write-Output "would download: $assetUrl"
    Write-Output "would verify against: $checksumsUrl"
    Write-Output "would install to: $installPath"
    exit 0
}

$temp = Join-Path ([System.IO.Path]::GetTempPath()) "rgit-install-$([guid]::NewGuid().ToString('N'))"
New-Item -ItemType Directory -Path $temp | Out-Null

try {
    $assetPath = Join-Path $temp $asset
    $checksumsPath = Join-Path $temp 'SHA256SUMS'
    Invoke-WebRequest -Uri $assetUrl -OutFile $assetPath
    Invoke-WebRequest -Uri $checksumsUrl -OutFile $checksumsPath

    $escapedAsset = [regex]::Escape($asset)
    $checksumPattern = "^\s*([0-9a-fA-F]{64})\s+\*?$escapedAsset\s*$"
    $checksumLine = Get-Content -LiteralPath $checksumsPath |
        Where-Object { $_ -match $checksumPattern } |
        Select-Object -First 1
    if ([string]::IsNullOrWhiteSpace([string]$checksumLine)) {
        throw "install.ps1: no checksum found for $asset"
    }

    $checksumMatch = [regex]::Match([string]$checksumLine, $checksumPattern)
    $expectedHash = $checksumMatch.Groups[1].Value
    $hash = (Get-FileHash -LiteralPath $assetPath -Algorithm SHA256).Hash
    if (-not $hash.Equals($expectedHash, [StringComparison]::OrdinalIgnoreCase)) {
        throw "install.ps1: checksum mismatch for $asset"
    }

    New-Item -ItemType Directory -Path $prefix -Force | Out-Null
    Copy-Item -LiteralPath $assetPath -Destination $installPath -Force
} finally {
    Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Output "installed to ${installPath}:"
& $installPath --version

if (-not (($env:PATH -split [IO.Path]::PathSeparator) -contains $prefix)) {
    Write-Output "note: $prefix is not on PATH -- add it to use 'rgit' directly"
}

Write-Output "optional language servers: see docs/INSTALL.md § Language servers"
