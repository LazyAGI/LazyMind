param([string]$SignedFile)
$ErrorActionPreference = 'Stop'
# Read only the operator's configured public DER. Never establish trust from a downloaded package.
$type = [System.Security.Cryptography.X509Certificates.X509Certificate2]
$der = [Convert]::FromBase64String($env:WINDOWS_TEST_CERTIFICATE)
if ($type::GetCertContentType($der) -ne 'Cert') { throw 'Expected a public DER certificate' }
$cert = $type::new($der)
$config = $env:LAZYMIND_WINDOWS_TEST_UPDATE_CONFIG_JSON | ConvertFrom-Json
$usage = $cert.Extensions | Where-Object { $_.Oid.Value -eq '2.5.29.37' }
$eku = @($usage.EnhancedKeyUsages | ForEach-Object { $_.Value })
$constraints = $cert.Extensions | Where-Object { $_.Oid.Value -eq '2.5.29.19' }
$keyUsage = $cert.Extensions | Where-Object { $_.Oid.Value -eq '2.5.29.15' }
if ($cert.HasPrivateKey -or $cert.Subject -cne $cert.Issuer -or $constraints.CertificateAuthority -or
    ($keyUsage -and ($keyUsage.KeyUsages -band [System.Security.Cryptography.X509Certificates.X509KeyUsageFlags]::KeyCertSign)) -or
    $eku.Count -ne 1 -or $eku[0] -ne '1.3.6.1.5.5.7.3.3' -or
    (Get-Date) -lt $cert.NotBefore -or (Get-Date) -gt $cert.NotAfter -or
    $cert.GetNameInfo('SimpleName', $false) -cne $config.signingIdentity) {
    throw 'Expected the configured, current, self-issued Windows code-signing leaf'
}
if ($SignedFile) {
    $sig = Get-AuthenticodeSignature -LiteralPath $SignedFile
    if ($sig.Status -ne 'Valid' -or [Convert]::ToBase64String($sig.SignerCertificate.RawData) -cne [Convert]::ToBase64String($cert.RawData)) {
        throw 'Windows test installer is not signed by the pinned certificate'
    }
} else {
    if ($env:GITHUB_ACTIONS -ne 'true' -or $env:RUNNER_ENVIRONMENT -ne 'github-hosted' -or $env:RUNNER_OS -ne 'Windows') {
        throw 'Automatic certificate trust requires a disposable GitHub-hosted Windows runner'
    }
    # CurrentUser Root can wait for an interactive confirmation on a hosted runner.
    $store = [System.Security.Cryptography.X509Certificates.X509Store]::new('Root', 'LocalMachine')
    try {
        $store.Open('ReadWrite')
        $store.Add($cert)
    } finally {
        $store.Close()
    }
}
