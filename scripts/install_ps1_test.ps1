# Tests install.ps1 against a local release, the way scripts/install_test.sh
# tests install.sh: $VERSION stamped like release.yml does and $base pointed at
# a local folder. The archive's agent-pane.exe is a stand-in built here with
# Go that records how it was run, so the test touches nothing outside a
# temporary folder. Runs each case in Windows PowerShell 5.1 and PowerShell 7.
#
#   pwsh -NoProfile -File scripts/install_ps1_test.ps1
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("install-ps1-test-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null

function Fail([string]$Message) {
	throw "install_ps1_test: $Message"
}

# Runs a script in a new PowerShell and returns its exit code and output.
function Invoke-Shell([string]$Shell, [string[]]$Arguments, [hashtable]$Env = @{}) {
	$saved = @{}
	foreach ($k in $Env.Keys) {
		$saved[$k] = [Environment]::GetEnvironmentVariable($k)
		[Environment]::SetEnvironmentVariable($k, $Env[$k])
	}
	try {
		$out = & $Shell -NoProfile -NonInteractive -ExecutionPolicy Bypass @Arguments 2>&1 | Out-String
		return @{ Code = $LASTEXITCODE; Out = $out }
	} finally {
		foreach ($k in $saved.Keys) {
			[Environment]::SetEnvironmentVariable($k, $saved[$k])
		}
	}
}

try {
	$shells = @('powershell', 'pwsh')

	# A stand-in agent-pane.exe: writes its arguments and its own path to
	# STUB_RECORD and exits with STUB_EXIT.
	$stubSrc = Join-Path $tmp 'stub'
	New-Item -ItemType Directory -Path $stubSrc | Out-Null
	Set-Content -LiteralPath (Join-Path $stubSrc 'go.mod') -Value "module stub`n`ngo 1.21`n"
	Set-Content -LiteralPath (Join-Path $stubSrc 'main.go') -Value @'
package main

import (
	"os"
	"strconv"
	"strings"
)

func main() {
	exe, _ := os.Executable()
	_ = os.WriteFile(os.Getenv("STUB_RECORD"), []byte(exe+"\n"+strings.Join(os.Args[1:], " ")+"\n"), 0o644)
	code, _ := strconv.Atoi(os.Getenv("STUB_EXIT"))
	os.Exit(code)
}
'@

	# A release laid out like the Windows archive.
	$rel = Join-Path $tmp 'release-src'
	New-Item -ItemType Directory -Path (Join-Path $rel 'extensions\chrome') | Out-Null
	Push-Location $stubSrc
	try {
		$env:GOOS = 'windows'
		$env:GOARCH = 'amd64'
		& go build -o (Join-Path $rel 'agent-pane.exe') .
		if ($LASTEXITCODE -ne 0) { Fail 'could not build the stand-in agent-pane.exe' }
	} finally {
		Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
		Pop-Location
	}
	Copy-Item -LiteralPath (Join-Path $root 'README.md') -Destination $rel
	Copy-Item -LiteralPath (Join-Path $root 'scripts\instructions.md') -Destination $rel
	Set-Content -LiteralPath (Join-Path $rel 'extensions\chrome\manifest.json') -Value '{"manifest_version":3,"name":"Agent Pane","version":"0.0.0"}'

	$releases = Join-Path $tmp 'releases'
	$dir = Join-Path $releases 'v9.9.9'
	New-Item -ItemType Directory -Path $dir | Out-Null
	$archive = 'agent-pane-9.9.9-windows_amd64.zip'
	$zip = Join-Path $dir $archive
	Compress-Archive -Path (Join-Path $rel '*') -DestinationPath $zip
	$sums = Join-Path $dir 'checksums.txt'
	$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLowerInvariant()
	Set-Content -LiteralPath $sums -Value "0000000000000000000000000000000000000000000000000000000000000000  agent-pane-9.9.9-linux_amd64.tar.gz`n$hash  $archive"

	# The repository copy refuses to run.
	foreach ($shell in $shells) {
		$r = Invoke-Shell $shell @('-File', (Join-Path $root 'install.ps1'))
		if ($r.Code -eq 0) { Fail "the install.ps1 template ran in $shell" }
		if ($r.Out -notmatch 'unreleased') { Fail "the install.ps1 template in $shell said: $($r.Out)" }
	}

	# Stamped like release.yml stamps it, with the base pointed at the local
	# folder.
	$stamped = Join-Path $tmp 'install.ps1'
	$lines = Get-Content -LiteralPath (Join-Path $root 'install.ps1')
	$versionLines = @($lines | Where-Object { $_ -match "^\`$VERSION = ''$" }).Count
	$baseLines = @($lines | Where-Object { $_ -match '^\$base = ' }).Count
	if ($versionLines -ne 1 -or $baseLines -ne 1) { Fail 'install.ps1 needs exactly one $VERSION and one $base line' }
	# ($$ is a literal $ in a -replace replacement.)
	$lines = $lines -replace "^\`$VERSION = ''$", "`$`$VERSION = 'v9.9.9'" -replace '^\$base = .*$', "`$`$base = '$releases'"
	Set-Content -LiteralPath $stamped -Value $lines

	$record = Join-Path $tmp 'record.txt'
	foreach ($shell in $shells) {
		# Installs: runs the archive's agent-pane.exe place from a temporary
		# folder, which is gone afterwards.
		Remove-Item -LiteralPath $record -ErrorAction SilentlyContinue
		$r = Invoke-Shell $shell @('-File', $stamped) @{ STUB_RECORD = $record; STUB_EXIT = '0' }
		if ($r.Code -ne 0) { Fail "install.ps1 failed in $shell ($($r.Code)): $($r.Out)" }
		$got = Get-Content -LiteralPath $record
		if ($got[1] -ne 'place') { Fail "agent-pane.exe ran with '$($got[1])' in $shell, not 'place'" }
		if ($got[0] -notlike '*\release\agent-pane.exe') { Fail "agent-pane.exe ran from $($got[0]) in $shell" }
		if (Test-Path -LiteralPath (Split-Path -Parent (Split-Path -Parent $got[0]))) { Fail "install.ps1 left $($got[0]) behind in $shell" }

		# The way a person runs it: piped to Invoke-Expression.
		Remove-Item -LiteralPath $record -ErrorAction SilentlyContinue
		$r = Invoke-Shell $shell @('-Command', "Get-Content -Raw -LiteralPath '$stamped' | Invoke-Expression") @{ STUB_RECORD = $record; STUB_EXIT = '0' }
		if ($r.Code -ne 0) { Fail "install.ps1 under iex failed in $shell ($($r.Code)): $($r.Out)" }
		if ((Get-Content -LiteralPath $record)[1] -ne 'place') { Fail "install.ps1 under iex did not run place in $shell" }

		# place's exit code is passed on.
		$r = Invoke-Shell $shell @('-File', $stamped) @{ STUB_RECORD = $record; STUB_EXIT = '3' }
		if ($r.Code -ne 3) { Fail "install.ps1 exited $($r.Code) in $shell when place exited 3: $($r.Out)" }
		$r = Invoke-Shell $shell @('-Command', "Get-Content -Raw -LiteralPath '$stamped' | Invoke-Expression") @{ STUB_RECORD = $record; STUB_EXIT = '3' }
		if ($r.Code -eq 0) { Fail "install.ps1 under iex succeeded in $shell when place exited 3" }
	}

	# A checksum mismatch runs nothing.
	Set-Content -LiteralPath $sums -Value "0000000000000000000000000000000000000000000000000000000000000000  $archive"
	foreach ($shell in $shells) {
		Remove-Item -LiteralPath $record -ErrorAction SilentlyContinue
		$r = Invoke-Shell $shell @('-File', $stamped) @{ STUB_RECORD = $record; STUB_EXIT = '0' }
		if ($r.Code -eq 0) { Fail "install.ps1 accepted a bad checksum in $shell" }
		if ($r.Out -notmatch 'checksum mismatch') { Fail "install.ps1 with a bad checksum in $shell said: $($r.Out)" }
		if (Test-Path -LiteralPath $record) { Fail "install.ps1 ran agent-pane.exe after a bad checksum in $shell" }
	}

	# A release with no entry for the archive runs nothing.
	Set-Content -LiteralPath $sums -Value "$hash  agent-pane-9.9.9-linux_amd64.tar.gz"
	foreach ($shell in $shells) {
		$r = Invoke-Shell $shell @('-File', $stamped) @{ STUB_RECORD = $record; STUB_EXIT = '0' }
		if ($r.Code -eq 0) { Fail "install.ps1 ran without a checksum in $shell" }
		if ($r.Out -notmatch 'no entry') { Fail "install.ps1 with no checksum in $shell said: $($r.Out)" }
		if (Test-Path -LiteralPath $record) { Fail "install.ps1 ran agent-pane.exe without a checksum in $shell" }
	}

	Write-Output 'install_ps1_test: ok'
} finally {
	Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
# The last cases leave $LASTEXITCODE non-zero on purpose, and a host such as
# GitHub's pwsh step exits with it, so a passing run exits 0 itself.
exit 0
