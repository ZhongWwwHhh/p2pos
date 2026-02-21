# P2POS 设计计划（当前基线 + 下一阶段）

更新日期：2026-02-21

## 1. 文档目的

本文件用于统一三件事：

- 当前代码已经实现了什么（真实基线）。
- 接下来要做什么（阶段计划）。
- 明确不再采用的旧思路（避免反复回滚）。

## 2. 当前实现基线（已落地）

### 2.1 架构分层

- `app`：进程编排、生命周期、任务注册。
- `config`：`config.json` 读写、规范化、节点私钥加载/生成。
- `network`：libp2p host、bootstrap、membership、heartbeat、status。
- `scheduler`：周期任务管理。
- `membership`：快照验签、admin proof 验签、成员集合维护。
- `presence` + `database`：对连接/心跳观测入库（SQLite）。
- `update`：版本检查与自更新。
- `web/`：浏览器端 Admin（Vue + libp2p）。

### 2.2 运行状态机（节点）

当前运行态为三态：

- `unconfigured`
- `degraded`
- `healthy`

判定规则（当前代码）：

- 本机不在 membership 成员表 -> `unconfigured`
- 本机在成员表，且在线成员数 `online` 满足 `online * 2 > memberCount` -> `healthy`
- 否则 -> `degraded`

说明：`online` 目前按“本机 + 已连接的成员节点”计算。

### 2.3 网络与传输

节点端默认监听：

- `tcp` + `quic-v1`（`listen` 端口，默认 4100）
- `websocket`（同样可由 websocket transport 接管）

AutoTLS：

- 配置：`auto_tls.mode = auto|on|off`
- 独立端口：`auto_tls.port`（默认 4101）
- 使用官方 `p2p-forge/client` + `libp2p.direct`
- `mode=on` 时允许在冷启动阶段强制尝试证书流程（首节点自举场景）

`network_mode`：

- `public|private|auto`
- `auto` 按本机是否存在公网 IPv4 做预判

### 2.4 Bootstrap 与 DNS

- `init_connections` 支持 `dns` 和 `multiaddr`。
- `dns` 查询优先 `_dnsaddr.<domain>`，再回退 `<domain>`。
- 解析全部 TXT 记录，支持：
  - `dnsaddr=/...`
  - 纯 multiaddr 文本
- 同一 peer 的多地址会合并。

### 2.5 Membership（当前主控面）

协议：

- 拉取：`/p2pos/membership/1.0.0`
- 推送：`/p2pos/membership-push/1.0.0`

快照结构：

- `cluster_id`
- `issued_at`（UTC RFC3339Nano）
- `issuer_peer_id`
- `members`
- `admin_proof`
- `sig`

校验要求（已实现）：

- 快照签名必填且可由 `issuer_peer_id` 公钥验证。
- 若配置了 `system_pubkey`，则必须验证 `admin_proof`（role/cluster/peer/有效期/签名）。
- 新快照必须比当前 `issued_at` 更新（LWW）。

### 2.6 Heartbeat 与 Status

- 心跳协议：`/p2pos/heartbeat/1.0.0`
- 状态查询协议：`/p2pos/status/1.0.0`
- 心跳为成员间点对点发送（30s 任务），非 pubsub。
- 心跳含签名与时间窗校验（默认 5 分钟窗）。

### 2.7 Web Admin（当前能力）

- 单输入导入 `p2pos-admin://<base64-json>` bundle。
- 浏览器 libp2p 连接 bootstrap（仅浏览器可用传输）。
- 生成 membership snapshot。
- 在浏览器本地用 admin 私钥签名后，推送到 `/p2pos/membership-push/1.0.0`。
- DNS 解析使用 Cloudflare DoH（`_dnsaddr` TXT）。

说明：当前 Web Admin 未实现 heartbeat handler，因此节点向其发送 heartbeat 可能报 `protocol not supported`，不影响 membership 发布。

### 2.8 安装脚本（当前行为）

`install.sh` 支持两类初始化：

- 新系统：生成 system/admin/node 材料，并打印
  - `_dnsaddr` TXT 样例（tcp + tls/ws）
  - Web Admin bundle
- 加入已有系统：输入 `system_pubkey`，仅生成节点材料

systemd 默认 `Restart=always`。

## 3. 已确认的设计决策

- 保留 admin 发布 membership 的治理模型。
- 不做“离线 N 小时自动踢出成员”。
- 成员身份以 membership 快照为准，不以临时连通性为准。
- 时间统一 UTC + RFC3339Nano。
- 浏览器管理端使用 bundle 单输入，不做持久化后端会话。

## 4. 下一阶段计划

### P0（先收敛，避免逻辑分叉）

- 统一“状态主通道”策略：
  - 选定 heartbeat 继续做主通道，或切到 pubsub；
  - 未选定前，不再新增第二套并行语义。
- 过滤 Web Admin heartbeat 噪声：
  - 节点侧忽略非成员/非业务 peer 的 heartbeat 发送。
- 完成日志统一：
  - 核心路径全部改为 `action=... key=value` 样式。

### P1（可观测性闭环）

- 给 Web Admin 增加状态读取能力：
  - 调 `/p2pos/status/1.0.0` 展示 cluster 视图。
- 明确 status 表字段语义（online/offline 口径）。
- 减少 SQLite 写竞争（批量/限频/串行写策略）。

### P2（治理与操作完善）

- `revoke snapshot` 真正实现（当前按钮占位）。
- admin 轮换流程文档化（proof 过期/换届）。
- 安装脚本增加“首节点上线检查清单”（DNS/TLS/端口联通）。

## 5. 明确不做（当前阶段）

- 不引入重型共识库。
- 不实现复杂自动成员剔除策略。
- 不在浏览器端保存长期敏感状态（数据库/服务端会话）。

## 6. 验收基线（当前应满足）

- 节点可启动并进入三态之一。
- Web Admin 可导入 bundle 并连上 bootstrap。
- 发布快照后，节点日志出现 `apply_snapshot_push`。
- 运行态可由 `unconfigured/degraded` 转为 `healthy`（满足多数派时）。

