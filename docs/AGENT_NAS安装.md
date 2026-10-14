# NAS Agent 安装手册

此流程面向用户明确授权的新 NAS 部署。NAS 型号、CPU 架构、Docker 套件和挂载盘差异较大，Agent 必须先只读判断现场能力；不能假设 ARM64 已支持，也不要为“试一下”在 NAS 上安装套件、开放 SSH 或改网络配置。

## 1. 现场检查

只读收集 NAS 型号/系统版本、`uname -m`、可用内存、系统卷和数据卷剩余空间、Docker/Compose 版本、现有容器/Compose 项目/卷、端口占用、反向代理与备份目标。检查 Docker context 指向本机引擎，不使用远程 Docker context。

需要确认：

- CPU 架构与镜像仓库是否提供 Go、Node 和 MySQL 8.4 的构建基础镜像。
- NAS Docker 是否允许构建 BuildKit 镜像、访问所需依赖源、保留容器命名卷。
- 计划目录、日志目录、数据库和备份的实际磁盘位置及配额。
- 现有网站、共享服务、反向代理和端口是否冲突。

无法确认兼容性、磁盘空间不足、备份盘与数据库同盘风险无法接受，或发现旧项目目录/卷时停止并报告。不要删除现有 NAS 目录、卷或应用以腾出空间。

## 2. 独立安装位置

在用户指定的独立共享目录下建立专用版本目录。先核对 ZIP 外部 SHA-256，再解压并校验：

```sh
sha256sum -c PACKAGE-MANIFEST.sha256
```

确认包模式和源码指纹。为本次实例选择从未使用过且之后保持不变的 `COMPOSE_PROJECT_NAME`，使数据库、应用数据和备份卷独立命名；`.env.example` 没有项目名默认值，未设置时 Compose 会失败退出。配置文件和密码仅部署账户可读；不要放入公开共享目录、同步网盘或 Git 工作树。

## 3. 配置和首次初始化

从 `.env.example` 创建新 `.env`，禁止覆盖：

```sh
cp -n .env.example .env
chmod 600 .env
```

使用唯一 Compose 项目名、随机数据库密码、HTTPS 站点地址、`COOKIE_SECURE=true`、回环绑定 `HTTP_BIND=127.0.0.1` 和未占用端口。若 NAS 反向代理不能转发到回环地址，先核实其网络边界与可访问范围，再调整；不能直接把数据库端口映射到局域网或公网。

检查配置并从源码构建应用：

```sh
docker compose config --quiet
docker compose build --pull api web
```

若构建因 CPU 架构、内存、BuildKit 或依赖源失败，保存错误并停止。不得通过下载来源不明的预编译镜像绕过失败。本包不含 Docker 镜像。

只在确认是新库、新命名卷后执行：

```sh
docker compose up -d --wait mysql
docker compose run --rm --no-deps api init-db
```

V1 仅在首次初始化时显式导入随包资料：

```sh
docker compose run --rm --no-deps \
  --volume "$PWD/catalog:/catalog:ro" \
  api import-catalog-seed /catalog/catalog-seed-v1.json
```

V2 没有初始化资料文件，不执行导入命令。随后按 [云服务器 Agent 安装](AGENT_云服务器安装.md) 中的密码文件步骤创建首位管理员，再首次启动：

```sh
docker compose up -d --wait
docker compose ps
```

只有在浏览器核对登录、工艺目录、价格、工资、打印和访问权限后，才确认本机验收通过。测试范围按实际包版本填写。

## 4. 持久化和 NAS 特殊事项

使用 Compose 创建的 `mysql-data`、`app-data`、`backups` 命名卷。备份文件还应复制到 NAS 的独立备份盘或异机位置，并校验 SHA-256。NAS 快照可作为额外保护，不能替代应用一致性备份。检查容器重启、NAS 更新重启、磁盘空间告警和备份任务失败通知。

严禁运行 `docker compose down -v`、`docker volume prune` 或清除 Docker 数据目录。NAS 上尚未跑过真实构建、故障重启和异机恢复的项目，必须在记录中标为“未验证”。

若通过 Git 获取源码，克隆 `https://github.com/wnnif/tegu-v3.git` 后固定至发布页指定的 tag/commit。不要把新版本 checkout 到正在运行的源码目录；先保留当前版本和配置，再在独立目录校验与构建。

安装完成后，按 [Agent 日常运维手册](AGENT_日常运维.md) 建立数据库快照、NAS 独立备份盘副本和恢复流程；回滚限制见 [安全与回滚](安全与回滚.md)。
