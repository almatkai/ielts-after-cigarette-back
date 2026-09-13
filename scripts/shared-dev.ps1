param(
    [ValidateSet('Check', 'Migrate', 'Up', 'Stop', 'Logs')]
    [string]$Action = 'Check'
)

$ErrorActionPreference = 'Stop'
$backendRoot = Split-Path -Parent $PSScriptRoot
foreach ($file in @('.env', '.env.dev')) {
    if (-not (Test-Path -LiteralPath (Join-Path $backendRoot $file))) {
        throw "Missing $file. Copy its .example file and configure credentials privately."
    }
}

$composeArgs = @('compose', '--env-file', '.env', '--env-file', '.env.dev', '-f', 'docker-compose.dev.yml')
Push-Location $backendRoot
try {
    switch ($Action) {
        'Check' { & docker @composeArgs config --quiet }
        'Migrate' { & docker @composeArgs run --rm migrate }
        'Up' { & docker @composeArgs up -d --build backend }
        'Stop' { & docker @composeArgs stop }
        'Logs' { & docker @composeArgs logs --tail 100 backend }
    }
    if ($LASTEXITCODE -ne 0) {
        throw "Shared-dev action '$Action' failed (exit $LASTEXITCODE)."
    }
} finally {
    Pop-Location
}
