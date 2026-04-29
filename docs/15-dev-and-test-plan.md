# 开发联调与测试计划

## 1. 目标

让 Server、Agent、Web、Mobile 可以并行开发，并在每个里程碑稳定联调。本文定义仓库结构、代码所有权、本地环境、测试策略和联调脚本要求。

## 2. 仓库结构

推荐 monorepo：

```text
server/
  cmd/server/
  internal/
  pkg/protocol/
agent/
  cmd/agent/
  internal/
web-admin/
  src/
mobile/
  lib/
deploy/
  docker-compose.yml
  .env.example
schema/
  openapi.yaml
  enums.yaml
  ws/
docs/
scripts/
```

## 3. 代码所有权

| 目录 | Owner | 说明 |
|---|---|---|
| `server/` | Server | API、Gateway、Worker、DB migration |
| `agent/` | Agent | PTY、CLI Adapter、本地队列、本地 UI |
| `web-admin/` | Web | React 前端 |
| `mobile/` | Mobile | Flutter 应用 |
| `schema/` | Server 主责，四端共同评审 | OpenAPI、WS schema、枚举、错误码 |
| `deploy/` | Server/DevOps | 本地和生产部署 |
| `docs/` | 全员 | 设计和实现约定 |
| `scripts/` | 全员 | 开发、测试、数据初始化脚本 |

规则：

- 修改 `schema/` 必须说明影响端。
- 修改状态枚举必须同步 `docs/14-state-transition-table.md`。
- 修改权限必须同步 `docs/13-permission-matrix.md`。
- Agent 新增 CLI 适配器必须补样本测试。

## 4. 本地环境

基础依赖：

- Go stable。
- Node.js LTS。
- Flutter stable。
- Docker Desktop 或兼容 Docker Engine。
- PostgreSQL client 可选。
- Redis client 可选。

本地服务：

```text
PostgreSQL
Redis
authentik/OIDC
Go Server
Web Admin
Agent
Mobile Emulator/Device
```

## 5. 环境变量

根目录提供：

```text
deploy/.env.example
server/.env.example
web-admin/.env.example
agent/.env.example
mobile/.env.example
```

禁止提交真实：

- OIDC secret。
- 数据库密码。
- 设备 token。
- Push credentials。
- 加密密钥。

## 6. 本地启动顺序

### 6.1 基础服务

```bash
docker compose -f deploy/docker-compose.yml up -d postgres redis authentik
```

### 6.2 数据库迁移

```bash
cd server
go run ./cmd/migrate up
```

### 6.3 Server

```bash
cd server
go run ./cmd/server
```

### 6.4 Web

```bash
cd web-admin
npm install
npm run dev
```

### 6.5 Agent

```bash
cd agent
go run ./cmd/agent version
go run ./cmd/agent register --activation-code <code>
go run ./cmd/agent run -- codex
```

### 6.6 Mobile

```bash
cd mobile
flutter pub get
flutter run
```

## 7. 本地 OIDC

开发环境需要预置：

- 一个 OIDC client 给 Web。
- 一个 OIDC client 给 Mobile。
- Agent 本地 UI 可以复用 public client，使用 device code flow 或浏览器回调。
- 测试用户：
  - owner@example.local
  - admin@example.local
  - approver@example.local
  - viewer@example.local

初始化脚本需要创建：

- 默认租户。
- 四个测试用户。
- 对应成员角色。

## 8. Push 本地降级

MVP 本地联调不强依赖真实 FCM/APNs。

降级策略：

- Server 记录 Push 请求到日志。
- Web 提供开发通知面板。
- Mobile 前台通过 WebSocket 收通知。
- 真实 Push 放到 M6。

## 9. 测试分层

### 9.1 Server 单元测试

覆盖：

- 状态机。
- 权限判断。
- 幂等逻辑。
- 策略匹配。
- 脱敏规则。

要求：

- domain service 不依赖真实数据库。
- 状态转移表中的每条关键转移都有测试。

### 9.2 Server 集成测试

覆盖：

- API handler。
- PostgreSQL migration。
- Repository。
- WebSocket Gateway。
- Worker。

要求：

- 使用测试数据库。
- 每个测试隔离租户和数据。
- 验证错误码和 HTTP 状态码。

