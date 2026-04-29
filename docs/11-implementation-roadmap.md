# 实施路线图

## 1. 开发原则

- 先打通最短端到端闭环，再扩展多端和多 CLI。
- 协议模型先行，Server、Agent、Web、Mobile 不各自发明字段。
- 每个里程碑必须有可演示场景和验收条件。
- 所有状态变化以服务端数据库为事实来源。
- Push、WebSocket、Agent 本地缓存都只服务于体验和可靠性补偿。

## 2. 推荐仓库策略

MVP 建议使用 monorepo，减少协议和部署割裂。

```text
server/       Go 服务端
agent/        Go Agent
web-admin/    React + TypeScript
mobile/       Flutter
deploy/       Docker Compose、网关、OIDC配置
schema/       OpenAPI、WebSocket JSON Schema、枚举定义
docs/         设计文档
```

`schema/` 是多端协作的中心。任何 API 或 WebSocket 字段变更都先改 schema，再改实现。

## 3. 里程碑

### M0: 工程骨架和协议基线

目标：

- 建立 monorepo 目录。
- Server、Agent、Web、Mobile 都能独立启动最小空壳。
- 定义共享枚举、API 基础响应、错误码、WebSocket 消息 envelope。
- Docker Compose 启动 PostgreSQL、Redis、OIDC、Server。

交付物：

- `server` health check。
- `agent` version 命令。
- `web-admin` 登录页空壳。
- `mobile` 登录页空壳。
- `schema/openapi.yaml` 初版。
- `schema/ws-messages/*.schema.json` 初版。

验收：

- `docker compose up` 后 Server ready。
- Web 能完成 OIDC 登录并调用 `/api/v1/me`。
- Agent 能读取配置并打印协议版本。

### M1: 设备绑定和 Agent 长连接

目标：

- Web 生成设备激活码。
- Agent 使用激活码注册设备。
- Agent 建立 WSS 长连接并心跳。
- Web/Mobile 能查看设备列表和在线状态。

交付物：

- `device_activation_codes`、`devices`、`device_tokens` 表。
- Agent 本地安全存储设备令牌。
- Gateway presence。
- 设备状态流转。

验收：

- 一个账号绑定两台测试设备。
- 设备断开 3 分钟后变为 offline。
- 设备重连后恢复 active。

### M2: 会话托管和会话查看

目标：

- Agent 通过 PTY/ConPTY 托管一个 CLI。
- Server 保存会话状态。
- Web/Mobile 按设备查看会话列表和详情。

交付物：

- `aicli run -- <command>`。
- `sessions` 表。
- `GET /devices/{device_id}/sessions`。
- `GET /sessions/{session_id}`。
- 最近输出摘要脱敏。

验收：

- 同一设备同时运行两个 CLI 会话。
- 移动端能看到该设备全部会话。
- 会话结束后状态、退出码、结束时间正确。

### M3: 审批闭环 Web 优先

目标：

- Agent 检测一个 CLI 审批 prompt。
- Server 创建审批单。
- Web 收到提醒并处理。
- Agent 写回 PTY。
- Server 记录 ACK。

交付物：

- Terminal Normalizer 最小实现。
- 一个内置 CLI 适配器，优先 Codex 或 custom。
- `approval_requests`、`approval_actions`、`approval_deliveries`。
- Web 审批收件箱和审批详情。

验收：

- 从 CLI prompt 到 Web 批准，再到 CLI 继续执行，端到端成功。
- 重复 prompt 不重复创建审批。
- 重复点击审批按钮只生效一次。

### M4: 多端通知和同步

目标：

- 支持多个移动端、Web、Agent 本地 UI 同时在线。
- 任意一端处理审批，其他端同步最终状态。
- 移动端可查看审批和会话。

交付物：

- `client_instances`。
- `approval_notifications`。
- `approval.updated` WebSocket 消息。
- 移动端审批列表、详情、设备详情、会话列表。
- Agent 本地 UI 审批入口。

验收：

- 两台手机和一个 Web 同时收到同一审批。
- 手机 A 处理后，手机 B 和 Web 显示已由手机 A 处理。
- Agent 本地 UI 处理后，手机端显示处理方为 Agent 本地。

### M5: 多 CLI 适配器和权限完善

目标：

- 扩展 Codex、Claude Code、openCode、Copilot、Gemini 适配器。
- 设备授权和权限矩阵落地。
- 审计查询可用。

交付物：

- 每个 CLI 的样本测试。
- `device_grants`。
- 权限检查中间件。
- 审计列表和筛选。

验收：

- approver 只能处理授权设备。
- viewer 不能提交审批。
- 每个适配器至少有 prompt 检测和回写测试。

### M6: 移动 Push 和可靠性补强

目标：

- 移动端离线时收到 Push。
- Agent 断线恢复后补发事件和补收投递。
- Worker 完成超时拒绝和投递重试。

交付物：

- Push Token 注册。
- Push Worker。
- Agent 本地队列。
- 超时扫描 Worker。
- 投递重试 Worker。

验收：

- 移动端后台收到审批提醒。
- Agent 离线期间审批超时后，重连收到默认拒绝投递。
- Redis 重启后审批事实状态不丢。

## 4. 并行开发分工

### Server

负责：

- 数据库迁移。
- HTTP API。
- WebSocket Gateway。
- 状态机和幂等。
- 权限检查。
- Worker。

不负责：

- CLI prompt 识别细节。
- 前端页面状态管理。

### Agent

负责：

- PTY/ConPTY。
- CLI Launcher。
- Terminal Normalizer。
- CLI Adapter。
- 本地队列。
- 本地 UI 与设备配置。

不负责：

- 审批最终状态裁决。
- 用户租户权限判断。

### Web

负责：

- OIDC 登录。
- 设备、会话、审批、策略、审计页面。
- WebSocket 实时同步。
- 审批动作提交。

不负责：

- 直接连接 Agent。
- 自行判断最终状态。

### Mobile

负责：

- OIDC 登录。
- Push Token 注册。
- 设备和会话查看。
- 审批列表、详情、处理和同步展示。

不负责：

- 直接连接 Agent。
- 保存长期业务状态。

## 5. 每个里程碑完成标准

每个里程碑必须满足：

- 文档中对应接口已更新。
- 数据库迁移可从空库执行。
- 关键路径有自动化测试或可重复手工脚本。
- Web/Mobile 不依赖 mock 才能通过主流程。
- Agent 和 Server 的协议版本一致。
- 错误码和状态枚举没有端内私有值。
