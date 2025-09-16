Param(
  [int]$Pairs = 75,
  [string]$Models = "gpt-4o-mini,gpt-4o,gpt-4.1-mini-2025-04-14,gpt-5-mini",
  [string[]]$Reasoning = @("", "low", "medium", "high")
)

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$secretPath = Join-Path $repoRoot 'secrets/openai_api_key.txt'
if (-not (Test-Path $secretPath)) {
  throw "Missing OpenAI API key. Create $secretPath with your key (one line)."
}
$env:OPENAI_API_SECRET_FILE = $secretPath

Write-Host "Running matrix: pairs=$Pairs models=$Models reasoning=[$($Reasoning -join ', ')]"

$env:DUEL_SEEDS = $Pairs

$modelList = $Models.Split(',') | ForEach-Object { $_.Trim() } | Where-Object { $_ }

foreach ($m in $modelList) {
  foreach ($effort in $Reasoning) {
    if ($effort -ne "") { $env:OPENAI_REASONING_EFFORT = $effort } else { Remove-Item Env:OPENAI_REASONING_EFFORT -ErrorAction SilentlyContinue }
    $env:OPENAI_MODEL_A = $m
    $env:OPENAI_MODEL_B = $m
    Write-Host "--> $m (reasoning='$effort')"
    docker compose run --rm duel /app/ai-thunderdome --duel
  }
}

Write-Host "Matrix complete."