### 9.3 Agent 单元测试

覆盖：

- Terminal Normalizer。
- CLI Adapter。
- idempotency_key 生成。
- Input Gate。
- 本地队列。

要求：

- 不依赖真实 AI CLI。
- 使用 PTY 输出样本回放。

### 9.4 Agent 集成测试

覆盖：

- 启动简单测试 CLI。
- 读取 PTY 输出。
- 检测 prompt。
- 写回 approve/reject。
- ACK 服务端。

建议提供测试 CLI：

```text
test-fixtures/fake-ai-cli
```

它可以输出固定审批 prompt 并等待输入。

### 9.5 Web 测试

覆盖：

- 登录回调。
- 设备列表。
- 会话列表。
- 审批详情。
- 审批提交。
- 多端同步 UI。

建议：

- 组件测试覆盖核心状态。
- Playwright 覆盖主流程。

### 9.6 Mobile 测试

覆盖：

- 登录状态。
- 设备列表。
- 会话列表。
- 审批详情。
- 审批提交。
- WebSocket 同步。

MVP 至少保留冒烟测试脚本和手工测试清单。

## 10. 端到端场景

### E2E-1: 设备绑定

1. owner 登录 Web。
2. 生成激活码。
3. Agent 注册。
4. Web 和 Mobile 看到设备 active。

### E2E-2: 会话查看

1. Agent 启动 fake CLI。
2. Server 保存 session。
3. Mobile 打开设备详情。
4. Mobile 查看该设备会话列表和详情。

### E2E-3: Web 审批闭环

1. fake CLI 输出审批 prompt。
2. Agent 上报审批。
3. Web 收到审批。
4. Web 批准。
5. Agent 写回。
6. Server 标记 delivered。

### E2E-4: 多端同步

1. Web、Mobile A、Mobile B 同时在线。
2. Agent 上报审批。
3. 三端都收到提醒。
4. Mobile A 拒绝。
5. Web 和 Mobile B 显示已由 Mobile A 处理。

### E2E-5: Agent 本地处理

1. Agent 本地 UI 登录。
2. Agent 上报审批。
3. Agent 本地 UI 批准。
4. Mobile 显示已由 Agent 本地处理。
5. Agent 设备通道 ACK 回写结果。

### E2E-6: 断线恢复

1. Agent 上报会话。
2. 断开 Agent 网络。
3. 审批超时。
4. Agent 重连。
5. Agent 收到 timeout_reject 投递并 ACK。

## 11. CLI 适配器测试样本

建议目录：

```text
agent/testdata/adapters/
  codex/
    permission_request.ansi
    permission_request.expected.json
  claude_code/
  opencode/
  copilot/
  gemini/
  custom/
```

每个样本包含：

- 原始 ANSI 输出。
- 期望 TerminalSnapshot。
- 期望 DetectedEvent。
- 期望 approve/reject/reply 写入字节。

## 12. 契约测试

契约测试要求：

- Server 响应必须符合 OpenAPI。
- WebSocket 消息必须符合 JSON Schema。
- Web 生成 client 后不能有 TypeScript 类型错误。
- Mobile client 模型不能缺少必填字段。
- Agent protocol 类型和 schema 枚举一致。

## 13. CI 建议

最小 CI：

```text
schema lint
server test
agent test
web typecheck
web test
mobile analyze
docs link check
```

集成 CI：

```text
docker compose up
server migration
api integration test
agent fake-cli e2e
web playwright smoke
```

## 14. 手工验收清单

每个里程碑发布前检查：

- 新数据库迁移可从空库执行。
- 老版本 Agent 连接行为明确。
- Web/Mobile 无未知枚举崩溃。
- 多端审批冲突提示正确。
- 审计记录包含 actor、client_instance、device、session、trace_id。
- Push 降级不影响审批主流程。

## 15. 缺陷分级

- P0: 审批状态错误、越权审批、决策丢失、敏感明文泄露。
- P1: Agent 无法回写、重复审批、设备无法重连、多端同步错误。
- P2: UI 展示错误、通知延迟、非核心 API 错误。
- P3: 文案、样式、低风险兼容问题。

P0/P1 必须阻断里程碑验收。
