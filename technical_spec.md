# P2POS 技术规范（v1）

## 1. 目标

本规范用于统一实现细节，避免模块命名、日志、协议行为不一致。

## 2. 运行状态规范

- 节点运行态仅允许：`unconfigured`、`degraded`、`healthy`
- 约束：
  - `unconfigured`：仅可执行 bootstrap 建连与 membership 拉取
  - `degraded`：成员已生效，只读能力可用，禁管理写操作
  - `healthy`：全功能

## 3. 时间规范

- 全系统时间统一使用 UTC。
- 时间编码统一使用 RFC3339Nano。
- 依赖本机 NTP 同步。
- 超出签名窗口/时间窗口的消息一律拒绝并记录告警。

## 4. 协议与命名规范

### 4.1 协议 ID

- 格式：`/p2pos/<domain>/<version>`
- 示例：
  - `/p2pos/membership/1.0.0`
  - `/p2pos/peer-exchange/1.0.0`
  - `/p2pos/status/1.0.0`

### 4.2 Topic 命名

- 格式：`cluster/<domain>`
- 示例：
  - `cluster/membership`
  - `cluster/status`

### 4.3 代码命名

- 文件：`snake_case.go`
- 导出标识符：`PascalCase`
- 非导出标识符：`camelCase`
- 事件名：`<Domain><Action>`（例：`PeerConnected`）

## 5. 日志规范

### 5.1 日志前缀

- 固定模块前缀：
  - `[APP]`
  - `[NODE]`
  - `[MEMBERSHIP]`
  - `[PEERSYNC]`
  - `[STATUS]`
  - `[UPDATE]`
  - `[SCHED]`
  - `[DB]`

### 5.2 最小字段

关键路径日志应包含以下键值：

- `cluster_id`
- `peer_id`
- `state`
- `reason`
- `issued_at`（涉及 membership 时）

推荐格式：

`[MODULE] action=<...> cluster_id=<...> peer_id=<...> state=<...> reason=<...>`

## 6. Membership 处理规范

- `issued_at` 必填，且必须是 RFC3339Nano UTC。
- 新快照应用条件：
  - admin 证明有效
  - 签名有效
  - `cluster_id` 匹配
  - `issued_at` 新于本地
- 同 `issued_at` 冲突默认视为错误发布：拒绝并告警。

## 7. 连接策略规范

- `members` 表示授权全集，不要求全连接。
- 网络应采用部分连接 + libp2p 路由/中继。
- 不可达节点不应触发成员自动删除。

## 8. Web Admin 规范

- Web Admin 每次启动手动输入：
  - `admin_node_private_key`
  - `admin_proof`
- 不在浏览器持久化保存密钥与证明。
- 发布配置时必须本地签名后发送。

## 9. 兼容性规范

- 协议升级遵循“新版本新增，不覆写旧版本”原则。
- 旧版本可并行保留一个过渡周期，待全网升级后再移除。
