$ErrorActionPreference = 'Stop'
if ($env:GITHUB_ACTIONS -ne 'true' -or $env:RUNNER_ENVIRONMENT -ne 'github-hosted' -or $env:RUNNER_OS -ne 'Windows') {
    throw 'Run the certificate fixture only on a disposable GitHub-hosted Windows runner'
}
$publicStore = 'Cert:\LocalMachine\Root'
$signerStore = 'Cert:\CurrentUser\My'
$trust = Join-Path $PSScriptRoot '..\windows-test-certificate.ps1'
$dir = [IO.Directory]::CreateDirectory((Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())))
$certs = @()
try {
    $subject = "Windows update fixture $([guid]::NewGuid())"
    foreach ($i in 1..2) {
        $certs += New-SelfSignedCertificate -Type CodeSigningCert -Subject "CN=$subject" -CertStoreLocation $signerStore
    }
    $sample = Join-Path $dir 'fixture.exe'
    $source = Join-Path $dir 'fixture.cs'
    Set-Content -LiteralPath $source -Value 'class Program { static void Main() {} }'
    $compiler = Join-Path $env:SystemRoot 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
    & $compiler /nologo /target:exe "/out:$sample" $source
    if ($LASTEXITCODE -ne 0 -or (Get-AuthenticodeSignature $sample).Status -ne 'NotSigned') { throw 'Expected an unsigned fixture PE' }
    $unsigned = [IO.File]::ReadAllBytes($sample)
    $env:LAZYMIND_WINDOWS_TEST_UPDATE_CONFIG_JSON = @{signingIdentity=$subject} | ConvertTo-Json -Compress
    $env:WINDOWS_TEST_CERTIFICATE = [Convert]::ToBase64String($certs[0].RawData)
    $savedRunner = $env:RUNNER_ENVIRONMENT
    try {
        $env:RUNNER_ENVIRONMENT = 'self-hosted'
        $refused = $false
        try { & $trust } catch { $refused = $true }
        if (-not $refused -or (Test-Path "$publicStore\$($certs[0].Thumbprint)")) { throw 'Persistent runner trust was not refused' }
    } finally { $env:RUNNER_ENVIRONMENT = $savedRunner }
    & $trust
    & $trust
    if (-not (Test-Path "$publicStore\$($certs[0].Thumbprint)")) { throw 'Public certificate was not trusted' }
    Set-AuthenticodeSignature -FilePath $sample -Certificate $certs[0] -HashAlgorithm SHA256 | Out-Null
    $actual = Get-AuthenticodeSignature $sample
    if ($actual.Status -ne 'Valid' -or $actual.SignerCertificate.Thumbprint -ne $certs[0].Thumbprint) { throw 'Bad positive fixture' }
    & $trust -SignedFile $sample

    # Even another valid certificate with the same display name cannot sign this round.
    $otherPublic = Join-Path $dir 'other.cer'
    [IO.File]::WriteAllBytes($otherPublic, $certs[1].RawData)
    Import-Certificate -FilePath $otherPublic -CertStoreLocation $publicStore | Out-Null
    [IO.File]::WriteAllBytes($sample, $unsigned)
    Set-AuthenticodeSignature -FilePath $sample -Certificate $certs[1] -HashAlgorithm SHA256 | Out-Null
    $actual = Get-AuthenticodeSignature $sample
    if ($actual.Status -ne 'Valid' -or $actual.SignerCertificate.Thumbprint -ne $certs[1].Thumbprint) { throw 'Bad negative fixture' }
    $rejected = $false
    try { & $trust -SignedFile $sample } catch { $rejected = $true }
    if (-not $rejected) { throw 'Different certificate accepted' }
    Write-Output 'pinned signer verified'
} finally {
    foreach ($cert in $certs) {
        foreach ($store in @($publicStore, $signerStore)) {
            $entry = "$store\$($cert.Thumbprint)"
            if (Test-Path $entry) { Remove-Item $entry -Force }
        }
    }
    Remove-Item -LiteralPath $dir.FullName -Recurse -Force
}
