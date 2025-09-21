Param(
  [int]$Pairs = 75,
  [string]$Models = "meta-llama/llama-3.1-70b-instruct,mistralai/mistral-nemo",
  [string[]]$Reasoning = @("", "low", "medium", "high")
)

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$secretPath = Join-Path $repoRoot 'secrets/openrouter_api_key.txt'
if (-not (Test-Path $secretPath)) {
  throw "Missing OpenRouter API key. Create $secretPath with your key (one line)."
}
$env:OPENROUTER_API_KEY_FILE = $secretPath

Write-Host "Running matrix: pairs=$Pairs models=$Models reasoning=[$($Reasoning -join ', ')]"

$env:DUEL_SEEDS = $Pairs

$modelList = $Models.Split(',') | ForEach-Object { $_.Trim() } | Where-Object { $_ }

foreach ($m in $modelList) {
  foreach ($effort in $Reasoning) {
    if ($effort -ne "") { $env:OPENROUTER_REASONING_EFFORT = $effort } else { Remove-Item Env:OPENROUTER_REASONING_EFFORT -ErrorAction SilentlyContinue }
    $env:OPENROUTER_MODEL_A = $m
    $env:OPENROUTER_MODEL_B = $m
    Write-Host "--> $m (reasoning='$effort')"
    docker compose run --rm duel /app/ai-thunderdome --duel
  }
}

Write-Host "Matrix complete."
