import React, { useState } from "react";
import { createRoot } from "react-dom/client";
import { Activity, CheckCircle2, Laptop, Smartphone } from "lucide-react";
import "./styles.css";

type Language = "zh" | "en";
type ApprovalFilter = "all" | "pending" | "completed";

const tenantId = "00000000-0000-0000-0000-000000000100";

const copy = {
  zh: {
    nav: ["总览", "审批", "设备", "会话"],
    title: "开发控制台",
    subtitle: "契约优先的 M0 工作台，用于 Server、Agent、Web、Mobile 并行开发。",
    schema: "契约 2026-04-01",
    language: "语言",
    createCode: "生成设备激活码",
    refreshDevices: "刷新设备",
    refreshSessions: "刷新会话",
    refreshApprovals: "刷新审批",
    approve: "批准",
    reject: "拒绝",
    reply: "回复",
    activationCode: "激活码",
    noDevices: "暂无设备",
    noSessions: "请选择设备并刷新会话",
    noApprovals: "暂无审批",
    deviceList: "设备列表",
    sessionList: "会话列表",
    approvalList: "审批列表",
    filters: {
      all: "全部",
      pending: "待处理",
      completed: "已完成"
    },
    requestFailed: "请求失败",
    lanes: [
      { title: "服务端", status: "M0 骨架", icon: Activity },
      { title: "Agent", status: "版本命令 + fake CLI", icon: Laptop },
      { title: "Web", status: "管理端壳应用", icon: CheckCircle2 },
      { title: "移动端", status: "Android 9+ 目标", icon: Smartphone }
    ]
  },
  en: {
    nav: ["Overview", "Approvals", "Devices", "Sessions"],
    title: "Development Console",
    subtitle: "Contract-first M0 workspace for parallel Server, Agent, Web, and Mobile development.",
    schema: "schema 2026-04-01",
    language: "Language",
    createCode: "Create activation code",
    refreshDevices: "Refresh devices",
    refreshSessions: "Refresh sessions",
    refreshApprovals: "Refresh approvals",
    approve: "Approve",
    reject: "Reject",
    reply: "Reply",
    activationCode: "Activation code",
    noDevices: "No devices yet",
    noSessions: "Select a device and refresh sessions",
    noApprovals: "No approvals",
    deviceList: "Devices",
    sessionList: "Sessions",
    approvalList: "Approvals",
    filters: {
      all: "All",
      pending: "Pending",
      completed: "Completed"
    },
    requestFailed: "Request failed",
    lanes: [
      { title: "Server", status: "M0 skeleton", icon: Activity },
      { title: "Agent", status: "version + fake CLI", icon: Laptop },
      { title: "Web", status: "admin shell", icon: CheckCircle2 },
      { title: "Mobile", status: "Android 9+ target", icon: Smartphone }
    ]
  }
};

type Device = {
  device_id: string;
  name: string;
  platform: string;
  arch: string;
  status: string;
  last_seen_at: string;
};

type Session = {
  session_id: string;
  cli_type: string;
  status: string;
  started_at: string;
  last_output_summary: string;
  pending_approval_count: number;
};

type Approval = {
  approval_id: string;
  session_id: string;
  cli_type: string;
  event_type: string;
  risk_level: string;
  prompt_text: string;
  status: string;
  delivery_id: string;
  delivery_status: string;
  decision_type: string;
  created_at: string;
  expires_at: string;
};

