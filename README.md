# sub2toimage

这个工具会：

1. 从下载账户接口拉取账户 JSON。
2. 递归提取 JSON 中所有包含 `access_token` 的对象。
3. 读取同一对象里的 `provider`，如果没有则使用 `target.default_provider`。
4. 调用目标接口 `POST /api/accounts`，传入：

```json
{
  "tokens": ["..."],
  "provider": "..."
}
```

## 配置

复制示例配置：

```powershell
Copy-Item config.example.json config.json
```

然后填写：

- `source.admin_key`：管理员密钥，用于拉取下载账户 JSON。
- `target.token`：调用目标 `/api/accounts` 的 token。
- 如果源账户 JSON 中没有 `provider` 字段，填写 `target.default_provider`。
- `max_import_count`：每次最多导入多少个账户；`0` 表示不限制。

默认配置中的 URL 已按当前需求拆成：

- `source.base_url`: `https://fishisub2api.personal.asynclab.club`
- `source.accounts_data_path`: `/api/v1/admin/accounts/data`
- `target.base_url`: `https://airouterweb.personal.asynclab.club`
- `target.accounts_path`: `/api/accounts`

鉴权默认使用：

```http
Authorization: Bearer <token>
```

如果实际接口要求不同的 header 或不需要 `Bearer`，改：

- `source.admin_key_header`
- `source.admin_key_scheme`
- `target.token_header`
- `target.token_scheme`

不需要 scheme 时设为空字符串：`""`。

## 本地运行

```powershell
go run . -config config.json
```

先只看会导入哪些账户，不实际 POST：

```json
"dry_run": true
```

## Docker Compose 本地构建运行

只运行一次：

```powershell
docker compose run --rm -e SCHEDULE_INTERVAL_SECONDS=0 sub2toimage
```

后台定时运行：

```powershell
docker compose up -d --build
```

`docker-compose.yml` 会把本地 `./config.json` 只读挂载到容器内 `/config/config.json`。

定时由环境变量控制：

- `SCHEDULE_INTERVAL_SECONDS`: 定时间隔秒数；`0` 或留空表示只运行一次。默认 compose 配置为 `43200`，即每 12 小时运行一次。
- `RUN_ON_START`: 是否容器启动后立刻跑一次，默认 `true`。
- `CONFIG_PATH`: 容器内配置路径，默认 `/config/config.json`。

## 行为说明

- 拉取接口的查询参数由配置控制：`status`、`sort_by`、`sort_order`、`timezone`。
- `max_import_count` 控制单次最多导入数量，避免一次导入过多导致目标接口超时。
- 程序会递归扫描返回 JSON，不强依赖固定的 `data` / `items` 包装结构。
- 相同 `access_token + provider` 会去重。
- 日志中会遮蔽 token，避免完整 token 出现在终端输出里。
- 任意一次 POST 失败会立即停止，避免静默跳过导入错误。
