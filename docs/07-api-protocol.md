# API 与协议设计

## 1. 通用约定

HTTP API 前缀：

```text
/api/v1
```

通用请求头：

- `Authorization: Bearer <access_token>`
- `Idempotency-Key: <uuid>`，用于会改变状态的用户动作。
- `X-Request-Id: <uuid>`，调用方可选，服务端没有收到时自动生成。

通用响应字段：

```json
{
  "data": {},
  "request_id": "req_...",
  "trace_id": "tr_..."
}
```

错误响应：

```json
{
  "error": {
    "code": "approval_conflict",
    "message": "approval already decided",
    "details": {}
  },
  "request_id": "req_...",
  "trace_id": "tr_..."
}
```

## 2. HTTP API

### 2.1 当前用户

```http
GET /api/v1/me
```

返回当前用户、租户、角色和权限。

### 2.2 设备激活码

```http
POST /api/v1/tenants/{tenant_id}/device-activation-codes
```

请求：

```json
{
  "name": "My Windows PC",
  "expires_in_seconds": 600
}
```

响应：

```json
{
  "data": {
    "activation_code": "ABCD-EFGH-IJKL",
    "expires_at": "2026-04-29T12:00:00Z"
  }
}
```

激活码只在响应中出现一次。

### 2.3 Agent 注册

```http
POST /api/v1/agent/register
```

请求：

```json
{
  "activation_code": "ABCD-EFGH-IJKL",
  "device_name": "DESKTOP-01",
  "platform": "windows",
  "arch": "amd64",
  "agent_version": "0.1.0",
  "protocol_version": "2026-04-01",
  "capabilities": {
    "pty": true,
    "conpty": true,
    "local_store": "sqlite"
  }
}
```

响应：

```json
{
  "data": {
    "device_id": "uuid",
    "device_token": "plain-token-returned-once",
    "server_ws_url": "wss://example.com/ws/agent"
  }
}
```

### 2.4 Agent 登录绑定注册

Agent 本地 UI 已经完成用户 OIDC 登录时，可以直接绑定本机：

```http
POST /api/v1/agent/register-with-login
Authorization: Bearer <access_token>
Idempotency-Key: <uuid>
```

请求：

```json
{
  "tenant_id": "uuid",
  "device_name": "DESKTOP-01",
  "platform": "windows",
  "arch": "amd64",
  "agent_version": "0.1.0",
  "protocol_version": "2026-04-01",
  "capabilities": {
    "pty": true,
    "conpty": true,
    "local_ui": true
  },
  "client_instance": {
    "client_type": "agent_desktop",
    "display_name": "DESKTOP-01 Agent",
    "app_version": "0.1.0"
  }
}
```

响应：

```json
{
  "data": {
    "device_id": "uuid",
    "device_token": "plain-token-returned-once",
    "client_instance_id": "uuid",
    "server_ws_url": "wss://example.com/ws/agent"
  }
}
```

### 2.5 客户端实例注册

```http
POST /api/v1/client-instances
Authorization: Bearer <access_token>
```

请求：

```json
{
  "tenant_id": "uuid",
  "client_type": "mobile_ios",
  "display_name": "Alice iPhone",
  "app_version": "0.1.0",
  "platform": "iOS"
}
```

响应：

```json
{
  "data": {
    "client_instance_id": "uuid"
  }
}
```

注册 Push Token：

```http
POST /api/v1/client-instances/{client_instance_id}/push-token
```

请求：

```json
{
  "provider": "apns",
  "token": "push-token"
}
```

### 2.6 设备授权

```http
GET /api/v1/devices/{device_id}/grants
POST /api/v1/devices/{device_id}/grants
DELETE /api/v1/devices/{device_id}/grants/{grant_id}
```

授权请求：

```json
{
  "user_id": "uuid",
  "permission": "approve",
  "expires_at": null
}
```

### 2.7 审批列表

```http
GET /api/v1/tenants/{tenant_id}/approvals?status=waiting_decision&limit=50
```

### 2.8 审批详情

```http
GET /api/v1/approvals/{approval_id}
```

返回审批主体、会话摘要、设备摘要、策略命中、投递状态和审计摘要。

### 2.9 提交审批决策

```http
POST /api/v1/approvals/{approval_id}/decision
Idempotency-Key: <uuid>
X-Client-Instance-Id: <client_instance_id>
```

请求：

```json
{
  "decision_type": "approve",
  "payload": null
}
```

自定义回复：

```json
{
  "decision_type": "reply",
  "payload": "continue with tests only"
}
```

响应：

```json
{
  "data": {
    "approval_id": "uuid",
    "status": "delivering",
    "decision_type": "approve",
    "delivery_status": "pending",
    "decided_by": {
      "actor_type": "user",
      "actor_id": "uuid",
      "display_name": "Alice",
      "client_instance_id": "uuid",
      "client_type": "mobile_ios"
    }
  }
}
```

冲突响应：

- HTTP `409`
- code: `approval_already_decided`

同一个 `Idempotency-Key` 重试返回第一次提交结果。

### 2.10 设备列表

```http
GET /api/v1/tenants/{tenant_id}/devices?status=active
```

### 2.11 设备会话列表

```http
GET /api/v1/devices/{device_id}/sessions?status=running&cli_type=codex&limit=50
```

响应摘要：

```json
{
  "data": {
    "items": [
      {
        "session_id": "uuid",
        "cli_type": "codex",
        "status": "waiting_approval",
        "started_at": "2026-04-29T12:00:00Z",
        "last_output_summary": "redacted summary",
        "pending_approval_count": 1
      }
    ]
  }
}
```

### 2.12 会话详情

