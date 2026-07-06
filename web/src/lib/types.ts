export interface Paths {
  opt: string;
  opkg: string;
  sing_box_bin: string;
  sing_box_config: string;
  sing_box_log: string;
  sing_box_cache: string;
  sing_box_init: string;
  ui_config_dir: string;
  ui_tls_dir: string;
}

export interface SystemInfo {
  os: string;
  arch: string;
  paths: Paths;
  entware?: { opkg_path: string };
  sing_box?: { path: string; version?: string };
  service: { init_path: string; present: boolean; enabled: boolean; running: boolean };
}

export interface InstallStatus {
  installed: boolean;
  path?: string;
  version?: string;
  entware: boolean;
}

export interface ServiceResult {
  action: string;
  result: { stdout: string; stderr: string };
  enabled: boolean;
}

export interface CheckResult {
  ok: boolean;
  stdout?: string;
  stderr?: string;
  errors?: string[];
}

export interface LogsResult {
  path: string;
  lines: string[];
}

// Clash API shapes (subset we use).
export interface ClashProxyNode {
  name: string;
  type: string;
  now?: string;
  all?: string[];
  udp?: boolean;
}

export interface ClashProxies {
  proxies: Record<string, ClashProxyNode>;
}

export interface ClashTraffic {
  up: number;
  down: number;
}

export interface BackupMeta {
  name: string;
  timestamp: string;
  bytes: number;
}

export interface Server {
  id?: string;
  name: string;
  type: "vless" | "trojan" | "shadowsocks" | "vmess";
  server: string;
  server_port: number;
  uuid?: string;
  password?: string;
  method?: string;
  alter_id?: number;
  flow?: string;
  tls?: boolean;
  sni?: string;
  fingerprint?: string;
  insecure?: boolean;
  public_key?: string;
  short_id?: string;
  network?: string;
  ws_path?: string;
  ws_host?: string;
  grpc_service_name?: string;
}

export interface ServersApplyResult {
  check: CheckResult;
  applied: boolean;
  backup?: string;
  servers: number;
  firewall_mode?: string;
  firewall_error?: string;
}

export interface SingboxSettings {
  inbound_mode: "tun" | "socks" | "tproxy" | "redirect";
  inbound_port: number;
  tun_stack: string;
  tun_mtu: number;
  // Transparent-routing (tproxy/redirect) settings.
  policy_name?: string;
  exclude_cidr?: string[];
  route_domains?: string[];
  route_cidr?: string[];
  reject_cidr?: string[];
  use_conntrack?: boolean;
  // Auto-update toggles + check interval.
  auto_update_singbox?: boolean;
  auto_update_ui?: boolean;
  update_check_hours?: number;
}

export interface UpdateComponent {
  current?: string;
  latest?: string;
  available: boolean;
  error?: string;
}

export interface UpdateStatus {
  sing_box: UpdateComponent;
  ui: UpdateComponent;
  checked_at: string; // zero time => never checked yet
  checking?: boolean;
  updating?: string; // "singbox" | "ui" while an install runs
  last_result?: string; // outcome of the most recent detached install
  last_error?: string;
}

export interface UpdateStatusResp {
  status: UpdateStatus;
  auto_update_singbox: boolean;
  auto_update_ui: boolean;
  update_check_hours: number;
}

export interface KeeneticPolicy {
  id: string;
  description: string;
  mark: string;
}

export interface ListSource {
  id: string;
  url: string;
  type: "auto" | "domains" | "cidr";
  interval: number; // minutes
  enabled: boolean;
  last_fetch?: string;
  last_count: number;
  last_error?: string;
  domains?: string[];
  cidrs?: string[];
}

// --- route trace (/api/diag/trace) ---

export interface TraceRuleMatch {
  source: "route_domains" | "route_cidr" | "exclude_cidr" | "reject_cidr" | "list";
  entry: string;
  match?: "exact" | "suffix";
  list_url?: string;
  list_kind?: "domain" | "cidr";
  effective: boolean;
}

export interface TraceSets {
  route: boolean;
  exclude: boolean;
  reject: boolean;
  err?: string;
}

export interface TraceFlow {
  proto: string;
  state?: string;
  src: string;
  sport: number;
  dport: number;
  reply_src?: string;
  mark?: string;
  redirected: boolean;
}

export type TraceVerdict =
  | "proxy"
  | "direct"
  | "bypass"
  | "reject"
  | "capture_missing"
  | "no_capture"
  | "unknown";

export interface TraceIPReport {
  ip: string;
  sets?: TraceSets;
  matches?: TraceRuleMatch[];
  conntrack?: TraceFlow[];
  conntrack_total: number;
  verdict: TraceVerdict;
  verdict_source: "live" | "static";
}

export interface TraceReport {
  target: string;
  kind: "domain" | "ip";
  mode: string;
  capture_installed?: boolean;
  resolve_error?: string;
  domain_matches?: TraceRuleMatch[];
  ips: TraceIPReport[];
  conntrack_error?: string;
  outbound?: { selector: string; now?: string; error?: string };
}
