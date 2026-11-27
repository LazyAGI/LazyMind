# Run without Pester: pwsh -NoProfile -File desktop/scripts/windows-installer-volume.test.ps1
[CmdletBinding()]
param([switch]$WorkflowOnly, [string]$VolumeCase)

$ErrorActionPreference = 'Stop'

function Assert-True($Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}

function Assert-Throws([scriptblock]$Action, [string]$Message, [string]$ExpectedError) {
    try { & $Action | Out-Null } catch {
        if ($ExpectedError) {
            Assert-True ($_.Exception.Message -match $ExpectedError) "Unexpected rejection: $($_.Exception.Message)"
        }
        return
    }
    throw $Message
}

function Read-InstallerVolumeFunctions([string]$Source) {
    $tokens = $null
    $errors = $null
    $ast = [System.Management.Automation.Language.Parser]::ParseInput($Source, [ref]$tokens, [ref]$errors)
    Assert-True ($errors.Count -eq 0) ($errors.Message -join '; ')
    $functions = @($ast.EndBlock.Statements)
    $names = @('Initialize-WindowsInstallerSmokeVolume', 'Clear-WindowsInstallerSmokeVolume')
    Assert-True ($null -eq $ast.BeginBlock -and $null -eq $ast.ProcessBlock -and $null -eq $ast.ParamBlock -and
        -not $ast.UsingStatements -and -not $ast.ScriptRequirements -and
        $functions.Count -eq $names.Count) 'The test loads only the two installer-volume function declarations.'
    foreach ($function in $functions) {
        Assert-True ($function -is [System.Management.Automation.Language.FunctionDefinitionAst] -and
            $function.Name -in $names) 'The installer-volume script must not execute commands while being loaded.'
    }
    Assert-True (@($functions.Name | Select-Object -Unique).Count -eq $names.Count) 'The two volume entry points must be distinct.'
    $definitions = $ast.FindAll({ param($node) $node -is [System.Management.Automation.Language.FunctionDefinitionAst] }, $true)
    Assert-True ($definitions.Count -eq $names.Count) 'The test cannot load functions that could shadow its command doubles.'
    $sharedAssignments = $ast.FindAll({ param($node)
        $node -is [System.Management.Automation.Language.AssignmentStatementAst] -and
        $node.Left -is [System.Management.Automation.Language.VariableExpressionAst] -and
        -not $node.Left.VariablePath.IsUnqualified
    }, $true)
    Assert-True ($sharedAssignments.Count -eq 0) 'Install and cleanup run in separate workflow shells and must not share in-memory ownership state.'

    # Every disk or filesystem command in this list is shadowed by a function
    # inside the test scope. Refuse unknown or indirectly invoked commands
    # before loading the candidate, even on a Windows machine with real disks.
    $allowed = @('Get-CimInstance', 'Get-Disk', 'Get-DiskImage', 'Mount-DiskImage', 'Initialize-Disk',
        'New-Partition', 'Format-Volume', 'Dismount-DiskImage', 'diskpart.exe',
        'Open-WindowsInstallerImageFile', 'Close-WindowsInstallerImageFile', 'Remove-WindowsInstallerImageFile',
        'Join-Path', 'Test-Path', 'Get-Content', 'Set-Content', 'New-Item', 'Remove-Item',
        'ForEach-Object', 'Where-Object', 'Select-Object', 'Out-Null', 'Write-Host', 'Write-Warning')
    foreach ($command in $ast.FindAll({ param($node) $node -is [System.Management.Automation.Language.CommandAst] }, $true)) {
        Assert-True ($command.GetCommandName() -in $allowed -and
            $command.InvocationOperator -ne 'Dot') "Unsafe or unmocked command in volume helper: $($command.Extent.Text)"
    }
    $redirections = $ast.FindAll({ param($node) $node -is [System.Management.Automation.Language.FileRedirectionAst] }, $true)
    Assert-True ($redirections.Count -eq 0) 'The test cannot allow direct file redirection.'
    foreach ($call in $ast.FindAll({ param($node) $node -is [System.Management.Automation.Language.InvokeMemberExpressionAst] }, $true)) {
        if ($call.Static) {
            Assert-True ($call.Expression.Extent.Text -in @('[Math]', '[IO.Path]', '[System.IO.Path]') -and
                $call.Member.Extent.Text -in @('Floor', 'Min', 'Max', 'GetFullPath', 'GetPathRoot', 'GetFileName')) `
                "Unsafe static member call in volume helper: $($call.Extent.Text)"
        } else {
            Assert-True ($call.Member.Extent.Text -in @('TrimEnd', 'Trim', 'ToUpperInvariant', 'ToLowerInvariant', 'StartsWith')) `
                "Unsafe member call in volume helper: $($call.Extent.Text)"
        }
    }
    return [scriptblock]::Create($Source)
}

