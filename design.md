# P2POS 设计说明（完整版）

## 1. 文档目标

本文件给出当前阶段的完整设计说明，覆盖：

- 已实现并保留的基础架构。
- 已确认的目标架构与行为规则。
- 已决定替换/删除的旧思路（仅保留结论，不保留旧实现细节）。

原则：尽量简单、减少可出错点、优先可落地。

## 2. 系统目标

- 自动组网并维持节点互联。
- 提供全网在线状态监控。
- 保持去中心化运行，同时允许受控的成员管理。
- 先实现稳定可用，再逐步扩展。

## 2.1 运行状态机（统一）

节点运行时统一为三态：

- `unconfigured`（未配置）
- `healthy`（健康）
- `degraded`（降级）

状态含义：

- `unconfigured`：节点未拿到并通过有效 membership 配置，只允许最小自举协议。
- `healthy`：节点已配置且观测到多数派。
- `degraded`：节点已配置但观测不到多数派（疑似网络分区/孤网）。

## 3. 当前基础架构（保留）

### 3.1 模块分层

- `app`：进程入口与生命周期控制。
- `config`：配置加载、更新、对外读取接口。
- `network`：libp2p 节点、连接、协议处理。
- `scheduler`：定时任务注册与执行。
- `events`：进程内事件总线。
- `presence/status`：状态观测与查询聚合。
- `database`：本地持久化（当前以 `peers` 为主）。
- `update`：版本检查与自更新。

### 3.2 生命周期

- `app.Run` 初始化顺序：
  1. `database.Init`
  2. `config.Init`
  3. `network.NewNode`
  4. runtime services（presence/status）
  5. scheduler 注册任务并启动
- 通过总线发布 `ShutdownRequested`，统一触发优雅退出。

### 3.3 调度器

- 任务注册后立即执行一次。
- 后续为“任务完成后等待一个完整间隔再执行”。
- 避免长任务导致 ticker 积压后的瞬间重跑。

### 3.4 当前网络能力

- 监听：`TCP + QUIC`（同端口）。
- 启用：`NATPortMap`、`AutoRelay`、`HolePunching`。
- `network_mode`：`auto/public/private`
  - `public` 启用 `RelayService/NATService`
  - `private` 不启用上述服务
  - `auto` 通过 IPv4 条件预判

### 3.5 当前同步与状态

- 已有 peer 交换能力（自定义协议）。
- 已有状态记录同步（带时间字段，LWW 合并）。
- 当前实现里仍有历史状态字段（待在目标态中收敛为 `online/offline` 两态）。

## 4. 节点身份与认证（目标）

### 4.1 基本模型

- 系统私钥仅用于离线签发身份证明。
- 每个节点持有自己的 `node_private_key`。
- 普通节点不持有 `node_proof`。
- 仅 admin 节点持有 `admin_proof`（由系统公钥可验签）。

### 4.2 `config.json`（目标字段）

- `node_private_key`（base64）
- `system_pubkey`（base64）
- `admin_proof`（仅 admin 节点需要，至少包含）
  - `cluster_id`
  - `peer_id`
  - `role`（固定 `admin`）
  - `valid_from`
  - `valid_to`
  - `sig`

### 4.3 启动校验

1. 解码 `node_private_key` 并推导本机 `peer_id`
2. 普通节点：仅校验基础配置有效
3. admin 节点：校验 `admin_proof.peer_id == local peer_id`
4. admin 节点：用 `system_pubkey` 验签 `admin_proof`
5. admin 节点：校验时间窗口
6. 失败即拒绝启动

## 5. 角色模型（目标）

- `node`：参与网络与状态发布。
- `admin`：管理/监控权限角色。

关键约束：

- 系统私钥不进入前端与线上普通节点。
- 前端可作为 libp2p 节点，启动时输入并持久化：
  - `admin_node_private_key`
  - `admin_proof`
- 前端在线操作只使用 admin 节点私钥签名，不使用系统私钥。

## 6. 在线状态设计（目标）

### 6.1 状态定义

- 仅两态：`online` / `offline`。
- 底层连接事件作为辅助信号，不作为唯一判定依据。
- 节点自身运行态为三态：`unconfigured/healthy/degraded`。

