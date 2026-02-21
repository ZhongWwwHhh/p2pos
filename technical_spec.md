# P2POS 技术明细（可执行规范）

更新日期：2026-02-21

## 1. 适用范围

本规范约束：

- 节点进程（Go）
- Web Admin（浏览器 libp2p 客户端）
- 安装与初始化流程

## 2. 全局规范

### 2.1 时间

- 全部时间使用 UTC。
- 编码格式使用 RFC3339Nano。
- 比较逻辑统一按 UTC 进行。

### 2.2 日志

- 统一格式：`[MODULE] action=<action> key=value ...`
- 关键路径最少字段：
  - `peer_id`
  - `cluster_id`（可得时）
  - `reason`（错误/拒绝时）

### 2.3 协议命名

- 统一：`/p2pos/<domain>/<version>`
- 当前有效协议：
  - `/p2pos/membership/1.0.0`
  - `/p2pos/membership-push/1.0.0`
  - `/p2pos/heartbeat/1.0.0`
  - `/p2pos/status/1.0.0`

## 3. 配置契约（config.json）

关键字段：

- `init_connections[]`
  - `type`: `dns|multiaddr`
  - `address`: string
- `listen[]`: `host:port` 列表（默认 `0.0.0.0:4100`, `[::]:4100`）
- `network_mode`: `auto|public|private`
- `auto_tls`
  - `mode`: `auto|on|off`
  - `port`: int（默认 4101）
  - `cache_dir`: string
  - `user_email`, `forge_auth`: optional
- `cluster_id`: string
- `system_pubkey`: base64（可为空）
- `members`: `[]peer_id`
- `admin_proof`
  - `cluster_id`, `peer_id`, `role`, `valid_from`, `valid_to`, `sig`
- `node_private_key`: base64

规范化规则：

- `network_mode` 非法值回退 `auto`。
- `auto_tls.mode` 非法值回退 `auto`。
- `auto_tls.port` 非法值回退 `4101`。
- `members` 去空白、去重。

## 4. 状态机规范

节点运行态仅允许：

- `unconfigured`
- `degraded`
- `healthy`

状态判定：

1. membership manager 为空 -> `unconfigured`
2. 本机不在成员集合 -> `unconfigured`
3. 成员数 `N=0` -> `unconfigured`
4. 在线成员数 `k`（当前口径：本机+已连接成员）
5. 若 `2*k > N` -> `healthy`，否则 `degraded`

权限约束：

- `unconfigured`：禁止业务协议写路径
- `degraded`：允许读/同步，不允许 admin 写操作
- `healthy`：允许 admin 写操作（发布 membership）

## 5. Membership 规范

### 5.1 Snapshot JSON

```json
{
  "cluster_id": "default",
  "issued_at": "2026-02-21T11:11:01.508Z",
  "issuer_peer_id": "12D3Koo...",
  "members": ["12D3Koo..."],
  "admin_proof": {
    "cluster_id": "default",
    "peer_id": "12D3Koo...",
    "role": "admin",
    "valid_from": "...",
    "valid_to": "...",
    "sig": "..."
  },
  "sig": "..."
}
```

### 5.2 快照签名 canonical

签名串：

`cluster_id|issued_at|issuer_peer_id|members_csv`

其中：

- `members_csv` 必须使用去重后字典序成员列表，用 `,` 连接。
- `issued_at` 使用 UTC RFC3339Nano 文本。

### 5.3 Admin proof canonical

签名串：

`cluster_id|peer_id|role|valid_from|valid_to`

### 5.4 应用规则

收到 snapshot 时，必须全部通过：

- `cluster_id` 匹配本地 cluster。
- `issued_at` 非空。
- `members` 非空。
- `issuer_peer_id` 非空。
- `sig` 非空且可由 `issuer_peer_id` 对应公钥验签通过。
- 若本地配置了 `system_pubkey`：
  - `admin_proof` 必须有效（role/cluster/peer/有效期/签名）。
- `issued_at` 必须严格新于本地当前 snapshot（LWW）。

## 6. Heartbeat 规范

消息结构：

```json
{
  "cluster_id": "default",
  "peer_id": "12D3Koo...",
  "ts": "2026-02-21T11:11:01.508Z",
  "sig": "..."
}
```

canonical：

`cluster_id|peer_id|ts`

校验：

- 字段完整。
- `peer_id` 必须是成员。
- 时间窗：默认 `±5m`。
- 签名有效。

调度：

- 周期 30s。
- 当前实现为成员 peer 点对点发送。

## 7. Status 规范

协议：`/p2pos/status/1.0.0`

请求：

```json
{ "scope": "local|cluster" }
```

响应：

```json
{
  "generated_at": "...",
  "peers": [],
  "error": ""
}
```

规则：

- `unconfigured` 节点返回 `error=node is unconfigured`。
- `cluster` scope 为本地 + 对已连接 peer 的 `local` 聚合。
- 聚合冲突按 `last_seen_at` 最新覆盖。

## 8. Bootstrap 与 DNS 规范

解析流程：

1. 对 `dns` 地址优先查 `_dnsaddr.<domain>` TXT。
2. 无结果时回退 `<domain>` TXT。
3. 支持 `dnsaddr=/...` 与纯 `/...` 两种 TXT 内容。
4. 同一 peer 多地址合并。

Go 节点 resolver：

- 优先 Cloudflare DoH
- 失败回退系统 DNS

Web Admin resolver：

- 当前仅 Cloudflare DoH（`_dnsaddr`）
- 仅保留浏览器可用传输地址（`/ws` `/wss` `/webtransport`）

## 9. AutoTLS 规范

- 采用 `p2p-forge/client`。
- WSS 监听与主节点监听端口分离：
  - 主端口：`listen`（如 4100）
  - WSS 端口：`auto_tls.port`（默认 4101）
- `auto_tls.mode=on`：允许首节点冷启动证书流程。

## 10. Web Admin 规范

### 10.1 输入

- 单输入 bundle：`p2pos-admin://<base64-json>`
- JSON 字段：
  - `v`
  - `cluster_id`
  - `bootstrap`
  - `admin_priv`
  - `admin_proof`

### 10.2 发布

- 浏览器本地计算 snapshot 签名。
- 推送到 `/p2pos/membership-push/1.0.0`。
- 返回 `applied=true` 才视为成功。

### 10.3 安全

- 私钥只在浏览器内存中使用。
- 当前不提供后端持久化会话。

## 11. 调度器规范

- 任务支持 `RunOnStart()`。
- 注册后立即执行一次（若任务声明需要）。
- 再按 interval 周期执行。
- 任务错误记录日志，不应导致进程崩溃。

## 12. 当前已知限制

- Web Admin 尚未实现 heartbeat 协议 handler，节点可能记录 `protocol not supported`。
- `Revoke Snapshot` 仅 UI 占位，尚未实现协议与后端处理。
- SQLite 在高并发写场景可能出现锁竞争，需继续优化写策略。

