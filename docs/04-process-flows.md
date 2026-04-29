# 关键流程设计

## 1. 用户登录流程

```mermaid
sequenceDiagram
    participant U as User
    participant Web as Web/Mobile
    participant OIDC as OIDC Provider
    participant API as API Service
    participant DB as PostgreSQL

    U->>Web: 打开系统
    Web->>OIDC: 发起授权码登录
    OIDC-->>Web: 返回授权码
    Web->>OIDC: 换取Token
    Web->>API: GET /api/v1/me
    API->>API: 校验Access Token
    API->>DB: 按OIDC subject查找或创建用户
    API-->>Web: 返回用户、租户、角色
```

要点：

- 服务端不保存 OIDC 密码。
- 本地用户以 OIDC `issuer + subject` 唯一标识。
- 角色以本地租户成员关系为准，不直接信任前端传入。

## 2. 设备激活流程

```mermaid
sequenceDiagram
    participant Admin as Admin/User
    participant Web as Web Admin
    participant API as API Service
    participant Agent as Agent
    participant DB as PostgreSQL

    Admin->>Web: 创建设备激活码
    Web->>API: POST /device-activation-codes
    API->>DB: 保存激活码哈希、租户、过期时间
    API-->>Web: 返回一次性激活码
    Agent->>API: POST /api/v1/agent/register
    API->>DB: 校验激活码哈希和有效期
    API->>DB: 创建设备并消费激活码
    API-->>Agent: 返回device_id和设备令牌
    Agent->>Agent: 本地安全存储令牌
```

约束：

- 激活码只显示一次。
- 激活码默认 10 分钟过期。
- 同一激活码只能成功消费一次。
- 注册请求需要包含 Agent 平台、版本、主机名摘要和能力集。

## 3. Agent 本地登录绑定流程

```mermaid
sequenceDiagram
    participant User
    participant AgentUI as Agent Local UI
    participant OIDC as OIDC Provider
    participant API as API Service
    participant Agent
    participant DB as PostgreSQL

    User->>AgentUI: 打开本地Agent
    AgentUI->>OIDC: 发起设备授权码或浏览器登录
    OIDC-->>AgentUI: 返回用户Token
    AgentUI->>API: GET /api/v1/me
    API-->>AgentUI: 返回可用租户
    User->>AgentUI: 选择租户并确认绑定本机
    AgentUI->>API: POST /api/v1/agent/register-with-login
    API->>DB: 创建设备、owner和agent_desktop客户端实例
    API-->>AgentUI: 返回device_id和device_token
    AgentUI->>Agent: 写入设备配置
    Agent->>Agent: 安全存储设备令牌
```

要点：

- Agent 本地 UI 的用户登录身份只用于绑定和提交本地审批动作。
- Agent 设备令牌仍是机器身份，用于长连接和回写 ACK。
- 服务端必须能区分“谁绑定了设备”和“哪台设备在回写”。

## 4. Agent 建立长连接流程

```mermaid
sequenceDiagram
    participant Agent
    participant Gateway as Realtime Gateway
    participant Redis
    participant DB as PostgreSQL

    Agent->>Gateway: WSS connect + device auth
    Gateway->>DB: 校验device_id和token_hash
    Gateway->>Redis: 写presence和route
    Gateway-->>Agent: agent.connected + server_time + protocol_version
    loop 每15秒
        Agent->>Gateway: agent.heartbeat
        Gateway->>Redis: 刷新last_seen
    end
```

要点：

- Gateway 支持协议版本协商。
- Agent 心跳携带当前会话数量、队列积压数、最近错误码。
- Gateway 不把 Redis presence 当作最终事实，设备状态仍落 PostgreSQL。

## 5. CLI 会话启动流程

```mermaid
flowchart TD
    A[用户通过Agent启动CLI] --> B[Agent创建本地Session]
    B --> C[选择平台PTY适配器]
    C --> D[启动目标AI CLI]
    D --> E[上报session.created]
    E --> F[读取PTY输出]
    F --> G[归一化输出并分配sequence_no]
    G --> H[保存本地短期缓冲]
    H --> I[按策略上报输出摘要]
    G --> J[Prompt Detector检测审批事件]
```

要点：

