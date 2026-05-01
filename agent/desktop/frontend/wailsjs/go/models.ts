export namespace main {

	export class AIToolConfig {
	    tool_id: string;
	    tool_type: string;
	    display_name: string;
	    enabled: boolean;
	    home_dir: string;
	    history_path: string;
	    sessions_dir: string;
	    executable_path: string;
	    continue_command_template: string;

	    static createFrom(source: any = {}) {
	        return new AIToolConfig(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tool_id = source["tool_id"];
	        this.tool_type = source["tool_type"];
	        this.display_name = source["display_name"];
	        this.enabled = source["enabled"];
	        this.home_dir = source["home_dir"];
	        this.history_path = source["history_path"];
	        this.sessions_dir = source["sessions_dir"];
	        this.executable_path = source["executable_path"];
	        this.continue_command_template = source["continue_command_template"];
	    }
	}
	export class AIToolConfigList {
	    items: AIToolConfig[];

	    static createFrom(source: any = {}) {
	        return new AIToolConfigList(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.items = this.convertValues(source["items"], AIToolConfig);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AIToolMessageRecord {
	    timestamp: string;
	    role: string;
	    type: string;
	    text: string;
	    raw?: Record<string, any>;

	    static createFrom(source: any = {}) {
	        return new AIToolMessageRecord(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.timestamp = source["timestamp"];
	        this.role = source["role"];
	        this.type = source["type"];
	        this.text = source["text"];
	        this.raw = source["raw"];
	    }
	}
	export class AIToolSessionRecord {
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

	    static createFrom(source: any = {}) {
	        return new AIToolSessionRecord(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.tool_id = source["tool_id"];
	        this.tool_type = source["tool_type"];
	        this.display_name = source["display_name"];
	        this.title = source["title"];
	        this.working_dir = source["working_dir"];
	        this.created_at = source["created_at"];
	        this.updated_at = source["updated_at"];
	        this.message_count = source["message_count"];
	        this.preview = source["preview"];
	        this.source_path = source["source_path"];
	        this.can_continue = source["can_continue"];
	    }
	}
	export class AIToolSessionDetail {
	    session: AIToolSessionRecord;
	    messages: AIToolMessageRecord[];

	    static createFrom(source: any = {}) {
	        return new AIToolSessionDetail(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session = this.convertValues(source["session"], AIToolSessionRecord);
	        this.messages = this.convertValues(source["messages"], AIToolMessageRecord);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AIToolSessionList {
	    items: AIToolSessionRecord[];

	    static createFrom(source: any = {}) {
	        return new AIToolSessionList(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.items = this.convertValues(source["items"], AIToolSessionRecord);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class AgentLocalSettings {
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

	    static createFrom(source: any = {}) {
	        return new AgentLocalSettings(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.start_on_login = source["start_on_login"];
	        this.notification_enabled = source["notification_enabled"];
	        this.notification_style = source["notification_style"];
	        this.history_retention_days = source["history_retention_days"];
	        this.capture_output_mode = source["capture_output_mode"];
	        this.default_cli_type = source["default_cli_type"];
	        this.server_url = source["server_url"];
	        this.tenant_id = source["tenant_id"];
	        this.device_id = source["device_id"];
	        this.client_instance_id = source["client_instance_id"];
	        this.ai_tools = this.convertValues(source["ai_tools"], AIToolConfig);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AgentStatus {
	    settings: AgentLocalSettings;
	    logged_in: boolean;
	    offline: boolean;
	    settings_path: string;
	    history_path: string;
	    tray_addr: string;

	    static createFrom(source: any = {}) {
	        return new AgentStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.settings = this.convertValues(source["settings"], AgentLocalSettings);
	        this.logged_in = source["logged_in"];
	        this.offline = source["offline"];
	        this.settings_path = source["settings_path"];
	        this.history_path = source["history_path"];
	        this.tray_addr = source["tray_addr"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LoginRequest {
	    server_url: string;
	    tenant_id: string;
	    device_id: string;
	    client_instance_id: string;

	    static createFrom(source: any = {}) {
	        return new LoginRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.server_url = source["server_url"];
	        this.tenant_id = source["tenant_id"];
	        this.device_id = source["device_id"];
	        this.client_instance_id = source["client_instance_id"];
	    }
	}
	export class SessionRecord {
	    session_id: string;
	    cli_type: string;
	    command_line_redacted: string;
	    working_dir: string;
	    working_dir_hash: string;
	    status: string;
	    started_at: string;
	    ended_at?: string;
	    last_output_summary: string;
	    pending_approval_count: number;
	    control_addr?: string;

	    static createFrom(source: any = {}) {
	        return new SessionRecord(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session_id = source["session_id"];
	        this.cli_type = source["cli_type"];
	        this.command_line_redacted = source["command_line_redacted"];
	        this.working_dir = source["working_dir"];
	        this.working_dir_hash = source["working_dir_hash"];
	        this.status = source["status"];
	        this.started_at = source["started_at"];
	        this.ended_at = source["ended_at"];
	        this.last_output_summary = source["last_output_summary"];
	        this.pending_approval_count = source["pending_approval_count"];
	        this.control_addr = source["control_addr"];
	    }
	}
	export class SessionDetail {
	    session: SessionRecord;
	    output: any[];
	    approvals: any[];
	    decisions: any[];

	    static createFrom(source: any = {}) {
	        return new SessionDetail(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session = this.convertValues(source["session"], SessionRecord);
	        this.output = source["output"];
	        this.approvals = source["approvals"];
	        this.decisions = source["decisions"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SessionList {
	    items: SessionRecord[];

	    static createFrom(source: any = {}) {
	        return new SessionList(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.items = this.convertValues(source["items"], SessionRecord);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}
