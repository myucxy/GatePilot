import {useEffect, useMemo, useState} from 'react';
import './App.css';
import {
  EnsureAgent,
  GetSessionDetail,
  GetStatus,
  ListSessions,
  Login,
  Logout,
  Offline,
  ReplySession,
  SaveSettings,
} from '../wailsjs/go/main/App';

type Settings = {
  mode: string;
  start_on_login: boolean;
  notification_enabled: boolean;
  notification_style: string;
  history_retention_days: number;
  capture_output_mode: string;
  default_cli_type: string;
  server_url: string;
  tenant_id: string;
  device_id: string;
  client_instance_id: string;
};

type Status = {
  settings: Settings;
  logged_in: boolean;
  offline: boolean;
  settings_path: string;
  history_path: string;
  tray_addr: string;
};

type Session = {
  session_id: string;
  cli_type: string;
  command_line_redacted: string;
  working_dir: string;
  status: string;
  started_at: string;
  ended_at?: string;
  last_output_summary: string;
  pending_approval_count: number;
};

type Detail = {
  session: Session;
  output: Record<string, unknown>[];
  approvals: Record<string, unknown>[];
  decisions: Record<string, unknown>[];
};

const emptySettings: Settings = {
  mode: 'offline',
  start_on_login: false,
  notification_enabled: true,
  notification_style: 'mini_window',
  history_retention_days: 30,
  capture_output_mode: 'summary_only',
  default_cli_type: 'custom',
  server_url: '',
  tenant_id: '',
  device_id: '',
  client_instance_id: '',
};

