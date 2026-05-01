import {useEffect, useMemo, useState} from 'react';
import './App.css';
import {
  ContinueAIToolSession,
  DeleteAIToolSession,
  DetectAIToolDefaults,
  EnsureAgent,
  GetAIToolSessionDetail,
  GetSessionDetail,
  GetStatus,
  InitialView,
  ListAIToolSessions,
  ListSessions,
  Login,
  Logout,
  Offline,
  ReplySession,
  SaveSettings,
} from '../wailsjs/go/main/App';

type Tab = 'processes' | 'ai-history' | 'settings';

type AIToolConfig = {
  tool_id: string;
  tool_type: string;
  display_name: string;
  enabled: boolean;
  home_dir: string;
  history_path: string;
  sessions_dir: string;
  executable_path: string;
  continue_command_template: string;
};

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
  ai_tools: AIToolConfig[];
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
  control_addr?: string;
};

type Detail = {
  session: Session;
  output: Record<string, unknown>[];
  approvals: Record<string, unknown>[];
  decisions: Record<string, unknown>[];
};

type AIToolSession = {
  id: string;
  tool_id: string;
  tool_type: string;
  display_name: string;
  title: string;
  working_dir: string;
  created_at: string;
  updated_at: string;
  message_count: number;
  preview: string;
  source_path: string;
  can_continue: boolean;
};

type AIToolDetail = {
  session: AIToolSession;
  messages: Record<string, unknown>[];
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
  ai_tools: [],
};

const emptyTool: AIToolConfig = {
  tool_id: '',
  tool_type: 'codex',
  display_name: '',
  enabled: true,
  home_dir: '',
  history_path: '',
  sessions_dir: '',
  executable_path: '',
  continue_command_template: '',
};

