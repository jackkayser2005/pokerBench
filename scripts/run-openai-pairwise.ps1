Param(
  [Parameter(Mandatory=$true)][string]$Models,
  [int]$Pairs = 1,
  [string]$ReasoningEffort = 'low',
  [int]$MaxOutputTokens = 32
)

# Example:
#   .\scripts\run-openai-pairwise.ps1 -Models "gpt-5,gpt-5-mini,o3,o3-mini,gpt-4o,gpt-4.1-mini-2025-04-14" -Pairs 1

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$secretPath = Join-Path $repoRoot 'secrets/openai_api_key.txt'
if (-not (Test-Path $secretPath)) {
  throw "Missing OpenAI API key. Create $secretPath with your key (one line)."
}
$env:OPENAI_API_SECRET_FILE = $secretPath

Write-Host "Pairwise matrix: models=$Models pairs=$Pairs reasoning=$ReasoningEffort max_out=$MaxOutputTokens"

$env:OPENAI_MODELS = $Models
$env:DUEL_SEEDS = $Pairs

if ($ReasoningEffort -ne '') { $env:OPENAI_REASONING_EFFORT = $ReasoningEffort } else { Remove-Item Env:OPENAI_REASONING_EFFORT -ErrorAction SilentlyContinue }
if ($MaxOutputTokens -gt 0) { $env:OPENAI_MAX_OUTPUT_TOKENS = $MaxOutputTokens } else { Remove-Item Env:OPENAI_MAX_OUTPUT_TOKENS -ErrorAction SilentlyContinue }

docker compose run --rm duel /app/ai-thunderdome --duel-matrix

Write-Host "Pairwise matrix complete."