### 6.2 主通道

- 使用 libp2p pubsub 心跳。
- topic（暂定）：`cluster/status`
- 每节点每 30 秒发布一次心跳。

### 6.3 判定规则

- 收到心跳更新 `last_seen_at`。
- 超过阈值（建议 90 秒）判 `offline`。
- 引入本机健康状态：
  - `healthy`：观测到多数派
  - `degraded`：观测不到多数派（疑似分区/孤网）

多数派规则：

- 成员总数 `N` 来自有效 membership snapshot。
- 本机观测在线数 `k`。
- `k > N/2` 视为多数派。

状态迁移：

1. 启动时无有效 membership -> `unconfigured`
2. 拉取并应用包含本机 `peer_id` 的有效 membership 后，先进入 `degraded`（只读能力可用）
3. 已配置运行中，多数派丢失 -> `degraded`
4. 多数派恢复 -> `healthy`

### 6.4 心跳安全（简化）

心跳字段至少包含：

- `peer_id`
- `ts`（UTC）
- `role`
- `sig`（节点私钥签名）

接收校验：

- 签名有效
- `peer_id` 在当前 membership 成员列表内
- `ts` 在窗口内（如 5 分钟）
- 本机时钟满足 NTP 同步要求，偏差超窗节点视为无效节点

超窗消息丢弃，记录告警。

## 7. Membership 管理（目标）

### 7.1 发布模型

- 由 `admin` 节点发布 membership snapshot。
- 成员变更不由在线状态自动触发。
- 不采用“offline 72h 自动剔除”。

### 7.2 发布主题与结构

- topic（暂定）：`cluster/membership`
- snapshot JSON 字段：
  - `cluster_id`
- `issued_at`（UTC RFC3339Nano）
  - `issuer_peer_id`
  - `members`（peer_id 数组）
  - `sig`

### 7.3 签名规范

- 去掉 `sig` 后做稳定序列化再签名。
- 固定字段顺序：`cluster_id, issued_at, issuer_peer_id, members`
- `members` 字典序排序。

### 7.4 应用规则

接收端应用前必须校验：

1. 发布者 `admin` 证明有效
2. `issuer_peer_id` 与消息签名匹配
3. `cluster_id` 匹配
4. `issued_at` 新于当前已应用 snapshot

### 7.4.1 新节点首次加入通道（补充）

- 未入成员节点允许访问 `cluster/membership` 的只读获取通道（订阅或拉取）。
- 未入成员节点禁止进入业务状态通道（如 `cluster/status`）与成员心跳统计。
- 未入成员节点即 `unconfigured` 态，仅允许：
  - bootstrap 建连
  - membership 拉取/订阅
  - 必要的身份校验
- `unconfigured` 节点持续重试拉取 membership（固定间隔重试，不设终止）。
- 未入成员节点收到 membership snapshot 后：
  1. 验签通过；
  2. 且 snapshot 中包含自身 `peer_id`；
  3. 则切换到 `degraded` 并进入只读运行；
  4. 达到多数派后切换到 `healthy`。

### 7.4.2 Admin 证明过期语义

- `admin_proof` 过期后，admin 节点不得再签发新 snapshot。
- `admin_proof` 过期前已签发并已传播的 snapshot 继续有效。
- 该行为等价于“管理者换届”，不影响历史已生效配置。

### 7.5 冲突处理（最简）

- 默认不考虑“同一 `issued_at` 冲突”场景（视为错误运维）。
- 发布端必须使用 RFC3339Nano 精度时间。
- 若仍出现冲突：实现可拒绝并告警。

## 8. 数据与存储设计（目标）

### 8.1 配置与身份

- 身份与认证配置全部放 `config.json`。
- 删除 SQLite `settings` 表使用路径。

### 8.2 状态存储职责

- 本地数据库仅承载运行时观测快照（`peers`）。
- 非成员在认证阶段直接拒绝；不建立业务连接，不落库，不缓存状态。
- 状态字段收敛为两态：
  - `online`
  - `offline`
