if (-not ('LazyMind.CI.InstallerImageFile' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Globalization;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;
using Microsoft.Win32.SafeHandles;

namespace LazyMind.CI
{
    public sealed class InstallerImageFile : IDisposable
    {
        private const uint FileReadAttributes = 0x80;
        private const uint DeleteAccess = 0x10000;
        private const uint FileShareReadWrite = 3;
        private const uint OpenExisting = 3;
        private const uint OpenReparsePoint = 0x00200000;
        private const uint BackupSemantics = 0x02000000;
        private const uint DirectoryAttribute = 0x10;
        private const uint ReparsePointAttribute = 0x400;
        private const uint VolumeNameGuid = 1;
        private const int FileDispositionInfoClass = 4;
        private const int FileAttributeTagInfoClass = 9;
        private const int FileIdInfoClass = 18;

        [StructLayout(LayoutKind.Sequential)]
        private struct FileAttributeTagInfo
        {
            public uint Attributes;
            public uint ReparseTag;
        }

        [StructLayout(LayoutKind.Sequential)]
        private struct FileIdInfo
        {
            public ulong VolumeSerialNumber;
            public ulong FileIdFirstHalf;
            public ulong FileIdSecondHalf;
        }

        [StructLayout(LayoutKind.Sequential)]
        private struct FileDispositionInfo
        {
            public byte DeleteFile;
        }

        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, ExactSpelling = true, SetLastError = true)]
        private static extern SafeFileHandle CreateFileW(string name, uint access, uint sharing,
            IntPtr security, uint disposition, uint flags, IntPtr template);

        [DllImport("kernel32.dll", EntryPoint = "GetFileInformationByHandleEx", ExactSpelling = true, SetLastError = true)]
        private static extern bool ReadAttributes(SafeFileHandle file, int infoClass,
            out FileAttributeTagInfo info, uint size);

        [DllImport("kernel32.dll", EntryPoint = "GetFileInformationByHandleEx", ExactSpelling = true, SetLastError = true)]
        private static extern bool ReadIdentity(SafeFileHandle file, int infoClass, out FileIdInfo info, uint size);

        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, ExactSpelling = true, SetLastError = true)]
        private static extern uint GetFinalPathNameByHandleW(SafeFileHandle file, StringBuilder path,
            uint pathSize, uint flags);

        [DllImport("kernel32.dll", ExactSpelling = true, SetLastError = true)]
        private static extern bool SetFileInformationByHandle(SafeFileHandle file, int infoClass,
            ref FileDispositionInfo info, uint size);

        private readonly SafeFileHandle directory;
        private readonly SafeFileHandle file;
        public string ImagePath { get; }
        public ulong VolumeSerialNumber { get; }
        public string FileId { get; }
        public bool ForDelete { get; }

        private InstallerImageFile(SafeFileHandle directory, SafeFileHandle file, string path,
            FileIdInfo identity, bool forDelete)
        {
            this.directory = directory;
            this.file = file;
            ImagePath = path;
            VolumeSerialNumber = identity.VolumeSerialNumber;
            FileId = identity.FileIdFirstHalf.ToString("X16", CultureInfo.InvariantCulture)
                + identity.FileIdSecondHalf.ToString("X16", CultureInfo.InvariantCulture);
            ForDelete = forDelete;
        }

        public static InstallerImageFile Open(string path, bool forDelete)
        {
            string absolutePath = Path.GetFullPath(path);
            string directoryPath = Path.GetDirectoryName(absolutePath);
            string drive = Path.GetPathRoot(absolutePath);
            if (!String.Equals(path, absolutePath, StringComparison.OrdinalIgnoreCase) ||
                drive == null || drive.Length != 3 || drive[1] != ':' || directoryPath == null ||
                !String.Equals(Path.GetDirectoryName(directoryPath), drive, StringComparison.OrdinalIgnoreCase) ||
                !String.Equals(Path.GetFileName(absolutePath), "volume.vhdx", StringComparison.OrdinalIgnoreCase))
            {
                throw new IOException("Invalid installer image path.");
            }

            SafeFileHandle directory = OpenOrdinaryPath(directoryPath, FileReadAttributes, true);
            SafeFileHandle file = null;
            try
            {
                // Keep the immediate parent and leaf open without delete
                // sharing. Use a volume GUID path instead of a drive alias.
                string physicalPath = NativePath(directory).TrimEnd('\\') + "\\volume.vhdx";
                file = OpenOrdinaryPath(physicalPath, FileReadAttributes | (forDelete ? DeleteAccess : 0u), false);
                if (!String.Equals(NativePath(file), physicalPath, StringComparison.OrdinalIgnoreCase))
                {
                    throw new IOException("The installer image resolved outside its guarded path.");
                }
                FileIdInfo identity;
                if (!ReadIdentity(file, FileIdInfoClass, out identity, (uint)Marshal.SizeOf<FileIdInfo>()))
                {
                    throw LastError("Cannot read the installer image file identity");
                }
                return new InstallerImageFile(directory, file, physicalPath, identity, forDelete);
            }
            catch
            {
                if (file != null) { file.Dispose(); }
                directory.Dispose();
                throw;
            }
        }

        private static SafeFileHandle OpenOrdinaryPath(string path, uint access, bool isDirectory)
        {
            SafeFileHandle handle = CreateFileW(path, access, FileShareReadWrite, IntPtr.Zero,
                OpenExisting, OpenReparsePoint | (isDirectory ? BackupSemantics : 0u), IntPtr.Zero);
            if (handle.IsInvalid)
            {
                Win32Exception error = LastError("Cannot guard the installer image path");
                handle.Dispose();
                throw error;
            }
            FileAttributeTagInfo attributes;
            if (!ReadAttributes(handle, FileAttributeTagInfoClass, out attributes, (uint)Marshal.SizeOf<FileAttributeTagInfo>()))
            {
                Win32Exception error = LastError("Cannot verify installer image attributes");
                handle.Dispose();
                throw error;
            }
            if ((attributes.Attributes & ReparsePointAttribute) != 0 ||
                ((attributes.Attributes & DirectoryAttribute) != 0) != isDirectory)
            {
                handle.Dispose();
                throw new IOException("An installer image or its directory is not an ordinary filesystem object.");
            }
            return handle;
        }

        private static string NativePath(SafeFileHandle handle)
        {
            var path = new StringBuilder(32768);
            uint length = GetFinalPathNameByHandleW(handle, path, (uint)path.Capacity, VolumeNameGuid);
            if (length == 0) { throw LastError("Cannot resolve the guarded installer image path"); }
            if (length >= path.Capacity) { throw new IOException("The installer image path is too long."); }
            return path.ToString();
        }

        private static Win32Exception LastError(string message)
        {
            int code = Marshal.GetLastWin32Error();
            return new Win32Exception(code, message + ": " + new Win32Exception(code).Message);
        }

        public void MarkForDeletion()
        {
            if (!ForDelete || file.IsClosed || directory.IsClosed)
            {
                throw new InvalidOperationException("A live installer image deletion guard is required.");
            }
            var disposition = new FileDispositionInfo { DeleteFile = 1 };
            if (!SetFileInformationByHandle(file, FileDispositionInfoClass, ref disposition, (uint)Marshal.SizeOf<FileDispositionInfo>()))
            {
                throw LastError("Cannot remove the verified installer image");
            }
        }

        public void Dispose()
        {
            file.Dispose();
            directory.Dispose();
        }
    }
}
'@
}

function Open-WindowsInstallerImageFile([string]$LiteralPath, [switch]$ForDelete) {
    [LazyMind.CI.InstallerImageFile]::Open($LiteralPath, $ForDelete.IsPresent)
}

function Close-WindowsInstallerImageFile([LazyMind.CI.InstallerImageFile]$Guard) {
    $Guard.Dispose()
}

function Remove-WindowsInstallerImageFile([LazyMind.CI.InstallerImageFile]$Guard) {
    $Guard.MarkForDeletion()
}
