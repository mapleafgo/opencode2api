# 配置说明

默认配置文件是 `config.json`。首次运行可以从示例复制：

```bash
cp config.example.json config.json
```

本项目只保留 SOCKS5 配置。Chat Completions 请求做 API 流级透传，不做任何请求体改写。

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

## 管理面板

打开 `http://127.0.0.1:8000/` 可进入管理面板。面板用于配置和刷新 SOCKS5 代理、刷新 OpenCode 会话。

默认管理密码是 `123456`，生产部署必须修改：

```bash
./opencode2api -password "your-strong-password"
```
