param(
  [string]$Uri = "ws://127.0.0.1:8080/ws/freeswitch/stream",
  [string]$CallId = "call-mock-001",
  [string]$Caller = "+8613800138000",
  [int]$SampleRateHz = 16000,
  [int]$Channels = 1,
  [int]$FrameMs = 20,
  [int]$DurationMs = 1000
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Send-TextMessage {
  param(
    [System.Net.WebSockets.ClientWebSocket]$Socket,
    [string]$Text
  )

  $buffer = [System.Text.Encoding]::UTF8.GetBytes($Text)
  $segment = [System.ArraySegment[byte]]::new($buffer)
  $task = $Socket.SendAsync(
    $segment,
    [System.Net.WebSockets.WebSocketMessageType]::Text,
    $true,
    [System.Threading.CancellationToken]::None
  )
  $task.GetAwaiter().GetResult()
}

function Send-BinaryMessage {
  param(
    [System.Net.WebSockets.ClientWebSocket]$Socket,
    [byte[]]$Bytes
  )

  $segment = [System.ArraySegment[byte]]::new($Bytes)
  $task = $Socket.SendAsync(
    $segment,
    [System.Net.WebSockets.WebSocketMessageType]::Binary,
    $true,
    [System.Threading.CancellationToken]::None
  )
  $task.GetAwaiter().GetResult()
}

function Read-TextMessage {
  param(
    [System.Net.WebSockets.ClientWebSocket]$Socket
  )

  $buffer = New-Object byte[] 4096
  $builder = New-Object System.Text.StringBuilder

  while ($true) {
    $segment = [System.ArraySegment[byte]]::new($buffer)
    $result = $Socket.ReceiveAsync($segment, [System.Threading.CancellationToken]::None).GetAwaiter().GetResult()

    if ($result.MessageType -eq [System.Net.WebSockets.WebSocketMessageType]::Close) {
      return $null
    }

    $null = $builder.Append([System.Text.Encoding]::UTF8.GetString($buffer, 0, $result.Count))
    if ($result.EndOfMessage) {
      return $builder.ToString()
    }
  }
}

$bytesPerSample = 2
$frameBytes = [int](($SampleRateHz * $Channels * $bytesPerSample * $FrameMs) / 1000)
if ($frameBytes -le 0) {
  throw "Frame size must be positive."
}

$frameCount = [Math]::Max([int]($DurationMs / $FrameMs), 1)
$silence = New-Object byte[] $frameBytes

$socket = [System.Net.WebSockets.ClientWebSocket]::new()
try {
  Write-Host "Connecting to $Uri"
  $socket.ConnectAsync([Uri]$Uri, [System.Threading.CancellationToken]::None).GetAwaiter().GetResult()

  $startPayload = @{
    type = "stream.start"
    callId = $CallId
    caller = $Caller
    stream = @{
      encoding = "pcm_s16le"
      sampleRateHz = $SampleRateHz
      channels = $Channels
    }
  } | ConvertTo-Json -Compress

  Write-Host "Sending stream.start for $CallId"
  Send-TextMessage -Socket $socket -Text $startPayload

  $ack = Read-TextMessage -Socket $socket
  if ($null -eq $ack) {
    throw "Socket closed before stream.ack was received."
  }
  Write-Host "Received: $ack"

  for ($index = 0; $index -lt $frameCount; $index++) {
    Send-BinaryMessage -Socket $socket -Bytes $silence
    Start-Sleep -Milliseconds $FrameMs
  }

  $stopPayload = @{
    type = "stream.stop"
    reason = "hangup"
  } | ConvertTo-Json -Compress

  Write-Host "Sending stream.stop"
  Send-TextMessage -Socket $socket -Text $stopPayload
  Write-Host "Completed mock stream with $frameCount frames of silence."
}
finally {
  if ($socket.State -eq [System.Net.WebSockets.WebSocketState]::Open) {
    try {
      $socket.CloseAsync(
        [System.Net.WebSockets.WebSocketCloseStatus]::NormalClosure,
        "client_done",
        [System.Threading.CancellationToken]::None
      ).GetAwaiter().GetResult()
    } catch {
      Write-Host "Socket already closed by remote peer."
    }
  }
  $socket.Dispose()
}