- Agent 不默认上传完整终端输出，只上传审批相关上下文和必要摘要。
- 如果用户启用会话回放，输出片段必须先脱敏再入库。
- 会话结束时 Agent 上报退出码、结束原因和最后输出摘要。
- 同一设备允许同时启动多个 CLI 会话，每个会话独立上报状态和审批事件。
- 移动端通过服务端按设备查看这些会话，不直接连接 Agent。

## 6. 审批创建流程

```mermaid
sequenceDiagram
    participant CLI
    participant Agent
    participant Gateway
    participant API
    participant DB as PostgreSQL
    participant Worker
    participant Clients as Web/Mobile/Agent UI

    CLI->>Agent: 输出确认提示
    Agent->>Agent: Detector生成事件和幂等键
    Agent->>Gateway: approval.detected
    Gateway->>API: 创建审批请求
    API->>DB: 按idempotency_key插入或返回已有审批
    API->>DB: 写audit_logs
    API->>Worker: 发布approval.created事件
    Worker->>DB: 计算有权限客户端实例并写approval_notifications
    Worker-->>Clients: WebSocket/Push通知
    API-->>Gateway: approval.accepted
    Gateway-->>Agent: approval.accepted
```

要点：

- `idempotency_key` 在同一租户内唯一。
- 重复上报返回已有 `approval_id`。
- 服务端创建审批后立即进入策略评估。
- 通知发送给所有有权限的客户端实例，包括多台手机、Web 页面和 Agent 本地 UI。

## 7. 策略评估流程

```mermaid
flowchart TD
    A[approval created] --> B[加载租户和用户策略]
    B --> C[按priority排序匹配]
    C --> D{命中策略?}
    D -- 否 --> E[waiting_decision]
    D -- 自动拒绝 --> F[decided: policy_reject]
    D -- 白名单自动批准 --> G{是否允许自动批准?}
    G -- 是 --> H[decided: policy_approve]
    G -- 否 --> E
    E --> I[通知Web/Mobile]
    F --> J[创建delivery]
    H --> J
```

要点：

- 高风险事件默认不能被全局自动批准。
- 策略命中结果写入审批记录和审计日志。
- 策略服务异常时默认进入人工审批。

## 8. 多端人工审批与同步流程

```mermaid
sequenceDiagram
    participant Phone1 as Mobile A
    participant Phone2 as Mobile B
    participant AgentUI as Agent Local UI
    participant API
    participant DB as PostgreSQL
    participant Gateway
    participant Agent

    Gateway-->>Phone1: approval.created
    Gateway-->>Phone2: approval.created
    Gateway-->>AgentUI: approval.created
    Phone1->>API: POST /approvals/{id}/decision + Idempotency-Key
    API->>DB: 乐观锁更新waiting_decision为decided
    API->>DB: 写approval_actions、处理客户端实例和audit_logs
    API->>DB: 创建approval_delivery
    API-->>Gateway: approval.updated
    Gateway-->>Phone2: 已由Mobile A处理
    Gateway-->>AgentUI: 已由Mobile A处理
    API->>Gateway: deliver decision
    Gateway->>Agent: approval.decision.deliver
    Agent->>Agent: 写入PTY
    Agent-->>Gateway: approval.decision.ack
    Gateway->>API: 保存ACK
    API->>DB: 更新delivered或delivery_failed
    API-->>Phone1: 返回最终状态
```

并发规则：

- 第一个成功提交的审批动作生效。
- 后续提交返回 409 或当前最终状态。
- 相同 `Idempotency-Key` 重试返回第一次结果。
- 同步消息必须包含处理方用户、客户端类型、处理动作和处理时间。
- 其他端收到同步消息后从 API 拉取审批详情，展示“已由其他端处理”。

## 9. Agent 本地 UI 审批流程

```mermaid
sequenceDiagram
    participant User
    participant AgentUI as Agent Local UI
    participant API
    participant DB as PostgreSQL
    participant Gateway
    participant Agent
    participant Mobile as Other Clients

    User->>AgentUI: 在本机点击批准/拒绝
    AgentUI->>API: POST /approvals/{id}/decision
    API->>DB: 记录actor=user, client_type=agent_desktop
    API->>Gateway: approval.updated
    Gateway-->>Mobile: 同步本机Agent已处理
    API->>Gateway: deliver decision
    Gateway->>Agent: approval.decision.deliver
    Agent->>Agent: 写回PTY
    Agent-->>Gateway: approval.decision.ack
    Gateway->>API: 保存ACK
```