```http
GET /api/v1/sessions/{session_id}
GET /api/v1/sessions/{session_id}/approvals
```

### 2.13 会话输出

```http
GET /api/v1/sessions/{session_id}/output?before_seq=1000&limit=100
```

只返回脱敏后的输出片段。

## 3. WebSocket 通用协议

所有 WebSocket 消息使用 JSON。通用结构：

```json
{
  "type": "approval.detected",
  "message_id": "uuid",
  "trace_id": "tr_...",
  "sent_at": "2026-04-29T12:00:00Z",
  "payload": {}
}
```

要求：

- `message_id` 由发送方生成，用于消息级去重和日志追踪。
- 服务端对关键消息返回 ACK。
- 业务最终状态以数据库为准，WebSocket ACK 不代表业务事务一定完成。

## 4. Agent WebSocket

连接地址：

```text
wss://example.com/ws/agent
```

认证方式：

- Header: `Authorization: Device <device_id>:<device_token>`
- 或先调用注册/换票据接口获取短期连接票据。

### 4.1 agent.hello

Agent 连接后发送：

```json
{
  "type": "agent.hello",
  "message_id": "uuid",
  "payload": {
    "device_id": "uuid",
    "agent_version": "0.1.0",
    "protocol_version": "2026-04-01",
    "platform": "windows",
    "capabilities": {}
  }
}
```

服务端响应：

```json
{
  "type": "agent.connected",
  "message_id": "uuid",
  "payload": {
    "server_time": "2026-04-29T12:00:00Z",
    "accepted_protocol_version": "2026-04-01",
    "heartbeat_interval_seconds": 15
  }
}
```

### 4.2 agent.heartbeat

```json
{
  "type": "agent.heartbeat",
  "message_id": "uuid",
  "payload": {
    "active_sessions": 2,
    "local_queue_depth": 0,
    "last_error": null
  }
}
```

### 4.3 session.created

```json
{
  "type": "session.created",
  "message_id": "uuid",
  "payload": {
    "session_id": "uuid",
    "cli_type": "codex",
    "command_line_redacted": "codex",
    "working_dir_hash": "sha256:...",
    "pty": {
      "cols": 120,
      "rows": 40
    }
  }
}
```

### 4.4 approval.detected

```json
{
  "type": "approval.detected",
  "message_id": "uuid",
  "trace_id": "tr_...",
  "payload": {
    "event_id": "agent-local-uuid",
    "idempotency_key": "sha256:...",
    "session_id": "uuid",
    "sequence_no": 123,
    "cli_type": "codex",
    "event_type": "permission_request",
    "risk_level": "high",
    "prompt_text": "redacted prompt",
    "context_before": "redacted context",
    "suggested_actions": ["approve", "reject"],
    "default_timeout_action": "reject",
    "expires_in_seconds": 300
  }
}
```

服务端响应：

```json
{
  "type": "approval.accepted",
  "message_id": "uuid",
  "payload": {
    "event_id": "agent-local-uuid",
    "approval_id": "uuid",
    "status": "waiting_decision"
  }
}
```

### 4.5 approval.decision.deliver

服务端下发：

```json
{
  "type": "approval.decision.deliver",
  "message_id": "uuid",
  "payload": {
    "delivery_id": "uuid",
    "approval_id": "uuid",
    "session_id": "uuid",
    "decision_type": "approve",
    "payload": null,
    "expires_at": "2026-04-29T12:05:00Z"
  }
}
```

Agent ACK：

```json
{
  "type": "approval.decision.ack",
  "message_id": "uuid",
  "payload": {
    "delivery_id": "uuid",
    "approval_id": "uuid",
    "session_id": "uuid",
    "ack_result": "accepted",
    "detail": {
      "bytes_written": 2
    }
  }
}
```

## 5. Web/Mobile WebSocket

连接地址：

```text
wss://example.com/ws/client
```

认证：

- `Authorization: Bearer <access_token>`

订阅消息：

- `approval.created`
- `approval.updated`
- `device.status_changed`
- `session.updated`

示例：

```json
{
  "type": "approval.created",
  "message_id": "uuid",
  "payload": {
    "tenant_id": "uuid",
    "approval_id": "uuid",
    "risk_level": "high",
    "event_type": "permission_request",
    "expires_at": "2026-04-29T12:05:00Z"
  }
}
```

审批已处理同步：

```json
{
  "type": "approval.updated",
  "message_id": "uuid",
  "payload": {
    "tenant_id": "uuid",
    "approval_id": "uuid",
    "status": "delivering",
    "decision_type": "approve",
    "decided_at": "2026-04-29T12:01:00Z",
    "decided_by": {
      "actor_type": "user",
      "actor_id": "uuid",
      "display_name": "Alice",
      "client_instance_id": "uuid",
      "client_type": "agent_desktop"
    },
    "decision_payload": null,
    "delivery_status": "pending"
  }
}
```

其他端收到该消息后必须重新调用审批详情接口，展示处理方、处理端类型、处理方式和投递状态。

## 6. 协议版本

版本格式：

```text
YYYY-MM-DD
```

兼容策略：

- 服务端至少支持当前版本和前一个稳定版本。
- Agent 连接时上报版本和能力集。
- 新字段默认可选，破坏性变更必须提升协议版本。
- 服务端拒绝不兼容 Agent 时返回明确升级原因。

## 7. 幂等规则

- Agent 上报审批使用 `tenant_id + idempotency_key` 去重。
- 用户审批动作使用 HTTP `Idempotency-Key` 去重。
- Agent ACK 使用 `delivery_id + ack_result` 去重。
- 重试不能生成新的审批单或重复执行决策。
