# 契约冻结规则

## 1. 目标

多端并行开发时，Server、Agent、Web、Mobile 必须以同一套契约为准。任何接口字段、枚举、错误码、WebSocket 消息变更，都必须先更新 schema，再更新代码。

## 2. 契约来源

必须目录：

```text
schema/
  openapi.yaml
  enums.yaml
  errors.yaml
  ws/
    envelope.schema.json
    agent.hello.schema.json
    agent.connected.schema.json
    agent.heartbeat.schema.json
    session.created.schema.json
    approval.detected.schema.json
    approval.accepted.schema.json
    approval.decision.deliver.schema.json
    approval.decision.ack.schema.json
    approval.created.schema.json
    approval.updated.schema.json
```

文档说明业务意图，`schema/` 是实现契约来源。

M0 必须先提交这些文件的最小可用版本。没有 schema 的接口和消息只能作为设计草案，不能作为四端实现依据。

schema 文件要求：

- `openapi.yaml` 必须能被标准 OpenAPI 3.x 工具解析。
- `enums.yaml` 必须集中列出所有跨端枚举值和说明。
- `errors.yaml` 必须列出错误码、HTTP 状态码、可重试性和客户端默认文案键。
- `ws/envelope.schema.json` 必须被所有 WebSocket payload schema 引用。
- 每个 `ws/*.schema.json` 必须包含 message `type`、payload 必填字段和兼容性说明。

不允许把以下内容只写在 Markdown 中：

- 新接口字段。
- 新 WebSocket 消息类型。
- 新枚举值。
- 新错误码。
- 状态机新增终态或可恢复状态。

## 3. 通用格式

### 3.1 时间

- 所有 API 和 WebSocket 时间使用 RFC3339 UTC 字符串。
- 字段名以 `_at` 结尾，例如 `created_at`。
- 客户端展示时转换为本地时区。

示例：

```json
"created_at": "2026-04-29T12:00:00Z"
```

### 3.2 ID

- 业务主键使用 UUID 字符串。
- `request_id`、`trace_id`、`message_id` 使用字符串，格式由服务端生成器决定。
- 客户端不能解析 ID 内部结构。

### 3.3 分页

列表接口统一使用 cursor 分页。

请求：

```http
GET /api/v1/approvals?limit=50&cursor=...
```

响应：

```json
{
  "data": {
    "items": [],
    "next_cursor": "opaque-cursor",
    "has_more": true
  },
  "request_id": "req_...",
  "trace_id": "tr_..."
}
```

约束：

- `limit` 默认 50，最大 200。
- `cursor` 是 opaque 字符串，客户端不能拼接或解析。
- 时间倒序列表默认按 `created_at desc, id desc`。

### 3.4 空值

- 可选字符串为空时使用 `null`，不要使用空字符串表达缺失。
- 可选数组为空时返回 `[]`。
- 可选对象为空时返回 `null`。

## 4. 通用响应

