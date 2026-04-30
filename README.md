# GatePilot

GatePilot 是面向 AI CLI/Agent 的远程审批与会话协同平台。它在本地采集 AI CLI 的权限确认、执行计划确认、高风险操作确认等事件，将审批请求上报到服务端，再通过 Web、移动端和 Agent 本地 UI 通知用户审批，并把结果可靠回写到本地 CLI 会话。

## 目标

- 支持 Windows、macOS、Linux 采集端
- 支持多用户、多组织、设备绑定、会话管理和审批审计
- 支持 Web 管理端和移动端审批
- 支持服务端容器化部署和水平扩展
- 支持审批超时、幂等处理、断线重连、消息补偿和审计追踪
- 支持后续接入多种 AI CLI 适配器

## 技术选型

- 服务端语言: Go
- 服务端框架: Go HTTP API + WebSocket Gateway + Worker
- Web 管理端: 独立前端，默认 React + TypeScript + Vite
- 手机客户端: Flutter
- 跨平台采集端: Go Agent
- Windows 终端接管: ConPTY
- macOS/Linux 终端接管: PTY
- 数据库: PostgreSQL
- 缓存与在线状态: Redis
- 事件总线: MVP 使用 Redis Streams，规模化后可切换 NATS JetStream 或 Kafka
- 身份认证: authentik 或兼容 OIDC Provider
- 部署方式: Docker Compose 起步，后续支持 Kubernetes

## 目录说明

- `schema/`: OpenAPI、WebSocket JSON Schema、枚举和错误码契约
- `server/`: Go 服务端骨架
- `agent/`: Go Agent 骨架和 CLI 适配器样本
- `web-admin/`: React + TypeScript + Vite Web 管理端骨架
- `mobile/`: 移动端骨架，Android 最低支持 Android 9/API 28
- `deploy/`: Docker Compose 和环境变量示例
- `scripts/`: 开发、验证和初始化脚本
- `docs/01-overview.md`: 项目目标、角色、范围和分阶段交付
- `docs/02-architecture.md`: 逻辑架构、服务边界、扩展架构
- `docs/03-detailed-design.md`: 服务端、Agent、前端、移动端详细设计
- `docs/04-process-flows.md`: 注册、会话、审批、断线恢复等关键流程
- `docs/05-data-model.md`: PostgreSQL 数据模型、状态枚举、索引与约束
- `docs/06-deployment.md`: Docker/Kubernetes 部署、配置、扩容和可观测性
- `docs/07-api-protocol.md`: HTTP API、WebSocket 消息、幂等和版本策略
- `docs/08-security-reliability.md`: 安全边界、敏感信息处理、可靠性和风险控制
- `docs/09-requirements-detail.md`: 多端登录、设备绑定、同机多 CLI、会话查看和通知同步需求
- `docs/10-agent-cli-integration.md`: Agent 接入 AI CLI 的 PTY 托管、适配器、检测、回写和输入协调设计
- `docs/11-implementation-roadmap.md`: MVP 里程碑、并行开发分工、交付门槛
- `docs/12-contract-freeze.md`: API/WS 契约冻结规则、通用字段、错误码和共享协议策略
- `docs/13-permission-matrix.md`: 租户角色、设备授权和操作权限矩阵
- `docs/14-state-transition-table.md`: 审批、投递、设备、会话的状态转移表
- `docs/15-dev-and-test-plan.md`: 仓库结构、本地联调、测试策略和代码所有权
- `docs/16-development-environment.md`: 本机开发环境目录、Android 版本和工具链约定

## 核心流程摘要

1. 用户在本机通过 Agent 启动或托管 AI CLI。
2. Agent 使用平台 PTY 接管 CLI 输入输出。
3. Agent 识别出待审批事件，生成稳定幂等键并上报服务端。
4. 服务端创建审批单，记录审计日志，并通知所有有权限的 Web、移动端和 Agent 本地 UI。
5. 用户在任意一个客户端批准、拒绝或回复自定义文本。
6. 服务端记录处理用户、客户端类型和处理内容，并同步给其他客户端。
7. 服务端将审批决策下发到对应在线 Agent；若 Agent 离线则进入待投递队列。
8. Agent 回写审批结果到 CLI，并向服务端确认投递结果。
9. 服务端完成审批闭环，保留可审计记录。

## 设计原则

- 控制面与执行面分离
- 推送只做提醒，可靠状态以服务端为准
- 审批链路必须幂等、可追踪、可补偿
- Agent 默认最小权限运行，不上传不必要的敏感内容
- 从第一版开始保留多租户字段和协议版本
- MVP 可以单体部署，但服务边界必须可拆分

## M0 本地启动

Go 工具链按本机标准目录使用：

```powershell
D:\Dev\Env\Go\bin\go.exe run .\server\cmd\server
D:\Dev\Env\Go\bin\go.exe run .\agent\cmd\agent version
```

Flutter 工具链：

```powershell
D:\Dev\Env\Flutter\flutter\bin\flutter.bat analyze --no-pub
```

M1/M2 端到端冒烟：

```powershell
.\scripts\e2e-device-session.ps1
```

该脚本会临时启动 Server，生成设备激活码，调用 Agent 注册设备，创建 fake CLI 会话，上报 fake 审批，提交批准决策，并模拟 Agent ACK 投递结果。

迁移文件复核：

```powershell
.\scripts\validate-migrations.ps1
```

Web 管理端：

```powershell
cd web-admin
npm install
npm run dev
```

移动端 Android 要求：

- 最低版本 Android 9
- API level 28
- 配置位置：`mobile/android/app/build.gradle.kts`
