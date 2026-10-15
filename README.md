# 宜宾特固施工管理系统 V3.0

本项目提供 Vue 3 + TypeScript 前端、Go 后端、MySQL 8.4 数据库和 Docker Compose 源码构建配置。系统程序、迁移、初始化与中文安装运维说明均在本目录。V3 双版本源码包共用同一套功能与数据库结构；包内初始化资料是否存在决定首次导入模式。

| 源码包 | 首次安装内容 |
|---|---|
| V1 带初始化资料 | 当前 `catalog/catalog-seed-v1.json` 有 13 家公司、95 项公共工艺、115 条已确认公司固定收费、2 种商品及 2 条公司商品售价、11 档套餐和 74 条覆盖；工资未明确处保持待配置。当前 seed 没有 1 元 `test_only` 客户价。无历史施工和其他业务流水。 |
| V2 干净源码 | 完整相同程序和迁移，但不含公司、工艺、价格、工资/奖金、商品、员工或任何业务 seed。管理员由操作者安全初始化。 |

用户已授权公开经审核整理的工艺、客户收费、工人工资标准和奖金规则。当前 V1 seed 保留 21 条旧真实收费并新增 94 条公司收费身份，覆盖 92 个唯一固定来源行及必要变体；旧说明中的“92 条未映射”统计已过时，当前收费以实际 seed 和 [初始化数据说明](docs/初始化与数据说明.md) 为准，来源审计中的旧 seed 描述仅供追溯。原始 Excel、员工身份、客户及员工交易流水、密码、Token、密钥、数据库备份和本地测试凭据不得进入公开快照或源码包。请按 [源码包中文安装与运维手册](README_先看我.md) 操作。

首次初始化的固定顺序为：为此主机选择此前未使用且之后保持不变的唯一 Compose 项目名、创建空库、迁移数据库结构、（仅 V1）显式导入来源清单、显式创建初始管理员、启动 API/Web。`.env.example` 中项目名留空；设置前 Compose 会报错退出，不会回退到固定名称。日常更新不自动导入首次 seed。已导入旧 V1 seed 的非空库只允许按 [增量回填命令](backend/API.md#v30-v1非空数据库客户报价增量回填) 显式处理；完整 seed 导入仍拒绝非空库。

## 构建和部署

本项目不预制 Docker 镜像。目标 Linux 或 NAS 环境需先由现场 Agent 只读盘点系统架构、磁盘、Docker/Compose、端口和现有卷，再从源码构建 API 和网页。云服务器、NAS、ARM64、异机恢复是否通过，以包内 [验证报告](docs/验证报告.md) 的实际证据为准；未实测项目不视为通过。

部署手册：

- [云服务器 Agent 安装](docs/AGENT_云服务器安装.md)
- [NAS Agent 安装](docs/AGENT_NAS安装.md)
- [日常运维](docs/AGENT_日常运维.md)
- [初始化与数据说明](docs/初始化与数据说明.md)
- [安全与回滚](docs/安全与回滚.md)
- [验证报告](docs/验证报告.md)

发布人可以运行 `release-tools/build_v3_source_bundles.ps1`，通过显式文件白名单生成同一源码快照的两个 ZIP。V1 seed 只能以 `catalog/` 下明确列出的文件追加，两个报价审计 Markdown 仅进入 V1，V2 自动排除；脚本拒绝覆盖同名 ZIP，检查输出 ACL，并生成逐文件 SHA-256 清单、构建信息和 ZIP 校验文件。运行前应先使用 `-PlanOnly` 检查完整源码、seed 和审计文件清单。V1 输出目录应限制访问；交付前复核包清单与数据范围。

校验范围要区分：GitHub 源码检出使用仓库根目录的 `SOURCE-MANIFEST.sha256`，ZIP 解压目录使用 ZIP 内的 `PACKAGE-MANIFEST.sha256`；两份清单属于不同文件集合，不能互换。生成源码清单前先暂存所有拟提交源码文件，再运行 `python3 release-tools/source_manifest.py --write`；`--check` 会核对 Git 追踪文件集合及实际检出字节。ZIP 构建先写定 `BUILD-INFO.json`，再以 LF 写 PACKAGE 清单，并在暂存目录和 ZIP 解压目录逐项核对路径及原始字节摘要。

```powershell
pwsh -NoProfile -File .\release-tools\build_v3_source_bundles.ps1 `
  -SourceRoot . `
  -VersionOneZipPath .\发布包\V3双版本交付\宜宾特固V3.0_含初始化资料_云测试源码包.zip `
  -VersionTwoZipPath .\发布包\V3双版本交付\宜宾特固V3.0_纯净无业务数据_源码包.zip `
  -VersionOneSeedPaths 'catalog/catalog-seed-v1.json' `
  -PlanOnly
```

复核预览后移除 `-PlanOnly` 才会生成 ZIP。该工具需要 PowerShell 7 或更高版本。部署端也可以从公开源码仓库取得发布指定的 tag/commit；上线版本应固定到已核验的 tag/commit，更新前先备份并阅读迁移说明，不要直接在旧网站目录执行 `git pull`。
