function Initialize-WindowsInstallerSmokeVolume([string]$RunDirectory) {
    # DiskPart receives this path as script text. Only allow a dedicated,
    # single directory directly below a drive letter.
    if ($RunDirectory -notmatch '^([a-z]:)\\[a-z0-9_-]+$') {
        throw 'Invalid installer volume run directory.'
    }
    $backingDrive = $Matches[1]
    $systemDrive = $env:SystemDrive
    if ($systemDrive -notmatch '^[a-z]:$') {
        throw 'Cannot identify the Windows system volume.'
    }

    $volumes = @(Get-CimInstance -ClassName Win32_Volume)
    $system = @($volumes | Where-Object DriveLetter -eq $systemDrive)
    if ($system.Count -ne 1 -or -not $system[0].DeviceID) {
        throw 'Cannot identify the Windows system volume.'
    }
    $systemId = $system[0].DeviceID
    $fixedVolumes = @($volumes | Where-Object {
        $_.DriveType -eq 3 -and $_.DriveLetter -match '^[a-z]:$' -and $_.DriveLetter -ne $systemDrive -and
        $_.DeviceID -and $_.DeviceID -ne $systemId
    })
    $ntfs = @($fixedVolumes | Where-Object FileSystem -eq 'NTFS')
    $volume = $ntfs | Where-Object DriveLetter -eq $backingDrive | Select-Object -First 1
    if (-not $volume) { $volume = $ntfs | Select-Object -First 1 }

    $imagePath = ''
    if (-not $volume) {
        if ($env:RUNNER_ENVIRONMENT -ne 'github-hosted') {
            throw 'Creating an installer test disk requires a GitHub-hosted Windows runner.'
        }
        $backing = @($fixedVolumes | Where-Object DriveLetter -eq $backingDrive)
        if ($backing.Count -ne 1 -or $backing[0].FileSystem -notin @('NTFS', 'ReFS')) {
            throw 'The installer test disk requires a separate non-system backing volume.'
        }
        # Leave room for host writes, and do not advertise more capacity in
        # the expandable image than the backing volume can supply.
        $maximumMiB = [long][Math]::Floor(($backing[0].FreeSpace - 1GB) / 1MB)
        if ($maximumMiB -le 0) { throw 'The backing volume lacks space for the installer test disk.' }

        # Without -Force, a previous directory cannot be adopted for this run.
        New-Item -Path $RunDirectory -ItemType Directory -ErrorAction Stop | Out-Null
        $imagePath = Join-Path $RunDirectory 'volume.vhdx'
        $ownerFile = Join-Path $RunDirectory 'volume-owner.txt'
        $identityFile = Join-Path $RunDirectory 'volume-identity.txt'
        $mountFile = Join-Path $RunDirectory 'volume-mount.txt'
        $createScript = Join-Path $RunDirectory 'volume-create.txt'
        Set-Content -LiteralPath $ownerFile -Value $RunDirectory -Encoding ascii
        Set-Content -LiteralPath $createScript -Value ('create vdisk file="{0}" maximum={1} type=expandable' -f $imagePath, $maximumMiB) -Encoding ascii
        $diskpartOutput = diskpart.exe /s $createScript
        $diskpartExit = $LASTEXITCODE
        if ($diskpartOutput) { Write-Host ($diskpartOutput -join "`n") }
        if ($diskpartExit -ne 0 -or -not (Test-Path -LiteralPath $imagePath -PathType Leaf)) {
            throw "Could not create the installer test VHDX (DiskPart exit code $diskpartExit)."
        }

        $existingDiskNumbers = @(Get-Disk | ForEach-Object { $_.Number })
        $guard = Open-WindowsInstallerImageFile -LiteralPath $imagePath
        try {
            Set-Content -LiteralPath $identityFile -Value ('{0:X16}:{1}' -f $guard.VolumeSerialNumber, $guard.FileId) -Encoding ascii
            Set-Content -LiteralPath $mountFile -Value $guard.ImagePath -Encoding ascii
            Mount-DiskImage -ImagePath $guard.ImagePath -Access ReadWrite -NoDriveLetter | Out-Null
            $mounted = @(Get-DiskImage -ImagePath $guard.ImagePath)
            if ($mounted.Count -ne 1 -or $mounted[0].ImagePath -ne $guard.ImagePath -or -not $mounted[0].Attached) {
                throw 'Cannot verify the attached installer test VHDX.'
            }
            $disks = @($mounted[0] | Get-Disk)
        } finally {
            Close-WindowsInstallerImageFile -Guard $guard
        }
        if ($disks.Count -ne 1) { throw 'The installer test image must resolve to exactly one disk.' }
        $disk = $disks[0]
        if ($disk.PartitionStyle -ne 'RAW' -or $disk.NumberOfPartitions -ne 0) {
            throw 'The installer test disk is not an empty RAW disk.'
        }
        if ($null -eq $disk.Number -or $disk.Number -in $existingDiskNumbers -or $disk.IsSystem -ne $false -or
            $disk.IsBoot -ne $false -or $disk.IsOffline -ne $false -or $disk.IsReadOnly -ne $false -or
            $disk.BusType -ne 'File Backed Virtual') {
            throw 'The installer test image did not resolve to a new writable non-system virtual disk.'
        }

        $initializedDisk = Initialize-Disk -InputObject $disk -PartitionStyle GPT -PassThru
        if ($null -eq $initializedDisk -or $initializedDisk.Number -ne $disk.Number) {
            throw 'Could not verify the initialized installer test disk.'
        }
        $partition = New-Partition -InputObject $initializedDisk -UseMaximumSize -AssignDriveLetter
        if ($null -eq $partition -or $partition.DiskNumber -ne $disk.Number -or $partition.DriveLetter -notmatch '^[a-z]$') {
            throw 'Could not verify the new installer test partition.'
        }
        Format-Volume -Partition $partition -FileSystem NTFS -NewFileSystemLabel 'LazyMind installer test' -Confirm:$false | Out-Null
        $targetDrive = "$($partition.DriveLetter):"
        $formatted = @(Get-CimInstance -ClassName Win32_Volume -Filter "DriveLetter='$targetDrive'")
        if ($formatted.Count -ne 1 -or $formatted[0].DriveType -ne 3 -or $formatted[0].FileSystem -ne 'NTFS' -or
            $targetDrive -eq $systemDrive -or -not $formatted[0].DeviceID -or $formatted[0].DeviceID -eq $systemId) {
            throw 'The installer test volume must be a separate fixed NTFS volume, distinct from the system volume.'
        }
        $volume = $formatted[0]
    }

    $storageRoot = Join-Path "$($volume.DriveLetter)\" "lm-layout-qa-$env:GITHUB_RUN_ID-$env:GITHUB_RUN_ATTEMPT"
    if (Test-Path -LiteralPath $storageRoot) { throw "Refusing to reuse an existing test root: $storageRoot" }
    return [pscustomobject]@{ StorageRoot = $storageRoot; ImagePath = $imagePath }
}

