import type {
  SystemInfo,
  InstallStatus,
  ServiceResult,
  CheckResult,
  LogsResult,
  ClashProxies,
  BackupMeta,
  Server,
  ServersApplyResult,
  SingboxSettings,
  KeeneticPolicy,
  ListSource,
} from "./types";

export class ApiError extends Error {
  status: number;
  detail: unknown;
  constructor(status: number, message: string, detail?: unknown) {
    super(message);
    this.status = status;
    this.detail = detail;
  }
}

function readCookie(name: string): string | null {
  const m = document.cookie.match(
    new RegExp("(?:^|; )" + name.replace(/([.$?*|{}()[\]\\/+^])/g, "\\$1") + "=([^;]*)"),
  );
  return m ? decodeURIComponent(m[1]) : null;
}

const CSRF_COOKIE = "ksbui_csrf";

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  const init: RequestInit = { method, credentials: "same-origin", headers };

  if (body !== undefined) {
    if (typeof body === "string") {
      headers["Content-Type"] = "application/json";
      init.body = body;
    } else {
      headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }
  }
  if (method !== "GET" && method !== "HEAD") {
    const csrf = readCookie(CSRF_COOKIE);
    if (csrf) headers["X-CSRF-Token"] = csrf;
  }

  const resp = await fetch(path, init);
  if (resp.status === 204) return undefined as T;
  const text = await resp.text();
  let data: unknown = text;
  if (text && resp.headers.get("content-type")?.includes("application/json")) {
    try {
      data = JSON.parse(text);
    } catch {
      /* leave as text */
    }
  }
  if (!resp.ok) {
    const msg =
      data && typeof data === "object" && "error" in data
        ? String((data as { error: unknown }).error)
        : text || resp.statusText;
    // Pass the parsed body through as detail so callers can surface
    // structured diagnostics (e.g. sing-box check output + log tail).
    throw new ApiError(resp.status, msg, typeof data === "object" ? data : undefined);
  }
  return data as T;
}

// requestText returns the raw response body without JSON-parsing it — used for
// the config editor, which must edit the exact bytes on disk.
async function requestText(method: string, path: string): Promise<string> {
  const headers: Record<string, string> = {};
  if (method !== "GET" && method !== "HEAD") {
    const csrf = readCookie(CSRF_COOKIE);
    if (csrf) headers["X-CSRF-Token"] = csrf;
  }
  const resp = await fetch(path, { method, credentials: "same-origin", headers });
  const text = await resp.text();
  if (!resp.ok) {
    let msg = text || resp.statusText;
    try {
      const j = JSON.parse(text);
      if (j && typeof j === "object" && "error" in j) msg = String(j.error);
    } catch {
      /* keep text */
    }
    throw new ApiError(resp.status, msg);
  }
  return text;
}

