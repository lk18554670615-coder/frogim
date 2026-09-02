$ErrorActionPreference = 'Stop'
# Deterministic, original two-note notification chime; no external sound asset.
$toneDirectory = Join-Path $PSScriptRoot '../apps/mobile/assets/sounds'
[void][System.IO.Directory]::CreateDirectory($toneDirectory)
$tonePath = [System.IO.Path]::GetFullPath((Join-Path $toneDirectory 'message.wav'))
$sampleRate = 22050
$samples = [int]($sampleRate * 0.24)
$stream = [System.IO.File]::Create($tonePath)
$writer = [System.IO.BinaryWriter]::new($stream)
try {
    $writer.Write([System.Text.Encoding]::ASCII.GetBytes('RIFF'))
    $writer.Write([int](36 + $samples * 2))
    $writer.Write([System.Text.Encoding]::ASCII.GetBytes('WAVEfmt '))
    $writer.Write([int]16)
    $writer.Write([int16]1)
    $writer.Write([int16]1)
    $writer.Write([int]$sampleRate)
    $writer.Write([int]($sampleRate * 2))
    $writer.Write([int16]2)
    $writer.Write([int16]16)
    $writer.Write([System.Text.Encoding]::ASCII.GetBytes('data'))
    $writer.Write([int]($samples * 2))
    for ($sample = 0; $sample -lt $samples; $sample++) {
        $t = $sample / $sampleRate
        $a = [Math]::Sin(2 * [Math]::PI * 880 * $t) * [Math]::Exp(-22 * $t)
        $b = 0.0
        if ($t -ge 0.08) {
            $u = $t - 0.08
            $b = [Math]::Sin(2 * [Math]::PI * 1320 * $u) * [Math]::Exp(-30 * $u) * [Math]::Min(1, $u / 0.005)
        }
        $envelope = [Math]::Min(1, $t / 0.005) * [Math]::Min(1, (0.24 - $t) / 0.02)
        $writer.Write([int16](9000 * ($a + 0.7 * $b) * $envelope))
    }
} finally {
    $writer.Dispose()
}
Get-FileHash -LiteralPath $tonePath -Algorithm SHA256
