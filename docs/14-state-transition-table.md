# 状态转移表

## 1. 设计原则

- 状态转移只能由服务端领域服务执行。
- Agent、Web、Mobile 只能提交事件，不能直接指定最终状态。
- 所有关键状态更新使用乐观锁。
- 每次状态转移必须写审计或产生可追踪事件。
- 重复事件必须幂等处理。

## 2. 审批状态

状态：

- `created`
- `policy_evaluating`
- `waiting_decision`
- `decided`
- `expired`
- `delivering`
- `delivered`
- `delivery_failed`
- `cancelled_by_local_input`

| 当前状态 | 事件 | 下一个状态 | Actor | 主要写入 | 发出事件 | 幂等/冲突 |
|---|---|---|---|---|---|---|
| 无 | Agent 上报审批 | created | device | 插入 approval_requests | approval.created_internal | `tenant_id + idempotency_key` 去重 |
| created | 开始策略评估 | policy_evaluating | system | status, version | policy.evaluate | 重复忽略 |
| policy_evaluating | 需要人工审批 | waiting_decision | system | expires_at, policy_result | approval.created | 重复忽略 |
| policy_evaluating | 策略自动拒绝 | decided | policy | decision_type=policy_reject | approval.updated, delivery.create | 重复返回当前状态 |
| policy_evaluating | 白名单自动批准 | decided | policy | decision_type=policy_approve | approval.updated, delivery.create | 高风险不允许 |
| waiting_decision | 用户批准 | decided | user | approval_actions, decided_by, decision_type | approval.updated, delivery.create | 乐观锁冲突返回 409 |
| waiting_decision | 用户拒绝 | decided | user | approval_actions, decided_by, decision_type | approval.updated, delivery.create | 乐观锁冲突返回 409 |
| waiting_decision | 用户自定义回复 | decided | user | approval_actions, payload_redacted | approval.updated, delivery.create | 乐观锁冲突返回 409 |
| waiting_decision | 超时扫描命中 | expired | system | status=expired | approval.expired_internal | 乐观锁冲突忽略 |
| expired | 写入默认拒绝决策 | delivering | system | decision_type=timeout_reject, delivery | approval.updated, delivery.create | 重复不创建新 delivery |
| decided | 创建投递 | delivering | system | approval_deliveries | delivery.pending | 每个审批只创建有效投递 |
| delivering | Agent ACK accepted/written | delivered | device | delivery ack, status=delivered | approval.updated | 重复 ACK 更新 last_seen |
| delivering | Agent ACK failed | delivery_failed | device | ack_result, last_error | approval.updated, alert | 可人工重试 |
| delivering | 重试次数耗尽 | delivery_failed | system | status=delivery_failed | approval.updated, alert | 重复忽略 |
| delivery_failed | 人工重试 | delivering | admin/system | next_attempt_at, attempt_count | delivery.retry | 需要权限 |
| waiting_decision | 本地用户接管 | cancelled_by_local_input | device | status, local detail | approval.updated | 若已 decided 则记录冲突审计 |

约束：

- 用户只能在 `waiting_decision` 提交审批动作。
- `delivered` 是正常终态。
- `delivery_failed` 是可恢复异常态。
- `cancelled_by_local_input` 是终态。
- `expired` 不应长期停留，Worker 应继续创建默认拒绝投递。

## 3. 投递状态

状态：

- `pending`
- `sent`
- `acked`
- `failed`
- `cancelled`

| 当前状态 | 事件 | 下一个状态 | Actor | 主要写入 | 发出事件 | 幂等/冲突 |
|---|---|---|---|---|---|---|
| 无 | 创建投递 | pending | system | approval_deliveries | delivery.pending | 同审批有效投递去重 |
| pending | 设备在线并发送 | sent | gateway | sent_at, attempt_count | approval.decision.deliver | 重复发送需同 delivery_id |
| pending | 设备离线 | pending | system | next_attempt_at | 无 | 等待重试 |
| sent | Agent ACK 成功 | acked | device | acked_at, ack_result | approval.delivered_internal | 重复 ACK 幂等 |
| sent | Agent ACK 失败 | failed | device | acked_at, ack_result, last_error | delivery.failed | 可重试 |
| sent | 发送超时 | pending | worker | next_attempt_at | delivery.retry | attempt_count 增加 |
| pending | 超过重试次数 | failed | worker | status=failed | alert | 重复忽略 |
| failed | 管理员重试 | pending | admin | next_attempt_at | delivery.retry | 需要权限 |
| pending/sent | 审批取消 | cancelled | system | status=cancelled | approval.updated | 只限未决取消场景 |

约束：

- `acked` 是正常终态。
- `failed` 不改变审批决策，只表示回写失败。
- Agent ACK 必须包含 `delivery_id`。