function Clear-WindowsInstallerSmokeVolume([string]$RunDirectory) {
    if ($RunDirectory -notmatch '^([a-z]:)\\[a-z0-9_-]+$') {
        throw 'Invalid installer volume run directory.'
    }
    $backingDrive = $Matches[1]
    $ownerFile = Join-Path $RunDirectory 'volume-owner.txt'
    if (-not (Test-Path -LiteralPath $ownerFile -PathType Leaf)) { return }
    if ($env:RUNNER_ENVIRONMENT -ne 'github-hosted' -or $backingDrive -eq $env:SystemDrive -or
        (Get-Content -LiteralPath $ownerFile -Raw).Trim() -cne $RunDirectory) {
        throw 'Cannot prove ownership of the installer test VHDX.'
    }
    $systemDrive = $env:SystemDrive
    if ($systemDrive -notmatch '^[a-z]:$') { throw 'Cannot identify the system volume for installer test cleanup.' }
    $system = @(Get-CimInstance -ClassName Win32_Volume -Filter "DriveLetter='$systemDrive'")
    $backing = @(Get-CimInstance -ClassName Win32_Volume -Filter "DriveLetter='$backingDrive'")
    if ($system.Count -ne 1 -or -not $system[0].DeviceID -or $backing.Count -ne 1 -or $backing[0].DriveType -ne 3 -or
        -not $backing[0].DeviceID -or $backing[0].DeviceID -eq $system[0].DeviceID) {
        throw 'Cannot verify an independent backing volume for installer test cleanup.'
    }
    $imagePath = Join-Path $RunDirectory 'volume.vhdx'
    $identityFile = Join-Path $RunDirectory 'volume-identity.txt'
    $mountFile = Join-Path $RunDirectory 'volume-mount.txt'
    $createScript = Join-Path $RunDirectory 'volume-create.txt'
    if (Test-Path -LiteralPath $imagePath) {
        if (-not (Test-Path -LiteralPath $identityFile -PathType Leaf)) {
            throw 'The installer test VHDX has no recorded file identity.'
        }
        $identity = (Get-Content -LiteralPath $identityFile -Raw).Trim()
        $guard = Open-WindowsInstallerImageFile -LiteralPath $imagePath
        try {
            if ($identity -cne ('{0:X16}:{1}' -f $guard.VolumeSerialNumber, $guard.FileId)) {
                throw 'The installer test VHDX identity changed before detachment.'
            }
            if (Test-Path -LiteralPath $mountFile -PathType Leaf) {
                if ((Get-Content -LiteralPath $mountFile -Raw).Trim() -cne $guard.ImagePath) {
                    throw 'The installer test mount receipt does not identify this guarded VHDX path.'
                }
                $image = @(Get-DiskImage -ImagePath $guard.ImagePath)
                if ($image.Count -ne 1 -or $image[0].ImagePath -ne $guard.ImagePath) {
                    throw 'Cannot verify the installer test VHDX for cleanup.'
                }
                if ($image[0].Attached) { Dismount-DiskImage -ImagePath $guard.ImagePath | Out-Null }
            }
        } finally {
            Close-WindowsInstallerImageFile -Guard $guard
        }

        # DELETE access is acquired after dismount. Recheck both ID fields on
        # that handle in case the name was replaced between the two opens.
        $guard = Open-WindowsInstallerImageFile -LiteralPath $imagePath -ForDelete
        try {
            if ($identity -cne ('{0:X16}:{1}' -f $guard.VolumeSerialNumber, $guard.FileId)) {
                throw 'The installer test VHDX identity changed before deletion.'
            }
            Remove-WindowsInstallerImageFile -Guard $guard
        } finally {
            Close-WindowsInstallerImageFile -Guard $guard
        }
        if (Test-Path -LiteralPath $imagePath) { throw 'The installer test image path remains after guarded removal.' }
    } elseif (Test-Path -LiteralPath $mountFile -PathType Leaf) {
        throw 'Cannot verify the previously mounted installer test VHDX at its recorded path.'
    }
    foreach ($file in @($mountFile, $identityFile, $createScript, $ownerFile)) {
        if (Test-Path -LiteralPath $file -PathType Leaf) { Remove-Item -LiteralPath $file }
    }
}
