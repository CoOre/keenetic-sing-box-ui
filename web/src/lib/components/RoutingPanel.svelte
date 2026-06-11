<script lang="ts">
  import { api, ApiError } from "../api";
  import type { SingboxSettings, KeeneticPolicy, CheckResult, ServersApplyResult, ListSource } from "../types";

  let { onApplied }: { onApplied?: () => void } = $props();

  let settings = $state<SingboxSettings>({
    inbound_mode: "redirect",
    inbound_port: 2080,
    tun_stack: "gvisor",
    tun_mtu: 1380,
    policy_name: "",
    exclude_cidr: [],
    route_domains: [],
    route_cidr: [],
    reject_cidr: [],
    use_conntrack: false,
  });

  let policies = $state<KeeneticPolicy[]>([]);
  let domainsText = $state("");
  let cidrText = $state("");
  let excludeText = $state("");
  let rejectText = $state("");

  let busy = $state(false);
  let applyBusy = $state(false);
  let notice = $state("");
  let error = $state("");
  let applyCheck = $state<CheckResult | null>(null);

  // List sources
  let sources = $state<ListSource[]>([]);
  let newUrl = $state("");
  let newType = $state("auto");
  let newInterval = $state(60);
  let sourcesBusy = $state(false);

  async function loadSources() {
    try { sources = await api.listSources(); } catch { /* ignore */ }
  }

  async function addSource() {
    if (!newUrl.trim()) return;
    sourcesBusy = true;
    try {
      await api.listAdd(newUrl.trim(), newType, newInterval);
      newUrl = "";
      await loadSources();
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally {
      sourcesBusy = false;
    }
  }

  async function deleteSource(id: string) {
    sourcesBusy = true;
    try {
      await api.listDelete(id);
      await loadSources();
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally {
      sourcesBusy = false;
    }
  }

  async function refreshSource(id: string) {
    await api.listRefreshOne(id);
    // Poll for update
    setTimeout(async () => { await loadSources(); }, 3000);
  }

  async function refreshAll() {
    await api.listRefreshAll();
    setTimeout(async () => { await loadSources(); }, 3000);
  }

  function fmtDate(s?: string): string {
    if (!s) return "—";
    try { return new Date(s).toLocaleString("ru"); } catch { return s; }
  }
  function intervalLabel(m: number): string {
    if (!m || m < 1) return "60 мин";
    if (m < 60) return `${m} мин`;
    const h = Math.floor(m / 60), rem = m % 60;
    return rem ? `${h}ч ${rem}мин` : `${h}ч`;
  }

  const isTransparent = $derived(
    settings.inbound_mode === "tproxy" || settings.inbound_mode === "redirect",
  );

  const toLines = (s: string): string[] =>
    s.split("\n").map((l) => l.trim()).filter((l) => l && !l.startsWith("#"));

  async function load() {
    try {
      const s = await api.settingsGet();
      settings = s;
      domainsText = (s.route_domains ?? []).join("\n");
      cidrText = (s.route_cidr ?? []).join("\n");
      excludeText = (s.exclude_cidr ?? []).join("\n");
      rejectText = (s.reject_cidr ?? []).join("\n");
    } catch { /* keep defaults */ }
    try { policies = await api.policies(); } catch { /* RCI unavailable */ }
  }

  $effect(() => { load(); loadSources(); });

  async function save() {
    busy = true;
    error = "";
    notice = "";
    try {
      const s: SingboxSettings = {
        ...settings,
        route_domains: toLines(domainsText),
        route_cidr: toLines(cidrText),
        exclude_cidr: toLines(excludeText),
        reject_cidr: toLines(rejectText),
      };
      settings = await api.settingsSave(s);
      domainsText = (settings.route_domains ?? []).join("\n");
      cidrText = (settings.route_cidr ?? []).join("\n");
      excludeText = (settings.exclude_cidr ?? []).join("\n");
      rejectText = (settings.reject_cidr ?? []).join("\n");
      notice = "Сохранено. Нажмите «Применить и перезапустить» чтобы активировать.";
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  async function applyAndRestart() {
    applyBusy = true;
    error = "";
    notice = "";
    applyCheck = null;
    // Save first, then apply.
    try {
      const s: SingboxSettings = {
        ...settings,
        route_domains: toLines(domainsText),
        route_cidr: toLines(cidrText),
        exclude_cidr: toLines(excludeText),
        reject_cidr: toLines(rejectText),
      };
      settings = await api.settingsSave(s);
    } catch (e) {
      error = "Ошибка сохранения: " + (e instanceof ApiError ? e.message : String(e));
      applyBusy = false;
      return;
    }
    try {
      const res: ServersApplyResult = await api.serversApply(true);
      applyCheck = res.check;
      if (res.applied) {
        let msg = `Активировано.`;
        if (res.firewall_mode && res.firewall_mode !== "off") {
          msg += ` Файрвол: ${res.firewall_mode}.`;
        }
        if (res.firewall_error) {
          error = `Файрвол: ${res.firewall_error}`;
        } else {
          notice = msg;
        }
        onApplied?.();
      } else {
        error = "Конфиг не прошёл проверку sing-box.";
      }
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally {
      applyBusy = false;
    }
  }
</script>

<div class="card">
  <h2>Маршрутизация трафика</h2>

  <!-- Mode + port -->
  <div class="row-inline">
    <label class="f">
      <span>Режим перехвата</span>
      <select bind:value={settings.inbound_mode}>
        <option value="socks">SOCKS — ручной прокси на клиентах</option>
        <option value="redirect">REDIRECT — прозрачный, только TCP (без модулей ядра)</option>
        <option value="tproxy">TProxy — прозрачный, TCP+UDP (рекомендуется, нужны модули Netfilter)</option>
        <option value="tun">TUN — весь трафик</option>
      </select>
    </label>
    {#if settings.inbound_mode !== "tun"}
      <label class="f narrow">
        <span>Порт</span>
        <input type="number" bind:value={settings.inbound_port} />
      </label>
    {/if}
  </div>

  {#if settings.inbound_mode === "socks"}
    <p class="hint muted">
      Трафик не заворачивается автоматически. Укажите прокси вручную на клиентах:
      <code>socks5h://192.168.1.1:{settings.inbound_port}</code>
    </p>
  {:else if settings.inbound_mode === "tun"}
    <p class="hint warn">TUN захватывает весь трафик. Может конфликтовать с WireGuard/OpenConnect на роутере.</p>
  {:else}
    <p class="hint muted">
      Прозрачный перехват через iptables nat REDIRECT.
      Только трафик к указанным ниже доменам и IP попадает в vless — всё остальное идёт напрямую.
      Правила автоматически восстанавливаются после перезагрузки роутера.
    </p>
  {/if}

  {#if isTransparent}
    <hr />

    <!-- Domains -->
    <div class="section">
      <div class="sec-head">
        <span class="sec-title">Домены → через vless</span>
        <span class="muted sec-hint">Проверка по SNI (TLS/HTTPS). Один домен на строку.</span>
      </div>
      <textarea
        rows="8"
        bind:value={domainsText}
        placeholder={"youtube.com\ninstagram.com\ntwitter.com\nx.com\nfacebook.com\nnetflix.com\nopenai.com\ntelegram.org"}
        spellcheck="false"
      ></textarea>
    </div>

    <!-- CIDRs -->
    <div class="section">
      <div class="sec-head">
        <span class="sec-title">IP-адреса и подсети → через vless</span>
        <span class="muted sec-hint">IPv4, CIDR или одиночный IP. Один на строку.</span>
      </div>
      <textarea
        rows="5"
        bind:value={cidrText}
        placeholder={"151.101.0.0/16\n104.26.0.0/15\n1.2.3.4"}
        spellcheck="false"
      ></textarea>
    </div>

    <!-- Advanced collapsible -->
    <details>
      <summary class="muted">Дополнительно</summary>
      <div class="advanced-inner">
        <div class="section">
          <div class="sec-head">
            <span class="sec-title">Всегда обходить (исключения)</span>
            <span class="muted sec-hint">
              CIDR, которые никогда не заворачиваются (дополнительно к зарезервированным диапазонам).
            </span>
          </div>
          <textarea
            rows="3"
            bind:value={excludeText}
            placeholder={"192.0.2.0/24"}
            spellcheck="false"
          ></textarea>
        </div>

        <div class="section">
          <div class="sec-head">
            <span class="sec-title">Блокировать (reject)</span>
            <span class="muted sec-hint">
              CIDR, которым отдаётся отказ (TCP-reset / ICMP) на FORWARD. Для «придушенных» CDN —
              например встроенного у провайдера Google Global Cache, который отвечает, но зависает:
              отказ заставляет клиента мгновенно перейти на рабочий узел через прокси.
            </span>
          </div>
          <textarea
            rows="3"
            bind:value={rejectText}
            placeholder={"87.245.216.0/21"}
            spellcheck="false"
          ></textarea>
        </div>

        <div class="row-inline">
          <label class="f">
            <span>Политика Keenetic (привязка)</span>
            <select bind:value={settings.policy_name}>
              <option value="">Всё устройство</option>
              {#each policies as p (p.id)}
                <option value={p.description}>{p.description || p.id}</option>
              {/each}
            </select>
          </label>
          <label class="f chk-wrap">
            <input type="checkbox" bind:checked={settings.use_conntrack} />
            <span>Conntrack-оптимизация</span>
          </label>
        </div>
        <p class="hint muted">
          Привязка к политике позволяет заворачивать трафик только устройств из выбранной политики
          Keenetic, не затрагивая остальных. Зарезервированные диапазоны (RFC1918 и др.)
          и WAN-IP роутера всегда исключаются автоматически.
        </p>
      </div>
    </details>
  {/if}

  <!-- URL-based list sources -->
  {#if isTransparent}
    <hr />
    <div class="sec-head">
      <span class="sec-title">URL-источники списков</span>
      <span class="muted sec-hint">
        Роутер периодически скачивает списки и применяет их автоматически при изменении.
      </span>
    </div>

    <!-- Add new source -->
    <div class="src-add">
      <input
        type="url"
        class="src-url"
        placeholder="https://iplist.opencck.org/?format=json&site=telegram.org"
        bind:value={newUrl}
        spellcheck="false"
      />
      <label class="f narrow">
        <span>Тип</span>
        <select bind:value={newType}>
          <option value="auto">Авто</option>
          <option value="domains">Домены</option>
          <option value="cidr">IP/CIDR</option>
        </select>
      </label>
      <label class="f narrow">
        <span>Интервал</span>
        <select bind:value={newInterval}>
          <option value={15}>15 мин</option>
          <option value={30}>30 мин</option>
          <option value={60}>1 час</option>
          <option value={180}>3 часа</option>
          <option value={360}>6 часов</option>
          <option value={720}>12 часов</option>
          <option value={1440}>24 часа</option>
        </select>
      </label>
      <button onclick={addSource} disabled={sourcesBusy || !newUrl.trim()} class="src-add-btn">
        {sourcesBusy ? "…" : "Добавить"}
      </button>
    </div>

    {#if sources.length}
      <div class="src-list">
        {#each sources as src (src.id)}
          <div class="src-item" class:src-error={!!src.last_error}>
            <div class="src-main">
              <span class="src-url-text" title={src.url}>{src.url}</span>
              <div class="src-meta muted">
                <span>{intervalLabel(src.interval)}</span>
                <span>·</span>
                <span>{src.type}</span>
                {#if src.last_fetch}
                  <span>·</span>
                  <span>обновлён {fmtDate(src.last_fetch)}</span>
                  <span>·</span>
                  <span>{src.last_count} записей</span>
                {/if}
                {#if src.last_error}
                  <span class="err-text">· ошибка: {src.last_error}</span>
                {/if}
              </div>
            </div>
            <div class="src-actions">
              <button onclick={() => refreshSource(src.id)} disabled={sourcesBusy} title="Обновить сейчас">↻</button>
              <button class="danger" onclick={() => deleteSource(src.id)} disabled={sourcesBusy}>✕</button>
            </div>
          </div>
        {/each}
      </div>
      <div class="src-footer">
        <button onclick={refreshAll} disabled={sourcesBusy}>Обновить все сейчас</button>
        <span class="muted">Изменения применяются автоматически при обнаружении новых данных.</span>
      </div>
    {:else}
      <p class="muted" style="font-size:0.85em;margin:6px 0 0">Источников нет. Добавьте URL со списком доменов или IP.</p>
    {/if}
  {/if}

  {#if error}<div class="msg err">{error}</div>{/if}
  {#if notice}<div class="msg ok">{notice}</div>{/if}
  {#if applyCheck && !applyCheck.ok}
    <div class="msg err">
      sing-box check:
      <pre>{(applyCheck.errors ?? []).join("\n") || applyCheck.stderr || ""}</pre>
    </div>
  {/if}

  <div class="actions">
    <button onclick={save} disabled={busy || applyBusy}>
      {busy ? "Сохранение…" : "Сохранить"}
    </button>
    <button class="primary" onclick={applyAndRestart} disabled={busy || applyBusy}>
      {applyBusy ? "Применение…" : "Применить и перезапустить"}
    </button>
  </div>
</div>

<style>
  h2 {
    margin: 0 0 14px;
    font-size: 1.1rem;
  }
  hr {
    border: none;
    border-top: 1px solid var(--border);
    margin: 14px 0;
  }
  .row-inline {
    display: flex;
    gap: 10px;
    align-items: flex-end;
    flex-wrap: wrap;
    margin-bottom: 8px;
  }
  .row-inline .f {
    flex: 1;
    min-width: 180px;
  }
  .row-inline .narrow {
    flex: 0 0 80px;
    min-width: 80px;
  }
  .hint {
    margin: 0 0 10px;
    font-size: 0.82em;
    line-height: 1.5;
  }
  .hint.warn { color: var(--warn); }
  .f {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 0.85em;
  }
  .f > span { color: var(--fg-dim); }
  .chk-wrap {
    flex-direction: row;
    align-items: center;
    gap: 6px;
    padding-bottom: 2px;
  }
  .section {
    margin-bottom: 14px;
  }
  .sec-head {
    display: flex;
    align-items: baseline;
    gap: 10px;
    margin-bottom: 6px;
    flex-wrap: wrap;
  }
  .sec-title {
    font-size: 0.88em;
    font-weight: 600;
    color: var(--fg);
  }
  .sec-hint {
    font-size: 0.80em;
  }
  textarea {
    width: 100%;
    box-sizing: border-box;
    font-family: ui-monospace, monospace;
    font-size: 0.85em;
    resize: vertical;
    min-height: 80px;
  }
  details {
    margin-bottom: 10px;
  }
  summary {
    cursor: pointer;
    font-size: 0.85em;
    user-select: none;
    padding: 4px 0;
  }
  summary:hover { color: var(--fg); }
  .advanced-inner {
    padding: 10px 0 4px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .actions {
    display: flex;
    gap: 10px;
    flex-wrap: wrap;
    margin-top: 14px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
  }
  .msg {
    margin-top: 10px;
    font-size: 0.88em;
  }
  .msg.err { color: var(--err); }
  .msg.ok { color: var(--ok, #22c55e); }
  .src-add {
    display: flex;
    gap: 8px;
    align-items: flex-end;
    flex-wrap: wrap;
    margin-bottom: 10px;
  }
  .src-url { flex: 1; min-width: 200px; }
  .src-add-btn { flex-shrink: 0; align-self: flex-end; }
  .src-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-bottom: 10px;
  }
  .src-item {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 10px;
    padding: 8px 10px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
  }
  .src-item.src-error { border-color: color-mix(in srgb, var(--err) 40%, transparent); }
  .src-main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
  .src-url-text {
    font-size: 0.83em;
    font-family: ui-monospace, monospace;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .src-meta { font-size: 0.78em; display: flex; gap: 5px; flex-wrap: wrap; }
  .err-text { color: var(--err); }
  .src-actions { display: flex; gap: 6px; flex-shrink: 0; }
  .src-actions button { padding: 3px 8px; font-size: 0.9em; }
  .src-footer {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
    font-size: 0.82em;
  }
  .msg pre {
    margin: 6px 0 0;
    padding: 8px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    white-space: pre-wrap;
    word-break: break-word;
  }
</style>
