<script lang="ts">
  import { api, ApiError } from "../api";
  import type { SingboxSettings, KeeneticPolicy, ListSource, ServersApplyResult } from "../types";
  import Icon from "./Icon.svelte";

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
  let sources = $state<ListSource[]>([]);
  let newUrl = $state("");
  let newType = $state("auto");
  let newInterval = $state(1440);

  let dirty = $state(false);
  let busy = $state("");
  let error = $state("");
  let notice = $state("");
  let confirmApply = $state(false);
  let showAdv = $state(false);
  let lastApply = $state("");

  const toLines = (s: string): string[] =>
    s.split("\n").map((l) => l.trim()).filter((l) => l && !l.startsWith("#"));

  const isTransparent = $derived(
    settings.inbound_mode === "tproxy" || settings.inbound_mode === "redirect"
  );

  async function load() {
    try {
      const s = await api.settingsGet();
      settings = s;
      domainsText = (s.route_domains ?? []).join("\n");
      cidrText = (s.route_cidr ?? []).join("\n");
      excludeText = (s.exclude_cidr ?? []).join("\n");
      rejectText = (s.reject_cidr ?? []).join("\n");
      dirty = false;
    } catch { /* keep defaults */ }
    try { policies = await api.policies(); } catch { /* RCI unavailable */ }
  }

  async function loadSources() {
    try { sources = await api.listSources(); } catch { /* ignore */ }
  }

  $effect(() => { load(); loadSources(); });

  function setMode(m: string) {
    settings = { ...settings, inbound_mode: m as SingboxSettings["inbound_mode"] };
    dirty = true;
  }

  async function save() {
    busy = "save"; error = ""; notice = "";
    try {
      const s: SingboxSettings = { ...settings, route_domains: toLines(domainsText), route_cidr: toLines(cidrText), exclude_cidr: toLines(excludeText), reject_cidr: toLines(rejectText) };
      settings = await api.settingsSave(s);
      domainsText = (settings.route_domains ?? []).join("\n");
      cidrText = (settings.route_cidr ?? []).join("\n");
      excludeText = (settings.exclude_cidr ?? []).join("\n");
      rejectText = (settings.reject_cidr ?? []).join("\n");
      dirty = false;
      notice = "Сохранено";
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  async function applyAndRestart() {
    busy = "apply"; error = ""; notice = ""; confirmApply = false;
    try {
      const s: SingboxSettings = { ...settings, route_domains: toLines(domainsText), route_cidr: toLines(cidrText), exclude_cidr: toLines(excludeText), reject_cidr: toLines(rejectText) };
      settings = await api.settingsSave(s);
      const res: ServersApplyResult = await api.serversApply(true);
      if (res.applied) {
        dirty = false;
        const now = new Date().toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
        lastApply = now;
        notice = "Применено и перезапущено";
      } else {
        error = "Конфиг не прошёл проверку sing-box.";
      }
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  async function addSource() {
    if (!newUrl.trim()) return;
    busy = "srcAdd";
    try {
      await api.listAdd(newUrl.trim(), newType, newInterval);
      newUrl = "";
      await loadSources();
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  async function deleteSource(id: string) {
    try {
      await api.listDelete(id);
      await loadSources();
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    }
  }

  async function refreshSource(id: string) {
    await api.listRefreshOne(id);
    setTimeout(loadSources, 3000);
  }

  async function refreshAll() {
    await api.listRefreshAll();
    setTimeout(loadSources, 3000);
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

  const MODES = [
    { id: "socks", title: "SOCKS", tag: "manual", desc: "Ручной прокси на клиентах. sing-box слушает SOCKS-порт, устройства настраиваются вручную." },
    { id: "redirect", title: "REDIRECT", tag: "TCP · без модулей", desc: "Прозрачный перехват через iptables nat. Только TCP. Запасной вариант, если модули ядра Netfilter недоступны." },
    { id: "tproxy", title: "TProxy", tag: "TCP+UDP · рекоменд.", desc: "Прозрачный перехват TCP и UDP — нужен для UDP-приложений и QUIC/HTTP3. Требует компонент «Модули ядра для Netfilter» в KeeneticOS." },
    { id: "tun", title: "TUN", tag: "весь трафик", desc: "Весь трафик роутера заворачивается в виртуальный интерфейс. Максимальный охват, выше нагрузка." },
  ];

  const MODE_HINT: Record<string, string> = {
    socks: "Клиенты должны вручную указать прокси-адрес роутера и порт ниже.",
    redirect: "Только TCP к доменам и подсетям ниже идёт через VPN — остальное напрямую. Запасной режим без модулей ядра; UDP/QUIC не проксируется.",
    tproxy: "TCP и UDP к доменам и подсетям ниже идут через VPN — включая UDP-приложения и QUIC/HTTP3. Нужен компонент «Модули ядра для Netfilter».",
    tun: "Весь трафик роутера идёт через VPN. Списки доменов/IP ниже не требуются.",
  };
</script>

<div class="page stack">
  {#if dirty}
    <div class="callout accent">
      <Icon name="info" size={17} />
      <div class="callout-body">
        <b>Есть несохранённые изменения</b><br />
        «Сохранить» запишет настройки. «Применить и перезапустить» пересоберёт конфиг — только тогда правила вступят в силу.
      </div>
    </div>
  {/if}

  <!-- Mode -->
  <div class="card">
    <div class="card-head">
      <div>
        <h3 class="card-title"><Icon name="route" size={17} />Режим перехвата</h3>
        <p class="card-sub">Как sing-box перехватывает трафик роутера</p>
      </div>
    </div>
    <div class="card-body stack-sm">
      <div class="seg">
        {#each MODES as m}
          <div class={"seg-card" + (settings.inbound_mode === m.id ? " on" : "")} onclick={() => setMode(m.id)} role="radio" aria-checked={settings.inbound_mode === m.id} tabindex="0" onkeydown={(e) => e.key === "Enter" && setMode(m.id)}>
            <div class="seg-radio"></div>
            <div class="seg-main">
              <b>{m.title}<span class="seg-tag">{m.tag}</span></b>
              <div class="seg-desc">{m.desc}</div>
            </div>
          </div>
        {/each}
      </div>
      <div class="callout">
        <Icon name="info" size={17} />
        <div class="callout-body">{MODE_HINT[settings.inbound_mode]}</div>
      </div>
      {#if settings.inbound_mode !== "tun"}
        <div style="max-width:200px">
          <div class="field">
            <label>Порт <span class="hint mono">inbound</span></label>
            <input class="input mono" type="number" bind:value={settings.inbound_port} oninput={() => dirty = true} />
          </div>
        </div>
      {/if}
    </div>
  </div>

  <!-- Domains/IPs — only transparent modes -->
  {#if isTransparent}
    <div class="card">
      <div class="card-head">
        <div>
          <h3 class="card-title"><Icon name="globe" size={17} />Что идёт через VPN</h3>
          <p class="card-sub">Точечная маршрутизация: только указанное заворачивается в туннель</p>
        </div>
      </div>
      <div class="card-body stack">
        <div class="field">
          <label>Домены → через vless <span class="hint mono">проверка по SNI (TLS/HTTPS), один домен на строку</span></label>
          <textarea class="textarea mono" rows={6} bind:value={domainsText} oninput={() => dirty = true} placeholder="example.com"></textarea>
        </div>
        <div class="field">
          <label>IP-адреса и подсети → через vless <span class="hint mono">IPv4, CIDR или одиночный IP, один на строку</span></label>
          <textarea class="textarea mono" rows={3} bind:value={cidrText} oninput={() => dirty = true} placeholder="151.101.0.0/16"></textarea>
        </div>

        <button class="btn ghost sm" style="align-self:flex-start;padding-left:6px" onclick={() => showAdv = !showAdv}>
          <Icon name={showAdv ? "chevDown" : "chevRight"} size={15} />Дополнительно
        </button>
        {#if showAdv}
          <div class="stack-sm" style="border-left:2px solid var(--border);padding-left:16px;margin-left:4px">
            <div class="field">
              <label>Исключения → всегда напрямую <span class="hint mono">эти адреса обходят VPN даже если попадают под правила выше</span></label>
              <textarea class="textarea mono" rows={3} bind:value={excludeText} oninput={() => dirty = true}></textarea>
            </div>
            <div class="field">
              <label>Блокировать (reject) <span class="hint mono">отдаётся отказ (TCP-reset/ICMP). Для придушенных CDN — напр. встроенного у провайдера Google-кэша: клиент сразу уходит на рабочий узел через прокси</span></label>
              <textarea class="textarea mono" rows={3} bind:value={rejectText} oninput={() => dirty = true} placeholder="87.245.216.0/21"></textarea>
            </div>
            <div class="grid-2">
              <div class="field">
                <label>Политика Keenetic <span class="hint">привязка правил к группе устройств</span></label>
                <select class="select" bind:value={settings.policy_name} onchange={() => dirty = true}>
                  <option value="">Без привязки</option>
                  {#each policies as p (p.id)}
                    <option value={p.description}>{p.description || p.id}</option>
                  {/each}
                </select>
              </div>
              <div style="display:flex;align-items:flex-end">
                <div class="card" style="width:100%;padding:2px 14px;background:var(--bg-soft)">
                  <div class="toggle-row">
                    <div class="toggle-text">
                      <b>conntrack-оптимизация</b>
                      <span>Ускоряет established-соединения</span>
                    </div>
                    <button class={"toggle" + (settings.use_conntrack ? " on" : "")} onclick={() => { settings = { ...settings, use_conntrack: !settings.use_conntrack }; dirty = true; }} role="switch" aria-checked={settings.use_conntrack}></button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        {/if}
      </div>
    </div>

    <!-- URL sources -->
    <div class="card">
      <div class="card-head">
        <div>
          <h3 class="card-title"><Icon name="list" size={17} />URL-источники списков</h3>
          <p class="card-sub">Роутер периодически скачивает списки и применяет их автоматически</p>
        </div>
      </div>
      <div class="card-body stack-sm">
        <div class="row" style="align-items:flex-end;gap:10px;flex-wrap:wrap">
          <div class="field" style="flex:1 1 320px">
            <label>URL</label>
            <input class="input mono" bind:value={newUrl} placeholder="https://iplist.opencck.org/?format=json&site=telegram.org" />
          </div>
          <div class="field" style="width:120px">
            <label>Тип</label>
            <select class="select" bind:value={newType}>
              <option value="auto">Авто</option>
              <option value="domains">Домены</option>
              <option value="cidr">CIDR</option>
            </select>
          </div>
          <div class="field" style="width:120px">
            <label>Интервал</label>
            <select class="select" bind:value={newInterval}>
              <option value={60}>1 час</option>
              <option value={360}>6 часов</option>
              <option value={1440}>24 часа</option>
            </select>
          </div>
          <button class="btn" disabled={!newUrl.trim() || busy === "srcAdd"} onclick={addSource}>
            <Icon name="plus" size={16} />Добавить
          </button>
        </div>

        {#if sources.length === 0}
          <div class="empty">
            <div class="empty-icon"><Icon name="list" size={20} /></div>
            <h4>Источников пока нет</h4>
            <p>Добавьте URL списка доменов или IP — роутер будет обновлять его автоматически.</p>
          </div>
        {:else}
          {#each sources as s (s.id)}
            <div class="lrow">
              <div class="lrow-main">
                <div class="lrow-title mono" style="font-size:12.5px;font-weight:500;word-break:break-all">{s.url}</div>
                <div class="lrow-meta">
                  <span class="tag">{intervalLabel(s.interval)}</span>
                  <span class="tag">{s.type}</span>
                  {#if s.last_error}
                    <span style="color:var(--danger-text)">ошибка: {s.last_error}</span>
                  {:else}
                    обновлён {fmtDate(s.last_fetch)} · {s.last_count.toLocaleString("ru-RU")} записей
                  {/if}
                </div>
              </div>
              <div class="lrow-actions">
                <button class="btn sm icon" onclick={() => refreshSource(s.id)} title="Обновить"><Icon name="refresh" size={14} /></button>
                <button class="btn sm danger icon" onclick={() => deleteSource(s.id)} title="Удалить"><Icon name="trash" size={14} /></button>
              </div>
            </div>
          {/each}
          <div class="row" style="margin-top:4px">
            <button class="btn sm" onclick={refreshAll}><Icon name="refresh" size={14} />Обновить все сейчас</button>
            <span class="hint-text">Изменения применяются автоматически при обнаружении новых данных.</span>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- sticky action bar -->
  <div class="card" style="position:sticky;bottom:12px;z-index:5;padding:13px 18px;display:flex;align-items:center;gap:12px;box-shadow:var(--shadow)">
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
    <button class="btn" disabled={busy === "save"} onclick={save}>
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