成功响应：

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
    "code": "approval_already_decided",
    "message": "approval already decided",
    "details": {
      "approval_id": "uuid"
    }
  },
  "request_id": "req_...",
  "trace_id": "tr_..."
}
```

`message` 用于开发和日志，不作为客户端最终展示文案。客户端根据 `code` 映射本地化文案。

## 5. HTTP 状态码

- `200`: 查询或幂等重试成功。
- `201`: 创建成功。
- `202`: 请求接受，异步处理中。
- `400`: 请求格式错误。
- `401`: 未登录或 token 无效。
- `403`: 无权限。
- `404`: 资源不存在或无权访问时需要隐藏资源。
- `409`: 状态冲突，例如审批已被其他端处理。
- `422`: 请求语义合法但业务校验失败。
- `429`: 限流。
- `500`: 服务端内部错误。
- `503`: 依赖不可用或服务未 ready。

## 6. 错误码

### 6.1 认证和权限

- `unauthorized`
- `token_expired`
- `forbidden`
- `tenant_required`
- `device_access_denied`
- `role_insufficient`

### 6.2 设备

- `activation_code_invalid`
- `activation_code_expired`
- `activation_code_consumed`
- `device_disabled`
- `device_token_invalid`
- `device_offline`

### 6.3 审批

- `approval_not_found`
- `approval_already_decided`
- `approval_expired`
- `approval_not_waiting_decision`
- `approval_decision_duplicate`
- `approval_decision_conflict`

### 6.4 投递

- `delivery_not_found`
- `delivery_already_acked`
- `delivery_failed`
- `agent_session_not_found`
- `agent_session_closed`

### 6.5 协议

- `protocol_version_unsupported`
- `message_type_unknown`
- `message_schema_invalid`
- `idempotency_key_required`
- `client_instance_required`

错误码命名规则：

- 使用小写 snake_case。
- 同一业务含义只能有一个错误码。
- 状态冲突类错误优先使用具体业务错误码，例如审批已处理使用 `approval_already_decided`。
- 不新增泛化的 `*_conflict` 错误码，除非没有更具体的业务含义。
- 每个错误码必须在 `schema/errors.yaml` 中登记 HTTP 状态码和客户端处理建议。

## 7. 枚举冻结

所有枚举必须集中定义：

- `tenant_member_role`
- `common_active_status`
- `device_status`
- `session_status`
- `approval_status`
- `decision_type`
- `delivery_status`
- `client_type`
- `client_instance_status`
- `notification_status`
- `device_grant_permission`
- `device_token_status`
- `platform`
- `arch`
- `push_provider`
- `notification_channel`
- `output_stream_type`
- `cli_type`
- `risk_level`
- `actor_type`
- `ack_result`
- `policy_decision`
- `action_result`
- `audit_result`

规则：

- 新增枚举值必须先进入 `schema/enums.yaml`。
- 删除或重命名枚举值视为破坏性变更。
- 客户端遇到未知枚举值时必须显示降级状态，不得崩溃。

## 8. WebSocket Envelope

所有 WebSocket 消息必须使用统一 envelope：

```json
{
  "type": "approval.updated",
  "message_id": "uuid",
  "trace_id": "tr_...",
  "sent_at": "2026-04-29T12:00:00Z",
  "schema_version": "2026-04-01",
  "payload": {}
}
```

字段：

- `type`: 消息类型。
- `message_id`: 消息唯一 ID。
- `trace_id`: 链路 ID。
- `sent_at`: 发送时间。
- `schema_version`: 消息 schema 版本。
- `payload`: 业务数据。

客户端必须忽略未知字段。

## 9. WebSocket ACK

关键消息需要 ACK。

Agent 必须 ACK：

- `approval.decision.deliver`
- `policy.updated`
- `device.disabled`

Client 可选 ACK：

- `approval.created`
- `approval.updated`

Client ACK 只用于通知追踪，不影响审批状态。

Agent ACK payload 必须包含：

- `delivery_id`
- `approval_id`
- `session_id`
- `ack_result`
- `detail`

`ack_result` 冻结值：

- `written`: 字节已写入 PTY。
- `accepted`: CLI 输出变化表明已继续执行。
- `session_closed`: 会话已经结束，无法回写。
- `write_failed`: 写入 PTY 失败。
- `stale_decision`: 决策已过期或重复。

服务端必须把 `written` 和 `accepted` 视为投递成功，把其他值视为投递失败或需人工介入。

## 10. 幂等契约

### 10.1 HTTP 幂等

以下接口必须带 `Idempotency-Key`：

- 创建设备激活码。
- Agent 登录绑定注册。
- 提交审批决策。
- 创建设备授权。
- 禁用设备。

重复请求：

- 参数完全一致，返回第一次结果。
- 参数不一致，返回 `409 approval_decision_conflict` 或对应冲突错误。

### 10.2 Agent 上报幂等

Agent 上报审批事件使用业务幂等键：

```text
tenant_id + idempotency_key
```

服务端返回已有审批单，不创建新记录。

## 11. 客户端生成策略

推荐：

- Web 从 OpenAPI 生成 TypeScript client 和类型。
- Mobile 从 OpenAPI 生成 Dart client，或使用手写 client 但类型必须由 schema 对照测试验证。
- Server 从 schema 生成基础 DTO 或使用契约测试保证一致。
- Agent 和 Server 共享 Go `pkg/protocol`，但仍以 schema 为对外契约。

不允许：

- Web/Mobile 私自复制后端未冻结字段。
- Server 返回未登记的枚举值。
- Agent 使用只存在代码里、schema 不存在的消息类型。

生成物要求：

- 生成代码应提交或在 CI 中稳定生成，团队需选择一种方式并保持一致。
- Web、Mobile、Agent 的生成或对照检查必须进入 CI。
- schema 修改必须触发受影响端的类型检查。
- 手写模型必须有 schema 对照测试，不能只依赖人工检查。

UI 端兼容要求：

- 未知枚举显示为通用“未知状态”或等价降级文案。
- 新增可选字段不应影响旧客户端渲染。
- 删除、重命名字段或新增必填字段视为破坏性变更。
- Web/Mobile 不把 `message` 字段作为最终展示文案，只根据 `error.code` 映射。

## 12. 契约变更流程

1. 提交 schema 变更。
2. 更新对应设计文档。
3. 标记变更类型：兼容或破坏性。
4. 更新 Server 契约测试。
5. 更新 Agent/Web/Mobile 生成代码或手写类型。
6. 在变更说明中列出受影响端。

兼容变更：

- 新增可选字段。
- 新增客户端可忽略的消息类型。
- 新增错误码但 HTTP 状态不变。

破坏性变更：

- 删除字段。
- 字段改名。
- 必填字段变更。
- 枚举重命名或删除。
- 状态机语义变化。

破坏性变更必须提升协议版本。

## 13. 契约冻结点

每个里程碑开始前冻结该里程碑所需契约：

- M1 冻结设备注册、长连接、心跳。
- M2 冻结会话 API。
- M3 冻结审批创建、决策、投递 ACK。
- M4 冻结客户端实例、通知同步、移动端会话查看。
- M5 冻结权限和策略接口。
- M6 冻结 Push 和恢复补偿协议。

冻结后只允许兼容变更，破坏性变更必须经过四端确认。
