# ccbs installer for Windows — Claude Code better status
#   irm https://heyjeay.github.io/ccbs/install.ps1 | iex
#
# Downloads the latest release from GitHub, checks its SHA-256 against the
# release's checksums.txt, installs to %LOCALAPPDATA%\ccbs and adds that
# folder to your user PATH. No admin rights needed.
$ErrorActionPreference = 'Stop'

$repo = if ($env:CCBS_REPO) { $env:CCBS_REPO } else { 'heyjeay/ccbs' }
$dir  = if ($env:CCBS_INSTALL_DIR) { $env:CCBS_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'ccbs' }
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$file = "ccbs_windows_$arch.zip"
$base = "https://github.com/$repo/releases/latest/download"

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("ccbs-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "› downloading $file" -ForegroundColor Green
    Invoke-WebRequest "$base/$file" -OutFile (Join-Path $tmp $file) -UseBasicParsing
    Invoke-WebRequest "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt') -UseBasicParsing

    $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match " $([regex]::Escape($file))$" }
    if (-not $line) { throw "no checksum listed for $file" }
    $want = ($line -split ' ')[0]
    $got  = (Get-FileHash (Join-Path $tmp $file) -Algorithm SHA256).Hash.ToLower()
    if ($want -ne $got) { throw "checksum mismatch for $file" }
    Write-Host "› checksum ok" -ForegroundColor Green

    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    Expand-Archive (Join-Path $tmp $file) -DestinationPath $dir -Force

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not ($userPath -split ';' | Where-Object { $_ -eq $dir })) {
        [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
        $env:Path += ";$dir"
        Write-Host "› added $dir to your PATH (open a new terminal to pick it up)" -ForegroundColor Green
    }
    Write-Host "› installed $(& (Join-Path $dir 'ccbs.exe') --version) → $dir" -ForegroundColor Green
    Write-Host "› run: ccbs" -ForegroundColor Green
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
