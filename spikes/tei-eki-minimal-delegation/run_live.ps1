<#
.SYNOPSIS
  Run one side-effect-free TEI operation through a real eki-control.

.DESCRIPTION
  Starts a throwaway eki-control on loopback with a temporary SQLite state
  store and artifact directory, runs the same operation locally with the TEI
  binary, then delegates it through EKI's public Client API. The negative
  probes and both traces are written under evidence/<date>/.

  This runs the REAL EKI binaries. It is not the Mock test: the Mock test
  lives in the Go package and needs no process. The two are recorded
  separately in RESULTS_2026-11-11.md.

  Nothing in this script touches a production database or an external
  service. The state store is a temporary file that is deleted at the end.

.PARAMETER EkiControl
  Path to the eki-control binary built from the EKI revision under test.

.PARAMETER Tei
  Path to the tei binary built from the TEI revision under test.

.PARAMETER TeiRepo
  Path to the tei-platform checkout whose revision is recorded.

.PARAMETER TeiRevision
  The revision the -Tei binary was built from. Give it explicitly when the
  checkout has moved since the build; the binary's own digest is recorded too,
  so a reader can tell that the pairing is what was measured.

.PARAMETER WorkspaceDir
  Directory the validate operation reads. Defaults to
  reference/workspace in TeiRepo. Pointing it at an extracted copy keeps the
  run independent of the checkout's working state.

.PARAMETER OutDir
  Evidence directory. Defaults to evidence/2026-11-11 next to this script.

.PARAMETER WorkDir
  Scratch directory for the throwaway state store and binaries. Defaults to
  .work next to the evidence. It is deleted at the end. Do not point it at a
  directory that matters.

.EXAMPLE
  pwsh -File run_live.ps1 `
    -EkiControl D:\work\bin\eki-control.exe `
    -Tei D:\work\bin\tei.exe `
    -TeiRepo D:\work\tei-platform
#>
param(
    [Parameter(Mandatory = $true)][string]$EkiControl,
    [Parameter(Mandatory = $true)][string]$Tei,
    [Parameter(Mandatory = $true)][string]$TeiRepo,
    [string]$TeiRevision,
    [string]$WorkspaceDir,
    [string]$OutDir,
    [string]$WorkDir
)

$ErrorActionPreference = 'Stop'
$spikeDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ekiRepo = (Resolve-Path (Join-Path $spikeDir '..\..')).Path
if (-not $OutDir) { $OutDir = Join-Path $spikeDir 'evidence\2026-11-11' }
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
if (-not $WorkspaceDir) { $WorkspaceDir = Join-Path $TeiRepo 'reference\workspace' }
if (-not $WorkDir) { $WorkDir = Join-Path $OutDir '.work' }

$work = Join-Path $WorkDir ("tei-eki-delegation-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $work | Out-Null

function Get-FreePort {
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $port = $listener.LocalEndpoint.Port
    $listener.Stop()
    return $port
}

function Read-Json([string]$Path) {
    # Get-Content -Raw decodes with the ANSI code page on Windows PowerShell
    # 5.1, which corrupts the UTF-8 Japanese fields and breaks the parse.
    $text = [System.IO.File]::ReadAllText($Path, [System.Text.Encoding]::UTF8)
    return $text | ConvertFrom-Json
}

function Write-Utf8NoBom([string]$Path, [string]$Text) {
    [System.IO.File]::WriteAllText($Path, $Text, [System.Text.UTF8Encoding]::new($false))
}

function Invoke-Cli {
    # $args collects the splatted array, so callers can write
    # Invoke-Cli @('live', '-base', $base, ...).
    #
    # A tool that reports a finding on stderr must not stop the run: with
    # $ErrorActionPreference = 'Stop' and 2>&1, Windows PowerShell turns a
    # native stderr line into a terminating error.
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $output = & $cliExe @args 2>&1
        $code = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previous
    }
    return [pscustomobject]@{ Code = $code; Output = ($output -join "`n") }
}

$clientPort = Get-FreePort
$workerPort = Get-FreePort
$token = 'tei-spike-' + [guid]::NewGuid().ToString('N')
$sha = [System.Security.Cryptography.SHA256]::Create()
$tokenHash = (($sha.ComputeHash([System.Text.Encoding]::UTF8.GetBytes($token)) | ForEach-Object { $_.ToString('x2') }) -join '')

