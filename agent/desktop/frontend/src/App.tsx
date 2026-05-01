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
  const [tab, setTab] = useState<'settings' | 'history'>('settings');
  const [status, setStatus] = useState<Status | null>(null);
  const [settings, setSettings] = useState<Settings>(emptySettings);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [aiSessions, setAiSessions] = useState<AIToolSession[]>([]);
  const [aiDetail, setAiDetail] = useState<AIToolDetail | null>(null);
  const [selectedSession, setSelectedSession] = useState('');
  const [selectedAIToolSession, setSelectedAIToolSession] = useState('');
  const [historySource, setHistorySource] = useState('gatepilot');
  const [cliFilter, setCliFilter] = useState('');
  const [statusFilter, setStatusFilter] = useState('');
  const [query, setQuery] = useState('');
  const [replyText, setReplyText] = useState('');
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');

  const enabledTools = useMemo(() => (settings.ai_tools || []).filter((tool) => tool.enabled && tool.tool_id), [settings.ai_tools]);
  const selectedAITool = historySource === 'gatepilot' ? '' : historySource;
  const canReply = detail?.session.status === 'running' || detail?.session.status === 'waiting_approval';

  useEffect(() => {
    boot();
  }, []);

  useEffect(() => {
    if (tab === 'history') {
      loadHistory();
    }
  }, [tab, historySource, cliFilter, statusFilter, query]);

  async function boot() {
    try {
      const initial = await InitialView();
      if (initial === 'history' || initial === 'settings') {
        setTab(initial);
      }
      await EnsureAgent();
      await loadStatus();
      await loadHistory();
    } catch (err) {
      showError(err);
    }
  }

  async function loadStatus() {
    const value = await GetStatus();
    setStatus(value);
    setSettings({...emptySettings, ...value.settings, ai_tools: value.settings.ai_tools || []});
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
      setNotice('已退出登录。');
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
        if (!merged.some((item) => item.tool_id === tool.tool_id)) {
          merged.push(tool);
        }
      }
      updateSetting('ai_tools', merged);
      setNotice('已检测 Codex 和 Claude 默认位置。保存设置后生效。');
    } catch (err) {
      showError(err);
    }
  }

  async function loadHistory() {
    if (historySource === 'gatepilot') {
      await loadSessions();
    } else {
      await loadAIToolSessions();
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

  async function loadAIToolSessions() {
    try {
      const result = await ListAIToolSessions(selectedAITool, query, 200);
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
    setSelectedAIToolSession(item.tool_id + ':' + item.id);
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
      setNotice('回复已发送。');
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
    const ok = window.confirm('确认删除这条本地 AI 历史记录？GatePilot 改写历史文件前会先备份到回收目录。');
    if (!ok) return;
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
        <button className={tab === 'settings' ? 'nav active' : 'nav'} onClick={() => setTab('settings')}>设置</button>
        <button className={tab === 'history' ? 'nav active' : 'nav'} onClick={() => setTab('history')}>会话</button>
        <div className="paths">
          <span>托盘接口</span>
          <code>{status?.tray_addr || '127.0.0.1:18731'}</code>
        </div>
      </aside>

      <main className="content">
        <header className="topbar">
          <div>
            <strong>{tab === 'settings' ? '桌面设置' : '会话历史'}</strong>
            <span>{status?.logged_in ? '已配置登录身份' : '默认离线使用，无需登录'}</span>
          </div>
          <button onClick={boot}>刷新</button>
        </header>

        {notice && <div className="notice ok">{notice}</div>}
        {error && <div className="notice error">{error}</div>}

        {tab === 'settings' ? (
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
        ) : (
          <HistoryView
            historySource={historySource}
            setHistorySource={(value) => {
              setHistorySource(value);
              setDetail(null);
              setAiDetail(null);
            }}
            enabledTools={enabledTools}
            sessions={sessions}
            aiSessions={aiSessions}
            detail={detail}
            aiDetail={aiDetail}
            selectedSession={selectedSession}
            selectedAIToolSession={selectedAIToolSession}
            cliFilter={cliFilter}
            setCliFilter={setCliFilter}
            statusFilter={statusFilter}
            setStatusFilter={setStatusFilter}
            query={query}
            setQuery={setQuery}
            selectSession={selectSession}
            selectAIToolSession={selectAIToolSession}
            canReply={canReply}
            replyText={replyText}
            setReplyText={setReplyText}
            sendReply={sendReply}
            continueAI={continueAI}
            deleteAI={deleteAI}
          />
        )}
      </main>
    </div>
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

function HistoryView(props: {
  historySource: string;
  setHistorySource: (value: string) => void;
  enabledTools: AIToolConfig[];
  sessions: Session[];
  aiSessions: AIToolSession[];
  detail: Detail | null;
  aiDetail: AIToolDetail | null;
  selectedSession: string;
  selectedAIToolSession: string;
  cliFilter: string;
  setCliFilter: (value: string) => void;
  statusFilter: string;
  setStatusFilter: (value: string) => void;
  query: string;
  setQuery: (value: string) => void;
  selectSession: (id: string) => void;
  selectAIToolSession: (item: AIToolSession) => void;
  canReply: boolean;
  replyText: string;
  setReplyText: (value: string) => void;
  sendReply: () => void;
  continueAI: () => void;
  deleteAI: () => void;
}) {
  const isGatePilot = props.historySource === 'gatepilot';
  return (
    <section className="history-layout">
      <section className="panel list-panel">
        <div className="filters vertical">
          <select value={props.historySource} onChange={(e) => props.setHistorySource(e.target.value)}>
            <option value="gatepilot">GatePilot 本地</option>
            {props.enabledTools.map((tool) => (
              <option value={tool.tool_id} key={tool.tool_id}>{tool.display_name || tool.tool_id}</option>
            ))}
          </select>
          {isGatePilot ? (
            <div className="filters inline">
              <select value={props.cliFilter} onChange={(e) => props.setCliFilter(e.target.value)}>
                <option value="">全部 CLI</option>
                <option value="custom">custom</option>
                <option value="codex">codex</option>
                <option value="claude_code">claude_code</option>
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
          ) : (
            <input value={props.query} onChange={(e) => props.setQuery(e.target.value)} placeholder="搜索标题、路径或提示内容" />
          )}
        </div>

        {isGatePilot ? (
          <div className="session-list">
            {props.sessions.map((item) => (
              <button key={item.session_id} className={props.selectedSession === item.session_id ? 'session active' : 'session'} onClick={() => props.selectSession(item.session_id)}>
                <strong>{item.session_id}</strong>
                <span>{item.cli_type} / {item.status}</span>
                <small>{item.working_dir || item.command_line_redacted}</small>
              </button>
            ))}
            {props.sessions.length === 0 && <div className="empty">暂无 GatePilot 会话。</div>}
          </div>
        ) : (
          <div className="session-list">
            {props.aiSessions.map((item) => (
              <button key={`${item.tool_id}:${item.id}`} className={props.selectedAIToolSession === `${item.tool_id}:${item.id}` ? 'session active' : 'session'} onClick={() => props.selectAIToolSession(item)}>
                <strong>{item.title || item.id}</strong>
                <span>{item.display_name} / {item.message_count} 条消息 / {formatDate(item.updated_at)}</span>
                <small>{item.working_dir || item.preview || item.id}</small>
              </button>
            ))}
            {props.aiSessions.length === 0 && <div className="empty">暂无已配置 AI 工具的会话。</div>}
          </div>
        )}
      </section>

      <section className="panel detail-panel">
        {isGatePilot ? (
          props.detail ? (
            <>
              <h2>{props.detail.session.session_id}</h2>
              <div className="kv">
                <span>状态</span><strong>{props.detail.session.status}</strong>
                <span>CLI</span><strong>{props.detail.session.cli_type}</strong>
                <span>目录</span><strong>{props.detail.session.working_dir || '-'}</strong>
                <span>摘要</span><strong>{props.detail.session.last_output_summary || '-'}</strong>
              </div>
              {props.canReply && (
                <div className="reply">
                  <input value={props.replyText} onChange={(e) => props.setReplyText(e.target.value)} placeholder="回复内容" />
                  <button className="primary" onClick={props.sendReply}>发送</button>
                </div>
              )}
              <RecordBlock title="输出" records={props.detail.output} />
              <RecordBlock title="审批" records={props.detail.approvals} />
              <RecordBlock title="决策" records={props.detail.decisions} />
            </>
          ) : (
            <div className="empty">请选择一个 GatePilot 会话。</div>
          )
        ) : props.aiDetail ? (
          <>
            <div className="detail-title">
              <h2>{props.aiDetail.session.title || props.aiDetail.session.id}</h2>
              <div className="actions compact">
                <button className="primary" onClick={props.continueAI}>继续</button>
                <button className="danger" onClick={props.deleteAI}>删除</button>
              </div>
            </div>
            <div className="kv">
              <span>工具</span><strong>{props.aiDetail.session.display_name}</strong>
              <span>会话 ID</span><strong>{props.aiDetail.session.id}</strong>
              <span>目录</span><strong>{props.aiDetail.session.working_dir || '-'}</strong>
              <span>更新时间</span><strong>{formatDate(props.aiDetail.session.updated_at)}</strong>
              <span>来源</span><strong>{props.aiDetail.session.source_path || '-'}</strong>
            </div>
            <RecordBlock title="消息" records={props.aiDetail.messages} />
          </>
        ) : (
          <div className="empty">请选择一个已配置 AI 工具会话。</div>
        )}
      </section>
    </section>
  );
}

function RecordBlock({title, records}: {title: string; records: Record<string, unknown>[]}) {
  return (
    <div className="record-block">
      <h3>{title}</h3>
      {records.length === 0 ? <p>暂无记录。</p> : records.map((record, index) => <pre key={index}>{JSON.stringify(record, null, 2)}</pre>)}
    </div>
  );
}

function formatDate(value: string) {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

export default App;
