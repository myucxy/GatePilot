# Windows Agent 托盘与离线本地模式需求

## 1. 背景

当前 Agent 已具备两条基础链路：

- 在线链路：Agent 注册设备，连接服务端，上报会话和审批，接收审批投递并写回 CLI。
- 离线链路：`run --local-only` 不连接服务端，在本机检测审批 prompt，提醒用户并把确认结果写回 CLI。

下一阶段需要把 Windows Agent 从命令行工具升级为用户可长期运行的桌面托盘应用。托盘应用要能默认离线运行，不强制登录；用户需要时再登录并切换到服务端同步模式。

## 2. 产品目标

- Windows 托盘常驻，用户能从托盘查看状态、打开设置、启动/查看会话。
- 默认离线、默认不登录、默认只服务本机 CLI 确认。
- 支持提醒开关和提醒样式配置。
- 审批提醒用右下角小窗口展示，不依赖控制台输入。
- 小窗口直接提供批准、拒绝、其他/回复等操作。
- 提醒内容显示当前会话目录、CLI 类型、简要提醒内容和风险级别。
- 历史页能查看各个 AI Agent/CLI 的历史会话、输出摘要、审批记录和本地回复记录。
- 用户能对仍在运行且等待输入的会话继续回复。
- 登录后支持设备绑定、在线同步、服务端审批、跨端状态同步；不登录时所有数据只保存在本地。

## 3. 运行模式

### 3.1 离线模式

离线模式是默认模式。

要求：

- 不要求激活码、OIDC、服务端地址或设备 token。
- 所有会话、输出摘要、审批事件和用户决策保存在本地。
- 审批决策直接写回当前托管 CLI。
- 不向服务端上报会话、审批、审计或输出。
- UI 必须明确显示“离线本地模式”。
- 用户可在设置中关闭提醒，此时审批仍可在托盘/会话窗口中查看和处理。

### 3.2 在线模式

在线模式由用户显式登录或绑定后启用。

要求：

- 支持从托盘打开登录/绑定入口。
- 登录身份用于本地 UI 决策归属。
- 设备身份用于 Agent 长连接、会话上报、审批投递 ACK。
- 在线模式仍允许本地直接处理审批，但必须通过服务端决策 API 形成最终状态，再由 Agent 写回 CLI。
- 网络断开时自动退化为“在线配置存在但当前离线”，本地会话可以继续运行；可补发的事件进入本地队列。

## 4. 托盘功能

托盘菜单最小集合：

- 状态：离线本地 / 在线已登录 / 在线未连接 / 正在运行会话数。
- 打开 GatePilot Agent。
- 最近审批。
- 会话历史。
- 设置。
- 登录/切换账号。
- 退出。

托盘状态图标：

- 灰色：离线本地模式。
- 绿色：在线且连接正常。
- 黄色：在线配置存在但连接异常。
- 红色：存在需要处理的高风险审批。

## 5. 设置项

设置必须本地持久化。

最小设置：

- `mode`: `offline` 或 `online`，默认 `offline`。
- `start_on_login`: 是否开机启动，默认关闭。
- `notification_enabled`: 是否提醒，默认开启。
- `notification_style`: `none`、`toast`、`mini_window`、`modal_popup`，默认 `mini_window`。
- `history_retention_days`: 历史保留天数，默认 30。
- `capture_output_mode`: `summary_only`、`redacted_recent`、`full_local_only`，默认 `summary_only`。
- `default_cli_type`: 默认 CLI 适配器，默认 `custom`。
- `server_url`: 在线模式服务端地址，可为空。
- `tenant_id`、`device_id`、`client_instance_id`: 在线模式绑定信息，可为空。

安全要求：

- 在线 token 必须放入 Windows 凭据管理器或 DPAPI 保护存储。
- 本地历史默认只保存摘要和脱敏上下文。
- `full_local_only` 只能影响本机，不允许自动同步到服务端。

## 6. 右下角小窗口提醒

### 6.1 展示内容