export const api = {
  // --- auth ---
  authStatus(): Promise<{ password_set: boolean }> {
    return request("GET", "/api/auth/status");
  },
  login(password: string): Promise<{ csrf_token: string; expires_at: number }> {
    return request("POST", "/api/login", { password });
  },
  setPassword(
    newPassword: string,
    currentPassword?: string,
  ): Promise<{ csrf_token: string; expires_at: number }> {
    return request("POST", "/api/password", {
      new_password: newPassword,
      current_password: currentPassword ?? "",
    });
  },
  async logout(): Promise<void> {
    await request("POST", "/api/logout");
  },
  async whoami(): Promise<{ auth: string }> {
    return request("GET", "/api/whoami");
  },

  // --- system / install / service ---
  system(): Promise<SystemInfo> {
    return request("GET", "/api/system");
  },
  installStatus(): Promise<InstallStatus> {
    return request("GET", "/api/install/status");
  },
  install(source: "opkg" | "github"): Promise<unknown> {
    return request("POST", "/api/install", { source });
  },
  service(action: string): Promise<ServiceResult> {
    return request("POST", `/api/service/${action}`);
  },
  serviceErrorDetail(err: unknown): { check?: CheckResult; log?: string[] } | null {
    if (err instanceof ApiError && err.detail && typeof err.detail === "object") {
      return err.detail as { check?: CheckResult; log?: string[] };
    }
    return null;
  },

  // --- config ---
  configRead(): Promise<string> {
    return requestText("GET", "/api/config");
  },
  configWrite(content: string): Promise<{ backup: unknown }> {
    return request("PUT", "/api/config", content);
  },
  configCheck(content?: string): Promise<CheckResult> {
    return request("POST", "/api/config/check", content ?? "");
  },
  async configBackups(): Promise<BackupMeta[]> {
    const r = await request<{ backups: BackupMeta[] }>("GET", "/api/config/backups");
    return r.backups ?? [];
  },
  configBackupRead(name: string): Promise<string> {
    return requestText("GET", `/api/config/backups/${encodeURIComponent(name)}`);
  },

  // --- logs ---
  logs(tail = 200): Promise<LogsResult> {
    return request("GET", `/api/logs?tail=${tail}`);
  },

  // --- servers (form-based outbounds) ---
  serverParse(link: string): Promise<Server> {
    return request("POST", "/api/servers/parse", { link });
  },
  async serverList(): Promise<Server[]> {
    const r = await request<{ servers: Server[] }>("GET", "/api/servers");
    return r.servers ?? [];
  },
  serverSave(s: Server): Promise<Server> {
    return request("POST", "/api/servers", s);
  },
  async serverDelete(id: string): Promise<void> {
    await request("DELETE", `/api/servers/${encodeURIComponent(id)}`);
  },
  serversApply(restart: boolean): Promise<ServersApplyResult> {
    return request("POST", "/api/servers/apply", { restart });
  },

  // --- settings (inbound mode) ---
  settingsGet(): Promise<SingboxSettings> {
    return request("GET", "/api/settings");
  },
  settingsSave(s: SingboxSettings): Promise<SingboxSettings> {
    return request("PUT", "/api/settings", s);
  },

  // --- transparent routing ---
  async policies(): Promise<KeeneticPolicy[]> {
    const r = await request<{ policies: KeeneticPolicy[] }>("GET", "/api/transparent/policies");
    return r.policies ?? [];
  },

  // --- list sources ---
  async listSources(): Promise<ListSource[]> {
    const r = await request<{ sources: ListSource[] }>("GET", "/api/lists");
    return r.sources ?? [];
  },
  listAdd(url: string, type: string, interval: number): Promise<ListSource> {
    return request("POST", "/api/lists", { url, type, interval });
  },
  async listDelete(id: string): Promise<void> {
    await request("DELETE", `/api/lists/${encodeURIComponent(id)}`);
  },
  listRefreshAll(): Promise<unknown> {
    return request("POST", "/api/lists/refresh");
  },
  listRefreshOne(id: string): Promise<unknown> {
    return request("POST", `/api/lists/${encodeURIComponent(id)}/refresh`);
  },

  // --- clash ---
  async clashProxies(): Promise<ClashProxies> {
    return request("GET", "/api/clash/proxies");
  },
  async clashSwitch(selector: string, name: string): Promise<void> {
    await request("PUT", `/api/clash/proxies/${encodeURIComponent(selector)}`, { name });
  },
  // clashDelay does a real latency test through the named outbound (the actual
  // VLESS server tag) — the ground truth for "is the tunnel established".
  clashDelay(
    name: string,
    opts?: { url?: string; timeout?: number },
  ): Promise<{ delay: number }> {
    const url = encodeURIComponent(opts?.url ?? "http://www.gstatic.com/generate_204");
    const timeout = opts?.timeout ?? 5000;
    return request(
      "GET",
      `/api/clash/proxies/${encodeURIComponent(name)}/delay?timeout=${timeout}&url=${url}`,
    );
  },
  // Returns the absolute URL for the Clash traffic SSE-ish stream.
  clashTrafficURL(): string {
    return "/api/clash/traffic";
  },

  // --- MTU probe / clamp ---
  probeMTU(): Promise<{ ip: string; pmtu: number; mss: number }> {
    return request("POST", "/api/diag/mtu");
  },
  applyMSSClamp(mss: number): Promise<{ ip: string; mss: number; applied: boolean }> {
    return request("POST", "/api/diag/mtu/clamp", { mss });
  },
  async clearMSSClamp(): Promise<void> {
    await request("DELETE", "/api/diag/mtu/clamp");
  },
};