function App() {
  const [tab, setTab] = useState<'settings' | 'history'>('settings');
  const [status, setStatus] = useState<Status | null>(null);
  const [settings, setSettings] = useState<Settings>(emptySettings);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [selectedSession, setSelectedSession] = useState('');
  const [cliFilter, setCliFilter] = useState('');
  const [statusFilter, setStatusFilter] = useState('');
  const [replyText, setReplyText] = useState('');
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');

  const canReply = useMemo(() => {
    const state = detail?.session.status;
    return state === 'running' || state === 'waiting_approval';
  }, [detail]);

  useEffect(() => {
    boot();
  }, []);

  useEffect(() => {
    if (tab === 'history') {
      loadSessions();
    }
  }, [tab, cliFilter, statusFilter]);

  async function boot() {
    try {
      await EnsureAgent();
      await loadStatus();
      await loadSessions();
    } catch (err) {
      showError(err);
    }
  }

  async function loadStatus() {
    const value = await GetStatus();
    setStatus(value);
    setSettings(value.settings);
  }

  async function saveSettings() {
    clearMessages();
    try {
      const saved = await SaveSettings(settings);
      setSettings(saved);
      await loadStatus();
      setNotice('设置已保存。');
    } catch (err) {
      showError(err);
    }
  }

  async function login() {
    clearMessages();
    try {
      await Login({
        server_url: settings.server_url,
        tenant_id: settings.tenant_id,
        device_id: settings.device_id,
        client_instance_id: settings.client_instance_id,
      });
      await loadStatus();
      setNotice('登录/绑定配置已保存。');
    } catch (err) {
      showError(err);
    }
  }

  async function offline() {
    clearMessages();
    try {
      await Offline();
      await loadStatus();
      setNotice('已切换为离线本地模式。');
    } catch (err) {
      showError(err);
    }
  }

  async function logout() {
    clearMessages();
    try {
      await Logout();
      await loadStatus();
      setNotice('已退出登录。');
    } catch (err) {
      showError(err);
    }
  }

  async function loadSessions() {
    try {
      const result = await ListSessions(cliFilter, statusFilter, 100);
      setSessions(result.items || []);
    } catch (err) {
      showError(err);
    }
  }

  async function selectSession(id: string) {
    setSelectedSession(id);
    setReplyText('');
    try {
      setDetail(await GetSessionDetail(id));
    } catch (err) {
      showError(err);
    }
  }

  async function sendReply() {
    if (!selectedSession || !replyText.trim()) return;
    clearMessages();
    try {
      await ReplySession(selectedSession, replyText.trim());
      setReplyText('');
      await selectSession(selectedSession);
      setNotice('回复已发送。');
    } catch (err) {
      showError(err);
    }
  }

  function updateSetting<K extends keyof Settings>(key: K, value: Settings[K]) {
    setSettings((current) => ({...current, [key]: value}));
  }

  function clearMessages() {
    setNotice('');
    setError('');
  }

  function showError(err: unknown) {
    setError(String(err));
  }

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="mark">GP</div>
          <div>
            <h1>GatePilot Agent</h1>
            <p>{status?.offline ? '离线本地模式' : '在线配置模式'}</p>
          </div>
        </div>
        <button className={tab === 'settings' ? 'nav active' : 'nav'} onClick={() => setTab('settings')}>设置</button>
        <button className={tab === 'history' ? 'nav active' : 'nav'} onClick={() => setTab('history')}>会话历史</button>
        <div className="paths">
          <span>托盘地址</span>
          <code>{status?.tray_addr || '127.0.0.1:18731'}</code>
        </div>
      </aside>

      <main className="content">
        <header className="topbar">
          <div>
            <strong>{tab === 'settings' ? '桌面设置' : '本地会话历史'}</strong>
            <span>{status?.logged_in ? '已配置登录身份' : '未登录，默认离线使用'}</span>
          </div>
          <button onClick={boot}>刷新</button>
        </header>

        {notice && <div className="notice ok">{notice}</div>}
        {error && <div className="notice error">{error}</div>}

        {tab === 'settings' ? (
          <section className="page-grid">
            <section className="panel">
              <h2>本地模式与提醒</h2>
              <div className="form-grid">
                <label>运行模式</label>
                <select value={settings.mode} onChange={(e) => updateSetting('mode', e.target.value)}>
                  <option value="offline">离线本地</option>
                  <option value="online">在线配置</option>
                </select>
                <label>启用提醒</label>
                <input type="checkbox" checked={settings.notification_enabled} onChange={(e) => updateSetting('notification_enabled', e.target.checked)} />
                <label>提醒样式</label>
                <select value={settings.notification_style} onChange={(e) => updateSetting('notification_style', e.target.value)}>
                  <option value="mini_window">右下角小窗口</option>
                  <option value="modal_popup">模态弹窗</option>
                  <option value="toast">Toast/小窗口</option>
                  <option value="none">不弹窗</option>
                </select>
                <label>开机启动</label>
                <input type="checkbox" checked={settings.start_on_login} onChange={(e) => updateSetting('start_on_login', e.target.checked)} />
                <label>历史保留天数</label>
                <input type="number" min={1} max={3650} value={settings.history_retention_days} onChange={(e) => updateSetting('history_retention_days', Number(e.target.value || 30))} />
                <label>输出捕获</label>
                <select value={settings.capture_output_mode} onChange={(e) => updateSetting('capture_output_mode', e.target.value)}>
                  <option value="summary_only">只保存摘要</option>
                  <option value="redacted_recent">保存脱敏近期输出</option>
                  <option value="full_local_only">完整本地保存</option>
                </select>
                <label>默认 CLI 类型</label>
                <select value={settings.default_cli_type} onChange={(e) => updateSetting('default_cli_type', e.target.value)}>
                  <option value="custom">custom</option>
                  <option value="codex">codex</option>
                  <option value="claude">claude</option>
                  <option value="gemini">gemini</option>
                  <option value="copilot">copilot</option>
                  <option value="opencode">opencode</option>
                </select>
              </div>
              <div className="actions">
                <button className="primary" onClick={saveSettings}>保存设置</button>
                <button onClick={offline}>切为离线</button>
              </div>
            </section>

            <section className="panel">
              <h2>登录与路径</h2>
              <div className="form-grid">
                <label>服务端地址</label>
                <input value={settings.server_url || ''} onChange={(e) => updateSetting('server_url', e.target.value)} placeholder="http://127.0.0.1:8080" />
                <label>Tenant ID</label>
                <input value={settings.tenant_id || ''} onChange={(e) => updateSetting('tenant_id', e.target.value)} />
                <label>Device ID</label>
                <input value={settings.device_id || ''} onChange={(e) => updateSetting('device_id', e.target.value)} />
                <label>Client Instance ID</label>
                <input value={settings.client_instance_id || ''} onChange={(e) => updateSetting('client_instance_id', e.target.value)} placeholder="可留空，自动注册" />
                <label>设置文件</label>
                <code>{status?.settings_path || '-'}</code>
                <label>历史文件</label>
                <code>{status?.history_path || '-'}</code>
              </div>
              <div className="actions">
                <button className="primary" onClick={login}>登录/绑定</button>
                <button className="danger" onClick={logout}>退出登录</button>
              </div>
            </section>
          </section>
        ) : (
          <section className="history-layout">
            <section className="panel list-panel">
              <div className="filters">
                <select value={cliFilter} onChange={(e) => setCliFilter(e.target.value)}>
                  <option value="">全部 CLI</option>
                  <option value="custom">custom</option>
                  <option value="codex">codex</option>
                  <option value="claude">claude</option>
                  <option value="gemini">gemini</option>
                  <option value="copilot">copilot</option>
                  <option value="opencode">opencode</option>
                </select>
                <select value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
                  <option value="">全部状态</option>
                  <option value="running">running</option>
                  <option value="waiting_approval">waiting_approval</option>
                  <option value="completed">completed</option>
                  <option value="failed">failed</option>
                </select>
              </div>
              <div className="session-list">
                {sessions.map((item) => (
                  <button key={item.session_id} className={selectedSession === item.session_id ? 'session active' : 'session'} onClick={() => selectSession(item.session_id)}>
                    <strong>{item.session_id}</strong>
                    <span>{item.cli_type} / {item.status}</span>
                    <small>{item.working_dir || item.command_line_redacted}</small>
                  </button>
                ))}
                {sessions.length === 0 && <div className="empty">暂无会话。</div>}
              </div>
            </section>

            <section className="panel detail-panel">
              {detail ? (
                <>
                  <h2>{detail.session.session_id}</h2>
                  <div className="kv">
                    <span>状态</span><strong>{detail.session.status}</strong>
                    <span>CLI</span><strong>{detail.session.cli_type}</strong>
                    <span>目录</span><strong>{detail.session.working_dir || '-'}</strong>
                    <span>摘要</span><strong>{detail.session.last_output_summary || '-'}</strong>
                  </div>
                  {canReply && (
                    <div className="reply">
                      <input value={replyText} onChange={(e) => setReplyText(e.target.value)} placeholder="继续回复内容" />
                      <button className="primary" onClick={sendReply}>发送</button>
                    </div>
                  )}
                  <RecordBlock title="输出" records={detail.output} />
                  <RecordBlock title="审批" records={detail.approvals} />
                  <RecordBlock title="决策" records={detail.decisions} />
                </>
              ) : (
                <div className="empty">选择一个会话查看详情。</div>
              )}
            </section>
          </section>
        )}
      </main>
    </div>
  );
}

function RecordBlock({title, records}: {title: string; records: Record<string, unknown>[]}) {
  return (
    <div className="record-block">
      <h3>{title}</h3>
      {records.length === 0 ? <p>无记录。</p> : records.map((record, index) => <pre key={index}>{JSON.stringify(record, null, 2)}</pre>)}
    </div>
  );
}

export default App;
