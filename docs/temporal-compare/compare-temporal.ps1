#Requires -Version 5.1
<#
.SYNOPSIS
    Runs four gortex analyze snapshots and prints an orphan-delta comparison table.

.DESCRIPTION
    Executes gortex analyze with --temporal off and --temporal on, parses the JSON
    output, and prints a delta table for each orphan category plus the temporal-stub
    edge count. Exits with code 0 on PASS (orphan_activity reduced), 1 on FAIL.

.PARAMETER Path
    Repository path to analyze. Default: current directory.

.PARAMETER Binary
    Path to the gortex binary. Default: gortex.exe (assumes it is on $env:PATH).

.EXAMPLE
    .\compare-temporal.ps1
    .\compare-temporal.ps1 -Path C:\repos\myapp -Binary C:\tools\gortex.exe
#>
param(
    [string]$Path   = ".",
    [string]$Binary = "gortex.exe"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# ---------------------------------------------------------------------------
# Helper: run gortex analyze and return parsed JSON; exit on command failure.
# ---------------------------------------------------------------------------
function Invoke-GortexAnalyze {
    param(
        [string]$Kind,
        [string]$Temporal,
        [string]$RepoPath,
        [string]$BinaryPath
    )

    $label = "$Kind --temporal $Temporal"
    Write-Host "Running: $BinaryPath analyze --kind $Kind --path $RepoPath --temporal $Temporal --format json"

    $Error.Clear()
    $stdout = $null
    $stderr = $null

    try {
        # Capture stdout and stderr separately via temp files to avoid stream mixing.
        $tmpOut = [System.IO.Path]::GetTempFileName()
        $tmpErr = [System.IO.Path]::GetTempFileName()

        $proc = Start-Process `
            -FilePath $BinaryPath `
            -ArgumentList "analyze", "--kind", $Kind, "--path", $RepoPath, "--temporal", $Temporal, "--format", "json" `
            -RedirectStandardOutput $tmpOut `
            -RedirectStandardError  $tmpErr `
            -NoNewWindow `
            -PassThru `
            -Wait

        $stdout = Get-Content -Raw -Path $tmpOut
        $stderr = Get-Content -Raw -Path $tmpErr
    }
    finally {
        if (Test-Path $tmpOut) { Remove-Item $tmpOut -Force }
        if (Test-Path $tmpErr) { Remove-Item $tmpErr -Force }
    }

    if ($proc.ExitCode -ne 0) {
        Write-Warning "COMMAND FAILED: $label (exit $($proc.ExitCode))"
        if ($stderr) {
            Write-Warning "--- stderr ---"
            Write-Warning $stderr
            Write-Warning "--------------"
        }
        return $null
    }

    if (-not $stdout -or $stdout.Trim() -eq "") {
        Write-Warning "COMMAND RETURNED EMPTY OUTPUT: $label"
        return $null
    }

    try {
        return $stdout | ConvertFrom-Json
    }
    catch {
        Write-Warning "FAILED TO PARSE JSON for $label : $_"
        Write-Warning "Raw output (first 500 chars): $($stdout.Substring(0, [Math]::Min(500, $stdout.Length)))"
        return $null
    }
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

# Resolve binary path early to give a clear error if not found.
if (-not (Get-Command $Binary -ErrorAction SilentlyContinue)) {
    # Try as a literal path too.
    if (-not (Test-Path $Binary)) {
        Write-Error "gortex binary not found: '$Binary'. Pass -Binary <path> or ensure it is on PATH."
        exit 2
    }
}

$resolvedPath = Resolve-Path $Path -ErrorAction Stop | Select-Object -ExpandProperty Path

Write-Host ""
Write-Host "=== Temporal Fork Comparison ==="
Write-Host "Repository : $resolvedPath"
Write-Host "Binary     : $Binary"
Write-Host ""

# Collect snapshots.
$offOrphans = Invoke-GortexAnalyze -Kind "temporal_orphans"    -Temporal "off" -RepoPath $resolvedPath -BinaryPath $Binary
$onOrphans  = Invoke-GortexAnalyze -Kind "temporal_orphans"    -Temporal "on"  -RepoPath $resolvedPath -BinaryPath $Binary
$onSynth    = Invoke-GortexAnalyze -Kind "synthesizers"        -Temporal "on"  -RepoPath $resolvedPath -BinaryPath $Binary
# resolution_outcomes (--temporal off) — informational only.
$offResout  = Invoke-GortexAnalyze -Kind "resolution_outcomes" -Temporal "off" -RepoPath $resolvedPath -BinaryPath $Binary

# ---------------------------------------------------------------------------
# Section 1 — Orphan delta table
# ---------------------------------------------------------------------------
Write-Host ""
Write-Host "--- Orphan delta table (totals sub-object) ---"
Write-Host ""

$categories = @(
    "broken_dispatch",
    "signal_no_handler",
    "query_no_handler",
    "orphan_activity",
    "orphan_workflow"
)

$fmt = "{0,-26} {1,6} {2,6} {3,8}"
Write-Host ($fmt -f "Category", "OFF", "ON", "DELTA")
Write-Host ("-" * 52)

$orphanActivityOff = $null
$orphanActivityOn  = $null

foreach ($cat in $categories) {
    $offVal = if ($offOrphans -and $offOrphans.totals) { $offOrphans.totals.$cat } else { "N/A" }
    $onVal  = if ($onOrphans  -and $onOrphans.totals)  { $onOrphans.totals.$cat  } else { "N/A" }

    if ($offVal -is [int] -and $onVal -is [int]) {
        $delta = $onVal - $offVal
        $deltaStr = if ($delta -le 0) { "$delta" } else { "+$delta" }
    }
    else {
        $delta    = $null
        $deltaStr = "N/A"
    }

    Write-Host ($fmt -f $cat, $offVal, $onVal, $deltaStr)

    if ($cat -eq "orphan_activity") {
        $orphanActivityOff = if ($offVal -is [int]) { $offVal } else { $null }
        $orphanActivityOn  = if ($onVal  -is [int]) { $onVal  } else { $null }
    }
}

# ---------------------------------------------------------------------------
# Section 2 — temporal-stub edge count
# ---------------------------------------------------------------------------
Write-Host ""
Write-Host "--- temporal-stub synthesizer (--temporal on) ---"
Write-Host ""

$temporalStubEdges = $null

if ($onSynth -and $onSynth.synthesizers) {
    $stubRow = $onSynth.synthesizers | Where-Object { $_.synthesizer -eq "temporal-stub" } | Select-Object -First 1
    if ($stubRow) {
        $temporalStubEdges = $stubRow.edges
        Write-Host "temporal-stub edges : $($stubRow.edges)"
        Write-Host "provenance          : $($stubRow.provenance)"

        if ($stubRow.samples) {
            $sampleCount = [Math]::Min(5, $stubRow.samples.Count)
            Write-Host ""
            Write-Host "Sample edges (up to 5):"
            $sfmt = "  {0,-50} -> {1,-50}  [{2}]"
            for ($i = 0; $i -lt $sampleCount; $i++) {
                $s = $stubRow.samples[$i]
                Write-Host ($sfmt -f $s.from, $s.to, $s.kind)
            }
        }
    }
    else {
        Write-Host "temporal-stub : NOT FOUND in synthesizers output"
    }
}
else {
    Write-Host "temporal-stub : synthesizers command failed or returned no data"
}

# ---------------------------------------------------------------------------
# Section 3 — resolution_outcomes by_reason (temporal=off)
# ---------------------------------------------------------------------------
Write-Host ""
Write-Host "--- resolution_outcomes by_reason (--temporal off) ---"
Write-Host ""

if ($offResout -and $offResout.by_reason) {
    Write-Host "total unresolved edges: $($offResout.total)"
    Write-Host ""
    # Sort by count descending.
    $offResout.by_reason.PSObject.Properties |
        Sort-Object { [int]$_.Value } -Descending |
        ForEach-Object { Write-Host ("  {0,-35} : {1}" -f $_.Name, $_.Value) }
}
else {
    Write-Host "resolution_outcomes command failed or returned no data"
}

# ---------------------------------------------------------------------------
# Verdict
# ---------------------------------------------------------------------------
Write-Host ""
Write-Host "=== VERDICT ==="
Write-Host ""

if ($null -eq $orphanActivityOff -or $null -eq $orphanActivityOn) {
    Write-Host "RESULT : INCONCLUSIVE"
    Write-Host "REASON : orphan_activity data missing (one or more commands failed)"
    exit 1
}

$activityDelta = $orphanActivityOn - $orphanActivityOff
$stubOk        = ($null -ne $temporalStubEdges -and $temporalStubEdges -gt 0)

Write-Host ("orphan_activity  OFF={0}  ON={1}  delta={2}" -f $orphanActivityOff, $orphanActivityOn, $activityDelta)
Write-Host ("temporal-stub edges : {0}" -f $(if ($null -ne $temporalStubEdges) { $temporalStubEdges } else { "N/A" }))
Write-Host ""

if ($activityDelta -lt 0 -and $stubOk) {
    $improvement = [Math]::Abs($activityDelta)
    Write-Host "RESULT : PASS"
    Write-Host "REASON : --temporal on reduces orphan_activity by $improvement and synthesizes $temporalStubEdges temporal-stub edges"
    exit 0
}
elseif ($activityDelta -ge 0) {
    Write-Host "RESULT : FAIL"
    Write-Host "REASON : --temporal on does not reduce orphan_activity (delta=$activityDelta)"
    exit 1
}
else {
    # delta < 0 but no temporal-stub edges
    Write-Host "RESULT : FAIL"
    Write-Host "REASON : orphan_activity reduced but temporal-stub edge count is 0 or unavailable"
    exit 1
}
