param(
    [int]$Port = 8080
)

$Root = "C:\repos\Eris"
$TestOut = Join-Path $Root ".phase2-test-out.txt"
$TestErr = Join-Path $Root ".phase2-test-err.txt"
$passed = 0
$failed = 0

function Test-Step {
    param($Name, $ScriptBlock)
    Write-Host "  [$Name] " -NoNewline
    try {
        $ok = & $ScriptBlock
        if ($ok) { Write-Host "PASS" -ForegroundColor Green; $script:passed++ }
        else { Write-Host "FAIL" -ForegroundColor Red; $script:failed++ }
    } catch {
        Write-Host "FAIL ($($_.Exception.Message))" -ForegroundColor Red
        $script:failed++
    }
}

function Send-WebSocketMessage {
    param($Ws, $Message)
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($Message)
    $seg = New-Object System.ArraySegment[byte] -ArgumentList @(,$bytes)
    $Ws.SendAsync($seg, [System.Net.WebSockets.WebSocketMessageType]::Text, $true, [System.Threading.CancellationToken]::None).Wait(3000)
}

function Receive-WebSocketMessage {
    param($Ws, $TimeoutMs = 3000)
    $buf = New-Object byte[] 4096
    $seg = New-Object System.ArraySegment[byte] -ArgumentList @(,$buf)
    $task = $Ws.ReceiveAsync($seg, [System.Threading.CancellationToken]::None)
    if ($task.Wait($TimeoutMs)) {
        $len = $task.Result.Count
        $text = [System.Text.Encoding]::UTF8.GetString($buf, 0, $len)
        return $text
    }
    return $null
}

Write-Host "=== Phase 2 + 3 Test Suite ===" -ForegroundColor Cyan
Write-Host ""

taskkill /F /IM node.exe 2>$null
Start-Sleep -Seconds 1

Write-Host "Starting server..." -ForegroundColor Yellow
$server = $null
try {
    $server = Start-Process -FilePath node -ArgumentList "server/index.js" -WorkingDirectory $Root -PassThru -NoNewWindow -RedirectStandardOutput $TestOut -RedirectStandardError $TestErr
    $server | Wait-Process -Timeout 3 -ErrorAction SilentlyContinue
    if ($server.HasExited) {
        Write-Host "  FAILED to start (exit code $($server.ExitCode))" -ForegroundColor Red
        Write-Host "  $(Get-Content $TestErr -Raw)" -ForegroundColor Red
        exit 1
    }
    Write-Host "  Server running (PID $($server.Id))" -ForegroundColor Green
} catch {
    Write-Host "  Failed to launch: $_" -ForegroundColor Red
    exit 1
}