$configPath = Join-Path $work 'control.json'
$dbPath = Join-Path $work 'state.db'
$artifactsDir = Join-Path $work 'artifacts'
New-Item -ItemType Directory -Force -Path $artifactsDir | Out-Null

$config = @"
{
  "listen": "127.0.0.1:$clientPort",
  "database": "$($dbPath -replace '\\','/')",
  "tokens": [
    { "principal": "tei-spike", "role": "client", "token_sha256": "$tokenHash" }
  ],
  "worker": {
    "listen": "127.0.0.1:$workerPort",
    "artifacts_dir": "$($artifactsDir -replace '\\','/')",
    "sessions": [
      { "worker_identity": "tei-spike-worker", "session_token_sha256": "$tokenHash" }
    ]
  },
  "policy": {
    "approved_executors": ["tei.validate"],
    "per_task": {
      "cpu_millis": 8000,
      "memory_bytes": 8589934592,
      "wall_time_seconds": 3600,
      "output_bytes_max": 67108864,
      "network_bytes_max": 0,
      "temporary_disk_bytes": 1073741824
    },
    "max_attempts": 2,
    "max_outstanding_tasks": 20
  }
}
"@
Write-Utf8NoBom $configPath $config

$controlOut = Join-Path $work 'control.out.log'
$controlErr = Join-Path $work 'control.err.log'
$control = Start-Process -FilePath $EkiControl -ArgumentList @('--config', $configPath) `
    -PassThru -NoNewWindow -RedirectStandardOutput $controlOut -RedirectStandardError $controlErr
$base = "http://127.0.0.1:$clientPort"

# A failure must not leave a Control Plane or a scratch directory behind. The
# trap runs for any terminating error raised after the process starts.
$cleanup = {
    if ($control -and -not $control.HasExited) {
        Stop-Process -Id $control.Id -Force -ErrorAction SilentlyContinue
    }
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
trap {
    $failure = $_
    & $cleanup
    Write-Host "FAILED: $($failure.Exception.Message)" -ForegroundColor Red
    Write-Host "at $($failure.InvocationInfo.PositionMessage)"
    exit 1
}

$ready = $false
for ($i = 0; $i -lt 150; $i++) {
    if ($control.HasExited) { break }
    try {
        $res = Invoke-WebRequest -Uri "$base/readyz" -UseBasicParsing -TimeoutSec 2
        if ($res.StatusCode -eq 200) { $ready = $true; break }
    } catch { }
    Start-Sleep -Milliseconds 200
}
if (-not $ready) {
    Get-Content $controlErr -ErrorAction SilentlyContinue | Select-Object -Last 40
    throw "eki-control did not become ready at $base"
}

$cliExe = Join-Path $work 'tei-eki-delegation.exe'
Push-Location $ekiRepo
try {
    & go build -o $cliExe ./spikes/tei-eki-minimal-delegation/cmd/tei-eki-delegation
    if ($LASTEXITCODE -ne 0) { throw 'go build of the spike CLI failed' }
} finally { Pop-Location }

$ekiRevision = (& git -C $ekiRepo rev-parse HEAD).Trim()
if (-not $TeiRevision) { $TeiRevision = (& git -C $TeiRepo rev-parse HEAD).Trim() }
$teiRevision = $TeiRevision

# 1. Input artifact: a deterministic bundle of the reference workspace.
$bundlePath = Join-Path $work 'workspace.tar.gz'
$bundle = Invoke-Cli @('bundle', '-dir', $WorkspaceDir, '-out', $bundlePath)
if ($bundle.Code -ne 0) { throw "bundle failed: $($bundle.Output)" }
$bundleInfo = $bundle.Output | ConvertFrom-Json
# The scratch path is deleted at the end; record the identity, not the location.
$bundleSummary = [ordered]@{
    schema     = $bundleInfo.schema
    sha256     = $bundleInfo.sha256
    size_bytes = $bundleInfo.size_bytes
}

# 2. Local baseline: the same read-only validate, measured on this host. Three
#    runs, because a single sample on a loaded developer machine is not a
#    baseline: the same command ranged from ~0.2s to ~9s during this work.
$localPaths = @()
foreach ($i in 1..3) {
    $path = Join-Path $work "local-$i.json"
    $local = Invoke-Cli @('local', '-tei', $Tei, '-workspace', $WorkspaceDir, '-source-revision', $teiRevision, '-out', $path)
    if ($local.Code -ne 0) { throw "local run $i failed: $($local.Output)" }
    $localPaths += $path
}
$localPath = $localPaths[0]

# 3. Projection: what TEI asks EKI for.
$projectionPath = Join-Path $work 'projection.json'
$projection = Invoke-Cli @(
    'offline',
    '-source-revision', $teiRevision,
    '-executable', $Tei,
    '-toolchain', 'go1.26.10',
    '-input', "workspace=$bundlePath",
    '-expected-output-sha256', (Read-Json $localPath).output_sha256,
    '-deadline', '300s',
    '-out', $projectionPath
)
if ($projection.Code -ne 0) { throw "offline projection failed: $($projection.Output)" }

# 4. Refusal probes. These run first: one probe expects the input digest to be
#    absent from the deployment.
$probePath = Join-Path $work 'probe.json'
$probe = Invoke-Cli @('probe', '-base', $base, '-token', $token, '-projection', $projectionPath, '-input', "workspace=$bundlePath", '-out', $probePath)
if (Test-Path $probePath) { Copy-Item $probePath $OutDir -Force }

# 5. Two identical delegations: the second must converge on the first Task.
$trace1 = Join-Path $work 'trace-1.json'
$trace2 = Join-Path $work 'trace-2.json'
$live1 = Invoke-Cli @('live', '-base', $base, '-token', $token, '-projection', $projectionPath, '-input', "workspace=$bundlePath", '-local', $localPath, '-wait', '3s', '-poll', '500ms', '-out', $trace1)
$live2 = Invoke-Cli @('live', '-base', $base, '-token', $token, '-projection', $projectionPath, '-input', "workspace=$bundlePath", '-local', $localPath, '-wait', '3s', '-poll', '500ms', '-out', $trace2)

# 6. Keep the evidence; the temporary deployment is deleted.
Copy-Item $bundlePath $OutDir
foreach ($path in $localPaths) { Copy-Item $path $OutDir }
Copy-Item $localPath $OutDir
Copy-Item $projectionPath $OutDir
Copy-Item $probePath $OutDir -ErrorAction SilentlyContinue
Copy-Item $trace1 $OutDir -ErrorAction SilentlyContinue
Copy-Item $trace2 $OutDir -ErrorAction SilentlyContinue
Copy-Item $controlErr $OutDir -ErrorAction SilentlyContinue

$probeReport = $null
if (Test-Path $probePath) { $probeReport = Read-Json $probePath }
$receipt1 = $null
$receipt2 = $null
$timing1 = $null
if (Test-Path $trace1) {
    $t1 = Read-Json $trace1
    $receipt1 = $t1.receipt
    $timing1 = [ordered]@{
        started_at   = $t1.observation.started_at
        finished_at  = $t1.observation.finished_at
        status_reads = $t1.observation.status_reads
        requests     = $t1.observation.requests
        local_millis = $t1.observation.local.duration_millis
    }
}
if (Test-Path $trace2) { $receipt2 = (Read-Json $trace2).receipt }
$localRun = Read-Json $localPath
$localRuns = @()
foreach ($path in $localPaths) { $localRuns += Read-Json $path }
$localMillis = @($localRuns | ForEach-Object { $_.duration_millis })
$localMin = ($localMillis | Measure-Object -Minimum).Minimum
$localMax = ($localMillis | Measure-Object -Maximum).Maximum

$summary = [ordered]@{
    schema            = 'tei.eki-delegation-live-run/v0'
    ran_at            = (Get-Date).ToUniversalTime().ToString('o')
    eki_revision      = $ekiRevision
    tei_revision      = $teiRevision
    tei_sha256        = $localRun.executable_sha256
    eki_control       = (Split-Path -Leaf $EkiControl)
    tei               = (Split-Path -Leaf $Tei)
    client_base       = $base
    input_bundle      = $bundleSummary
    local_exit_code   = $localRun.exit_code
    local_sha256      = $localRun.output_sha256
    local_runs        = $localRuns
    local_min_millis  = $localMin
    local_max_millis  = $localMax
    live_1_exit_code  = $live1.Code
    live_2_exit_code  = $live2.Code
    probe_exit_code   = $probe.Code
    probe_report      = $probeReport
    trace_1_receipt   = $receipt1
    trace_2_receipt   = $receipt2
    trace_1_timing    = $timing1
    live_1_output     = $live1.Output
    live_2_output     = $live2.Output
}
Write-Utf8NoBom (Join-Path $OutDir 'live-run.json') ($summary | ConvertTo-Json -Depth 12)

& $cleanup
Write-Host "evidence written to $OutDir"