function Assert-InstallerRunGate([string]$Run) {
    $tokens = $null
    $errors = $null
    $ast = [System.Management.Automation.Language.Parser]::ParseInput($Run, [ref]$tokens, [ref]$errors)
    Assert-True ($errors.Count -eq 0) ($errors.Message -join '; ')
    $commands = $ast.FindAll({ param($node) $node -is [System.Management.Automation.Language.CommandAst] }, $true)
    $setup = @($commands | Where-Object { $_.GetCommandName() -eq 'Initialize-WindowsInstallerSmokeVolume' })
    $launch = @($commands | Where-Object { $_.GetCommandName() -eq 'Start-Process' })
    $stop = @($ast.FindAll({ param($node) $node -is [System.Management.Automation.Language.AssignmentStatementAst] -and
        $node.Left.Extent.Text -eq '$ErrorActionPreference' }, $true))
    Assert-True ($setup.Count -eq 1 -and $launch.Count -eq 1 -and $stop.Count -eq 1) `
        'The Install step must contain one volume setup, one launch and one error policy.'
    Assert-True ($stop[0] -in $ast.EndBlock.Statements -and $stop[0].Right.Extent.Text -match '^["'']Stop["'']$' -and
        $stop[0].Extent.EndOffset -lt $setup[0].Extent.StartOffset) 'Set the terminating error policy before volume setup.'

    # Both calls must be direct statements in order, outside try/finally and
    # any other nested flow. The workflow must not install from a finally block.
    $setupStatement = $setup[0].Parent.Parent
    $launchStatement = $launch[0].Parent.Parent
    Assert-True ($setupStatement -is [System.Management.Automation.Language.AssignmentStatementAst] -and
        $launchStatement -is [System.Management.Automation.Language.AssignmentStatementAst] -and
        $setupStatement -in $ast.EndBlock.Statements -and $launchStatement -in $ast.EndBlock.Statements -and
        $setupStatement.Extent.EndOffset -lt $launchStatement.Extent.StartOffset -and -not $ast.EndBlock.Traps) `
        'Volume setup and installer launch must be ordered top-level assignments in the Install step.'

    # Run only the actual setup-to-launch statements. Source loading and the
    # remaining installer diagnostics are outside this unit; each command
    # in the executed span must have a same-name test double.
    $span = $Run.Substring($setupStatement.Extent.StartOffset, $launchStatement.Extent.EndOffset - $setupStatement.Extent.StartOffset)
    $spanAst = [System.Management.Automation.Language.Parser]::ParseInput($span, [ref]$tokens, [ref]$errors)
    Assert-True ($errors.Count -eq 0) ($errors.Message -join '; ')
    foreach ($node in $spanAst.FindAll({ param($item) $true }, $true)) {
        if ($node -is [System.Management.Automation.Language.CommandAst]) {
            Assert-True ($node.GetCommandName() -in @('Initialize-WindowsInstallerSmokeVolume', 'Add-Content', 'Start-Process') -and
                $node.InvocationOperator -ne 'Dot') "Unmocked command in Install gate: $($node.Extent.Text)"
        }
        Assert-True ($node -isnot [System.Management.Automation.Language.InvokeMemberExpressionAst] -and
            $node -isnot [System.Management.Automation.Language.TypeConstraintAst] -and
            $node -isnot [System.Management.Automation.Language.TypeExpressionAst] -and
            $node -isnot [System.Management.Automation.Language.FileRedirectionAst] -and
            $node -isnot [System.Management.Automation.Language.FunctionDefinitionAst]) `
            "Unsafe operation in Install gate: $($node.Extent.Text)"
        if ($node -is [System.Management.Automation.Language.AssignmentStatementAst]) {
            Assert-True ($node.Left -is [System.Management.Automation.Language.VariableExpressionAst] -and
                ($node.Left.VariablePath.IsUnqualified -or $node.Left.VariablePath.UserPath -eq 'env:INSTALL_ARGUMENTS')) `
                "Unexpected assignment in Install gate: $($node.Extent.Text)"
        }
    }
    $gate = [scriptblock]::Create("$($stop[0].Extent.Text)`n$span")
    $observed = @{ Setup = 0; Launch = 0; FailSetup = $false }
    $runDirectory = 'D:\mock-volume-run'
    function Initialize-WindowsInstallerSmokeVolume($RunDirectory) {
        $observed.Setup++
        if ($observed.FailSetup) { throw 'injected volume setup failure' }
        [pscustomobject]@{ StorageRoot = 'V:\mock-install-root'; ImagePath = 'D:\mock-volume-run\volume.vhdx' }
    }
    function Add-Content($LiteralPath, [Parameter(ValueFromPipeline)]$Value) { process { } }
    function Start-Process($FilePath, $ArgumentList, [switch]$Wait, [switch]$PassThru) {
        $observed.Launch++
        [pscustomobject]@{ ExitCode = 0 }
    }
    foreach ($name in @('Initialize-WindowsInstallerSmokeVolume', 'Add-Content', 'Start-Process')) {
        Assert-True ((Get-Command -Name $name).CommandType -eq 'Function') "Missing in-scope Install command double: $name"
    }
    $originalArguments = $env:INSTALL_ARGUMENTS
    try {
        $env:INSTALL_ARGUMENTS = '/S --simple-install'
        & $gate | Out-Null
        Assert-True ($observed.Setup -eq 1 -and $observed.Launch -eq 1) 'A successful preparation must reach the mock installer.'

        $observed.Setup = 0
        $observed.Launch = 0
        $observed.FailSetup = $true
        Assert-Throws $gate 'The Install gate swallowed a volume preparation failure.' 'injected volume setup failure'
        Assert-True ($observed.Setup -eq 1 -and $observed.Launch -eq 0) 'A failed preparation must make zero installer launch calls.'
    } finally {
        $env:INSTALL_ARGUMENTS = $originalArguments
    }
}

