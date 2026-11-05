# ccbs installer for Windows - Claude Code better status
#   irm https://heyjeay.github.io/ccbs/install.ps1 | iex
#
# Downloads the latest release from GitHub, checks its SHA-256 against the
# release's checksums.txt, installs to %LOCALAPPDATA%\ccbs and adds that
# folder to your user PATH. No admin rights needed. Safe to run again to
# update, even while ccbs is open.
#
# Keep this file plain ASCII: `irm | iex` in Windows PowerShell 5.1 decodes
# it without a charset, so any other character prints as garbage.
$ErrorActionPreference = 'Stop'

$repo = if ($env:CCBS_REPO) { $env:CCBS_REPO } else { 'heyjeay/ccbs' }
$dir  = if ($env:CCBS_INSTALL_DIR) { $env:CCBS_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'ccbs' }
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$file = "ccbs_windows_$arch.zip"
$base = if ($env:CCBS_BASE_URL) { $env:CCBS_BASE_URL } else { "https://github.com/$repo/releases/latest/download" }

function Say($msg) { Write-Host "> $msg" -ForegroundColor Green }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("ccbs-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Say "downloading $file"
    Invoke-WebRequest "$base/$file" -OutFile (Join-Path $tmp $file) -UseBasicParsing
    Invoke-WebRequest "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt') -UseBasicParsing

    $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match " $([regex]::Escape($file))$" }
    if (-not $line) { throw "no checksum listed for $file" }
    $want = ($line -split ' ')[0]
    $got  = (Get-FileHash (Join-Path $tmp $file) -Algorithm SHA256).Hash.ToLower()
    if ($want -ne $got) { throw "checksum mismatch for $file" }
    Say "checksum ok"

    # Unpack next to the install, not over it.
    $staged = Join-Path $tmp 'out'
    Expand-Archive (Join-Path $tmp $file) -DestinationPath $staged -Force
    New-Item -ItemType Directory -Force -Path $dir | Out-Null

    # Leftovers from earlier updates (deleted once nothing is using them).
    Get-ChildItem $dir -Filter 'ccbs.exe.old-*' -ErrorAction SilentlyContinue |
        Remove-Item -Force -ErrorAction SilentlyContinue

    # Windows won't overwrite a .exe that is running, but it will rename it.
    # So if ccbs is open somewhere, move it aside and put the new one in place.
    $exe = Join-Path $dir 'ccbs.exe'
    $wasRunning = $false
    if (Test-Path $exe) {
        try {
            Remove-Item $exe -Force
        } catch {
            $aside = 'ccbs.exe.old-' + [guid]::NewGuid().ToString('N').Substring(0, 8)
            Rename-Item $exe $aside
            $wasRunning = $true
        }
    }
    Copy-Item (Join-Path $staged '*') $dir -Recurse -Force

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not ($userPath -split ';' | Where-Object { $_ -eq $dir })) {
        [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
        $env:Path += ";$dir"
        Say "added $dir to your PATH (open a new terminal to pick it up)"
    }
    Say "installed $(& $exe --version) -> $dir"
    if ($wasRunning) {
        Say "ccbs was open: quit it (q) and start it again to use the new version"
    } else {
        Say "run: ccbs"
    }
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
