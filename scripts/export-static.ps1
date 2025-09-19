Param(
  [string]$BaseUrl = 'http://localhost:8080',
  [string]$OutDir = 'server/web/data',
  [int]$MaxBots = 500,
  [int]$MaxMatches = 200
)

Write-Host "Exporting static JSON from $BaseUrl to $OutDir"
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

function SaveJson($Endpoint, $Path){
  $url = ($BaseUrl.TrimEnd('/') + $Endpoint)
  try {
    $r = Invoke-WebRequest -Uri $url -TimeoutSec 60
    if ($r.StatusCode -ge 200 -and $r.StatusCode -lt 300) {
      $r.Content | Out-File -FilePath $Path -Encoding utf8
      Write-Host "Saved" $Endpoint "->" $Path
    } else {
      Write-Warning "Failed $Endpoint ($($r.StatusCode))"
    }
  } catch {
    Write-Warning "Error fetching $Endpoint: $($_.Exception.Message)"
  }
}

# Core pages
SaveJson '/api/leaderboard'    (Join-Path $OutDir 'leaderboard.json')
SaveJson '/api/judge-accuracy' (Join-Path $OutDir 'judge-accuracy.json')
SaveJson '/api/matrix'         (Join-Path $OutDir 'matrix.json')
SaveJson '/api/elo-history'    (Join-Path $OutDir 'elo-history.json')
SaveJson '/api/matches'        (Join-Path $OutDir 'matches.json')

# Per-bot pages
try {
  $lb = Get-Content (Join-Path $OutDir 'leaderboard.json') -Raw | ConvertFrom-Json
  $rows = @()
  if ($lb -and $lb.rows) { $rows = $lb.rows } elseif ($lb) { $rows = $lb }
  $rows = $rows | Select-Object -First $MaxBots
  foreach($b in $rows){
    $id = $b.bot_id
    if ($null -eq $id) { continue }
    SaveJson ("/api/bot?id=$id")       (Join-Path $OutDir ("bot-$id.json"))
    SaveJson ("/api/bot-style?id=$id") (Join-Path $OutDir ("bot-style-$id.json"))
  }
} catch {
  Write-Warning "Skipping per-bot export: $($_.Exception.Message)"
}

# Match logs for recent matches (for replay offline)
try {
  $mh = Get-Content (Join-Path $OutDir 'matches.json') -Raw | ConvertFrom-Json
  $list = $mh.rows | Select-Object -First $MaxMatches
  foreach($m in $list){
    $mid = $m.id; if ($null -eq $mid) { continue }
    SaveJson ("/api/match-logs?match_id=$mid") (Join-Path $OutDir ("match-logs-$mid.json"))
  }
} catch {
  Write-Warning "Skipping match-logs export: $($_.Exception.Message)"
}

Write-Host "Static export complete."