function Assert-InstallerGateCounterexample {
    $badRun = @'
$ErrorActionPreference = 'Stop'
try {
    $volume = Initialize-WindowsInstallerSmokeVolume -RunDirectory $runDirectory
} finally {
    $install = Start-Process -FilePath $env:INSTALLER_PATH -Wait -PassThru
}
'@
    Assert-Throws { Assert-InstallerRunGate $badRun } 'The gate accepted installer launch from finally.' 'top-level assignments'
}

function Assert-InstallerWorkflow([string]$Path) {
    $source = Get-Content -LiteralPath $Path -Raw
    $jobStart = $source.IndexOf('  test-windows-installer:')
    Assert-True ($jobStart -ge 0) 'Windows installer matrix job is missing.'
    $job = $source.Substring($jobStart)
    Assert-True ($job -match 'os: windows-2022\s+install_arguments: "/S --simple-install"\s+verify_installer_warmup: false') 'Windows 2022 simple install is missing.'
    Assert-True ($job -match 'os: windows-2025\s+install_arguments: "/S --full-install"\s+verify_installer_warmup: true') 'Windows 2025 full install and warmup are missing.'

    $checksum = $job.IndexOf('Downloaded installer hash mismatch')
    $prepare = $job.IndexOf('Initialize-WindowsInstallerSmokeVolume')
    $install = $job.IndexOf('Start-Process -FilePath $env:INSTALLER_PATH')
    $collect = $job.IndexOf('name: Collect installer diagnostics')
    $cleanup = $job.IndexOf('Clear-WindowsInstallerSmokeVolume')
    Assert-True ($checksum -ge 0 -and $prepare -gt $checksum -and $install -gt $prepare) `
        'The verified installer must be followed by checked volume setup before launch. The original D: NTFS-only setup aborts before launch on ReFS.'
    $steps = [regex]::Split($job, '(?m)(?=^      - name: )')
    $prepareStep = @($steps | Where-Object { $_.Contains('Initialize-WindowsInstallerSmokeVolume') })
    $installStep = @($steps | Where-Object { $_.Contains('Start-Process -FilePath $env:INSTALLER_PATH') })
    $cleanupStep = @($steps | Where-Object { $_.Contains('Clear-WindowsInstallerSmokeVolume') })
    Assert-True ($prepareStep.Count -eq 1 -and $installStep.Count -eq 1 -and $cleanupStep.Count -eq 1) `
        'Preparation, installer launch and cleanup must each have one workflow entry.'
    Assert-True ($prepareStep[0] -ceq $installStep[0] -and $installStep[0] -match '(?m)^        id: install\s*$') `
        'Use one testable Install step for volume setup and installer launch.'
    Assert-InstallerRunGate ([regex]::Match($installStep[0], '(?ms)^        run: \|\r?\n(.*)$').Groups[1].Value)
    Assert-True ($job -match 'name: Collect installer diagnostics\s+if: always\(\)' -and $cleanup -gt $collect -and
        $cleanupStep[0] -match '(?m)^        if:\s*(?:\$\{\{\s*)?always\(\)\s*(?:\}\})?\s*$') `
        'Always clean the temporary volume after copying installer diagnostics.'
    Assert-True ($job -match 'name: Upload installer diagnostics\s+if: always\(\)' -and
        $job -match 'name: Report installer test result\s+if: always\(\)') 'Upload diagnostics and report installer failures even if a step failed.'
    Assert-True ($job -match 'INSTALL_OUTCOME: \$\{\{ steps\.install\.outcome \}\}' -and
        $job.Contains('Installer test stages failed or were skipped')) 'A failed installer must continue to fail the job.'
}

