# Fixed program: request data arrives only over stdin, never as executable source.
$ErrorActionPreference = 'Stop'
[Console]::InputEncoding = [System.Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$request = [Console]::In.ReadToEnd() | ConvertFrom-Json
$connection = $null
$rows = $null
try {
    $service = Get-Service -Name WSearch -ErrorAction SilentlyContinue
    if ($null -eq $service -or $service.Status -ne 'Running') { exit 3 }
    # SQL literals and LIKE patterns have separate escaping requirements.
    $literal = ([string]$request.query).Replace("'", "''")
    $like = $literal.Replace('[', '[[]').Replace('%', '[%]').Replace('_', '[_]')
    $name = "System.FileName LIKE '%$like%'"
    $content = "CONTAINS(System.Search.Contents, '`"$literal`"')"
    $predicate = switch ($request.match) {
        'name' { $name }
        'content' { $content }
        'either' { "($name OR $content)" }
        default { throw 'Invalid match' }
    }
    # SystemIndex can include mail/history; only file URLs belong to this tool.
    $where = "System.ItemUrl LIKE 'file:%' AND ($predicate)"
    if ($request.path) {
        $scopeUri = ([System.Uri]::new([string]$request.path)).AbsoluteUri.TrimEnd('/')
        $where += " AND SCOPE = '" + $scopeUri.Replace("'", "''") + "'"
    }
    switch ($request.kind) {
        'file' { $where += " AND System.ItemType <> 'Directory'" }
        'directory' { $where += " AND System.ItemType = 'Directory'" }
        'any' { }
        default { throw 'Invalid kind' }
    }
    $count = [Math]::Min(101, [Math]::Max(2, [int]$request.limit + 1))
    $sql = "SELECT TOP $count System.ItemUrl, System.ItemPathDisplay FROM SYSTEMINDEX WHERE $where"
    $connection = New-Object -ComObject ADODB.Connection
    $connection.ConnectionTimeout = 5
    $connection.CommandTimeout = 7
    $connection.Open("Provider=Search.CollatorDSO;Extended Properties='Application=Windows';")
    $rows = $connection.Execute($sql)
    while (-not $rows.EOF) {
        $uri = [System.Uri]::new([string]$rows.Fields.Item('System.ItemUrl').Value)
        if ($uri.IsFile) {
            $filePath = $uri.LocalPath
            # Reading metadata does not read file contents or hydrate cloud files.
            try {
                $item = Get-Item -LiteralPath $filePath -Force -ErrorAction Stop
                $hit = @{path=$filePath; title=$item.Name; kind='file'; modified_at=$item.LastWriteTimeUtc.ToString('o')}
                if ($item.PSIsContainer) { $hit.kind = 'directory' }
                else { $hit.size = $item.Length }
                [Console]::Out.WriteLine(($hit | ConvertTo-Json -Compress))
                [Console]::Out.Flush()
            } catch { }
        }
        $rows.MoveNext()
    }
} catch {
    # No query, paths or credentials in errors; caller reports a source failure.
    exit 1
} finally {
    if ($null -ne $rows) { try { $rows.Close() } catch { }; [void][Runtime.InteropServices.Marshal]::ReleaseComObject($rows) }
    if ($null -ne $connection) { try { $connection.Close() } catch { }; [void][Runtime.InteropServices.Marshal]::ReleaseComObject($connection) }
}