function App() {
  const [tab, setTab] = useState<Tab>('processes');
  const [status, setStatus] = useState<Status | null>(null);
  const [settings, setSettings] = useState<Settings>(emptySettings);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [aiSessions, setAiSessions] = useState<AIToolSession[]>([]);
  const [aiDetail, setAiDetail] = useState<AIToolDetail | null>(null);
  const [selectedSession, setSelectedSession] = useState('');
  const [selectedAIToolSession, setSelectedAIToolSession] = useState('');
  const [cliFilter, setCliFilter] = useState('');
  const [statusFilter, setStatusFilter] = useState('');
  const [aiToolFilter, setAiToolFilter] = useState('');
  const [query, setQuery] = useState('');
  const [replyText, setReplyText] = useState('');
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');

  const enabledTools = useMemo(() => (settings.ai_tools || []).filter((tool) => tool.enabled && tool.tool_id), [settings.ai_tools]);
  const runningCount = sessions.filter((item) => item.status === 'running' || item.status === 'waiting_approval').length;
  const pendingCount = sessions.reduce((sum, item) => sum + (item.pending_approval_count || 0), 0);
  const canReply = detail?.session.status === 'running' || detail?.session.status === 'waiting_approval';

  useEffect(() => {
    boot();
  }, []);

  useEffect(() => {
    if (tab === 'processes') {
      loadSessions();
    }
    if (tab === 'ai-history') {
      loadAIToolSessions();
    }
  }, [tab, cliFilter, statusFilter, aiToolFilter, query]);

  async function boot() {
    try {
      const initial = await InitialView();
      if (initial === 'settings') setTab('settings');
      if (initial === 'history') setTab('processes');
      await EnsureAgent();
      await loadStatus();
      await loadSessions();
      await loadAIToolSessions();
    } catch (err) {
      showError(err);
    }
  }

  async function loadStatus() {
    const value = await GetStatus();
    setStatus(value);
    setSettings({...emptySettings, ...value.settings, ai_tools: value.settings.ai_tools || []});
  }

  async function loadSessions() {
    try {
      const result = await ListSessions(cliFilter, statusFilter, 200);
      setSessions(result.items || []);
    } catch (err) {
      showError(err);
    }
  }

  async function loadAIToolSessions() {
    try {
      const result = await ListAIToolSessions(aiToolFilter, query, 200);
      setAiSessions(result.items || []);
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

  async function selectAIToolSession(item: AIToolSession) {
    setSelectedAIToolSession(`${item.tool_id}:${item.id}`);
    try {
      setAiDetail((await GetAIToolSessionDetail(item.tool_id, item.id)) as unknown as AIToolDetail);
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
      setNotice('回复已发送到对应 GP 子进程。');
    } catch (err) {
      showError(err);
    }
  }

  async function saveSettings() {
    clearMessages();
    try {
      const saved = await SaveSettings(settings as any);
      setSettings({...emptySettings, ...saved, ai_tools: saved.ai_tools || []});
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
      setNotice('登录配置已保存。');
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
      setNotice('已退出登录配置。');
    } catch (err) {
      showError(err);
    }
  }

  async function detectTools() {
    clearMessages();
    try {
      const defaults = await DetectAIToolDefaults();
      const current = settings.ai_tools || [];
      const merged = [...current];
      for (const tool of defaults.items || []) {
        if (!merged.some((item) => item.tool_id === tool.tool_id)) merged.push(tool);
      }
      updateSetting('ai_tools', merged);
      setNotice('已检测 Codex 和 Claude 默认位置，保存设置后生效。');
    } catch (err) {
      showError(err);
    }
  }

  async function continueAI() {
    if (!aiDetail) return;
    clearMessages();
    try {
      await ContinueAIToolSession(aiDetail.session.tool_id, aiDetail.session.id);
      setNotice('已在终端打开继续会话命令。');
    } catch (err) {
      showError(err);
    }
  }

  async function deleteAI() {
    if (!aiDetail) return;
    if (!window.confirm('确认删除这条本地 AI 历史记录？删除前会自动备份原历史文件。')) return;
    clearMessages();
    try {
      await DeleteAIToolSession(aiDetail.session.tool_id, aiDetail.session.id);
      setAiDetail(null);
      setSelectedAIToolSession('');
      await loadAIToolSessions();
      setNotice('AI 会话已从配置的本地历史中移除。');
    } catch (err) {
      showError(err);
    }
  }

  function updateSetting<K extends keyof Settings>(key: K, value: Settings[K]) {
    setSettings((current) => ({...current, [key]: value}));
  }

  function updateTool(index: number, patch: Partial<AIToolConfig>) {
    const next = [...(settings.ai_tools || [])];
    next[index] = {...next[index], ...patch};
    updateSetting('ai_tools', next);
  }

  function addTool() {
    updateSetting('ai_tools', [...(settings.ai_tools || []), {...emptyTool, tool_id: `tool_${Date.now()}`}]);
  }

  function removeTool(index: number) {
    updateSetting('ai_tools', (settings.ai_tools || []).filter((_, itemIndex) => itemIndex !== index));
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
        <button className={tab === 'processes' ? 'nav active' : 'nav'} onClick={() => setTab('processes')}>GP 进程</button>
        <button className={tab === 'ai-history' ? 'nav active' : 'nav'} onClick={() => setTab('ai-history')}>AI 历史</button>
        <button className={tab === 'settings' ? 'nav active' : 'nav'} onClick={() => setTab('settings')}>设置</button>
        <div className="paths">
          <span>托盘接口</span>
          <code>{status?.tray_addr || '127.0.0.1:18731'}</code>
        </div>
      </aside>

      <main className="content">
        <header className="topbar">
          <div>
            <strong>{tabTitle(tab)}</strong>
            <span>{status?.logged_in ? '已配置登录身份' : '默认离线使用，无需登录'} · {runningCount} 个运行中 · {pendingCount} 个待确认</span>
          </div>
          <button onClick={boot}>刷新</button>
        </header>

        {notice && <div className="notice ok">{notice}</div>}
        {error && <div className="notice error">{error}</div>}

        {tab === 'processes' && (
          <ProcessView
            sessions={sessions}
            detail={detail}
            selectedSession={selectedSession}
            cliFilter={cliFilter}
            setCliFilter={setCliFilter}
            statusFilter={statusFilter}
            setStatusFilter={setStatusFilter}
            selectSession={selectSession}
            reload={loadSessions}
            canReply={canReply}
            replyText={replyText}
            setReplyText={setReplyText}
            sendReply={sendReply}
          />
        )}
        {tab === 'ai-history' && (
          <AIHistoryView
            enabledTools={enabledTools}
            aiToolFilter={aiToolFilter}
            setAiToolFilter={setAiToolFilter}
            query={query}
            setQuery={setQuery}
            aiSessions={aiSessions}
            aiDetail={aiDetail}
            selectedAIToolSession={selectedAIToolSession}
            selectAIToolSession={selectAIToolSession}
            continueAI={continueAI}
            deleteAI={deleteAI}
          />
        )}
        {tab === 'settings' && (
          <SettingsView
            settings={settings}
            status={status}
            updateSetting={updateSetting}
            updateTool={updateTool}
            addTool={addTool}
            removeTool={removeTool}
            detectTools={detectTools}
            saveSettings={saveSettings}
            login={login}
            logout={logout}
            offline={offline}
          />
        )}
      </main>
    </div>
  );
}

function ProcessView(props: {
  sessions: Session[];
  detail: Detail | null;
  selectedSession: string;
  cliFilter: string;
  setCliFilter: (value: string) => void;
  statusFilter: string;
  setStatusFilter: (value: string) => void;
  selectSession: (id: string) => void;
  reload: () => void;
  canReply: boolean;
  replyText: string;
  setReplyText: (value: string) => void;
  sendReply: () => void;
}) {
  return (
    <section className="history-layout process-layout">
      <section className="panel list-panel">
        <div className="filters vertical">
          <div className="filters inline">
            <select value={props.cliFilter} onChange={(e) => props.setCliFilter(e.target.value)}>
              <option value="">全部 CLI</option>
              <option value="codex">codex</option>
              <option value="claude_code">claude_code</option>
              <option value="custom">custom</option>
              <option value="gemini">gemini</option>
              <option value="copilot">copilot</option>
              <option value="opencode">opencode</option>
            </select>
            <select value={props.statusFilter} onChange={(e) => props.setStatusFilter(e.target.value)}>
              <option value="">全部状态</option>
              <option value="running">running</option>
              <option value="waiting_approval">waiting_approval</option>
              <option value="completed">completed</option>
              <option value="failed">failed</option>
            </select>
          </div>
          <button onClick={props.reload}>刷新进程列表</button>
        </div>
        <div className="session-list">
          {props.sessions.map((item) => (
            <button key={item.session_id} className={props.selectedSession === item.session_id ? 'session active' : 'session'} onClick={() => props.selectSession(item.session_id)}>
              <strong>{item.command_line_redacted || item.session_id}</strong>
              <span>{item.cli_type} · {statusLabel(item.status)} · {formatDate(item.started_at)}</span>
              <small>{item.working_dir || item.session_id}</small>
            </button>
          ))}
          {props.sessions.length === 0 && <div className="empty">暂无 GP 托管进程。使用 `gp codex` 或 `gp claude` 启动后会显示在这里。</div>}
        </div>
      </section>

      <section className="panel detail-panel">
        {props.detail ? (
          <ProcessDetail
            detail={props.detail}
            canReply={props.canReply}
            replyText={props.replyText}
            setReplyText={props.setReplyText}
            sendReply={props.sendReply}
          />
        ) : (
          <div className="empty">请选择一个 GP 子进程查看目录、命令、输出、审批和决策。</div>
        )}
      </section>
    </section>
  );
}

function ProcessDetail(props: {
  detail: Detail;
  canReply: boolean;
  replyText: string;
  setReplyText: (value: string) => void;
  sendReply: () => void;
}) {
  const {session} = props.detail;
  return (
    <>
      <div className="detail-title">
        <h2>{session.command_line_redacted || session.session_id}</h2>
        <span className={`status-pill ${session.status}`}>{statusLabel(session.status)}</span>
      </div>
      <div className="summary-grid">
        <InfoCard label="会话 ID" value={session.session_id} />
        <InfoCard label="CLI" value={session.cli_type} />
        <InfoCard label="工作目录" value={session.working_dir || '-'} wide />
        <InfoCard label="命令" value={session.command_line_redacted || '-'} wide />
        <InfoCard label="开始时间" value={formatDate(session.started_at)} />
        <InfoCard label="结束时间" value={formatDate(session.ended_at || '')} />
        <InfoCard label="控制端口" value={session.control_addr || '-'} />
        <InfoCard label="待确认" value={String(session.pending_approval_count || 0)} />
      </div>
      <section className="panel-section">
        <h3>当前摘要</h3>
        <p>{session.last_output_summary || '暂无摘要'}</p>
      </section>
      {props.canReply && (
        <section className="reply-bar">
          <input value={props.replyText} onChange={(e) => props.setReplyText(e.target.value)} placeholder="发送到该 GP 子进程的输入" />
          <button className="primary" onClick={props.sendReply}>发送</button>
        </section>
      )}
      <ReadableOutput title="输出内容" records={props.detail.output} />
      <ApprovalList title="审批请求" records={props.detail.approvals} />
      <DecisionList title="已写回决策" records={props.detail.decisions} />
    </>
  );
}

function AIHistoryView(props: {
  enabledTools: AIToolConfig[];
  aiToolFilter: string;
  setAiToolFilter: (value: string) => void;
  query: string;
  setQuery: (value: string) => void;
  aiSessions: AIToolSession[];
  aiDetail: AIToolDetail | null;
  selectedAIToolSession: string;
  selectAIToolSession: (item: AIToolSession) => void;
  continueAI: () => void;
  deleteAI: () => void;
}) {
  return (
    <section className="history-layout">
      <section className="panel list-panel">
        <div className="filters vertical">
          <select value={props.aiToolFilter} onChange={(e) => props.setAiToolFilter(e.target.value)}>
            <option value="">全部已配置工具</option>
            {props.enabledTools.map((tool) => (
              <option value={tool.tool_id} key={tool.tool_id}>{tool.display_name || tool.tool_id}</option>
            ))}
          </select>
          <input value={props.query} onChange={(e) => props.setQuery(e.target.value)} placeholder="搜索标题、目录或提示内容" />
        </div>
        <div className="session-list">
          {props.aiSessions.map((item) => (
            <button key={`${item.tool_id}:${item.id}`} className={props.selectedAIToolSession === `${item.tool_id}:${item.id}` ? 'session active' : 'session'} onClick={() => props.selectAIToolSession(item)}>
              <strong>{item.title || item.id}</strong>
              <span>{item.display_name} · {item.message_count} 条消息 · {formatDate(item.updated_at)}</span>
              <small>{item.working_dir || item.preview || item.id}</small>
            </button>
          ))}
          {props.aiSessions.length === 0 && <div className="empty">暂无已配置 AI 工具的历史会话。</div>}
        </div>
      </section>
      <section className="panel detail-panel">
        {props.aiDetail ? (
          <>
            <div className="detail-title">
              <h2>{props.aiDetail.session.title || props.aiDetail.session.id}</h2>
              <div className="actions compact">
                <button className="primary" onClick={props.continueAI}>继续</button>
                <button className="danger" onClick={props.deleteAI}>删除</button>
              </div>
            </div>
            <div className="summary-grid">
              <InfoCard label="工具" value={props.aiDetail.session.display_name} />
              <InfoCard label="会话 ID" value={props.aiDetail.session.id} />
              <InfoCard label="目录" value={props.aiDetail.session.working_dir || '-'} wide />
              <InfoCard label="来源文件" value={props.aiDetail.session.source_path || '-'} wide />
            </div>
            <ReadableMessages title="消息内容" records={props.aiDetail.messages} />
          </>
        ) : (
          <div className="empty">请选择一个 AI 历史会话。</div>
        )}
      </section>
    </section>
  );
}

function SettingsView(props: {
  settings: Settings;
  status: Status | null;
  updateSetting: <K extends keyof Settings>(key: K, value: Settings[K]) => void;
  updateTool: (index: number, patch: Partial<AIToolConfig>) => void;
  addTool: () => void;
  removeTool: (index: number) => void;
  detectTools: () => void;
  saveSettings: () => void;
  login: () => void;
  logout: () => void;
  offline: () => void;
}) {
  const {settings, status, updateSetting, updateTool, addTool, removeTool} = props;
  return (
    <section className="page-stack">
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
              <option value="toast">Toast</option>
              <option value="none">不提醒</option>
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
          </div>
          <div className="actions">
            <button className="primary" onClick={props.saveSettings}>保存设置</button>
            <button onClick={props.offline}>切换离线</button>
          </div>
        </section>

        <section className="panel">
          <h2>登录与路径</h2>
          <div className="form-grid">
            <label>服务端地址</label>
            <input value={settings.server_url || ''} onChange={(e) => updateSetting('server_url', e.target.value)} placeholder="http://127.0.0.1:8080" />
            <label>租户 ID</label>
            <input value={settings.tenant_id || ''} onChange={(e) => updateSetting('tenant_id', e.target.value)} />
            <label>设备 ID</label>
            <input value={settings.device_id || ''} onChange={(e) => updateSetting('device_id', e.target.value)} />
            <label>客户端实例 ID</label>
            <input value={settings.client_instance_id || ''} onChange={(e) => updateSetting('client_instance_id', e.target.value)} />
            <label>设置文件</label>
            <code>{status?.settings_path || '-'}</code>
            <label>GatePilot 历史</label>
            <code>{status?.history_path || '-'}</code>
          </div>
          <div className="actions">
            <button className="primary" onClick={props.login}>登录 / 绑定</button>
            <button className="danger" onClick={props.logout}>退出登录</button>
          </div>
        </section>
      </section>

      <section className="panel">
        <div className="panel-head">
          <h2>AI 工具历史来源</h2>
          <div className="actions compact">
            <button onClick={props.detectTools}>检测默认路径</button>
            <button onClick={addTool}>添加工具</button>
          </div>
        </div>
        <div className="tool-list">
          {(settings.ai_tools || []).map((tool, index) => (
            <div className="tool-row" key={`${tool.tool_id}-${index}`}>
              <div className="tool-main">
                <input type="checkbox" checked={tool.enabled} onChange={(e) => updateTool(index, {enabled: e.target.checked})} />
                <select value={tool.tool_type} onChange={(e) => updateTool(index, {tool_type: e.target.value})}>
                  <option value="codex">Codex</option>
                  <option value="claude">Claude Code</option>
                </select>
                <input value={tool.tool_id} onChange={(e) => updateTool(index, {tool_id: e.target.value})} placeholder="工具 ID" />
                <input value={tool.display_name} onChange={(e) => updateTool(index, {display_name: e.target.value})} placeholder="显示名称" />
                <button className="danger subtle" onClick={() => removeTool(index)}>移除</button>
              </div>
              <div className="tool-paths">
                <input value={tool.home_dir} onChange={(e) => updateTool(index, {home_dir: e.target.value})} placeholder="工具目录，例如 C:\\Users\\you\\.codex" />
                <input value={tool.history_path} onChange={(e) => updateTool(index, {history_path: e.target.value})} placeholder="history.jsonl 路径" />
                <input value={tool.sessions_dir} onChange={(e) => updateTool(index, {sessions_dir: e.target.value})} placeholder="sessions 目录" />
                <input value={tool.executable_path} onChange={(e) => updateTool(index, {executable_path: e.target.value})} placeholder="codex 或 claude 可执行文件" />
                <input value={tool.continue_command_template} onChange={(e) => updateTool(index, {continue_command_template: e.target.value})} placeholder="继续命令，可使用 {session_id}" />
              </div>
            </div>
          ))}
          {(settings.ai_tools || []).length === 0 && <div className="empty">尚未配置 AI 工具。可以先检测默认路径或添加工具，然后保存设置。</div>}
        </div>
      </section>
    </section>
  );
}

function InfoCard({label, value, wide}: {label: string; value: string; wide?: boolean}) {
  return (
    <div className={wide ? 'info-card wide' : 'info-card'}>
      <span>{label}</span>
      <strong>{value || '-'}</strong>
    </div>
  );
}

function ReadableOutput({title, records}: {title: string; records: Record<string, unknown>[]}) {
  return (
    <div className="record-block">
      <h3>{title}</h3>
      {records.length === 0 ? <p>暂无输出记录。</p> : records.map((record, index) => (
        <article className="record-card" key={index}>
          <header>
            <span>{String(record.stream_type || 'stdout')} #{String(record.sequence_no || index + 1)}</span>
            <time>{formatDate(String(record.created_at || ''))}</time>
          </header>
          <pre>{String(record.content_redacted || record.content || JSON.stringify(record, null, 2))}</pre>
        </article>
      ))}
    </div>
  );
}

function ReadableMessages({title, records}: {title: string; records: Record<string, unknown>[]}) {
  return (
    <div className="record-block">
      <h3>{title}</h3>
      {records.length === 0 ? <p>暂无消息。</p> : records.map((record, index) => (
        <article className="record-card" key={index}>
          <header>
            <span>{String(record.role || record.type || 'message')}</span>
            <time>{formatDate(String(record.timestamp || ''))}</time>
          </header>
          <pre>{String(record.text || JSON.stringify(record.raw || record, null, 2))}</pre>
        </article>
      ))}
    </div>
  );
}

function ApprovalList({title, records}: {title: string; records: Record<string, unknown>[]}) {
  return (
    <div className="record-block">
      <h3>{title}</h3>
      {records.length === 0 ? <p>暂无审批请求。</p> : records.map((record, index) => (
        <article className="record-card" key={index}>
          <header>
            <span>{String(record.event_type || 'approval')} · {String(record.status || '-')}</span>
            <time>{formatDate(String(record.created_at || ''))}</time>
          </header>
          <pre>{String(record.prompt_text || record.context_before || JSON.stringify(record, null, 2))}</pre>
        </article>
      ))}
    </div>
  );
}

function DecisionList({title, records}: {title: string; records: Record<string, unknown>[]}) {
  return (
    <div className="record-block">
      <h3>{title}</h3>
      {records.length === 0 ? <p>暂无写回决策。</p> : records.map((record, index) => (
        <article className="record-card" key={index}>
          <header>
            <span>{String(record.decision_type || '-')} · {String(record.result || '-')}</span>
            <time>{formatDate(String(record.created_at || ''))}</time>
          </header>
          <pre>{String(record.payload_redacted || `写入字节数：${String(record.bytes_written || 0)}`)}</pre>
        </article>
      ))}
    </div>
  );
}

function tabTitle(tab: Tab) {
  switch (tab) {
    case 'processes':
      return 'GP 子进程管理';
    case 'ai-history':
      return 'AI 工具历史';
    default:
      return '桌面设置';
  }
}

function statusLabel(value: string) {
  switch (value) {
    case 'running':
      return '运行中';
    case 'waiting_approval':
      return '等待确认';
    case 'completed':
      return '已完成';
    case 'failed':
      return '失败';
    default:
      return value || '-';
  }
}

function formatDate(value: string) {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

export default App;
