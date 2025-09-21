Param(
  [Parameter(Mandatory=$true)][string]$Models,
  [int]$Pairs = 1,
  [string]$ReasoningEffort = 'low',
  [int]$MaxOutputTokens = 32
)

# Example:
#   .\scripts\run-openrouter-pairwise.ps1 -Models "meta-llama/llama-3.1-70b-instruct,mistralai/mistral-nemo" -Pairs 1

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$secretPath = Join-Path $repoRoot 'secrets/openrouter_api_key.txt'
if (-not (Test-Path $secretPath)) {
  throw "Missing OpenRouter API key. Create $secretPath with your key (one line)."
}
$env:OPENROUTER_API_KEY_FILE = $secretPath

Write-Host "Pairwise matrix: models=$Models pairs=$Pairs reasoning=$ReasoningEffort max_out=$MaxOutputTokens"

$env:OPENROUTER_MODELS = $Models
$env:DUEL_SEEDS = $Pairs

if ($ReasoningEffort -ne '') { $env:OPENROUTER_REASONING_EFFORT = $ReasoningEffort } else { Remove-Item Env:OPENROUTER_REASONING_EFFORT -ErrorAction SilentlyContinue }
if ($MaxOutputTokens -gt 0) { $env:OPENROUTER_MAX_OUTPUT_TOKENS = $MaxOutputTokens } else { Remove-Item Env:OPENROUTER_MAX_OUTPUT_TOKENS -ErrorAction SilentlyContinue }

docker compose run --rm duel /app/ai-thunderdome --duel-matrix

Write-Host "Pairwise matrix complete."
