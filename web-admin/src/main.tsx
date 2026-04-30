import React, { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { Activity, CheckCircle2, Laptop, Radio, RefreshCw, Smartphone } from "lucide-react";
import "./styles.css";

type Language = "zh" | "en";
type ApprovalFilter = "all" | "pending" | "completed";
type WSState = "idle" | "connecting" | "connected" | "closed";

const tenantId = "00000000-0000-0000-0000-000000000100";
const clientInstanceKeyStorage = "gatepilot.web.clientInstanceKey";

const copy = {
  zh: {
    nav: ["总览", "审批", "设备", "会话"],
    title: "开发控制台",
    subtitle: "用于设备绑定、会话查看、审批处理和多端同步联调。",
    schema: "协议 2026-04-01",
    language: "语言",
    createCode: "生成激活码",
    refreshDevices: "刷新设备",
    refreshSessions: "刷新会话",
    refreshApprovals: "刷新审批",
    approve: "批准",
    reject: "拒绝",
    reply: "回复",
    activationCode: "激活码",
    noDevices: "暂无设备",
    noSessions: "选择设备后刷新会话",
    noApprovals: "暂无审批",
    deviceList: "设备列表",
    sessionList: "会话列表",
    approvalList: "审批列表",
    sync: "实时同步",
    client: "客户端实例",
    submitting: "提交中",
    filters: {
      all: "全部",
      pending: "待处理",
      completed: "已完成"
    },
    requestFailed: "请求失败",
    roleInsufficient: "当前角色没有审批权限",
    alreadyDecided: "该审批已被处理，请刷新列表",
    lanes: [
      { title: "服务端", status: "设备、会话、审批与多端同步", icon: Activity },
      { title: "Agent", status: "注册、长连接、fake CLI 托管", icon: Laptop },
      { title: "Web", status: "审批工作台和实时刷新", icon: CheckCircle2 },
      { title: "移动端", status: "Android 9+ 目标壳应用", icon: Smartphone }
    ]
  },
  en: {
    nav: ["Overview", "Approvals", "Devices", "Sessions"],
    title: "Development Console",
    subtitle: "Device binding, session viewing, approval handling, and multi-client sync.",
    schema: "protocol 2026-04-01",
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
    sync: "Live sync",
    client: "Client instance",
    submitting: "Submitting",
    filters: {
      all: "All",
      pending: "Pending",
      completed: "Completed"
    },
    requestFailed: "Request failed",
    roleInsufficient: "Current role cannot submit approval decisions",
    alreadyDecided: "This approval was already decided. Refresh the list.",
    lanes: [
      { title: "Server", status: "devices, sessions, approvals, sync", icon: Activity },
      { title: "Agent", status: "registration, websocket, fake CLI host", icon: Laptop },
      { title: "Web", status: "approval workspace and live refresh", icon: CheckCircle2 },
      { title: "Mobile", status: "Android 9+ shell", icon: Smartphone }
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

type AuditLog = {
	audit_id: number;
	actor_type: string;
	actor_id: string;
  action: string;
  resource_type: string;
  resource_id: string;
  result: string;
  trace_id: string;
	created_at: string;
};

type OutputChunk = {
	chunk_id: number;
	session_id: string;
	sequence_no: number;
	stream_type: string;
	content_redacted: string;
	created_at: string;
};

function App() {
	const [language, setLanguage] = useState<Language>("zh");
	const [activationCode, setActivationCode] = useState<string>("");
	const [devices, setDevices] = useState<Device[]>([]);
	const [selectedDeviceID, setSelectedDeviceID] = useState<string>("");
	const [sessions, setSessions] = useState<Session[]>([]);
	const [selectedSessionID, setSelectedSessionID] = useState<string>("");
	const [outputChunks, setOutputChunks] = useState<OutputChunk[]>([]);
	const [approvals, setApprovals] = useState<Approval[]>([]);
  const [auditLogs, setAuditLogs] = useState<AuditLog[]>([]);
  const [approvalFilter, setApprovalFilter] = useState<ApprovalFilter>("pending");
  const [clientInstanceID, setClientInstanceID] = useState<string>("");
  const [wsState, setWSState] = useState<WSState>("idle");
	const [submittingApprovals, setSubmittingApprovals] = useState<Set<string>>(new Set());
	const [error, setError] = useState<string>("");
	const selectedDeviceRef = useRef("");
	const selectedSessionRef = useRef("");
	const approvalFilterRef = useRef<ApprovalFilter>("pending");
	const text = copy[language];

	useEffect(() => {
		selectedDeviceRef.current = selectedDeviceID;
	}, [selectedDeviceID]);

	useEffect(() => {
		selectedSessionRef.current = selectedSessionID;
	}, [selectedSessionID]);

  useEffect(() => {
    approvalFilterRef.current = approvalFilter;
  }, [approvalFilter]);

  useEffect(() => {
    registerClientInstance().then((id) => {
      if (id) {
        setClientInstanceID(id);
        connectClientWebSocket(id);
      }
    });
    refreshDevices();
    refreshApprovals("pending");
    refreshAuditLogs();
  }, []);

  async function registerClientInstance() {
    setError("");
    const idempotencyKey = getOrCreateClientInstanceKey();
    const response = await fetch("/api/v1/client-instances", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Idempotency-Key": idempotencyKey
      },
      body: JSON.stringify({
        tenant_id: tenantId,
        client_type: "web",
        display_name: browserDisplayName(),
        app_version: "0.1.0",
        platform: "browser"
      })
    });
    if (!response.ok) {
      setError(await responseErrorMessage(response, text));
      return "";
    }
    const body = await response.json();
    return body.data.client_instance_id as string;
  }

  function connectClientWebSocket(instanceID: string) {
    setWSState("connecting");
    const url = new URL("/ws/client", window.location.href);
    url.protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    url.searchParams.set("tenant_id", tenantId);
    url.searchParams.set("client_instance_id", instanceID);

    const socket = new WebSocket(url);
    socket.onmessage = (event) => {
      const message = JSON.parse(event.data);
      if (message.type === "client.connected") {
        setWSState("connected");
        return;
      }
      if (message.type === "approval.created" || message.type === "approval.updated") {
        refreshApprovals(approvalFilterRef.current);
        refreshSessions(selectedDeviceRef.current);
        refreshAuditLogs();
        return;
      }
      if (message.type === "session.updated") {
        refreshSessions(selectedDeviceRef.current);
        if (selectedSessionRef.current === message.payload?.session_id) {
          refreshOutputChunks(selectedSessionRef.current);
        }
        return;
      }
      if (message.type === "device.status_changed") {
        refreshDevices();
      }
    };
    socket.onerror = () => setWSState("closed");
    socket.onclose = () => setWSState("closed");
  }

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
      setError(await responseErrorMessage(response, text));
      return;
    }
    const body = await response.json();
    setActivationCode(body.data.activation_code);
  }

  async function refreshDevices() {
    setError("");
    const response = await fetch(`/api/v1/tenants/${tenantId}/devices`);
    if (!response.ok) {
      setError(await responseErrorMessage(response, text));
      return;
    }
    const body = await response.json();
    const items = body.data.items as Device[];
    setDevices(items);
    if (!selectedDeviceRef.current && items.length > 0) {
      setSelectedDeviceID(items[0].device_id);
      refreshSessions(items[0].device_id);
    }
  }

  async function refreshSessions(deviceID = selectedDeviceRef.current) {
    if (!deviceID) {
      setSessions([]);
      setSelectedSessionID("");
      setOutputChunks([]);
      return;
    }
    setError("");
    const response = await fetch(`/api/v1/devices/${deviceID}/sessions`);
    if (!response.ok) {
      setError(await responseErrorMessage(response, text));
      return;
    }
    const body = await response.json();
    const items = body.data.items as Session[];
    setSessions(items);
    if (selectedSessionRef.current && !items.some((item) => item.session_id === selectedSessionRef.current)) {
      setSelectedSessionID("");
      setOutputChunks([]);
    }
  }

  async function refreshOutputChunks(sessionID = selectedSessionRef.current) {
    if (!sessionID) {
      setOutputChunks([]);
      return;
    }
    setError("");
    const response = await fetch(`/api/v1/sessions/${sessionID}/output-chunks`);
    if (!response.ok) {
      setError(await responseErrorMessage(response, text));
      return;
    }
    const body = await response.json();
    setOutputChunks(body.data.items as OutputChunk[]);
  }

  async function refreshApprovals(filter = approvalFilterRef.current) {
    setError("");
    const statusQuery = filter === "pending" ? "?status=waiting_decision" : "";
    const response = await fetch(`/api/v1/tenants/${tenantId}/approvals${statusQuery}`);
    if (!response.ok) {
      setError(await responseErrorMessage(response, text));
      return;
    }
    const body = await response.json();
    const items = body.data.items as Approval[];
    const completedStatuses = new Set(["delivered", "delivery_failed", "expired", "cancelled_by_local_input"]);
    setApprovals(filter === "completed" ? items.filter((item) => completedStatuses.has(item.status)) : items);
  }

  async function refreshAuditLogs() {
    setError("");
    const response = await fetch(`/api/v1/tenants/${tenantId}/audit-logs`);
    if (!response.ok) {
      setError(await responseErrorMessage(response, text));
      return;
    }
    const body = await response.json();
    setAuditLogs(body.data.items as AuditLog[]);
  }

  function changeApprovalFilter(filter: ApprovalFilter) {
    setApprovalFilter(filter);
    refreshApprovals(filter);
  }

  async function decideApproval(approvalID: string, decisionType: "approve" | "reject" | "reply") {
    setError("");
    setSubmittingApprovals((current) => new Set(current).add(approvalID));
    try {
      const response = await fetch(`/api/v1/approvals/${approvalID}/decision`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": crypto.randomUUID(),
          "X-Client-Instance-Id": clientInstanceID
        },
        body: JSON.stringify({
          decision_type: decisionType,
          payload: decisionType === "reply" ? "continue with tests only" : ""
        })
      });
      if (!response.ok) {
        setError(await responseErrorMessage(response, text));
        return;
      }
      await refreshApprovals();
      await refreshSessions();
      await refreshAuditLogs();
    } finally {
      setSubmittingApprovals((current) => {
        const next = new Set(current);
        next.delete(approvalID);
        return next;
      });
    }
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
            <span className={`syncPill ${wsState}`}>
              <Radio size={14} />
              {text.sync}: {wsState}
            </span>
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
            <div>
              <h2>{text.deviceList}</h2>
              <p className="metaText">{text.client}: {shortID(clientInstanceID) || "-"}</p>
            </div>
            <div className="toolActions">
              <button onClick={createActivationCode}>{text.createCode}</button>
              <button onClick={refreshDevices}>
                <RefreshCw size={16} />
                {text.refreshDevices}
              </button>
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
                    setSelectedSessionID("");
                    setOutputChunks([]);
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
              <button onClick={() => refreshSessions()}>
                <RefreshCw size={16} />
                {text.refreshSessions}
              </button>
            </div>
          </div>
          <div className="deviceTable">
            {sessions.length === 0 ? (
              <p>{text.noSessions}</p>
            ) : (
              sessions.map((session) => (
                <article
                  className={`deviceRow ${selectedSessionID === session.session_id ? "selected" : ""}`}
                  key={session.session_id}
                  onClick={() => {
                    setSelectedSessionID(session.session_id);
                    refreshOutputChunks(session.session_id);
                  }}
                >
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
            <h2>Output replay</h2>
            <div className="toolActions">
              <button onClick={() => refreshOutputChunks()}>
                <RefreshCw size={16} />
                Refresh output
              </button>
            </div>
          </div>
          <div className="outputList">
            {outputChunks.length === 0 ? (
              <p>No output chunks</p>
            ) : (
              outputChunks.map((chunk) => (
                <article className="outputRow" key={chunk.chunk_id}>
                  <span>#{chunk.sequence_no} {chunk.stream_type}</span>
                  <pre>{chunk.content_redacted}</pre>
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
              <button onClick={() => refreshApprovals()}>
                <RefreshCw size={16} />
                {text.refreshApprovals}
              </button>
            </div>
          </div>
          <div className="approvalList">
            {approvals.length === 0 ? (
              <p>{text.noApprovals}</p>
            ) : (
              approvals.map((approval) => {
                const isSubmitting = submittingApprovals.has(approval.approval_id);
                return (
                  <article className="approvalRow" key={approval.approval_id}>
                    <div>
                      <strong>{approval.prompt_text}</strong>
                      <p>{approval.cli_type} / {approval.risk_level} / {approval.status}</p>
                    </div>
                    {approval.status === "waiting_decision" ? (
                      <div className="toolActions">
                        <button disabled={isSubmitting} onClick={() => decideApproval(approval.approval_id, "approve")}>
                          {isSubmitting ? text.submitting : text.approve}
                        </button>
                        <button disabled={isSubmitting} onClick={() => decideApproval(approval.approval_id, "reject")}>
                          {text.reject}
                        </button>
                        <button disabled={isSubmitting} onClick={() => decideApproval(approval.approval_id, "reply")}>
                          {text.reply}
                        </button>
                      </div>
                    ) : null}
                  </article>
                );
              })
            )}
          </div>
        </section>
        <section className="toolPanel">
          <div className="toolHeader">
            <h2>{language === "zh" ? "审计日志" : "Audit logs"}</h2>
            <div className="toolActions">
              <button onClick={refreshAuditLogs}>
                <RefreshCw size={16} />
                {language === "zh" ? "刷新审计" : "Refresh audit"}
              </button>
            </div>
          </div>
          <div className="approvalList">
            {auditLogs.length === 0 ? (
              <p>{language === "zh" ? "暂无审计日志" : "No audit logs"}</p>
            ) : (
              auditLogs.map((log) => (
                <article className="auditRow" key={log.audit_id}>
                  <strong>{log.action}</strong>
                  <span>{log.actor_type} / {log.result}</span>
                  <span>{shortID(log.resource_id)} / {new Date(log.created_at).toLocaleString()}</span>
                </article>
              ))
            )}
          </div>
        </section>
      </section>
    </main>
  );
}

function getOrCreateClientInstanceKey() {
  const existing = localStorage.getItem(clientInstanceKeyStorage);
  if (existing) {
    return existing;
  }
  const next = crypto.randomUUID();
  localStorage.setItem(clientInstanceKeyStorage, next);
  return next;
}

function browserDisplayName() {
  const platform = navigator.platform || "Browser";
  return `Web ${platform}`;
}

function shortID(value: string) {
  return value ? value.slice(0, 8) : "";
}

async function responseErrorMessage(response: Response, text: typeof copy.zh) {
  try {
    const body = await response.json();
    const code = body.error?.code;
    if (code === "role_insufficient") {
      return text.roleInsufficient;
    }
    if (code === "approval_already_decided") {
      return text.alreadyDecided;
    }
    return `${text.requestFailed}: ${response.status} ${code || ""}`.trim();
  } catch {
    return `${text.requestFailed}: ${response.status}`;
  }
}

createRoot(document.getElementById("root")!).render(<App />);
