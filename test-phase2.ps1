param(
    [int]$Port = 8080,
    [int]$TimeoutSeconds = 15
)

$Root = "C:\Desktop\mesh"
$TestOut = Join-Path $Root ".phase2-test-out.txt"
$TestErr = Join-Path $Root ".phase2-test-err.txt"
$passed = 0
$failed = 0

function Test-Step {
    param($Name, $ScriptBlock)
    Write-Host "  [$Name] " -NoNewline
    try {
        $ok = & $ScriptBlock
        if ($ok) {
            Write-Host "PASS" -ForegroundColor Green
            $script:passed++
        } else {
            Write-Host "FAIL" -ForegroundColor Red
            $script:failed++
        }
    } catch {
        Write-Host "FAIL ($($_.Exception.Message))" -ForegroundColor Red
        $script:failed++
    }
}

Write-Host "=== Phase 2 Test Suite ===" -ForegroundColor Cyan
Write-Host ""

# ── Kill any leftover servers ──
taskkill /F /IM node.exe 2>$null
Start-Sleep -Seconds 1

# ── Start server with timeout ──
Write-Host "Starting server..." -ForegroundColor Yellow
$server = $null
try {
    $server = Start-Process -FilePath node -ArgumentList "server/index.js" -WorkingDirectory $Root -PassThru -NoNewWindow -RedirectStandardOutput $TestOut -RedirectStandardError $TestErr
    $server | Wait-Process -Timeout 3 -ErrorAction SilentlyContinue
    if ($server.HasExited) {
        Write-Host "  Server failed to start (exit code $($server.ExitCode))" -ForegroundColor Red
        Write-Host "  $(Get-Content $TestErr -Raw)" -ForegroundColor Red
        exit 1
    }
    Write-Host "  Server running (PID $($server.Id))" -ForegroundColor Green
} catch {
    Write-Host "  Failed to launch server: $_" -ForegroundColor Red
    exit 1
}

# ── Run tests with overall timeout ──
$timer = [System.Diagnostics.Stopwatch]::StartNew()
$allOk = $true

try {
    # 1. Root page loads
    Test-Step "GET /" { (Invoke-WebRequest -Uri "http://localhost:$Port/" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }

    # 2. app.js loads
    Test-Step "GET /app.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/app.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }

    # 3. ui/shell.js loads
    Test-Step "GET /ui/shell.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/ui/shell.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }

    # 4. ui/manual.js loads
    Test-Step "GET /ui/manual.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/ui/manual.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }

    # 5. webrtc/peer.js loads
    Test-Step "GET /webrtc/peer.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/webrtc/peer.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }

    # 6. webrtc/crypto.js loads
    Test-Step "GET /webrtc/crypto.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/webrtc/crypto.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }

    # 7. CSS loads
    Test-Step "GET /styles/main.css" { (Invoke-WebRequest -Uri "http://localhost:$Port/styles/main.css" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }

    # 8. Download endpoint
    Test-Step "GET /download/mesh.zip" { (Invoke-WebRequest -Uri "http://localhost:$Port/download/mesh.zip" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }

    # 9. 404 for missing files
    Test-Step "GET /nonexistent (404)" { try { Invoke-WebRequest -Uri "http://localhost:$Port/nonexistent.js" -UseBasicParsing -TimeoutSec 5; $false } catch { $_.Exception.Response.StatusCode -eq 404 } }

    # 10. WebSocket /signal accepts connection
    Test-Step "WebSocket /signal" {
        $ws = New-Object System.Net.WebSockets.ClientWebSocket
        $ct = New-Object System.Threading.CancellationToken
        $ws.ConnectAsync((New-Object System.Uri("ws://localhost:$Port/signal")), $ct).Wait(5000)
        $ok = $ws.State -eq [System.Net.WebSockets.WebSocketState]::Open
        $ws.CloseAsync(1000, "test", $ct).Wait(1000)
        $ws.Dispose()
        $ok
    }

    # 11. Content-Type headers correct
    Test-Step "Content-Type: text/html" {
        $r = Invoke-WebRequest -Uri "http://localhost:$Port/" -UseBasicParsing -TimeoutSec 5
        $r.Headers["Content-Type"] -eq "text/html"
    }

    Test-Step "Content-Type: application/javascript" {
        $r = Invoke-WebRequest -Uri "http://localhost:$Port/app.js" -UseBasicParsing -TimeoutSec 5
        $r.Headers["Content-Type"] -eq "application/javascript"
    }

    Test-Step "Content-Type: text/css" {
        $r = Invoke-WebRequest -Uri "http://localhost:$Port/styles/main.css" -UseBasicParsing -TimeoutSec 5
        $r.Headers["Content-Type"] -eq "text/css"
    }

} catch {
    Write-Host "  Test error: $_" -ForegroundColor Red
    $allOk = $false
} finally {
    # Kill server
    if ($server -and !$server.HasExited) {
        Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
    }
    Remove-Item $TestOut, $TestErr -Force -ErrorAction SilentlyContinue
}

# ── Summary ──
Write-Host ""
$total = $passed + $failed
Write-Host "=== Results: $passed/$total passed, $failed failed ===" -ForegroundColor $(if ($failed -eq 0) { "Green" } else { "Red" })
if ($failed -eq 0) { exit 0 } else { exit 1 }
