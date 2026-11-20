# Installs or updates Agent Pane on Windows from one GitHub release.
#
#   irm https://github.com/asakin/agent-pane/releases/latest/download/install.ps1 | iex
#
# The release workflow publishes this script with $VERSION set to the
# release's tag, so each published copy installs the release it came with. It
# downloads that release's zip, checks it against the release's checksums.txt,
# unpacks it to a temporary folder and runs the archive's `agent-pane.exe
# place`. Running it again is safe. It works in Windows PowerShell 5.1 and in
# PowerShell 7.
#
# This copy in the repository has no $VERSION and refuses to run.
#
# Everything runs in a script block, so nothing is left in the session that
# ran `iex`, and failures throw rather than exit, so that session stays open.
& {
$VERSION = ''
$base = 'https://github.com/asakin/agent-pane/releases/download'

$ErrorActionPreference = 'Stop'
# Invoke-WebRequest in Windows PowerShell 5.1 is slow while it draws progress.
$ProgressPreference = 'SilentlyContinue'

if (-not $VERSION) {
	throw @"
agent-pane: this is the unreleased install.ps1 template. Install a release with:
  irm https://github.com/asakin/agent-pane/releases/latest/download/install.ps1 | iex
"@
}

if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
	throw 'agent-pane: install.ps1 is for Windows; on macOS and Linux use install.sh'
}
# The machine's own architecture: a 32-bit or emulated PowerShell reports its
# own in PROCESSOR_ARCHITECTURE.
$arch = $null
try {
	$arch = (Get-ItemProperty -LiteralPath 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Environment' -Name PROCESSOR_ARCHITECTURE).PROCESSOR_ARCHITECTURE
} catch {
	$arch = $null
}
if (-not $arch) {
	$arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
}
if (-not [Environment]::Is64BitOperatingSystem -or $arch -ne 'AMD64') {
	throw "agent-pane: unsupported architecture: $arch (Agent Pane supports Windows x86_64)"
}

# GitHub needs TLS 1.2, which Windows PowerShell 5.1 may not enable.
if ($PSVersionTable.PSVersion.Major -lt 6) {
	[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
}

$archive = "agent-pane-$($VERSION.TrimStart('v'))-windows_amd64.zip"

# Copies a file of this release from $base, a URL or a local folder.
function Get-ReleaseFile([string]$Name, [string]$OutFile) {
	$url = "$base/$VERSION/$Name"
	try {
		$uri = [Uri]$url
		if ($uri.IsFile) {
			Copy-Item -LiteralPath $uri.LocalPath -Destination $OutFile
		} else {
			Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $OutFile
		}
	} catch {
		throw "agent-pane: download failed: $url ($($_.Exception.Message))"
	}
}

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("agent-pane-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
	Write-Host "agent-pane: downloading $VERSION for windows_amd64"
	$zip = Join-Path $tmp $archive
	$sums = Join-Path $tmp 'checksums.txt'
	Get-ReleaseFile $archive $zip
	Get-ReleaseFile 'checksums.txt' $sums

	$want = $null
	foreach ($line in Get-Content -LiteralPath $sums) {
		$fields = $line -split '\s+', 2
		if ($fields.Count -eq 2 -and $fields[1].Trim() -eq $archive) {
			$want = $fields[0]
			break
		}
	}
	if (-not $want) {
		throw "agent-pane: checksums.txt has no entry for $archive"
	}
	$got = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash
	if ($got -ne $want) {
		throw "agent-pane: checksum mismatch for $archive"
	}

	$release = Join-Path $tmp 'release'
	try {
		Expand-Archive -LiteralPath $zip -DestinationPath $release
	} catch {
		throw "agent-pane: could not unpack $archive ($($_.Exception.Message))"
	}
	$exe = Join-Path $release 'agent-pane.exe'
	if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) {
		throw "agent-pane: $archive has no agent-pane.exe"
	}

	# Windows PowerShell 5.1 turns a native program's stderr into error
	# records when output is redirected, and Stop would end the script on
	# place's first warning. The exit code says whether place worked.
	$ErrorActionPreference = 'Continue'
	try {
		& $exe place
		$code = $LASTEXITCODE
	} finally {
		$ErrorActionPreference = 'Stop'
	}
} finally {
	Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

if ($code -ne 0) {
	# Run from a file (powershell -File install.ps1), pass place's exit code
	# on. Under iex there is no file, and exit would close the session.
	if ($MyInvocation.ScriptName) {
		exit $code
	}
	throw "agent-pane: agent-pane.exe place failed with exit code $code"
}
}