function Invoke-VolumeCase([string]$Mode, [scriptblock]$Helper) {
    $ownedRunDirectory = 'D:\lm-volume-test'
    $runDirectory = if ($Mode -eq 'traversal') { "$ownedRunDirectory\..\different-run" } else { $ownedRunDirectory }
    $existingVolume = [pscustomobject]@{ DriveLetter = 'D:'; DriveType = 3; FileSystem = 'NTFS'; DeviceID = 'work'; FreeSpace = 32GB }
    $systemVolume = [pscustomobject]@{ DriveLetter = 'C:'; DriveType = 3; FileSystem = 'NTFS'; DeviceID = 'system'; FreeSpace = 32GB }
    $imageVolume = [pscustomobject]@{ DriveLetter = 'V:'; DriveType = 3; FileSystem = 'NTFS'; DeviceID = 'image'; FreeSpace = 24GB }
    $imageDisk = [pscustomobject]@{ Number = 7; IsBoot = $false; IsSystem = $false; IsReadOnly = $false;
        IsOffline = $false; PartitionStyle = 'RAW'; NumberOfPartitions = 0; BusType = 'File Backed Virtual'; Size = 24GB }
    $partition = [pscustomobject]@{ DiskNumber = 7; PartitionNumber = 1; DriveLetter = 'V' }
    $state = @{ Files = @{}; ImagePath = ''; Attached = $false; Calls = [Collections.Generic.List[string]]::new();
        MockViolations = [Collections.Generic.List[string]]::new(); VolumeSerialNumber = [uint64]0x0000000100000005;
        FileId = ('00' * 15) + '01';
        Guard = $null; UsedIdentityProvider = $false; Cleaning = $false; ReplacedAfterDetach = $false }
    $neighbor = 'D:\different-run\existing.vhdx'
    $state.Files[$neighbor] = 'unrelated image'
    $replaceBeforeCleanup = $Mode -in @('replaced-detached', 'replaced-mounted', 'replaced-mounted-file-id')
    $replaceAfterDetach = $Mode -in @('replace-after-detach', 'replace-after-detach-volume')
    $replaced = $replaceBeforeCleanup -or $replaceAfterDetach

    if ($Mode -ne 'reuse') { $existingVolume.FileSystem = 'ReFS' }
    if ($Mode -eq 'system-disk') { $imageDisk.IsSystem = $true; $imageDisk.IsBoot = $true }
    if ($Mode -eq 'existing-disk') { $imageDisk.Number = 4 }
    if ($Mode -eq 'non-raw') { $imageDisk.PartitionStyle = 'GPT'; $imageDisk.NumberOfPartitions = 1 }
    if ($Mode -eq 'system-volume') { $imageVolume.DeviceID = $systemVolume.DeviceID }
    if ($Mode -eq 'system-backing') { $existingVolume.DeviceID = $systemVolume.DeviceID }

    function Assert-Mock($Condition, [string]$Message) {
        if (-not $Condition) {
            $state.MockViolations.Add($Message)
            throw "Mock contract violated: $Message"
        }
    }
    function Test-OwnedPath([string]$Path) {
        $Path.StartsWith("$ownedRunDirectory\", [StringComparison]::OrdinalIgnoreCase) -and
            $Path -notmatch '(?:^|[\\/])\.\.(?:[\\/]|$)'
    }

    function Join-Path($Path, $ChildPath) { "$($Path.TrimEnd('\'))\$ChildPath" }
    function Test-Path($Path, $LiteralPath, $PathType) {
        if ($LiteralPath) { $Path = $LiteralPath }
        $Path -eq $ownedRunDirectory -or $state.Files.ContainsKey($Path)
    }
    function Get-Content($Path, $LiteralPath, [switch]$Raw) {
        if ($LiteralPath) { $Path = $LiteralPath }
        Assert-Mock ($state.Files.ContainsKey($Path)) "Attempt to read an untracked fake file: $Path"
        $state.Files[$Path]
    }
    function Set-Content($Path, $LiteralPath, $Value, $Encoding) {
        if ($LiteralPath) { $Path = $LiteralPath }
        Assert-Mock (Test-OwnedPath $Path) "Attempt to write outside the fake run directory: $Path"
        $state.Files[$Path] = $Value
    }
    function New-Item($Path, $ItemType, [switch]$Force) {
        Assert-Mock ($Path -eq $ownedRunDirectory -and $ItemType -eq 'Directory') "Unexpected fake directory: $Path"
    }
    function Remove-Item($LiteralPath, [switch]$Force) {
        if ($LiteralPath -eq $state.ImagePath) {
            $state.Calls.Add('delete-image')
            Assert-Mock (-not $state.Attached) 'The virtual disk must be detached before its file is removed.'
            Assert-Mock (-not $state.UsedIdentityProvider) 'Checking the image identity does not authorize a later delete by path.'
        }
        Assert-Mock ($state.Files.ContainsKey($LiteralPath) -and (Test-OwnedPath $LiteralPath)) `
            "Attempt to remove a file not created by this run: $LiteralPath"
        $state.Files.Remove($LiteralPath)
    }
    function Get-CimInstance($ClassName, $Filter) {
        Assert-Mock ($ClassName -eq 'Win32_Volume') 'Only fake Win32_Volume reads are available.'
        $volumes = @($systemVolume, $existingVolume)
        if ($state.Calls.Contains('format')) { $volumes += $imageVolume }
        if ($Filter) {
            Assert-Mock ($Filter -match "^DriveLetter\s*=\s*'([A-Z]:)'$") "Unexpected volume filter: $Filter"
            $letter = $Matches[1]
            $volumes | Where-Object DriveLetter -eq $letter
        } else { $volumes }
    }
    function diskpart.exe($Option, $ScriptPath) {
        $state.Calls.Add('create')
        Assert-Mock ($Option -eq '/s' -and $state.Files.ContainsKey($ScriptPath)) 'DiskPart must receive the run-owned fake script.'
        $command = ($state.Files[$ScriptPath] -join "`n").Trim()
        Assert-Mock ($command -match '^create vdisk file="([^"\r\n]+\.vhdx)" maximum=([1-9][0-9]*) type=expandable$') `
            'DiskPart may only create the expandable image for this run.'
        $imagePath = $Matches[1]
        Assert-Mock (([long]$Matches[2] * 1MB) -lt $existingVolume.FreeSpace) 'The expandable image must fit on its backing volume.'
        Assert-Mock ((Test-OwnedPath $imagePath) -and -not $state.Files.ContainsKey($imagePath)) 'The VHDX must be a new file inside the run directory.'
        $state.ImagePath = $imagePath
        $state.Files[$imagePath] = 'fake image'
        $global:LASTEXITCODE = 0
    }
    function Mount-DiskImage($ImagePath, $Access, [switch]$NoDriveLetter, [switch]$PassThru) {
        $state.Calls.Add('mount')
        Assert-Mock ($ImagePath -eq $state.ImagePath -and $state.Files.ContainsKey($ImagePath)) 'Only the new fake image may be mounted.'
        $state.Attached = $true
        if ($Mode -eq 'mount-error') { throw 'injected failure after mount took effect' }
        if ($PassThru) { Get-DiskImage -ImagePath $ImagePath }
    }
    function Get-DiskImage($ImagePath) {
        Assert-Mock ($ImagePath -eq $state.ImagePath -and $state.Files.ContainsKey($ImagePath)) 'Only the new fake image may be queried.'
        if ($state.Cleaning -and $state.UsedIdentityProvider) {
            Assert-Mock ($state.Guard.ImagePath -eq $ImagePath) 'Hold the verified image guard while resolving the attachment.'
        }
        [pscustomobject]@{ ImagePath = $ImagePath; Attached = $state.Attached }
    }
    function Get-Disk {
        param([Parameter(ValueFromPipeline)]$DiskImage)
        process {
            if ($null -eq $DiskImage) {
                [pscustomobject]@{ Number = 0; IsSystem = $true }
                [pscustomobject]@{ Number = 4; IsSystem = $false; PartitionStyle = 'RAW' }
                if ($state.Attached) { $imageDisk }
                return
            }
            Assert-Mock ($DiskImage.ImagePath -eq $state.ImagePath -and $state.Attached) 'Disk discovery must use the current mounted image.'
            if ($Mode -eq 'no-disk') { return }
            $imageDisk
            if ($Mode -eq 'ambiguous-disk') { [pscustomobject]@{ Number = 8; PartitionStyle = 'RAW' } }
        }
    }
    function Initialize-Disk($InputObject, $PartitionStyle, [switch]$PassThru) {
        $state.Calls.Add('initialize')
        Assert-Mock ($InputObject -eq $imageDisk -and $PartitionStyle -eq 'GPT') 'Initialization must target the positively identified test disk.'
        $imageDisk.PartitionStyle = 'GPT'
        if ($PassThru) { $imageDisk }
    }
    function New-Partition($InputObject, [switch]$UseMaximumSize, [switch]$AssignDriveLetter) {
        $state.Calls.Add('partition')
        Assert-Mock ($InputObject -eq $imageDisk -and $imageDisk.PartitionStyle -eq 'GPT') 'Partition creation must target the initialized test disk.'
        $partition
    }
    function Format-Volume($Partition, $FileSystem, $NewFileSystemLabel, [switch]$Confirm) {
        $state.Calls.Add('format')
        Assert-Mock ($Partition -eq $partition -and $FileSystem -eq 'NTFS') 'Only the partition created for this test disk may be formatted as NTFS.'
        if ($Mode -eq 'format-error') { throw 'injected format failure' }
        $imageVolume
    }
    function Dismount-DiskImage($ImagePath) {
        $state.Calls.Add('detach')
        Assert-Mock ($ImagePath -eq $state.ImagePath -and $state.Attached) 'Only the test-owned attached image may be detached.'
        if ($state.UsedIdentityProvider) {
            Assert-Mock ($state.Guard.ImagePath -eq $ImagePath -and $state.Guard.FileId -ceq $state.FileId -and
                $state.Guard.VolumeSerialNumber -eq $state.VolumeSerialNumber -and -not $state.Guard.ForDelete) `
                'Keep the verified image guard open through detachment.'
        }
        $state.Attached = $false
    }

    # The two workflow shells share the persisted receipt, not a handle. These
    # doubles model the Windows boundary: open pins the file and its path,
    # a delete guard requires a detached image, and removal uses that handle.
    function Open-WindowsInstallerImageFile($LiteralPath, [switch]$ForDelete) {
        $state.UsedIdentityProvider = $true
        Assert-Mock ($LiteralPath -eq $state.ImagePath -and $state.Files.ContainsKey($LiteralPath) -and $null -eq $state.Guard) `
            'Open a guard on the actual image without reusing a previous handle.'
        Assert-Mock (-not $ForDelete -or ($state.Cleaning -and -not $state.Attached)) 'Only a detached image can acquire a delete guard.'
        if ($Mode -eq 'identity-capture-error' -and -not $state.Cleaning) { throw 'injected image identity capture failure' }
        if ($replaceAfterDetach -and $ForDelete) {
            if ($Mode -eq 'replace-after-detach-volume') {
                $state.VolumeSerialNumber = [uint64]0x0000000200000005
            } else {
                $state.FileId = ('00' * 15) + '02'
            }
            $state.Files[$LiteralPath] = 'replacement image'
            $state.ReplacedAfterDetach = $true
        }
        $state.Guard = @{ ImagePath = $LiteralPath; VolumeSerialNumber = $state.VolumeSerialNumber;
            FileId = $state.FileId; ForDelete = $ForDelete.IsPresent }
        $state.Guard
    }
    function Close-WindowsInstallerImageFile($Guard) {
        Assert-Mock ($null -ne $state.Guard -and [object]::ReferenceEquals($Guard, $state.Guard)) 'Close the actual active image guard.'
        $state.Guard = $null
    }
    function Remove-WindowsInstallerImageFile($Guard) {
        $state.Calls.Add('delete-image')
        Assert-Mock ($null -ne $state.Guard -and [object]::ReferenceEquals($Guard, $state.Guard) -and $Guard.ForDelete -and
            $Guard.FileId -ceq $state.FileId -and $Guard.VolumeSerialNumber -eq $state.VolumeSerialNumber -and
            -not $state.Attached) 'Remove only through the live guard on the verified detached image.'
        $state.Files.Remove($Guard.ImagePath)
    }

    . $Helper
    $originalRunner = $env:RUNNER_ENVIRONMENT
    $originalSystemDrive = $env:SystemDrive
    $originalExit = Get-Variable LASTEXITCODE -Scope Global -ErrorAction SilentlyContinue
    $originalExitValue = $originalExit.Value
    try {
        $env:RUNNER_ENVIRONMENT = if ($Mode -like 'self-hosted*') { 'self-hosted' } else { 'github-hosted' }
        $env:SystemDrive = 'C:'
        if ($Mode -in @('reuse', 'refs') -or $replaced) {
            $result = Initialize-WindowsInstallerSmokeVolume -RunDirectory $runDirectory
            $expectedDrive = if ($Mode -eq 'reuse') { 'D:' } else { 'V:' }
            Assert-True ($result.StorageRoot -like "$expectedDrive\*" -and $result.StorageRoot -ne "$expectedDrive\" -and
                $result.StorageRoot -notmatch '(?:^|[\\/])\.\.(?:[\\/]|$)') `
                "The selected install root must be a child of the verified $expectedDrive volume."
            Assert-True ($result.ImagePath -eq $state.ImagePath -or ($Mode -eq 'reuse' -and -not $result.ImagePath)) `
                'The result must identify only the image created by this run.'
        } else {
            $expectedError = switch ($Mode) {
                'self-hosted' { 'github.hosted|hosted.runner|disposable' }
                'system-backing' { 'system|separate|independent|backing' }
                'traversal' { 'run.directory|unsafe|canonical|traversal|invalid.*path' }
                'non-raw' { 'raw|blank|partition' }
                'system-volume' { 'system|separate|independent' }
                'mount-error' { 'injected failure after mount took effect' }
                'format-error' { 'injected format failure' }
                'identity-capture-error' { 'injected image identity capture failure' }
                default { 'disk|vhdx|image' }
            }
            Assert-Throws { Initialize-WindowsInstallerSmokeVolume -RunDirectory $runDirectory } `
                "Unsafe or failed setup unexpectedly succeeded: $Mode" $expectedError
        }
        if ($replaceBeforeCleanup) {
            # Replace the image at the very same path after the real setup
            # returns. Change one part of FILE_ID_INFO at a time.
            $state.Files[$state.ImagePath] = 'replacement image'
            if ($Mode -eq 'replaced-detached') { $state.Attached = $false }
            if ($Mode -eq 'replaced-mounted') {
                $state.VolumeSerialNumber = [uint64]0x0000000200000005
            } else {
                $state.FileId = ('00' * 15) + '02'
            }
        }
        Assert-True ($null -eq $state.Guard) 'The preparation shell must release its image guard.'
        $state.Cleaning = $true
        if ($Mode -eq 'traversal') {
            try { Clear-WindowsInstallerSmokeVolume -RunDirectory $runDirectory } catch {
                Assert-True ($_.Exception.Message -match $expectedError) 'Cleanup rejected the contaminated path for an unexpected reason.'
            }
        } elseif ($replaced -or $Mode -eq 'identity-capture-error') {
            $cleanupError = $null
            try { Clear-WindowsInstallerSmokeVolume -RunDirectory $runDirectory } catch { $cleanupError = $_ }
            $destructiveCalls = @($state.Calls | Where-Object { $_ -in @('detach', 'delete-image') })
            $expectedCleanup = if ($replaceAfterDetach) { 'detach' } else { '' }
            Assert-True (($destructiveCalls -join ',') -ceq $expectedCleanup) `
                "Cleanup acted on an unverified image for ${Mode}: $($destructiveCalls -join ',')."
            Assert-True ($null -ne $cleanupError -and $cleanupError.Exception.Message -match 'identity|owner|serial|file.id|verify') `
                "Cleanup did not report the unverified image for ${Mode}: $cleanupError"
            if ($replaceAfterDetach) {
                Assert-True $state.ReplacedAfterDetach 'The replacement must occur between detachment and the delete guard.'
            }
            $protectedImage = if ($replaced) { 'replacement image' } else { 'fake image' }
            Assert-True ($state.Files[$state.ImagePath] -ceq $protectedImage) 'Cleanup changed the unverified image.'
        } else {
            Clear-WindowsInstallerSmokeVolume -RunDirectory $runDirectory
        }

        $expected = switch ($Mode) {
            { $_ -in @('reuse', 'self-hosted', 'system-backing', 'traversal') } { '' }
            { $_ -in @('refs', 'format-error', 'system-volume') } { 'create,mount,initialize,partition,format,detach,delete-image' }
            { $replaceBeforeCleanup } { 'create,mount,initialize,partition,format' }
            { $replaceAfterDetach } { 'create,mount,initialize,partition,format,detach' }
            'identity-capture-error' { 'create' }
            default { 'create,mount,detach,delete-image' }
        }
        $actual = $state.Calls -join ','
        Assert-True ($state.MockViolations.Count -eq 0) "Mock contract failures for ${Mode}: $($state.MockViolations -join '; ')"
        Assert-True ($actual -ceq $expected) "Unexpected disk operations for ${Mode}: [$actual]; wanted [$expected]."
        Assert-True ($state.Attached -eq ($Mode -in @('replaced-mounted', 'replaced-mounted-file-id'))) "Unexpected attachment state for $Mode."
        Assert-True ($null -eq $state.Guard) 'Cleanup must release its image guard on success and refusal.'
        Assert-True ($state.Files[$neighbor] -ceq 'unrelated image') 'Cleanup changed an unrelated image.'
        Write-Host "PASS volume $Mode"
    } finally {
        $env:RUNNER_ENVIRONMENT = $originalRunner
        $env:SystemDrive = $originalSystemDrive
        if ($null -eq $originalExit) { Remove-Variable LASTEXITCODE -Scope Global -ErrorAction SilentlyContinue }
        else { $global:LASTEXITCODE = $originalExitValue }
    }
}