- 过期清理仅针对成员节点的历史状态数据（按在线判定窗口执行）。
- 连通拓扑不要求全连接：`members` 是授权全集，不等于必须直连全集。
- 实际连接策略采用部分连接 + libp2p 路由/中继转发。

## 9. 安装与初始化（目标）

`install.sh` 改为交互式初始化，分两种模式：

1. 询问 `system_pubkey`
2. 若填写：
   - 视为“加入已有系统”
   - 生成或导入本机 `node_private_key`
   - 推导并打印本机 `peer_id`
   - 写入 `config.json`
3. 若留空：
   - 视为“创建新系统”
   - 生成 `system_keypair`
   - 生成 Web Admin 的 `admin_node_private_key`
   - 基于 `system_privkey` 签发 `admin_proof`（有效期可交互，默认长期）
   - 生成首节点 `node_private_key` 与配置
4. 安装完成输出：
   - 加入模式：仅输出 `peer_id`
   - 新系统模式：输出 `system keypair`、`admin_node_private_key`、`admin_proof`

### 9.1 前端（Web Admin）初始化

- 前端首次启动输入：
  1. `admin_node_private_key`
  2. `admin_proof`
- 前端不持久化任何密钥或证明；每次启动手动输入。
- 前端消息验权依赖：
  - `admin_proof` 有效（由 `system_pubkey` 验证）
  - 消息签名与 `admin_node_private_key` 对应的 `peer_id` 一致

### 9.2 首节点自举与扩容流程（标准路径）

1. 首节点部署：
   - 首节点作为 bootstrap 启动，首节点初始可处于 `unconfigured`，仅开放最小自举协议。
2. Web Admin 启动：
   - 输入 `admin_node_private_key + admin_proof` 启动前端 libp2p。
   - Web Admin 连接 bootstrap 节点。
3. 首次发布成员列表：
   - 前端按“+”输入节点 `peer_id`（至少包含 admin 与首节点）。
   - 前端签名发布权威 membership snapshot。
4. 节点配置生效：
   - bootstrap 节点拉取并验签 snapshot，通过后切到成员态。
   - 节点运行态从 `unconfigured` 先进入 `degraded`，多数派后进入 `healthy`。
5. 后续扩容：
   - 新节点安装后拿到 `peer_id`。
   - 前端继续“+”加入并发布新 snapshot。
   - 全网验签并收敛。

## 10. 已决定替换/删除的旧方案

- 删除对 SQLite `settings` 表的依赖。
- 放弃以 `ping` 为主的在线判定路径（改为 pubsub 心跳主导）。
- 放弃 `offline 72h` 自动成员剔除策略。
- 不引入共识库作为当前阶段前提（先用 admin 发布 snapshot）。

## 11. 实施里程碑

### Phase A：身份与配置收敛

- 配置结构改造（`node_private_key/system_pubkey`，admin 场景包含 `admin_proof`）
- 启动强校验落地
- 移除 settings 表路径

### Phase B：状态与心跳

- pubsub 心跳协议与判定落地
- online/offline 两态替换现有多态主判定
- `unconfigured/healthy/degraded` 三态判定落地

### Phase C：membership 发布与过滤

- admin snapshot 发布与接收校验
- 成员过滤接入同步链路
- 非成员认证拒绝接入（不进入连接与存储链路），并补齐成员状态清理策略
- 前端 admin 私钥+证明“每次启动手动输入”流程落地

## 12. 验收标准

- 新节点仅靠 `config.json` 可完成启动与认证。
- 普通节点篡改私钥/关键配置会导致启动失败；admin 节点篡改 `admin_proof` 会导致认证失败。
- 在线状态以心跳驱动，且可稳定收敛。
- `N` 由 membership snapshot 决定，不受瞬时观察波动影响。
- 非成员不进入主状态集合。
- 运行态严格收敛到 `unconfigured/healthy/degraded`。
- 无 settings 表依赖代码残留。

## 13. 配套规范文档

- 技术规范见 `technical_spec.md`：
  - 统一日志字段与日志级别
  - 统一命名规范（协议 ID、topic、事件名、函数命名）
  - 时间与时区规范（UTC、RFC3339Nano、NTP 约束）
  - 协议版本与兼容性约束