要点：

- Agent 本地 UI 提交审批动作时使用用户身份。
- Agent 回写 ACK 使用设备身份。
- 审计必须同时保存用户、客户端实例、设备和会话。

## 10. 审批超时流程

```mermaid
flowchart TD
    A[Worker扫描waiting_decision] --> B{expires_at <= now?}
    B -- 否 --> C[跳过]
    B -- 是 --> D[乐观锁更新为expired]
    D --> E[创建timeout_reject决策]
    E --> F[创建delivery]
    F --> G[在线则立即下发]
    F --> H[离线则等待重连]
    G --> I[记录审计]
    H --> I
```

要点：

- 超时不是简单关闭审批，而是生成默认拒绝决策。
- 默认拒绝仍需投递给 Agent，避免 CLI 长时间阻塞。

## 11. Agent 断线恢复流程

```mermaid
sequenceDiagram
    participant Agent
    participant Gateway
    participant API
    participant DB as PostgreSQL

    Agent-xGateway: 网络中断
    Gateway->>DB: 标记suspect_offline/offline
    Agent->>Gateway: 重连
    Gateway->>API: 校验设备
    Agent->>Gateway: agent.resume + last_ack_sequence
    Gateway->>API: 拉取待投递delivery
    API->>DB: 查询pending/sent未ack记录
    Gateway-->>Agent: 重放审批决策
    Agent-->>Gateway: ACK
    Gateway->>API: 更新delivery状态
```

要点：

- Agent 本地队列补发未确认的 `approval.detected`。
- 服务端按幂等键去重。
- 对已决审批，Agent 重复 ACK 不改变最终状态，只更新最后确认时间。

## 12. 本地人工接管流程

```mermaid
flowchart TD
    A[审批等待中] --> B[用户在本地终端直接输入]
    B --> C[Agent检测到prompt消失或输出继续]
    C --> D[Agent上报approval.superseded]
    D --> E{服务端审批仍waiting?}
    E -- 是 --> F[状态改为cancelled_by_local_input]
    E -- 否 --> G[保留已决状态并记录冲突审计]
```

要点：

- 本地用户永远可以接管本机 CLI。
- 远程审批系统不能阻止本地输入。
- 发生冲突时以已经写入 CLI 的实际结果和审计记录为准。

## 13. 移动端查看设备会话流程

```mermaid
sequenceDiagram
    participant User
    participant Mobile
    participant API
    participant DB as PostgreSQL

    User->>Mobile: 打开设备详情
    Mobile->>API: GET /api/v1/devices/{device_id}
    API->>DB: 校验用户租户角色和设备授权
    API-->>Mobile: 返回设备状态和统计
    Mobile->>API: GET /api/v1/devices/{device_id}/sessions
    API->>DB: 查询该设备会话列表
    API-->>Mobile: 返回CLI类型、状态、摘要、待审批数
    User->>Mobile: 打开会话详情
    Mobile->>API: GET /api/v1/sessions/{session_id}
    API-->>Mobile: 返回脱敏上下文和关联审批
```

要点：

- 移动端只通过服务端查询，不直连 Agent。
- 查询权限由租户角色和设备授权共同决定。
- 默认返回脱敏后的命令、输出摘要和审批上下文。

## 14. 管理员禁用设备流程

```mermaid
sequenceDiagram
    participant Admin
    participant Web
    participant API
    participant Gateway
    participant Agent
    participant DB as PostgreSQL

    Admin->>Web: 禁用设备
    Web->>API: POST /devices/{id}/disable
    API->>DB: 更新设备为disabled并吊销令牌
    API->>Gateway: disconnect device
    Gateway->>Agent: device.disabled
    Gateway-xAgent: 关闭连接
    API->>DB: 记录审计
```

禁用后：

- 新连接被拒绝。
- 未投递审批标记为 `cancelled` 或由管理员选择超时拒绝。
- Agent 本地提示设备已被禁用。
