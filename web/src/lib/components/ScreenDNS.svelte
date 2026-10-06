<script lang="ts">
  import { api, ApiError } from "../api";
  import type { DNSOptions, DNSServer, DNSPreset, DNSLookupResult, DNSLookupServer, InstallStatus, ServersApplyResult, SingboxSettings } from "../types";
  import Icon from "./Icon.svelte";
  import DNSChain, { type DNSEntry, LOCAL, canProxy } from "./DNSChain.svelte";

  type RuleForm = { domains: string; chain: DNSEntry[] };

  // What the user edits: chains of servers. The tagged server registry the
  // backend stores is derived from them on save (toPayload).
  let main = $state<DNSEntry[]>([]);
  let blocked = $state<DNSEntry[]>([]);
  let rules = $state<RuleForm[]>([]);
  let strategy = $state<DNSOptions["strategy"]>("ipv4_only");
  let intercept = $state(false);
  let port = $state(1053);
  let timeout = $state(2);

  let presets = $state<DNSPreset[]>([]);
  let mode = $state<SingboxSettings["inbound_mode"]>("socks");
  let install = $state<InstallStatus | null>(null);
  // Tags of the stored servers, keyed by address|detour — reused on save so
  // tags stay stable across edits.
  let knownTags = new Map<string, string>();

  let dirty = $state(false);
  let busy = $state("");
  let error = $state("");
  let notice = $state("");
  let confirmApply = $state(false);
  let lastApply = $state("");
  let showAdv = $state(false);

  let lookupDomain = $state("");
  let lookup = $state<DNSLookupResult | null>(null);

  const isTransparent = $derived(mode === "tproxy" || mode === "redirect");
  // Server label for lookup results: preset name, else the address itself.
  function serverName(address: string): string {
    if (address === LOCAL) return "Системный DNS роутера";
    return presets.find((p) => p.address === address)?.name ?? address;
  }

  const STATUS: Record<DNSLookupServer["status"], string> = {
    ok: "", blocked: "заблокирован", nxdomain: "нет такого домена",
    error: "не ответил", not_applied: "примените настройки",
  };

  const STRATEGIES = [
    { v: "ipv4_only", label: "Только IPv4" },
    { v: "prefer_ipv4", label: "Предпочитать IPv4" },
    { v: "prefer_ipv6", label: "Предпочитать IPv6" },
    { v: "ipv6_only", label: "Только IPv6" },
  ];

  const toLines = (s: string): string[] =>
    s.split(/[\n,]/).map((l) => l.trim()).filter((l) => l && !l.startsWith("#"));

  const detourOf = (e: DNSEntry) => (e.proxy && canProxy(e.address) ? "proxy" : "direct");
  const keyOf = (e: DNSEntry) => e.address.trim() + "|" + detourOf(e);

  function fromServer(d: DNSOptions) {
    const byTag = new Map(d.servers.map((s) => [s.tag, s]));
    knownTags = new Map(d.servers.map((s) => [s.address + "|" + (s.detour || "direct"), s.tag]));
    const chain = (primary: string, backups?: string[]): DNSEntry[] =>
      (primary ? [primary, ...(backups ?? [])] : []).flatMap((t) => {
        if (t === LOCAL) return [{ address: LOCAL, proxy: false }];
        const s = byTag.get(t);
        return s ? [{ address: s.address, proxy: s.detour === "proxy" }] : [];
      });
    main = chain(d.final, d.final_backups);
    if (!main.length) main = [{ address: LOCAL, proxy: false }];
    blocked = chain(d.proxied_server, d.proxied_backups);
    rules = (d.rules ?? []).map((r) => ({ domains: r.domains.join("\n"), chain: chain(r.server, r.backups) }));
    strategy = d.strategy;
    intercept = d.intercept_clients;
    port = d.port;
    timeout = d.failover_timeout || 2;
  }

  function hostTag(address: string): string {
    const host = address.replace(/^[a-z0-9]+:\/\//i, "").replace(/[/:?].*$/, "").replace(/^\[|\]$/g, "");
    const t = host.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 24);
    return /^[a-z0-9]/.test(t) ? t : "dns";
  }

  // Build the backend options: one server per distinct address+detour, with
  // stable tags (previous tag → preset tag → host), suffixed on collision.
  function toPayload(): DNSOptions {
    const servers: DNSServer[] = [];
    const tagByKey = new Map<string, string>();
    const used = new Set<string>([LOCAL, "dns-in"]);

    function tagFor(e: DNSEntry): string {
      const address = e.address.trim();
      if (address === LOCAL) return LOCAL;
      if (!address) throw new Error("Укажите адрес DNS-сервера");
      const key = keyOf(e);
      const have = tagByKey.get(key);
      if (have) return have;
      const detour = detourOf(e);
      let base = knownTags.get(key) ?? presets.find((p) => p.address === address)?.tag ?? hostTag(address);
      if (!knownTags.has(key) && detour === "proxy" && !presets.find((p) => p.address === address)?.proxy) base += "-vpn";
      base = base.slice(0, 28);
      let tag = base;
      for (let n = 2; used.has(tag); n++) tag = `${base}-${n}`;
      used.add(tag);
      tagByKey.set(key, tag);
      servers.push({ tag, address, detour });
      return tag;
    }

    function chainTags(c: DNSEntry[], what: string): string[] {
      const tags = c.map(tagFor);
      if (new Set(tags).size !== tags.length) throw new Error(`${what}: сервер повторяется в цепочке`);
      return tags;
    }

    const m = chainTags(main, "Основной DNS");
    const b = chainTags(blocked, "Заблокированные сайты");
    const r = rules
      .map((x, i) => ({ domains: toLines(x.domains), tags: chainTags(x.chain, `Правило ${i + 1}`) }))
      .filter((x) => x.domains.length > 0 && x.tags.length > 0)
      .map((x) => ({ domains: x.domains, server: x.tags[0], backups: x.tags.slice(1) }));

    return {
      servers,
      final: m[0],
      final_backups: m.slice(1),
      proxied_server: b[0] ?? "",
      proxied_backups: b.slice(1),
      rules: r,
      strategy,
      intercept_clients: intercept,
      port,
      failover_timeout: timeout,
    };
  }

  async function load() {
    try {
      const st = await api.dnsGet();
      presets = st.presets ?? [];
      fromServer(st.dns);
      mode = st.inbound_mode;
      dirty = false;
      showAdv = rules.length > 0;
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    }
    try { install = await api.installStatus(); } catch { /* unknown */ }
  }

  $effect(() => { load(); });

  function touch() { dirty = true; notice = ""; error = ""; }

  async function save(): Promise<boolean> {
    busy = busy || "save"; error = ""; notice = "";
    try {
      fromServer(await api.dnsSave(toPayload()));
      dirty = false;
      notice = "Сохранено";
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    } finally { if (busy === "save") busy = ""; }
  }

  async function applyAndRestart() {
    busy = "apply"; confirmApply = false;
    try {
      if (!(await save())) return;
      const res: ServersApplyResult = await api.serversApply(true);
      if (res.applied) {
        lastApply = new Date().toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
        notice = res.firewall_error ? "Применено, но фаервол: " + res.firewall_error : "Применено и перезапущено";
      } else {
        error = "Конфиг не прошёл проверку sing-box: " + (res.check?.errors ?? []).join("; ");
      }
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  async function runLookup() {
    const d = lookupDomain.trim();
    if (!d) return;
    busy = "lookup"; lookup = null;
    try {
      lookup = await api.dnsLookup(d);
    } catch (e) {
      lookup = { ips: [], ms: 0, error: e instanceof ApiError ? e.message : String(e) };
    } finally { busy = ""; }
  }
</script>

<div class="page stack">
  <div class="card">
    <div class="card-head">
      <div>
        <h3 class="card-title"><Icon name="globe" size={17} />DNS</h3>
        <p class="card-sub">Через какие серверы sing-box резолвит домены</p>
      </div>
    </div>
    <div class="card-body stack">
      <DNSChain id="dns-main" {presets} entries={main} onchange={(e) => { main = e; touch(); }} />

      <div class="field">
        <label for="dns-blocked">Заблокированные сайты <span class="hint">домены с экрана «Маршрутизация»</span></label>
        <DNSChain id="dns-blocked" {presets} entries={blocked} emptyLabel="Как остальные"
          onchange={(e) => { blocked = e; touch(); }} />
      </div>

      {#if install?.outdated}
        <div class="callout warn">
          <Icon name="warn" size={17} />
          <div class="callout-body"><b>Нужен sing-box {install.min_version}+</b>, установлен {install.version}. Обновите ядро в «Дополнительно → Обновления».</div>
        </div>
      {/if}

      <div class="toggle-row">
        <div class="toggle-text">
          <b>Перехватывать DNS устройств</b>
          <span>
            {#if !isTransparent}
              Работает только в режимах TProxy и REDIRECT.
            {:else if intercept}
              Устройства LAN резолвят через эти серверы. Если sing-box остановится, перехват снимется сам.
            {:else}
              Сейчас устройства ходят в DNS роутера; настройки выше действуют только внутри sing-box.
            {/if}
          </span>
        </div>
        <button class={"toggle" + (intercept ? " on" : "")} onclick={() => { intercept = !intercept; touch(); }}
          role="switch" aria-checked={intercept} aria-label="Перехватывать DNS устройств"></button>
      </div>

      <div class="lookup">
        <input class="input mono" bind:value={lookupDomain} placeholder="проверить домен, напр. youtube.com"
          aria-label="Домен для проверки" onkeydown={(e) => e.key === "Enter" && runLookup()} />
        <button class="btn" disabled={!lookupDomain.trim() || busy === "lookup"} onclick={runLookup}>
          {#if busy === "lookup"}<span class="btn-spinner"></span>{:else}<Icon name="play" size={15} />{/if}
          Проверить
        </button>
      </div>
      {#if lookup}
        <div class="lookup-res">
          <div class="lookup-sum">
            {#if lookup.error}
              <span style="color:var(--danger-text)">{lookup.error}</span>
            {:else}
              {#each lookup.ips as ip (ip)}<span class="tag">{ip}</span>{/each}
              <span class="hint-text mono">{lookup.ms} мс</span>
            {/if}
            {#if lookup.chain}<span class="hint-text">· {lookup.chain}</span>{/if}
          </div>
          {#each lookup.servers ?? [] as sv (sv.tag)}
            <div class={"lookup-srv" + (sv.used ? " used" : "")}>
              <span class={"dot " + (sv.status === "ok" || sv.status === "blocked" || sv.status === "nxdomain" ? "ok" : sv.status === "not_applied" ? "warn" : "err")}></span>
              <span class="srv-name">{serverName(sv.address)}</span>
              {#if sv.proxy}<span class="seg-tag">VPN</span>{/if}
              {#if sv.used}<span class="pill accent">ответил</span>{/if}
              <span class="spacer"></span>
              {#if STATUS[sv.status]}
                <span class="hint-text" title={sv.error}>{STATUS[sv.status]}</span>
              {:else}
                <span class="mono srv-ips">{sv.ips.join(", ")}</span>
              {/if}
              {#if sv.status !== "not_applied"}<span class="hint-text mono">{sv.ms} мс</span>{/if}
            </div>
          {/each}
        </div>
      {/if}

      <button class="btn ghost sm" style="align-self:flex-start;padding-left:6px" onclick={() => showAdv = !showAdv}>
        <Icon name={showAdv ? "chevDown" : "chevRight"} size={15} />Дополнительно
      </button>
      {#if showAdv}
        <div class="adv stack">
          <div class="stack-sm">
            <b class="adv-title">Свои правила</b>
            <span class="hint-text">Отдельные серверы для конкретных доменов. Проверяются раньше всего остального.</span>
            {#each rules as r, i}
              <div class="rule-row">
                <textarea class="textarea mono" rows={2} bind:value={r.domains} oninput={touch}
                  placeholder="example.com, corp.lan" aria-label={"Домены правила " + (i + 1)}></textarea>
                <DNSChain id={"dns-rule-" + i} {presets} entries={r.chain} onchange={(e) => { r.chain = e; touch(); }} />
                <button class="btn sm ghost danger" style="align-self:flex-end" onclick={() => { rules = rules.filter((_, j) => j !== i); touch(); }}>
                  <Icon name="trash" size={14} />Удалить правило
                </button>
              </div>
            {/each}
            <button class="btn sm" style="align-self:flex-start" onclick={() => { rules = [...rules, { domains: "", chain: [{ address: LOCAL, proxy: false }] }]; touch(); }}>
              <Icon name="plus" size={14} />Правило
            </button>
          </div>
          <div class="adv-grid">
            <div class="field">
              <label for="dns-strategy">IP-версия</label>
              <select id="dns-strategy" class="select" bind:value={strategy} onchange={touch}>
                {#each STRATEGIES as s (s.v)}<option value={s.v}>{s.label}</option>{/each}
              </select>
            </div>
            <div class="field">
              <label for="dns-timeout">Ждать сервер <span class="hint">перед резервным, с</span></label>
              <input id="dns-timeout" class="input mono" type="number" min="1" max="30" bind:value={timeout} oninput={touch} />
            </div>
            <div class="field">
              <label for="dns-port">Порт <span class="hint mono">dns-in</span></label>
              <input id="dns-port" class="input mono" type="number" bind:value={port} oninput={touch} />
            </div>
          </div>
        </div>
      {/if}
    </div>
  </div>

  <!-- sticky action bar -->
  <div class="card" style="position:sticky;bottom:12px;z-index:5;padding:13px 18px;display:flex;align-items:center;gap:12px;box-shadow:var(--shadow);flex-wrap:wrap">
    {#if dirty}
      <span class="pill warn"><span class="dot warn"></span>не сохранено</span>
    {:else}
      <span class="pill ok"><span class="dot ok"></span>сохранено</span>
    {/if}
    {#if lastApply}
      <span class="hint-text mono">применено в {lastApply}</span>
    {/if}
    <span class="spacer"></span>
    {#if error}
      <span class="hint-text" style="color:var(--danger-text)">{error}</span>
    {/if}
    {#if notice}
      <span class="hint-text" style="color:var(--ok-text)">{notice}</span>
    {/if}
    <button class="btn" disabled={!!busy} onclick={save}>
      {#if busy === "save"}<span class="btn-spinner"></span>{:else}<Icon name="save" size={16} />{/if}
      Сохранить
    </button>
    <button class="btn primary" disabled={!!busy} onclick={() => confirmApply = true}>
      <Icon name="restart" size={16} />Применить и перезапустить
    </button>
  </div>

  {#if confirmApply}
    <div class="modal-scrim" onmousedown={(e) => { if (e.target === e.currentTarget) confirmApply = false; }}>
      <div class="modal">
        <div class="modal-head">
          <div class="modal-icon accent"><Icon name="info" size={19} /></div>
          <div style="flex:1;padding-top:2px">
            <h3>Применить и перезапустить?</h3>
            <p>sing-box пересоберёт конфиг из текущих настроек и перезапустится. Активные соединения разорвутся на пару секунд.</p>
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn ghost" onclick={() => confirmApply = false}>Отмена</button>
          <button class="btn primary" disabled={busy === "apply"} onclick={applyAndRestart}>
            {#if busy === "apply"}<span class="btn-spinner"></span>{/if}
            Применить
          </button>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  .lookup { display: flex; gap: 8px; }
  .lookup .input { flex: 1; min-width: 0; }
  .lookup-res { display: flex; flex-direction: column; gap: 6px; margin-top: -6px; font-size: 12.5px; }
  .lookup-sum { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; }
  .lookup-srv {
    display: flex; gap: 8px; align-items: center; flex-wrap: wrap;
    padding: 7px 10px; border-radius: 8px; border: 1px solid var(--border-soft);
  }
  .lookup-srv.used { border-color: var(--accent); background: var(--accent-soft); }
  .srv-name { font-weight: 500; }
  .srv-ips { color: var(--text-dim); word-break: break-all; }
  .adv { border-left: 2px solid var(--border); padding-left: 16px; margin-left: 4px; }
  .adv-title { font-size: 13px; }
  .adv-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 12px; }
  .rule-row {
    display: flex; flex-direction: column; gap: 8px;
    padding: 12px; border: 1px solid var(--border-soft); border-radius: 8px; background: var(--bg-soft);
  }
</style>
