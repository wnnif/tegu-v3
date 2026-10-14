# 云服务器 Agent 安装手册

本手册用于在用户已明确指定的新云主机上安装独立 V3 实例。安装前只读检查；没有本次目标服务器写入授权时，不继续创建文件、启动容器、执行迁移或导入数据。

## 1. 只读盘点

先记录以下信息，不输出环境变量、口令、完整 Compose 展开配置或私钥：

```sh
uname -a
uname -m
cat /etc/os-release
free -h
df -hT
docker version
docker compose version
docker context show
docker info --format '{{.ID}} {{.OSType}}/{{.Architecture}}'
ss -ltn
docker compose ls
docker ps --format 'table {{.Names}}\t{{.Image}}\t{{.Ports}}'
```

核对系统架构、剩余磁盘、Docker 本地 context、现存 Compose 项目、监听端口、反向代理和备份目标。Docker 不可用、架构不明、目录属于现有网站、端口冲突或目标卷归属不清时停止，先请用户确认隔离安装位置。不要用 `docker system prune`、`docker volume prune` 或 `down -v`。

## 2. 校验与隔离目录

把用户确认的源码 ZIP 和同名 `.sha256` 文件放在新目录。先核对交付时提供的 ZIP SHA-256，再解压到单独的版本目录。进入解压根目录后核对包内逐文件清单：

```sh
sha256sum -c PACKAGE-MANIFEST.sha256
```

确认 `BUILD-INFO.json` 的 `package_mode`。使用新的 Compose 项目名，例如 `tegu-v3-cloud-202610`，不要复用现有项目名或数据卷。版本目录只放源码；`.env` 和密码文件单独设为仅部署账户可读。先确认磁盘、目录属主和备份盘空间，再继续。

## 3. 配置并从源码构建

从模板创建私密配置，不覆盖已存在的配置：

```sh
cp -n .env.example .env
chmod 600 .env
```

通过主机的受控编辑器或密钥管理方式设置此前未使用、在此实例生命周期内保持不变的 `COMPOSE_PROJECT_NAME`、随机 `MYSQL_PASSWORD`、随机 `MYSQL_ROOT_PASSWORD`、站点 `PUBLIC_ORIGIN`、`COOKIE_SECURE=true`、`HTTP_BIND=127.0.0.1` 和未占用的 `HTTP_PORT`。项目名须使用小写字母、数字、下划线或连字符，首字符为字母或数字；模板不提供默认名，未设置会导致 Compose 报错。不要在命令行参数、shell 历史、聊天或日志里放密码。V1 可以含经审核整理且获准公开的工艺、客户收费、工资和奖金标准；原始 Excel 及本地测试凭据不随包交付。

仅检查配置是否有效，不输出展开后的配置：

```sh
docker compose config --quiet
```

确认 Compose 项目名与端口后，从包内源码构建 API 和前端：

```sh
docker compose build --pull api web
```

本包没有预制应用镜像。Docker 会按 Dockerfile 获取构建依赖；MySQL 8.4 镜像由目标环境拉取。构建失败时保留日志和当前目录，不切换到其他版本或现有网站。

## 4. 首次初始化顺序

确认这是本次新建的空业务库和新命名卷。首次初始化严格按以下顺序：

```sh
docker compose up -d --wait mysql
docker compose run --rm --no-deps api init-db
```

`init-db` 执行版本化结构迁移，不创建管理员，也不导入业务资料。确认命令连接的是本项目 `tegu_v3` 库、Compose 项目名正确，且没有把环境指向现有生产数据库。

只有 V1 包执行一次下列显式资料导入。若目标表已有数据、资料摘要不匹配或命令提示冲突，停止操作并核对；不要清表或手动改 JSON：

```sh
docker compose run --rm --no-deps \
  --volume "$PWD/catalog:/catalog:ro" \
  api import-catalog-seed /catalog/catalog-seed-v1.json
```

V2 包没有该初始化文件，不执行资料导入命令。日常启动和更新期间不得再次导入 seed。具体导入范围、真实值、模拟值和待确认项见 [初始化与数据说明](初始化与数据说明.md)。

为初始管理员设置独立密码。密码必须非空、单行且不超过 72 个 UTF-8 字节，不设固定的 8 位门槛。可在受限目录中使用权限为 `0600` 的临时单行文件，再只读挂载给初始化容器：

```sh
install -d -m 700 /etc/tegu-v3/secrets
umask 077
read -r -s -p '输入初始管理员密码：' ADMIN_PASSWORD
printf '\n'
printf '%s' "$ADMIN_PASSWORD" > /etc/tegu-v3/secrets/admin-initial-password
unset ADMIN_PASSWORD
docker compose run --rm --no-deps \
  --user 0:0 \
  --volume /etc/tegu-v3/secrets/admin-initial-password:/run/secrets/admin-initial-password:ro \
  --env ADMIN_INITIAL_PASSWORD_FILE=/run/secrets/admin-initial-password \
  api init-admin
```

此一次性初始化容器以容器内 root 身份运行，仅为读取 root 所有且权限为 `0600` 的只读口令文件；API 服务仍按镜像内的非特权 UID 运行。不要给常驻 API 容器提升权限。

初始化命令不输出密码。确认创建结果后，启动应用：

```sh
docker compose up -d --wait
docker compose ps
```

通过配置的 HTTPS 地址登录并由操作者确认账号和基本页面。确认成功后再删除临时密码文件：

```sh
rm -- /etc/tegu-v3/secrets/admin-initial-password
```

已有可用系统管理员时，初始化不会重置密码。遇到“已有账号但无可用系统管理员”应停止并使用受控账号恢复流程，不要重建库。

也可从公开源码仓库部署，但必须使用发布页给出的明确 tag/commit：

```sh
git clone https://github.com/wnnif/tegu-v3.git tegu-v3
# 先替换为发布页公布的实际 tag 或 commit，再执行 checkout。
release_ref='<填写实际发布 tag 或 commit>'
git -C tegu-v3 checkout --detach "$release_ref"
```

不要把示例占位文本当作有效版本号使用。

不要在已有运行目录直接执行 `git pull`。更新前先备份，将新发布检出到独立版本目录并复核差异，确认配置与卷仍指向原实例后按运维手册更新。

## 5. 首次验收

确认反向代理只转发到本机回环地址、MySQL 未发布公网端口、HTTPS 有效、主机防火墙仅放行业务需要的 80/443 入口。登录后检查管理员权限、V1 目录（仅 V1）和健康状态；不要用生产真实流水填充测试系统。记录版本、`BUILD-INFO.json` 指纹、迁移最大版本、备份位置和验证范围。NAS/ARM64、云端行为没有实际测试证据时仍标为未验证。

安装完成后，按 [Agent 日常运维手册](AGENT_日常运维.md) 建立应用快照、离机备份和独立恢复流程；涉及回滚时同时遵守 [安全与回滚](安全与回滚.md)。
