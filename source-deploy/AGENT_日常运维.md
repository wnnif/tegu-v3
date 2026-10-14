# Agent 日常运维手册

本手册覆盖日常状态检查、备份、更新和故障取证。实际执行前确认当前目录、Compose 项目名、目标主机和用户授权；命令只能在对应部署目录执行。

## 状态检查

```sh
docker compose ps
docker compose logs --since 30m api web mysql
```

日志可能包含业务信息。查看前确认访问权限，不将完整日志贴入公开 issue 或聊天。配置检查使用 `docker compose config --quiet`，不要打印带密码的 Compose 展开配置。发现服务不健康时先收集状态、近期日志、磁盘空间和退出码，不要重建卷或清库。

应用健康检查地址为部署主机本地 `http://127.0.0.1:${HTTP_PORT}/api/health`；外部用户入口由 HTTPS 反向代理提供。MySQL 服务不应发布主机端口。

## 备份

使用系统内“备份与维护”页面的“立即备份”生成数据库一致性快照。等待任务完成，并在该页选择“校验 / 恢复”完成快照校验。应用生成的 `.sha256` 文件只含一行十六进制摘要，不是 `sha256sum -c` 清单格式；需要在主机侧复核时这样比较：

```sh
set -eu
snapshot=/secure/backup/path/tegu-go-YYYYMMDDTHHMMSS-唯一编号.ndjson
expected=$(tr -d '[:space:]' < "$snapshot.sha256")
actual=$(sha256sum "$snapshot" | awk '{print $1}')
test "$expected" = "$actual"
```

将示例 `YYYYMMDDTHHMMSS-唯一编号` 替换为页面列出的实际文件名；不要原样执行示例路径。

每次备份还要把 `/backups` 和 `/var/lib/tegu` 持久卷及 Compose `.env` 副本保存到 Compose 项目目录之外的受限位置。`/backups` 同时包含数据库快照、校验文件和 `monthly/` 归档本体；`/var/lib/tegu` 可能包含解密云存储凭据所需的 `backup.key`。先在应用页面完成一次备份并确认没有运行中的维护任务。随后短暂停止 API，使新数据库快照和归档文件在卷打包期间保持静止；这段时间 Web 登录和业务操作不可用。运行下列命令后 API 会重新启动：

```sh
set -eu
umask 077
stamp=$(date -u +%Y%m%dT%H%M%SZ)
dest="/secure/backup/path/tegu-v3-$stamp"
install -d -m 700 "$dest"
cp -- .env "$dest/instance.env"
chmod 600 "$dest/instance.env"
docker compose stop api
trap 'docker compose start api' EXIT
docker compose run --rm --no-deps -T --entrypoint sh api \
  -c 'tar -czf - -C /backups .' > "$dest/backups-$stamp.tar.gz"
docker compose run --rm --no-deps -T --entrypoint sh api \
  -c 'tar -czf - -C /var/lib/tegu .' > "$dest/app-data-$stamp.tar.gz"
docker compose start api
trap - EXIT
chmod 600 "$dest"/*.tar.gz
(cd "$dest" && sha256sum ./instance.env ./*.tar.gz > FILES.sha256)
chmod 600 "$dest/FILES.sha256"
```

`/secure/backup/path` 必须替换为 Compose 主机上的受限目录，最好位于另一块备份盘或异机挂载；不要把归档写回本项目、源码目录或同一 Compose 卷。操作账户须有权读取 `.env` 和访问 Docker；归档由容器内 `tar` 读取命名卷，不会显示密钥内容。保留备份目录为 `0700`、配置/归档/校验清单为 `0600`，仅授予部署管理员和明确需要的备份服务账户访问。将该目录复制到异机后，先进入副本目录，再运行 `sha256sum -c FILES.sha256`。

备份包包含数据库快照和业务归档，也包含配置或可能用于解密云凭据的密钥，应按最高敏感级别加密保存并限制访问。`backup.key` 不一定存在；若系统启用了云存储加密凭据，必须保留对应 app-data 归档，否则在新实例需要重新填写云凭据。月度 CSV/XLSX 导出不能替代数据库一致性快照。用户已授权公开经审核整理的工艺、客户收费、工资和奖金标准；原始 Excel、员工身份、业务流水及本地测试凭据仍不得进入公开包或发布记录。

维护人员至少定期做一次独立空库恢复演练，核验施工、收款、工资、权限、附件和余额，不以备份文件存在或表行数相同作为恢复通过。备份保留期限、异机复制、磁盘满告警由部署方依现场容量明确设置。

## 更新

更新前暂停业务写入，记录当前源码版本与 `BUILD-INFO.json` 指纹，完成并校验备份，同时保留 `backup.key`、月度文件、`.env` 和当前源码目录。通过用户指定的可信发布源取得新版本，校验 SHA-256，并阅读版本变更和迁移说明。

先在独立恢复实例验证迁移和回滚方案，再于目标实例按授权更新。版本目录应保持清洁；不得把包解压覆盖到旧版本或现有网站目录。配置和 Compose 项目名沿用原部署值，数据库卷保持原名。源码容器重建命令：

```sh
docker compose config --quiet
docker compose build --pull api web
```

完成备份、切换到新版本源码并确认配置和卷仍指向原实例后，先显式执行数据库迁移，再启动新代码：

```sh
docker compose run --rm --no-deps api init-db
docker compose up -d --wait
docker compose ps
```

`serve` 本身不会代替 `init-db` 执行迁移。日常更新不运行初始化资料导入器，不再次创建管理员，也不改写已维护的公司价格、工资或业务数据。不得执行 `docker compose down -v`。迁移失败时保持现场和备份，不手工改 `schema_migrations`。

## 故障处理

按顺序核实服务状态、磁盘空间、端口、Compose 配置、近期日志、数据库健康与最近备份。不得输出密码、密钥、完整 `.env` 或连接串。未查清错误来源前不要反复运行导入、恢复或初始化命令。

涉及数据库迁移、恢复、删除文件、改防火墙或切换域名时，先明确目标和影响；无明确授权时只提供诊断结果和建议。系统不可用且当前库结构已升级时，不要直接切回旧程序继续写入，按 [安全与回滚](安全与回滚.md) 处理。