try {
    # Phase 2 tests — HTTP + basic WebSocket
    Test-Step "GET /" { (Invoke-WebRequest -Uri "http://localhost:$Port/" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /app.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/app.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /ui/shell.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/ui/shell.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /ui/manual.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/ui/manual.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /ui/room.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/ui/room.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /ui/chat.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/ui/chat.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /ui/tiles.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/ui/tiles.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /channels/text.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/channels/text.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /channels/file.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/channels/file.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /channels/media.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/channels/media.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /channels/screen.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/channels/screen.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /webrtc/peer.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/webrtc/peer.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /webrtc/crypto.js" { (Invoke-WebRequest -Uri "http://localhost:$Port/webrtc/crypto.js" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /styles/main.css" { (Invoke-WebRequest -Uri "http://localhost:$Port/styles/main.css" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /download/eris.zip" { (Invoke-WebRequest -Uri "http://localhost:$Port/download/eris.zip" -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200 }
    Test-Step "GET /nonexistent (404)" { try { Invoke-WebRequest -Uri "http://localhost:$Port/nonexistent.js" -UseBasicParsing -TimeoutSec 5; $false } catch { $_.Exception.Response.StatusCode -eq 404 } }

    Test-Step "MIME text/html" {
        $r = Invoke-WebRequest -Uri "http://localhost:$Port/" -UseBasicParsing -TimeoutSec 5
        $r.Headers["Content-Type"] -eq "text/html"
    }
    Test-Step "MIME application/javascript" {
        $r = Invoke-WebRequest -Uri "http://localhost:$Port/app.js" -UseBasicParsing -TimeoutSec 5
        $r.Headers["Content-Type"] -eq "application/javascript"
    }
    Test-Step "MIME text/css" {
        $r = Invoke-WebRequest -Uri "http://localhost:$Port/styles/main.css" -UseBasicParsing -TimeoutSec 5
        $r.Headers["Content-Type"] -eq "text/css"
    }

    # Phase 3 tests — Room WebSocket protocol
    Write-Host "`n── Room protocol ──" -ForegroundColor Yellow

    Test-Step "Room: create room" {
        $ws = New-Object System.Net.WebSockets.ClientWebSocket
        $ws.ConnectAsync((New-Object System.Uri("ws://localhost:$Port/signal")), [System.Threading.CancellationToken]::None).Wait(3000)
        Send-WebSocketMessage $ws '{"type":"create","room":"testroom","password":"abc123"}'
        $resp = Receive-WebSocketMessage $ws
        $ws.CloseAsync(1000, "done", [System.Threading.CancellationToken]::None).Wait(1000)
        $ws.Dispose()
        if (-not $resp) { return $false }
        $obj = $resp | ConvertFrom-Json
        $obj.type -eq "created" -and $obj.room -eq "testroom"
    }

    Test-Step "Room: create duplicate (error)" {
        $ws1 = New-Object System.Net.WebSockets.ClientWebSocket
        $ws1.ConnectAsync((New-Object System.Uri("ws://localhost:$Port/signal")), [System.Threading.CancellationToken]::None).Wait(3000)
        Send-WebSocketMessage $ws1 '{"type":"create","room":"dup","password":"x"}'
        Receive-WebSocketMessage $ws1 | Out-Null  # consume "created"

        $ws2 = New-Object System.Net.WebSockets.ClientWebSocket
        $ws2.ConnectAsync((New-Object System.Uri("ws://localhost:$Port/signal")), [System.Threading.CancellationToken]::None).Wait(3000)
        Send-WebSocketMessage $ws2 '{"type":"create","room":"dup","password":"x"}'
        $resp = Receive-WebSocketMessage $ws2

        $ws1.CloseAsync(1000, "done", [System.Threading.CancellationToken]::None).Wait(1000)
        $ws2.CloseAsync(1000, "done", [System.Threading.CancellationToken]::None).Wait(1000)
        $ws1.Dispose(); $ws2.Dispose()
        if (-not $resp) { return $false }
        ($resp | ConvertFrom-Json).type -eq "error"
    }

    Test-Step "Room: join nonexistent (error)" {
        $ws = New-Object System.Net.WebSockets.ClientWebSocket
        $ws.ConnectAsync((New-Object System.Uri("ws://localhost:$Port/signal")), [System.Threading.CancellationToken]::None).Wait(3000)
        Send-WebSocketMessage $ws '{"type":"join","room":"nope","password":"x"}'
        $resp = Receive-WebSocketMessage $ws
        $ws.CloseAsync(1000, "done", [System.Threading.CancellationToken]::None).Wait(1000)
        $ws.Dispose()
        if (-not $resp) { return $false }
        ($resp | ConvertFrom-Json).type -eq "error"
    }

    Test-Step "Room: wrong password (error)" {
        $creator = New-Object System.Net.WebSockets.ClientWebSocket
        $creator.ConnectAsync((New-Object System.Uri("ws://localhost:$Port/signal")), [System.Threading.CancellationToken]::None).Wait(3000)
        Send-WebSocketMessage $creator '{"type":"create","room":"pwtest","password":"correct"}'
        Receive-WebSocketMessage $creator | Out-Null

        $joiner = New-Object System.Net.WebSockets.ClientWebSocket
        $joiner.ConnectAsync((New-Object System.Uri("ws://localhost:$Port/signal")), [System.Threading.CancellationToken]::None).Wait(3000)
        Send-WebSocketMessage $joiner '{"type":"join","room":"pwtest","password":"wrong"}'
        $resp = Receive-WebSocketMessage $joiner

        $creator.CloseAsync(1000, "done", [System.Threading.CancellationToken]::None).Wait(1000)
        $joiner.CloseAsync(1000, "done", [System.Threading.CancellationToken]::None).Wait(1000)
        $creator.Dispose(); $joiner.Dispose()
        if (-not $resp) { return $false }
        ($resp | ConvertFrom-Json).type -eq "error"
    }

    Test-Step "Room: full join/leave cycle" {
        $c = New-Object System.Net.WebSockets.ClientWebSocket
        $c.ConnectAsync((New-Object System.Uri("ws://localhost:$Port/signal")), [System.Threading.CancellationToken]::None).Wait(3000)
        Send-WebSocketMessage $c '{"type":"create","room":"fullcycle","password":"p"}'
        $msg1 = Receive-WebSocketMessage $c
        if (-not ($msg1 -and ($msg1 | ConvertFrom-Json).type -eq "created")) { return $false }

        $j = New-Object System.Net.WebSockets.ClientWebSocket
        $j.ConnectAsync((New-Object System.Uri("ws://localhost:$Port/signal")), [System.Threading.CancellationToken]::None).Wait(3000)
        Send-WebSocketMessage $j '{"type":"join","room":"fullcycle","password":"p"}'
        $msg2 = Receive-WebSocketMessage $j
        $msg3 = Receive-WebSocketMessage $c

        Send-WebSocketMessage $c '{"type":"signal","room":"fullcycle","data":{"type":"offer","sdp":"test-sdp"}}'
        $msg4 = Receive-WebSocketMessage $j

        Send-WebSocketMessage $j '{"type":"signal","room":"fullcycle","data":{"type":"answer","sdp":"test-answer"}}'
        $msg5 = Receive-WebSocketMessage $c

        try { $j.CloseAsync(1000, "done", [System.Threading.CancellationToken]::None).Wait(1000) } catch {}
        try { $c.CloseAsync(1000, "done", [System.Threading.CancellationToken]::None).Wait(1000) } catch {}
        $c.Dispose(); $j.Dispose()

        $ok = $true
        if (-not ($msg2 -and ($msg2 | ConvertFrom-Json).type -eq "joined")) { $ok = $false }
        if (-not ($msg3 -and ($msg3 | ConvertFrom-Json).type -eq "peer-joined")) { $ok = $false }
        if (-not ($msg4 -and ($msg4 | ConvertFrom-Json).data.type -eq "offer" -and ($msg4 | ConvertFrom-Json).data.sdp -eq "test-sdp")) { $ok = $false }
        if (-not ($msg5 -and ($msg5 | ConvertFrom-Json).data.type -eq "answer" -and ($msg5 | ConvertFrom-Json).data.sdp -eq "test-answer")) { $ok = $false }
        $ok
    }

} catch {
    Write-Host "  Test error: $_" -ForegroundColor Red
} finally {
    if ($server -and !$server.HasExited) { Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue }
    Remove-Item $TestOut, $TestErr -Force -ErrorAction SilentlyContinue
}

Write-Host ""
$total = $passed + $failed
Write-Host "=== Results: $passed/$total passed, $failed failed ===" -ForegroundColor $(if ($failed -eq 0) { "Green" } else { "Red" })
if ($failed -eq 0) { exit 0 } else { exit 1 }