小窗口必须展示：

- CLI 类型，例如 Codex、Claude Code、Gemini、custom。
- 当前会话目录，优先展示可读路径；需要脱敏时显示目录名和 hash。
- 简要提醒内容，来自 CLI Adapter 提取的 `prompt_text`。
- 风险级别。
- 会话启动时间或持续时间。

不能展示：

- 未脱敏的大段终端输出。
- 密钥、token、完整环境变量。

### 6.2 操作按钮

基础按钮：

- 通过：映射为标准 `approve`。
- 拒绝：映射为标准 `reject`。
- 其他：打开更多操作。

其他操作：

- 自定义回复：映射为 `reply`，内容由用户输入。
- 查看详情：打开会话详情窗口。
- 稍后处理：关闭提醒但保留待处理状态。
- 静音此会话：本会话后续不弹窗，但仍在托盘显示待处理。

映射规则：

- UI 展示统一动作名：通过、拒绝、回复。
- 实际写回 CLI 的字节由对应 CLI Adapter 的 `BuildDecisionInput` 决定。
- 若具体 CLI prompt 提供了更明确选项，UI 应优先展示 Adapter 解析出的动作说明。

## 7. 会话历史与继续回复

### 7.1 会话历史

Agent 本地必须维护本机历史会话索引。

列表字段：

- 会话 ID。
- CLI 类型。
- 命令行摘要。
- 工作目录。
- 状态：运行中、等待确认、已完成、失败、丢失。
- 启动时间、结束时间。
- 最近输出摘要。
- 审批次数、待处理审批数。

详情字段：

- 会话基础信息。
- 脱敏后的最近上下文。
- 审批时间线。
- 用户本地决策记录。
- 在线模式下的服务端同步状态。

### 7.2 继续回复

对仍然运行的托管 CLI 会话，Agent UI 可以继续写入用户输入。

规则：

- 只允许写入 Agent 自己托管的 CLI 会话。
- 已结束、丢失或非托管会话不可继续回复。
- 继续回复必须经过 Input Gate。
- 当会话处于审批等待状态时，普通文本回复需要用户确认，避免误把自由文本当作批准动作。
- 所有继续回复记录必须写入本地历史；在线模式可只同步摘要，不同步原文。

## 8. 本地数据模型

建议使用嵌入式 SQLite 或单文件 KV。MVP 可先用 JSONL，但托盘和历史页进入可用阶段前必须迁移到 SQLite。

表或集合：

- `local_settings`
- `local_sessions`
- `local_output_chunks`
- `local_approvals`
- `local_decisions`
- `local_sync_queue`

关键字段：

- `local_sessions`: `session_id`、`cli_type`、`command_line_redacted`、`working_dir`、`working_dir_hash`、`status`、`started_at`、`ended_at`、`last_output_summary`。
- `local_output_chunks`: `session_id`、`sequence_no`、`stream_type`、`content_redacted`、`content_hash`、`created_at`。
- `local_approvals`: `approval_id`、`session_id`、`event_type`、`risk_level`、`prompt_text`、`context_before`、`status`、`created_at`、`decided_at`。
- `local_decisions`: `approval_id`、`decision_type`、`payload_redacted`、`bytes_written`、`result`、`created_at`。

## 9. 技术方案建议

### 9.1 进程结构

建议拆为一个可执行文件内的多个模式：

- `agent tray`: Windows 托盘主进程。
- `agent run`: 启动或托管一个 CLI 会话。
- `agent local-ui`: 在线本地 UI 辅助命令，后续可被托盘取代。

托盘主进程职责：

- 管理设置。
- 管理本地会话索引。
- 接收 Session Host 的本地事件。
- 展示小窗口提醒。
- 将用户决策发送给对应 Session Host。

Session Host 职责：

- 启动 CLI。
- 捕获输出。
- 检测审批。
- 写回审批或用户回复。
- 记录本地历史。

### 9.2 Windows UI 技术

短期可选：

