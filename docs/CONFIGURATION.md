# 配置说明

配置文件按以下顺序解析：

1. 环境变量 `OPENCODE2API_CONFIG`
2. 显式传入的 `-config` / `--config`
3. 当前目录已存在的 `./config.json`
4. 用户配置目录下的 `opencode2api/config.json`

第 4 项的完整位置由 `os.UserConfigDir()` 决定（Linux 常见为 `~/.config/opencode2api/config.json`）。选择用户目录回退且服务模式需要保存配置时，目录会自动创建。首次运行也可以从示例复制：

```bash
cp config.example.json config.json
```

本项目只保留 SOCKS5 配置。Chat Completions 请求做 API 流级透传，不做任何请求体改写。

服务收到 `SIGINT` 或 `SIGTERM` 后停止接收新请求，并最多等待 10 秒让活跃请求退出。

## 字段

### `socks5_proxies`

SOCKS5 代理列表。

```json
{
  "socks5_proxies": [
    {
      "name": "local",
      "addr": "127.0.0.1:1080",
      "username": "",
      "password": ""
    }
  ]
}
```

### `active_socks5`

启用的代理。

- 空字符串：直连
- 某个 `addr`：固定使用该代理
- `__round_robin__`：在多个代理之间轮询

### `socks5_paid_direct`

控制带 key / 付费上游请求是否绕过 SOCKS5。

- 不填或 `false`（默认）：只要启用了 `active_socks5`，public 与带 key 请求都走代理
- `true`：带 key / 付费请求直连；仅 public / 免费层走代理

```json
{
  "active_socks5": "127.0.0.1:1080",
  "socks5_paid_direct": false
}
```

## 管理面板

打开 `http://127.0.0.1:8000/` 可进入管理面板。面板用于配置和刷新 SOCKS5 代理、刷新 OpenCode 会话。

默认管理密码是 `123456`，生产部署必须修改：

```bash
./opencode2api -password "your-strong-password"
```