// 当前页面是并行开发工作台，占位数据只表达各端开工状态；真实数据接入由 schema 生成的 API client 完成。
function App() {
  const [language, setLanguage] = useState<Language>("zh");
  const [activationCode, setActivationCode] = useState<string>("");
  const [devices, setDevices] = useState<Device[]>([]);
  const [selectedDeviceID, setSelectedDeviceID] = useState<string>("");
  const [sessions, setSessions] = useState<Session[]>([]);
  const [approvals, setApprovals] = useState<Approval[]>([]);
  const [approvalFilter, setApprovalFilter] = useState<ApprovalFilter>("pending");
  const [error, setError] = useState<string>("");
  const text = copy[language];

  async function createActivationCode() {
    setError("");
    const response = await fetch(`/api/v1/tenants/${tenantId}/device-activation-codes`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Idempotency-Key": crypto.randomUUID()
      },
      body: JSON.stringify({ name: "开发测试设备", expires_in_seconds: 600 })
    });
    if (!response.ok) {
      setError(`${text.requestFailed}: ${response.status}`);
      return;
    }
    const body = await response.json();
    setActivationCode(body.data.activation_code);
  }

  async function refreshDevices() {
    setError("");
    const response = await fetch(`/api/v1/tenants/${tenantId}/devices`);
    if (!response.ok) {
      setError(`${text.requestFailed}: ${response.status}`);
      return;
    }
    const body = await response.json();
    const items = body.data.items as Device[];
    setDevices(items);
    if (!selectedDeviceID && items.length > 0) {
      setSelectedDeviceID(items[0].device_id);
    }
  }

  async function refreshSessions(deviceID = selectedDeviceID) {
    if (!deviceID) {
      setSessions([]);
      return;
    }
    setError("");
    const response = await fetch(`/api/v1/devices/${deviceID}/sessions`);
    if (!response.ok) {
      setError(`${text.requestFailed}: ${response.status}`);
      return;
    }
    const body = await response.json();
    setSessions(body.data.items);
  }

  async function refreshApprovals(filter = approvalFilter) {
    setError("");
    const statusQuery = filter === "pending" ? "?status=waiting_decision" : "";
    const response = await fetch(`/api/v1/tenants/${tenantId}/approvals${statusQuery}`);
    if (!response.ok) {
      setError(`${text.requestFailed}: ${response.status}`);
      return;
    }
    const body = await response.json();
    const items = body.data.items as Approval[];
    // “已完成”只聚合终态，delivering 这类中间态保留在“全部”中可见。
    const completedStatuses = new Set(["delivered", "delivery_failed", "expired", "cancelled_by_local_input"]);
    setApprovals(filter === "completed" ? items.filter((item) => completedStatuses.has(item.status)) : items);
  }

  function changeApprovalFilter(filter: ApprovalFilter) {
    setApprovalFilter(filter);
    refreshApprovals(filter);
  }

  async function decideApproval(approvalID: string, decisionType: "approve" | "reject" | "reply") {
    setError("");
    const response = await fetch(`/api/v1/approvals/${approvalID}/decision`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Idempotency-Key": crypto.randomUUID(),
        "X-Client-Instance-Id": "00000000-0000-0000-0000-000000000200"
      },
      body: JSON.stringify({
        decision_type: decisionType,
        payload: decisionType === "reply" ? "continue with tests only" : ""
      })
    });
    if (!response.ok) {
      setError(`${text.requestFailed}: ${response.status}`);
      return;
    }
    await refreshApprovals();
    await refreshSessions();
  }

  return (
    <main className="shell">
      <aside className="nav">
        <div className="brand">GatePilot</div>
        {text.nav.map((item, index) => (
          <button className={`navItem ${index === 0 ? "active" : ""}`} key={item}>
            {item}
          </button>
        ))}
      </aside>
      <section className="content">
        <header className="topbar">
          <div>
            <h1>{text.title}</h1>
            <p>{text.subtitle}</p>
          </div>
          <div className="topActions" aria-label={text.language}>
            <div className="languageSwitch">
              <button className={language === "zh" ? "selected" : ""} onClick={() => setLanguage("zh")}>
                中文
              </button>
              <button className={language === "en" ? "selected" : ""} onClick={() => setLanguage("en")}>
                EN
              </button>
            </div>
            <span className="pill">{text.schema}</span>
          </div>
        </header>
        <div className="grid">
          {text.lanes.map((lane) => {
            const Icon = lane.icon;
            return (
              <article className="card" key={lane.title}>
                <Icon size={22} />
                <h2>{lane.title}</h2>
                <p>{lane.status}</p>
              </article>
            );
          })}
        </div>
        <section className="toolPanel">
          <div className="toolHeader">
            <h2>{text.deviceList}</h2>
            <div className="toolActions">
              <button onClick={createActivationCode}>{text.createCode}</button>
              <button onClick={refreshDevices}>{text.refreshDevices}</button>
            </div>
          </div>
          {activationCode ? (
            <div className="activationBox">
              <span>{text.activationCode}</span>
              <strong>{activationCode}</strong>
            </div>
          ) : null}
          {error ? <p className="errorText">{error}</p> : null}
          <div className="deviceTable">
            {devices.length === 0 ? (
              <p>{text.noDevices}</p>
            ) : (
              devices.map((device) => (
                <article
                  className={`deviceRow ${selectedDeviceID === device.device_id ? "selected" : ""}`}
                  key={device.device_id}
                  onClick={() => {
                    setSelectedDeviceID(device.device_id);
                    refreshSessions(device.device_id);
                  }}
                >
                  <strong>{device.name}</strong>
                  <span>{device.platform} / {device.arch}</span>
                  <span>{device.status}</span>
                </article>
              ))
            )}
          </div>
        </section>
        <section className="toolPanel">
          <div className="toolHeader">
            <h2>{text.sessionList}</h2>
            <div className="toolActions">
              <button onClick={() => refreshSessions()}>{text.refreshSessions}</button>
            </div>
          </div>
          <div className="deviceTable">
            {sessions.length === 0 ? (
              <p>{text.noSessions}</p>
            ) : (
              sessions.map((session) => (
                <article className="deviceRow" key={session.session_id}>
                  <strong>{session.cli_type}</strong>
                  <span>{session.status}</span>
                  <span>{session.last_output_summary}</span>
                </article>
              ))
            )}
          </div>
        </section>
        <section className="toolPanel">
          <div className="toolHeader">
            <h2>{text.approvalList}</h2>
            <div className="toolActions">
              {(["all", "pending", "completed"] as ApprovalFilter[]).map((filter) => (
                <button
                  className={approvalFilter === filter ? "selectedAction" : ""}
                  key={filter}
                  onClick={() => changeApprovalFilter(filter)}
                >
                  {text.filters[filter]}
                </button>
              ))}
              <button onClick={() => refreshApprovals()}>{text.refreshApprovals}</button>
            </div>
          </div>
          <div className="approvalList">
            {approvals.length === 0 ? (
              <p>{text.noApprovals}</p>
            ) : (
              approvals.map((approval) => (
                <article className="approvalRow" key={approval.approval_id}>
                  <div>
                    <strong>{approval.prompt_text}</strong>
                    <p>{approval.cli_type} / {approval.risk_level} / {approval.status}</p>
                  </div>
                  {approval.status === "waiting_decision" ? (
                    <div className="toolActions">
                      <button onClick={() => decideApproval(approval.approval_id, "approve")}>{text.approve}</button>
                      <button onClick={() => decideApproval(approval.approval_id, "reject")}>{text.reject}</button>
                      <button onClick={() => decideApproval(approval.approval_id, "reply")}>{text.reply}</button>
                    </div>
                  ) : null}
                </article>
              ))
            )}
          </div>
        </section>
      </section>
    </main>
  );
}

createRoot(document.getElementById("root")!).render(<App />);