$repoRoot = Join-Path $PSScriptRoot '../..'
$workflowPath = Join-Path $repoRoot '.github/workflows/windows-installer.yml'
Assert-InstallerGateCounterexample
Assert-True (-not ($WorkflowOnly -and $VolumeCase)) 'Choose either -WorkflowOnly or -VolumeCase.'
if ($WorkflowOnly) {
    Assert-InstallerWorkflow $workflowPath
    return
}

$helperPath = Join-Path $PSScriptRoot 'windows-installer-volume.ps1'
Assert-True (Test-Path -LiteralPath $helperPath -PathType Leaf) 'Test dependency missing: the installer-volume production entry points are not implemented yet. Run -WorkflowOnly to check the current D: volume wiring separately.'
$unmocked = @'
function Initialize-WindowsInstallerSmokeVolume { Storage\Get-Disk }
function Clear-WindowsInstallerSmokeVolume { }
'@
Assert-Throws { Read-InstallerVolumeFunctions $unmocked } 'The test permitted a cmdlet that bypasses the mock.' 'Unsafe or unmocked command'
$helper = Read-InstallerVolumeFunctions (Get-Content -LiteralPath $helperPath -Raw)
$modes = @('reuse', 'refs', 'self-hosted', 'system-backing', 'traversal', 'no-disk', 'ambiguous-disk',
    'system-disk', 'existing-disk', 'non-raw', 'system-volume', 'mount-error', 'format-error',
    'replaced-detached', 'replaced-mounted', 'replaced-mounted-file-id',
    'replace-after-detach', 'replace-after-detach-volume', 'identity-capture-error')
if ($VolumeCase) {
    Assert-True ($VolumeCase -in $modes) "Unknown volume case: $VolumeCase"
    Invoke-VolumeCase $VolumeCase $helper
    return
}
foreach ($mode in $modes) {
    Invoke-VolumeCase $mode $helper
}
Assert-InstallerWorkflow $workflowPath
