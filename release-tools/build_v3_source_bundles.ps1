[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$SourceRoot,

    [Parameter(Mandatory = $true)]
    [string]$VersionOneZipPath,

    [Parameter(Mandatory = $true)]
    [string]$VersionTwoZipPath,

    [string[]]$VersionOneSeedPaths = @(),

    [switch]$PlanOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Get-RelativePath([string]$Base, [string]$Path) {
    $baseUri = [Uri]::new(($Base.TrimEnd('\') + '\'))
    $pathUri = [Uri]::new($Path)
    [Uri]::UnescapeDataString($baseUri.MakeRelativeUri($pathUri).ToString()).Replace('\', '/')
}

function Test-IsUnderPath([string]$Path, [string]$Parent) {
    $prefix = $Parent.TrimEnd('\') + '\'
    return $Path.Equals($Parent, [StringComparison]::OrdinalIgnoreCase) -or
        $Path.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)
}

function Get-Sha256([string]$Path) {
    (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Assert-PackageManifest([string]$Directory) {
    $root = [IO.Path]::GetFullPath($Directory).TrimEnd('\')
    $manifestPath = Join-Path $root 'PACKAGE-MANIFEST.sha256'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw "ZIP 内缺少 PACKAGE-MANIFEST.sha256：$Directory"
    }

    $encoding = [Text.UTF8Encoding]::new($false, $true)
    $manifest = $encoding.GetString([IO.File]::ReadAllBytes($manifestPath))
    if ($manifest.Contains("`r") -or -not $manifest.EndsWith("`n")) {
        throw "PACKAGE-MANIFEST.sha256 必须使用 LF 并以换行结束：$Directory"
    }

    $listed = [Collections.Generic.Dictionary[string, string]]::new([StringComparer]::Ordinal)
    $lines = $manifest.Substring(0, $manifest.Length - 1).Split("`n")
    foreach ($line in $lines) {
        if ($line -cnotmatch '^([0-9a-f]{64})  (.+)$') {
            throw "PACKAGE-MANIFEST.sha256 包含格式错误的行：$line"
        }
        $hash = $Matches[1]
        $relative = $Matches[2]
        $parts = $relative.Split('/')
        if ([IO.Path]::IsPathRooted($relative) -or $relative.Contains('\') -or
            @($parts | Where-Object { $_ -in @('', '.', '..') }).Count -gt 0) {
            throw "PACKAGE-MANIFEST.sha256 包含不安全路径：$relative"
        }
        if ($listed.ContainsKey($relative)) { throw "PACKAGE-MANIFEST.sha256 存在重复路径：$relative" }

        $fullPath = [IO.Path]::GetFullPath((Join-Path $root ($relative.Replace('/', [IO.Path]::DirectorySeparatorChar))))
        if (-not (Test-IsUnderPath $fullPath $root) -or -not (Test-Path -LiteralPath $fullPath -PathType Leaf)) {
            throw "PACKAGE-MANIFEST.sha256 引用的文件缺失或越界：$relative"
        }
        if ((Get-Sha256 $fullPath) -ne $hash) { throw "PACKAGE-MANIFEST.sha256 摘要不匹配：$relative" }
        $listed.Add($relative, $hash)
    }

    $actual = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    foreach ($file in Get-ChildItem -LiteralPath $root -File -Recurse -Force) {
        $relative = Get-RelativePath $root $file.FullName
        if ($relative -ne 'PACKAGE-MANIFEST.sha256') { [void]$actual.Add($relative) }
    }
    foreach ($relative in $actual) {
        if (-not $listed.ContainsKey($relative)) { throw "ZIP 包含未列入清单的文件：$relative" }
    }
    foreach ($relative in $listed.Keys) {
        if (-not $actual.Contains($relative)) { throw "清单列出了 ZIP 中不存在的文件：$relative" }
    }
}

function Test-SuspiciousContent([string]$Path) {
    $bytes = [IO.File]::ReadAllBytes($Path)
    $content = ([Text.Encoding]::UTF8.GetString($bytes) -replace "`0", '')
    $patterns = @(
        '-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----',
        '\bgh[pousr]_[A-Za-z0-9]{30,}\b',
        '\bAKIA[A-Z0-9]{16}\b',
        '\bAIza[0-9A-Za-z_-]{35}\b',
        '\bxox[baprs]-[0-9A-Za-z-]{20,}\b',
        '\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b',
        '(?i)\b(?:mysql|postgres(?:ql)?|mongodb(?:\+srv)?|redis):\/\/[^\s:@]+:[^\s@]+@'
    )
    foreach ($pattern in $patterns) {
        if ($content -match $pattern) { return $true }
    }
    return $false
}

function Get-TreeDigest([object[]]$Entries) {
    $lines = foreach ($entry in ($Entries | Sort-Object RelativePath)) {
        '{0}  {1}' -f $entry.Hash, $entry.RelativePath
    }
    $bytes = [Text.UTF8Encoding]::new($false).GetBytes(($lines -join "`n") + "`n")
    $algorithm = [Security.Cryptography.SHA256]::Create()
    try {
        [Convert]::ToHexString($algorithm.ComputeHash($bytes)).ToLowerInvariant()
    }
    finally {
        $algorithm.Dispose()
    }
}

function Assert-RestrictedArtifactAcl([string]$Path) {
    $acl = Get-Acl -LiteralPath $Path
    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $requiredSids = @('S-1-5-18', 'S-1-5-32-544', $currentSid)
    $allowedSids = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach ($rule in $acl.Access) {
        if ($rule.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow) { continue }
        $sid = $rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value
        if (-not $requiredSids.Contains($sid)) {
            throw "交付目录 ACL 包含非授权访问主体：$Path ($sid)"
        }
        [void]$allowedSids.Add($sid)
    }
    foreach ($sid in $requiredSids) {
        if (-not $allowedSids.Contains($sid)) { throw "交付目录 ACL 缺少必要访问主体：$Path ($sid)" }
    }
}

function Test-ExcludedRelativePath([string]$RelativePath) {
    $parts = $RelativePath.Split('/')
    $excludedSegments = @(
        '.git', '.trellis', '.agents', '.codex', '.preview', '.acceptance',
        'node_modules', 'dist', '.vite', 'coverage', '发布包', 'deploy-v2',
        'runtime-package-template', '.state', 'secrets', 'backups', 'logs'
    )
    foreach ($part in $parts) {
        if ($excludedSegments -contains $part) { return $true }
    }

    $leaf = $parts[-1]
    if ($leaf -match '^\.env($|\.)' -and $leaf -ne '.env.example') { return $true }
    if ($leaf -match '(?i)\.(log|bak|backup|dump|db|sqlite|sqlite3|pem|key|secret|crt|cer|pfx|p12|xlsx|xls|zip|7z|rar|exe|dll|so|dylib|a|o|obj|test|pdb|class|pyc|ndjson|jsonl|tgz|gz|bz2|xz)$') { return $true }
    if ($leaf -match '(?i)\.tar(?:\.gz)?$') { return $true }
    if ($leaf -match '(?i)\.sql$' -and $RelativePath -notmatch '^backend/migrations/[0-9]+_[A-Za-z0-9_]+\.sql$') { return $true }
    if ($RelativePath -eq 'backend/v3-backup-recovery.md') { return $true }
    if ($leaf -in @('Thumbs.db', '.DS_Store')) { return $true }
    return $false
}

function Assert-PlainRegularFile([string]$Path, [string]$Root) {
    $full = [IO.Path]::GetFullPath($Path)
    if (-not (Test-IsUnderPath $full $Root)) {
        throw "路径超出源码根目录：$Path"
    }
    $item = Get-Item -LiteralPath $full -Force
    if ($item.PSIsContainer -or (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0)) {
        throw "只接受普通文件，不接受目录或符号链接：$Path"
    }
    $relative = Get-RelativePath $Root $full
    if (Test-ExcludedRelativePath $relative) {
        throw "输入文件命中了禁止打包规则：$relative"
    }
    return [pscustomobject]@{ FullPath = $full; RelativePath = $relative }
}

function Assert-NoReparsePoint([string]$Path, [string]$Boundary) {
    $full = [IO.Path]::GetFullPath($Path)
    $root = [IO.Path]::GetFullPath($Boundary).TrimEnd('\')
    if (-not (Test-IsUnderPath $full $root)) {
        throw "路径超出允许目录：$Path"
    }

    $current = $root
    if (Test-Path -LiteralPath $current) {
        $item = Get-Item -LiteralPath $current -Force
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "目录不能是符号链接或重解析点：$current"
        }
    }

    $relative = $full.Substring($root.Length).TrimStart('\')
    $parts = $relative.Split('\')
    for ($index = 0; $index -lt $parts.Length; $index++) {
        $current = Join-Path $current $parts[$index]
        if (-not (Test-Path -LiteralPath $current)) { continue }
        $item = Get-Item -LiteralPath $current -Force
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "输出路径不能经过符号链接或重解析点：$current"
        }
        if ($index -lt ($parts.Length - 1) -and -not $item.PSIsContainer) {
            throw "输出目录组件不是目录：$current"
        }
    }
}

$source = (Resolve-Path -LiteralPath $SourceRoot).Path
$sourceItem = Get-Item -LiteralPath $source -Force
if (-not $sourceItem.PSIsContainer) { throw 'SourceRoot 必须是目录。' }
if (($sourceItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    throw 'SourceRoot 不能是符号链接或重解析目录。'
}
$source = [IO.Path]::GetFullPath($source).TrimEnd('\')
$zipOne = [IO.Path]::GetFullPath($VersionOneZipPath)
$zipTwo = [IO.Path]::GetFullPath($VersionTwoZipPath)
foreach ($path in @($zipOne, $zipTwo)) {
    if ([IO.Path]::GetExtension($path) -ne '.zip') { throw "输出路径必须使用 .zip 扩展名：$path" }
}
if ($zipOne.Equals($zipTwo, [StringComparison]::OrdinalIgnoreCase)) { throw '两个版本的 ZIP 输出路径必须不同。' }
$outputDirectoryOne = [IO.Path]::GetFullPath((Split-Path -Parent $zipOne)).TrimEnd('\')
$outputDirectoryTwo = [IO.Path]::GetFullPath((Split-Path -Parent $zipTwo)).TrimEnd('\')
if (-not $outputDirectoryOne.Equals($outputDirectoryTwo, [StringComparison]::OrdinalIgnoreCase)) {
    throw '两个 ZIP 必须输出到同一目录，便于在同一卷内安全暂存和发布。'
}
$releaseRoot = [IO.Path]::GetFullPath((Join-Path $source '发布包'))
if (-not (Test-IsUnderPath $releaseRoot $source)) { throw '发布包目录必须位于源码根目录内。' }
foreach ($path in @($zipOne, $zipTwo)) {
    if (-not (Test-IsUnderPath $path $releaseRoot)) {
        throw "ZIP 必须输出到 Git 忽略的发布包目录：$path"
    }
    Assert-NoReparsePoint $path $source
    Assert-NoReparsePoint ($path + '.sha256') $source
}
if (Test-Path -LiteralPath $outputDirectoryOne -PathType Container) {
    Assert-RestrictedArtifactAcl $outputDirectoryOne
}

$rootFiles = @('README.md', 'VERSION', 'compose.yaml', '.env.example', '.dockerignore')
$requiredRootFiles = @('README.md', 'VERSION', 'compose.yaml', '.env.example')
foreach ($relative in $requiredRootFiles) {
    if (-not (Test-Path -LiteralPath (Join-Path $source $relative) -PathType Leaf)) {
        throw "缺少源码包必需文件：$relative"
    }
}
$sourceDirectories = @('backend', 'frontend', 'source-deploy')
foreach ($relativeDirectory in $sourceDirectories) {
    if (-not (Test-Path -LiteralPath (Join-Path $source $relativeDirectory) -PathType Container)) {
        throw "缺少源码包必需目录：$relativeDirectory"
    }
}
$documentationMap = @(
    [pscustomobject]@{ Source = 'source-deploy/README_先看我.md'; Target = 'README_先看我.md' },
    [pscustomobject]@{ Source = 'source-deploy/AGENT_云服务器安装.md'; Target = 'docs/AGENT_云服务器安装.md' },
    [pscustomobject]@{ Source = 'source-deploy/AGENT_NAS安装.md'; Target = 'docs/AGENT_NAS安装.md' },
    [pscustomobject]@{ Source = 'source-deploy/AGENT_日常运维.md'; Target = 'docs/AGENT_日常运维.md' },
    [pscustomobject]@{ Source = 'source-deploy/初始化与数据说明.md'; Target = 'docs/初始化与数据说明.md' },
    [pscustomobject]@{ Source = 'source-deploy/安全与回滚.md'; Target = 'docs/安全与回滚.md' },
    [pscustomobject]@{ Source = 'source-deploy/验证报告.md'; Target = 'docs/验证报告.md' }
)
foreach ($document in $documentationMap) {
    $documentPath = Join-Path $source ($document.Source.Replace('/', [IO.Path]::DirectorySeparatorChar))
    if (-not (Test-Path -LiteralPath $documentPath -PathType Leaf)) {
        throw "缺少源码包必需的中文说明：$($document.Source)"
    }
    $documentItem = Get-Item -LiteralPath $documentPath -Force
    if (($documentItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "中文说明不能是符号链接：$($document.Source)"
    }
}
foreach ($path in @($zipOne, $zipTwo)) {
    foreach ($relativeDirectory in $sourceDirectories) {
        $allowedDirectory = Join-Path $source $relativeDirectory
        if (Test-IsUnderPath $path $allowedDirectory) {
            throw "ZIP 不能输出到源码白名单目录内部：$path"
        }
    }
}
$allowed = [Collections.Generic.Dictionary[string, string]]::new([StringComparer]::OrdinalIgnoreCase)

foreach ($relative in $rootFiles) {
    $full = Join-Path $source $relative
    if (Test-Path -LiteralPath $full -PathType Leaf) {
        $item = Get-Item -LiteralPath $full -Force
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "白名单文件不能是符号链接：$relative"
        }
        if (-not (Test-ExcludedRelativePath $relative)) { $allowed[$relative] = $full }
    }
}

foreach ($relativeDirectory in $sourceDirectories) {
    $directory = Join-Path $source $relativeDirectory
    if (-not (Test-Path -LiteralPath $directory -PathType Container)) { continue }
    $dirItem = Get-Item -LiteralPath $directory -Force
    if (($dirItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "白名单目录不能是符号链接：$relativeDirectory"
    }
    foreach ($item in Get-ChildItem -LiteralPath $directory -File -Recurse -Force) {
        $relative = Get-RelativePath $source $item.FullName
        if (Test-ExcludedRelativePath $relative) { continue }
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "源码白名单中发现符号链接：$relative"
        }
        $allowed[$relative] = $item.FullName
    }
    foreach ($item in Get-ChildItem -LiteralPath $directory -Directory -Recurse -Force) {
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "源码白名单中发现符号链接目录：$(Get-RelativePath $source $item.FullName)"
        }
    }
}

$builderRelative = 'release-tools/build_v3_source_bundles.ps1'
$builderPath = Join-Path $source $builderRelative
if (Test-Path -LiteralPath $builderPath -PathType Leaf) {
    $item = Get-Item -LiteralPath $builderPath -Force
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw '构建脚本不能是符号链接。'
    }
    $allowed[$builderRelative] = $builderPath
}

$seedFiles = [Collections.Generic.Dictionary[string, string]]::new([StringComparer]::OrdinalIgnoreCase)
foreach ($seedPath in $VersionOneSeedPaths) {
    $resolved = Assert-PlainRegularFile (Join-Path $source $seedPath) $source
    if (-not $resolved.RelativePath.Equals('catalog/catalog-seed-v1.json', [StringComparison]::OrdinalIgnoreCase)) {
        throw "V1 只允许加入 catalog/catalog-seed-v1.json：$($resolved.RelativePath)"
    }
    if ([IO.Path]::GetExtension($resolved.RelativePath) -ne '.json') {
        throw "V1 初始化清单必须是 JSON：$($resolved.RelativePath)"
    }
    if ($allowed.ContainsKey($resolved.RelativePath)) {
        throw "初始化资料路径同时属于通用源码白名单：$($resolved.RelativePath)"
    }
    $seedFiles[$resolved.RelativePath] = $resolved.FullPath
}
if ($seedFiles.Count -gt 1) { throw 'V1 只允许附带一个 catalog/catalog-seed-v1.json 初始化清单。' }

$auditFiles = [Collections.Generic.Dictionary[string, string]]::new([StringComparer]::OrdinalIgnoreCase)
foreach ($auditPath in @('catalog/数据导入审计.md', 'catalog/客户报价逐行清单.md')) {
    $resolved = Assert-PlainRegularFile (Join-Path $source $auditPath) $source
    if (-not $resolved.RelativePath.Equals($auditPath, [StringComparison]::OrdinalIgnoreCase)) {
        throw "V1 只允许附带指定的来源审计文档：$($resolved.RelativePath)"
    }
    $auditFiles[$resolved.RelativePath] = $resolved.FullPath
}

if ($allowed.Count -eq 0) { throw '源码白名单为空，停止构建。' }
$baseEntries = foreach ($relative in ($allowed.Keys | Sort-Object)) {
    [pscustomobject]@{ RelativePath = $relative; FullPath = $allowed[$relative]; Hash = Get-Sha256 $allowed[$relative] }
}
$seedEntries = foreach ($relative in ($seedFiles.Keys | Sort-Object)) {
    [pscustomobject]@{ RelativePath = $relative; FullPath = $seedFiles[$relative]; Hash = Get-Sha256 $seedFiles[$relative] }
}
$treeHash = Get-TreeDigest @($baseEntries)

if ($PlanOnly) {
    [pscustomobject]@{
        source_snapshot_sha256 = $treeHash
        common_source_file_count = @($baseEntries).Count
        common_source_files = @($baseEntries | ForEach-Object { $_.RelativePath })
        version1_seed_files = @($seedEntries | ForEach-Object { $_.RelativePath })
        version1_audit_documents = @($auditFiles.Keys | Sort-Object)
        version2_seed_file_count = 0
        version1_output = $zipOne
        version2_output = $zipTwo
    } | ConvertTo-Json -Depth 4
    exit 0
}

$seedFiles.Count -eq 1 -or (throw '正式构建要求提供唯一的 V1 初始化清单 catalog/catalog-seed-v1.json。')
$outputs = @(
    $zipOne,
    $zipTwo,
    ($zipOne + '.sha256'),
    ($zipTwo + '.sha256')
)
if (@($outputs | Select-Object -Unique).Count -ne $outputs.Count) { throw 'ZIP 和校验文件输出路径不能重复。' }
foreach ($path in $outputs) {
    if (Test-Path -LiteralPath $path) { throw "拒绝覆盖已有交付文件：$path" }
}
foreach ($parent in @((Split-Path -Parent $zipOne), (Split-Path -Parent $zipTwo) | Select-Object -Unique)) {
    Assert-NoReparsePoint $parent $source
    if (-not (Test-Path -LiteralPath $parent -PathType Container)) {
        New-Item -ItemType Directory -Path $parent -Force | Out-Null
    }
}

Add-Type -AssemblyName System.IO.Compression.FileSystem
$stageRoot = Join-Path $outputDirectoryOne ('.tegu-v3-source-' + [Guid]::NewGuid().ToString('N'))
$stageCommon = Join-Path $stageRoot 'common-source'
$stageOne = Join-Path $stageRoot 'version1'
$stageTwo = Join-Path $stageRoot 'version2'
$publishedArtifacts = [Collections.Generic.List[string]]::new()

try {
    New-Item -ItemType Directory -Path $stageCommon, $stageOne, $stageTwo | Out-Null
    foreach ($entry in $baseEntries) {
        $destination = Join-Path $stageCommon ($entry.RelativePath.Replace('/', [IO.Path]::DirectorySeparatorChar))
        New-Item -ItemType Directory -Path (Split-Path -Parent $destination) -Force | Out-Null
        Copy-Item -LiteralPath $entry.FullPath -Destination $destination
    }
    $stagedEntries = foreach ($entry in $baseEntries) {
        $stagedPath = Join-Path $stageCommon ($entry.RelativePath.Replace('/', [IO.Path]::DirectorySeparatorChar))
        $stagedHash = Get-Sha256 $stagedPath
        if ($stagedHash -ne $entry.Hash) { throw "工作树在创建源码快照时发生变化：$($entry.RelativePath)" }
        [pscustomobject]@{ RelativePath = $entry.RelativePath; FullPath = $stagedPath; Hash = $stagedHash }
    }
    if ((Get-TreeDigest @($stagedEntries)) -ne $treeHash) {
        throw '暂存源码快照与计划摘要不一致。'
    }
    foreach ($stage in @($stageOne, $stageTwo)) {
        foreach ($entry in $stagedEntries) {
            $destination = Join-Path $stage ($entry.RelativePath.Replace('/', [IO.Path]::DirectorySeparatorChar))
            New-Item -ItemType Directory -Path (Split-Path -Parent $destination) -Force | Out-Null
            Copy-Item -LiteralPath $entry.FullPath -Destination $destination
        }
    }
    foreach ($document in $documentationMap) {
        $sourcePath = Join-Path $source ($document.Source.Replace('/', [IO.Path]::DirectorySeparatorChar))
        $relativeTarget = $document.Target.Replace('/', [IO.Path]::DirectorySeparatorChar)
        foreach ($stage in @($stageOne, $stageTwo)) {
            $destination = Join-Path $stage $relativeTarget
            New-Item -ItemType Directory -Path (Split-Path -Parent $destination) -Force | Out-Null
            Copy-Item -LiteralPath $sourcePath -Destination $destination
            if ($document.Target -eq 'README_先看我.md') {
                $readme = [IO.File]::ReadAllText($destination)
                foreach ($linkedDocument in $documentationMap | Where-Object { $_.Target.StartsWith('docs/') }) {
                    $leaf = [IO.Path]::GetFileName($linkedDocument.Target)
                    $readme = $readme.Replace("]($leaf)", "]($($linkedDocument.Target))")
                }
                [IO.File]::WriteAllText($destination, $readme, [Text.UTF8Encoding]::new($false))
            }
        }
    }
    foreach ($stage in @($stageOne, $stageTwo)) {
        $readmePath = Join-Path $stage 'README.md'
        $readme = [IO.File]::ReadAllText($readmePath)
        $readme = $readme.Replace('(source-deploy/README_先看我.md)', '(README_先看我.md)')
        foreach ($document in $documentationMap | Where-Object { $_.Target.StartsWith('docs/') }) {
            $readme = $readme.Replace("($($document.Source))", "($($document.Target))")
        }
        [IO.File]::WriteAllText($readmePath, $readme, [Text.UTF8Encoding]::new($false))
    }
    $stagedSeedEntries = foreach ($entry in $seedEntries) {
        $destination = Join-Path $stageOne ($entry.RelativePath.Replace('/', [IO.Path]::DirectorySeparatorChar))
        New-Item -ItemType Directory -Path (Split-Path -Parent $destination) -Force | Out-Null
        Copy-Item -LiteralPath $entry.FullPath -Destination $destination
        $stagedHash = Get-Sha256 $destination
        if ($stagedHash -ne $entry.Hash) { throw "初始化资料在暂存时发生变化：$($entry.RelativePath)" }
        [pscustomobject]@{ RelativePath = $entry.RelativePath; FullPath = $destination; Hash = $stagedHash }
    }
    foreach ($relative in $auditFiles.Keys) {
        $target = Join-Path $stageOne ('docs' + [IO.Path]::DirectorySeparatorChar + [IO.Path]::GetFileName($relative))
        Copy-Item -LiteralPath $auditFiles[$relative] -Destination $target
    }

    $version = 'unknown'
    $versionPath = Join-Path $stageOne 'VERSION'
    if (Test-Path -LiteralPath $versionPath -PathType Leaf) { $version = (Get-Content -LiteralPath $versionPath -Raw).Trim() }
    $seedManifest = $null
    $seedSummary = $null
    if (@($seedEntries).Count -eq 1) {
        $seedManifest = Get-Content -LiteralPath $stagedSeedEntries[0].FullPath -Raw | ConvertFrom-Json -Depth 100
        $crafts = @($seedManifest.crafts)
        $companyCrafts = @($seedManifest.company_crafts)
        $companies = @($seedManifest.companies)
        $subitemCount = 0
        foreach ($craft in $crafts) { $subitemCount += @($craft.subitems).Count }
        $priceCounts = [ordered]@{}
        foreach ($class in @('confirmed', 'test_only', 'package', 'included', 'free', 'conditional', 'pending')) {
            $priceCounts[$class] = @($companyCrafts | Where-Object { $_.price.classification -eq $class }).Count
        }
        $wageValues = [Collections.Generic.List[object]]::new()
        foreach ($craft in $crafts) {
            $wageProperty = $craft.PSObject.Properties['wage']
            if ($null -ne $wageProperty -and $null -ne $wageProperty.Value) { [void]$wageValues.Add($wageProperty.Value) }
            $subitemsProperty = $craft.PSObject.Properties['subitems']
            if ($null -ne $subitemsProperty) {
                foreach ($subitem in @($subitemsProperty.Value)) {
                    if ($null -eq $subitem) { continue }
                    $wageProperty = $subitem.PSObject.Properties['wage']
                    if ($null -ne $wageProperty -and $null -ne $wageProperty.Value) { [void]$wageValues.Add($wageProperty.Value) }
                }
            }
        }
        foreach ($companyCraft in $companyCrafts) {
            $wageProperty = $companyCraft.PSObject.Properties['wage_override']
            if ($null -ne $wageProperty -and $null -ne $wageProperty.Value) { [void]$wageValues.Add($wageProperty.Value) }
        }
        foreach ($override in @($seedManifest.company_wage_overrides)) {
            $wageProperty = $override.PSObject.Properties['wage']
            if ($null -ne $wageProperty -and $null -ne $wageProperty.Value) { [void]$wageValues.Add($wageProperty.Value) }
        }
        $wageCounts = [ordered]@{}
        foreach ($class in @('confirmed', 'test_only', 'pending')) {
            $wageCounts[$class] = @($wageValues.ToArray() | Where-Object { $_.classification -eq $class }).Count
        }
        $productCount = 0
        if ($null -ne $seedManifest.products) { $productCount = @($seedManifest.products).Count }
        $seedSummary = [ordered]@{
            seed_id = $seedManifest.seed_id
            seed_sha256 = $stagedSeedEntries[0].Hash
            company_count = $companies.Count
            public_craft_count = $crafts.Count
            subitem_count = $subitemCount
            company_craft_count = $companyCrafts.Count
            customer_price_classifications = $priceCounts
            wage_value_classifications = $wageCounts
            automatic_bonus_rule_count = @($seedManifest.bonus_rules).Count
            package_definition_count = @($seedManifest.packages).Count
            package_coverage_count = @($seedManifest.package_coverage).Count
            product_count = $productCount
        }
    }
    foreach ($spec in @(
        @{ Stage = $stageOne; Mode = 'with_initial_data'; SeedEntries = @($seedEntries); Summary = $seedSummary },
        @{ Stage = $stageTwo; Mode = 'clean_no_business_data'; SeedEntries = @(); Summary = $null }
    )) {
        foreach ($file in Get-ChildItem -LiteralPath $spec.Stage -File -Recurse -Force) {
            $relative = Get-RelativePath $spec.Stage $file.FullName
            if (Test-ExcludedRelativePath $relative) { throw "暂存包包含禁止文件：$relative" }
            if (Test-SuspiciousContent $file.FullName) { throw "暂存包包含疑似密钥内容：$relative" }
        }
        $buildInfo = [ordered]@{
            package_version = $version
            package_mode = $spec.Mode
            built_utc = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
            source_snapshot_sha256 = $treeHash
            common_source_file_count = @($baseEntries).Count
            included_initial_data = @($spec.SeedEntries | ForEach-Object { $_.RelativePath })
            included_audit_documents = if ($spec.Mode -eq 'with_initial_data') { @($auditFiles.Keys | ForEach-Object { 'docs/' + [IO.Path]::GetFileName($_) } | Sort-Object) } else { @() }
            initial_data_file_count = @($spec.SeedEntries).Count
            initial_data_summary = $spec.Summary
        }
        $infoPath = Join-Path $spec.Stage 'BUILD-INFO.json'
        $json = $buildInfo | ConvertTo-Json -Depth 4
        [IO.File]::WriteAllText($infoPath, $json + "`n", [Text.UTF8Encoding]::new($false))

        $manifestPath = Join-Path $spec.Stage 'PACKAGE-MANIFEST.sha256'
        $manifestLines = foreach ($file in (Get-ChildItem -LiteralPath $spec.Stage -File -Recurse -Force | Sort-Object FullName)) {
            $relative = Get-RelativePath $spec.Stage $file.FullName
            if ($relative -ne 'PACKAGE-MANIFEST.sha256') {
                '{0}  {1}' -f (Get-Sha256 $file.FullName), $relative
            }
        }
        [IO.File]::WriteAllText($manifestPath, (($manifestLines -join "`n") + "`n"), [Text.UTF8Encoding]::new($false))
        Assert-PackageManifest $spec.Stage
    }

    $zipOneTemp = Join-Path $stageRoot 'version1.zip'
    $zipTwoTemp = Join-Path $stageRoot 'version2.zip'
    [IO.Compression.ZipFile]::CreateFromDirectory($stageOne, $zipOneTemp, [IO.Compression.CompressionLevel]::Optimal, $false)
    [IO.Compression.ZipFile]::CreateFromDirectory($stageTwo, $zipTwoTemp, [IO.Compression.CompressionLevel]::Optimal, $false)
    $extractOne = Join-Path $stageRoot 'verify-version1'
    $extractTwo = Join-Path $stageRoot 'verify-version2'
    [IO.Compression.ZipFile]::ExtractToDirectory($zipOneTemp, $extractOne)
    [IO.Compression.ZipFile]::ExtractToDirectory($zipTwoTemp, $extractTwo)
    Assert-PackageManifest $extractOne
    Assert-PackageManifest $extractTwo
    $hashOne = Get-Sha256 $zipOneTemp
    $hashTwo = Get-Sha256 $zipTwoTemp
    $zipOneSidecar = Join-Path $stageRoot 'version1.zip.sha256'
    $zipTwoSidecar = Join-Path $stageRoot 'version2.zip.sha256'
    [IO.File]::WriteAllText($zipOneSidecar, "$hashOne  $(Split-Path -Leaf $zipOne)`n", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText($zipTwoSidecar, "$hashTwo  $(Split-Path -Leaf $zipTwo)`n", [Text.UTF8Encoding]::new($false))

    $stagedOutputs = @($zipOneTemp, $zipTwoTemp, $zipOneSidecar, $zipTwoSidecar)
    for ($index = 0; $index -lt $outputs.Count; $index++) {
        Assert-NoReparsePoint $outputs[$index] $source
        if (Test-Path -LiteralPath $outputs[$index]) {
            throw "拒绝覆盖已有交付文件：$($outputs[$index])"
        }
        [IO.File]::Move($stagedOutputs[$index], $outputs[$index])
        $publishedArtifacts.Add($outputs[$index])
        Assert-RestrictedArtifactAcl $outputs[$index]
    }

    [pscustomobject]@{
        version1_zip = $outputs[0]
        version1_bytes = (Get-Item -LiteralPath $outputs[0]).Length
        version1_sha256 = $hashOne
        version2_zip = $outputs[1]
        version2_bytes = (Get-Item -LiteralPath $outputs[1]).Length
        version2_sha256 = $hashTwo
        source_snapshot_sha256 = $treeHash
    } | ConvertTo-Json -Depth 3
}
catch {
    foreach ($path in $publishedArtifacts) {
        if (Test-Path -LiteralPath $path -PathType Leaf) {
            Remove-Item -LiteralPath $path -Force
        }
    }
    throw
}
finally {
    $stageFull = [IO.Path]::GetFullPath($stageRoot)
    if (-not (Test-IsUnderPath $stageFull $outputDirectoryOne) -or $stageFull.Equals($outputDirectoryOne, [StringComparison]::OrdinalIgnoreCase)) {
        throw '暂存目录安全检查失败；未删除暂存目录。'
    }
    if (Test-Path -LiteralPath $stageFull) {
        $stageItem = Get-Item -LiteralPath $stageFull -Force
        if (($stageItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw '暂存目录为重解析点；未删除。'
        }
        Remove-Item -LiteralPath $stageFull -Recurse -Force
    }
}