## 4. 设备状态

状态：

- `pending_activation`
- `active`
- `suspect_offline`
- `offline`
- `disabled`

| 当前状态 | 事件 | 下一个状态 | Actor | 主要写入 | 发出事件 | 幂等/冲突 |
|---|---|---|---|---|---|---|
| 无 | 激活码消费成功 | active | user/device | devices, device_tokens | device.created | 激活码唯一消费 |
| active | 45 秒无心跳 | suspect_offline | worker | status | device.status_changed | 重复忽略 |
| suspect_offline | 心跳恢复 | active | device | last_seen_at | device.status_changed | 幂等 |
| suspect_offline | 3 分钟无心跳 | offline | worker | status | device.status_changed | 重复忽略 |
| offline | 重连成功 | active | device | last_seen_at | device.status_changed | 幂等 |
| active/offline/suspect_offline | 管理员禁用 | disabled | admin | disabled_at, token revoked | device.disabled | 重复返回 disabled |
| disabled | Agent 连接 | disabled | device | 拒绝连接审计 | 无 | 返回 device_disabled |

约束：

- disabled 只能由管理员或 owner 解除，MVP 可不支持解除。
- 设备心跳不能让 disabled 设备恢复 active。

## 5. 会话状态

状态：

- `starting`
- `running`
- `waiting_approval`
- `completed`
- `failed`
- `closed`
- `lost`

| 当前状态 | 事件 | 下一个状态 | Actor | 主要写入 | 发出事件 | 幂等/冲突 |
|---|---|---|---|---|---|---|
| 无 | Agent 创建会话 | starting | device | sessions | session.created | session_id 去重 |
| starting | 进程启动成功 | running | device | started_at | session.updated | 幂等 |
| starting | 进程启动失败 | failed | device | ended_at, error | session.updated | 幂等 |
| running | 检测到审批 | waiting_approval | device | status | session.updated | 可有多个审批，状态保持 |
| waiting_approval | 审批回写成功且 CLI 继续 | running | device | status | session.updated | 若仍有待审批则保持 |
| running/waiting_approval | 进程正常退出 | completed | device | ended_at, exit_code | session.updated | 终态 |
| running/waiting_approval | 进程异常退出 | failed | device | ended_at, exit_code, error | session.updated | 终态 |
| running/waiting_approval | 用户关闭托管 | closed | device/user | ended_at | session.updated | 终态 |
| running/waiting_approval | Agent 离线超时 | lost | worker | status | session.updated | Agent 恢复后可修正 |
| lost | Agent 补报会话仍运行 | running | device | last_seen | session.updated | 幂等 |
| lost | Agent 补报已结束 | completed/failed/closed | device | ended_at | session.updated | 终态 |

约束：

- `completed`、`failed`、`closed` 是终态。
- `lost` 是服务端观察状态，不代表 CLI 一定退出。

## 6. 客户端实例状态

状态：

- `active`
- `logged_out`
- `revoked`

| 当前状态 | 事件 | 下一个状态 | Actor | 主要写入 | 发出事件 | 幂等/冲突 |
|---|---|---|---|---|---|---|
| 无 | 客户端登录注册 | active | user | client_instances | client.registered | 幂等键可选 |
| active | WebSocket 心跳 | active | client | last_seen_at | 无 | 幂等 |
| active | 用户退出登录 | logged_out | user | status | client.logged_out | 重复忽略 |
| active/logged_out | 管理员强制注销 | revoked | admin | status | client.revoked | 重复忽略 |
| revoked | 客户端连接 | revoked | client | 拒绝审计 | 无 | 返回 forbidden |

## 7. 通知状态

状态：

- `pending`
- `sent`
- `failed`
- `read`

| 当前状态 | 事件 | 下一个状态 | Actor | 主要写入 | 说明 |
|---|---|---|---|---|---|
| 无 | 创建通知记录 | pending | worker | approval_notifications | 每个目标客户端一条 |
| pending | WebSocket/Push 发送成功 | sent | worker/gateway | sent_at | 不代表用户已读 |
| pending | 发送失败 | failed | worker | failed_at,error | 不影响审批状态 |
| sent | 客户端已读 ACK | read | client | read_at | 可选 |
| failed | 重试发送成功 | sent | worker | sent_at | 移动 Push 可重试 |

## 8. 状态转移实现要求

- 每个状态机实现为服务端 domain service。
- 不允许 handler 直接写状态字段。
- 状态更新 SQL 必须包含当前状态和 version 条件。
- 每次转移返回领域事件，由调用方发布 WebSocket、Push 或 Worker 任务。
- 所有冲突都必须返回稳定错误码。