- Go 调用 Win32/PowerShell WinForms 完成托盘和小窗口原型。
- 优点：复用现有 Go Agent，构建简单。
- 缺点：复杂 UI 维护成本高。

中期建议：

- Agent Core 继续使用 Go。
- Windows UI 使用 WebView2 或 Flutter Windows。
- Go Core 通过本地 HTTP/Named Pipe 与 UI 通信。
- 优点：设置页、历史页、详情页、输入框和状态管理更容易维护。

MVP 建议：

- 先用 Go 实现托盘菜单和右下角小窗口。
- 历史页先用本地 Web UI 或简化窗口。
- 当功能稳定后再评估 WebView2/Flutter Windows 外壳。

## 10. 开发分期

### W1: 离线托盘 MVP

目标：

- `agent tray` 可启动并常驻托盘。
- 默认离线，不要求登录。
- 托盘菜单可打开设置和退出。
- 设置可切换提醒开关和提醒样式。
- `run --local-only` 检测到审批时能通知托盘。
- 右下角小窗口可通过/拒绝。

验收：

- 不启动 Server 时，Windows 托盘 Agent 能处理 fake CLI 审批。
- 关闭提醒后不弹窗，但托盘菜单能看到待处理审批。
- 通过/拒绝按钮写回 fake CLI 正确。

### W2: 本地历史与会话详情

目标：

- 保存本地会话列表、输出摘要、审批记录。
- 托盘打开会话历史窗口。
- 会话详情展示目录、CLI 类型、状态、最近上下文、审批时间线。
- 支持对运行中会话继续回复。

验收：

- fake CLI 多次运行后历史可查询。
- 运行中会话能从 UI 发送文本回复。
- 已结束会话不可继续回复。

### W3: 登录与在线切换

目标：

- 设置页支持服务端地址和登录/绑定入口。
- 支持激活码绑定。
- 支持在线模式注册 `agent_desktop` client instance。
- 在线模式下托盘处理审批走服务端决策 API。
- 网络异常时展示离线状态并保留本地能力。

验收：

- 登录后 Agent 本地处理的审批在服务端记录 `client_type=agent_desktop`。
- 断网后本地 CLI 仍可继续审批。
- 恢复连接后可补发允许补发的事件。

### W4: 多 AI Agent 历史与适配器增强

目标：

- 按 CLI 类型筛选 Codex、Claude Code、openCode、Copilot、Gemini、custom 会话。
- 每个适配器提供按钮文案和动作映射。
- 历史页支持搜索、状态筛选、时间筛选。

验收：

- 每个内置适配器至少有一组审批样本。
- UI 展示动作与实际写回字节一致。

## 11. 测试计划

单元测试：

- 设置默认值和持久化。
- 通知策略：不提醒、toast、小窗口、modal。
- CLI Adapter 动作映射。
- Input Gate 写回和继续回复。
- 本地历史查询。

集成测试：

- `agent tray` 启动和退出。
- `run --local-only` 与托盘通信。
- fake CLI approve/reject/reply。
- 提醒关闭时仍能通过托盘处理。

端到端测试：

- 离线托盘审批闭环。
- 在线托盘审批闭环。
- 本地历史多会话查询。
- 运行中会话继续回复。

手工测试：

- Windows 10/11 托盘图标显示。
- 多显示器和 DPI 缩放下小窗口位置。
- 开机启动开关。
- PowerShell、cmd、Windows Terminal 启动差异。

## 12. 当前缺口

当前代码与目标差距：

- 已有控制台离线确认和 Windows modal popup，但没有托盘常驻。
- 已有在线 `local-ui` 命令，但没有桌面设置页和登录 UI。
- 会话只在服务端保存，离线本地历史仍不完整。
- fake CLI 只覆盖单审批 prompt，未覆盖继续回复和多会话历史。
- 右下角小窗口还未实现，当前只有阻塞式 MessageBox。
- CLI 实际命令托管仍是 fake CLI 占位，真实 ConPTY 托管需要补齐。
