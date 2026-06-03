<script lang="ts">
  import { api, ApiError } from "../api";
  import type { Server, CheckResult } from "../types";
  import Icon from "./Icon.svelte";

  function blank(): Server {
    return { name: "", type: "vless", server: "", server_port: 443, tls: true, network: "" };
  }

  let servers = $state<Server[]>([]);
  let form = $state<Server>(blank());
  let link = $state("");
  let busy = $state("");
  let error = $state("");
  let notice = $state("");
  let applyCheck = $state<CheckResult | null>(null);
  let confirmDelete = $state<Server | null>(null);
  let confirmApply = $state(false);

  const editing = $derived(!!form.id);
  const needsUuid = $derived(form.type === "vless" || form.type === "vmess");
  const needsPass = $derived(form.type === "trojan" || form.type === "shadowsocks");
  const showTLS = $derived(form.type !== "shadowsocks");

  async function loadList() {
    try { servers = await api.serverList(); } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => { loadList(); });

  async function parseLink() {
    if (!link.trim()) return;
    busy = "parse"; error = ""; notice = "";
    try {
      const s = await api.serverParse(link.trim());
      form = { ...blank(), ...s };
      notice = "Ссылка разобрана. Проверьте поля и нажмите «Добавить сервер».";
      link = "";
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  async function save() {
    if (!form.server || !form.type) { error = "Адрес и тип сервера обязательны."; return; }
    busy = "save"; error = ""; notice = "";
    try {
      await api.serverSave(form);
      form = blank();
      await loadList();
      notice = "Сохранено. Нажмите «Применить» чтобы активировать.";
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  async function doDelete(s: Server) {
    if (!s.id) return;
    confirmDelete = null;
    await api.serverDelete(s.id);
    await loadList();
  }

  async function applyAndRestart() {
    busy = "apply"; error = ""; notice = ""; applyCheck = null; confirmApply = false;
    try {
      const res = await api.serversApply(true);
      applyCheck = res.check;
      if (res.applied) { notice = `Применено (${res.servers} серверов), sing-box перезапущен.`; }
      else { error = "Конфиг не прошёл проверку sing-box."; }
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  function SectionHeader(title: string, sub?: string) {
    return `<div class="row" style="gap:9px;margin-bottom:8px">
      <span class="section-title" style="margin:0;white-space:nowrap;flex-shrink:0">${title}</span>
      ${sub ? `<span class="hint-text" style="white-space:nowrap;flex-shrink:0">· ${sub}</span>` : ""}
      <hr class="divider" style="flex:1"/>
    </div>`;
  }
</script>

<div class="page stack">
  <!-- Form card -->
  <div class="card">
    <div class="card-head">
      <div style="min-width:0">
        <h3 class="card-title">
          <Icon name="server" size={17} />
          {editing ? "Редактирование сервера" : "Новый VPN-сервер"}
        </h3>
        <p class="card-sub">{editing ? (form.name || form.server) : "Вставьте share-ссылку или заполните вручную"}</p>
      </div>
      {#if editing}
        <div class="card-head-actions">
          <button class="btn sm ghost" onclick={() => { form = blank(); notice = ""; error = ""; }}>
            <Icon name="x" size={14} />Отменить правку
          </button>
        </div>
      {/if}
    </div>
    <div class="card-body stack">
      {#if !editing}
        <div class="row" style="gap:10px">
          <input class="input mono" bind:value={link} placeholder="vless:// · trojan:// · ss:// · vmess://" onkeydown={(e) => e.key === "Enter" && parseLink()} />
          <button class="btn" disabled={!link.trim() || busy === "parse"} onclick={parseLink}>
            <Icon name="link" size={16} />Разобрать
          </button>
        </div>
      {/if}

      <!-- Main -->
      <div class="stack-sm" style="padding-top:4px">
        <div class="row" style="gap:9px;margin-bottom:8px">
          <span class="section-title" style="margin:0;white-space:nowrap">Основное</span>
          <hr class="divider" style="flex:1" />
        </div>
        <div class="grid-3">
          <div class="field"><label>Название</label><input class="input" bind:value={form.name} placeholder="Мой сервер" /></div>
          <div class="field">
            <label>Тип</label>
            <select class="select" bind:value={form.type}>
              <option value="vless">VLESS</option>
              <option value="trojan">Trojan</option>
              <option value="shadowsocks">Shadowsocks</option>
              <option value="vmess">VMess</option>
            </select>
          </div>
          <div class="field"><label>Порт</label><input class="input mono" type="number" bind:value={form.server_port} /></div>
        </div>
        <div class="field"><label>Адрес <span class="hint mono">домен или IP</span></label><input class="input mono" bind:value={form.server} placeholder="1.2.3.4 или host.example.net" /></div>
      </div>

      <!-- Credentials -->
      <div class="stack-sm" style="padding-top:4px">
        <div class="row" style="gap:9px;margin-bottom:8px">
          <span class="section-title" style="margin:0;white-space:nowrap">Учётные данные</span>
          <hr class="divider" style="flex:1" />
        </div>
        <div class="grid-2">
          {#if needsUuid}
            <div class="field"><label>UUID</label><input class="input mono" bind:value={form.uuid} placeholder="00000000-0000-…" /></div>
          {/if}
          {#if needsPass}
            <div class="field"><label>Пароль</label><input class="input mono" bind:value={form.password} /></div>
          {/if}
          {#if form.type === "shadowsocks"}
            <div class="field">
              <label>Method</label>
              <select class="select" bind:value={form.method}>
                <option>aes-128-gcm</option><option>aes-256-gcm</option><option>chacha20-ietf-poly1305</option><option>2022-blake3-aes-128-gcm</option>
              </select>
            </div>
          {/if}
          {#if form.type === "vmess"}
            <div class="field"><label>AlterID</label><input class="input mono" type="number" bind:value={form.alter_id} /></div>
          {/if}
          {#if form.type === "vless"}
            <div class="field"><label>Flow <span class="hint">xtls-rprx-vision или пусто</span></label><input class="input mono" bind:value={form.flow} placeholder="xtls-rprx-vision" /></div>
          {/if}
        </div>
      </div>

      <!-- TLS -->
      {#if showTLS}
        <div class="stack-sm" style="padding-top:4px">
          <div class="row" style="gap:9px;margin-bottom:8px">
            <span class="section-title" style="margin:0;white-space:nowrap">Безопасность</span>
            <span class="hint-text" style="white-space:nowrap;flex-shrink:0">· TLS и Reality</span>
            <hr class="divider" style="flex:1" />
          </div>
          <div class="card" style="background:var(--bg-soft);padding:2px 14px">
            <div class="toggle-row">
              <div class="toggle-text"><b>TLS</b><span>Шифрование транспорта TLS</span></div>
              <button class={"toggle" + (form.tls ? " on" : "")} onclick={() => form = { ...form, tls: !form.tls }} role="switch" aria-checked={form.tls}></button>
            </div>
          </div>
          {#if form.tls}
            <div class="grid-2">
              <div class="field"><label>SNI <span class="hint">server name</span></label><input class="input mono" bind:value={form.sni} placeholder="www.microsoft.com" /></div>
              <div class="field">
                <label>Fingerprint</label>
                <select class="select" bind:value={form.fingerprint}>
                  <option>chrome</option><option>firefox</option><option>safari</option><option>edge</option><option>random</option>
                </select>
              </div>
            </div>
            {#if form.type === "vless"}
              <div class="card" style="background:var(--bg-soft);padding:2px 14px;margin-top:2px">
                <div class="toggle-row">
                  <div class="toggle-text"><b>Reality</b><span>TLS-маскировка для VLESS</span></div>
                  <button class={"toggle" + (form.public_key ? " on" : "")} onclick={() => form = { ...form, public_key: form.public_key ? "" : " " }} role="switch" aria-checked={!!form.public_key}></button>
                </div>
              </div>
              {#if form.public_key !== undefined && form.public_key !== ""}
                <div class="grid-2">
                  <div class="field"><label>Reality public key</label><input class="input mono" bind:value={form.public_key} /></div>
                  <div class="field"><label>Reality short ID</label><input class="input mono" bind:value={form.short_id} /></div>
                </div>
              {/if}
            {/if}
          {/if}
        </div>
      {/if}

      <!-- Transport -->
      <div class="stack-sm" style="padding-top:4px">
        <div class="row" style="gap:9px;margin-bottom:8px">
          <span class="section-title" style="margin:0;white-space:nowrap">Транспорт</span>
          <hr class="divider" style="flex:1" />
        </div>
        <div class="grid-3">
          <div class="field">
            <label>Транспорт</label>
            <select class="select" bind:value={form.network}>
              <option value="">TCP</option>
              <option value="ws">WebSocket</option>
              <option value="grpc">gRPC</option>
              <option value="http">HTTP</option>
            </select>
          </div>
          {#if form.network === "ws"}
            <div class="field"><label>WS path</label><input class="input mono" bind:value={form.ws_path} placeholder="/path" /></div>
            <div class="field"><label>WS host</label><input class="input mono" bind:value={form.ws_host} placeholder="host header" /></div>
          {/if}
          {#if form.network === "grpc"}
            <div class="field"><label>gRPC service name</label><input class="input mono" bind:value={form.grpc_service_name} /></div>
          {/if}
        </div>
      </div>

      {#if error}<div class="callout err"><Icon name="alert" size={17} /><div class="callout-body">{error}</div></div>{/if}
      {#if notice}<div class="callout ok"><Icon name="check" size={17} /><div class="callout-body">{notice}</div></div>{/if}
      {#if applyCheck && !applyCheck.ok}
        <div class="callout err">
          <Icon name="alert" size={17} />
          <div class="callout-body">
            <b>sing-box check не пройден</b><br />
            <span class="mono" style="font-size:12px">{(applyCheck.errors ?? []).join("\n") || applyCheck.stderr || ""}</span>
          </div>
        </div>
      {/if}

      <div class="row" style="margin-top:4px">
        <button class="btn primary" disabled={!!busy} onclick={save}>
          <Icon name={editing ? "check" : "plus"} size={16} />
          {editing ? "Сохранить сервер" : "Добавить сервер"}
        </button>
      </div>
    </div>
  </div>

  <!-- Server list -->
  <div class="card">
    <div class="card-head">
      <div>
        <h3 class="card-title"><Icon name="database" size={17} />Сохранённые серверы</h3>
      </div>
      <div class="card-head-actions">
        <span class="pill mono">{servers.length}</span>
      </div>
    </div>
    <div class="card-body stack-sm">
      {#if servers.length === 0}
        <div class="empty">
          <div class="empty-icon"><Icon name="server" size={20} /></div>
          <h4>Серверов пока нет</h4>
          <p>Вставьте share-ссылку или заполните форму выше, чтобы добавить первый outbound.</p>
        </div>
      {:else}
        {#each servers as s (s.id)}
          <div class="lrow">
            <div class="lrow-main">
              <div class="lrow-title">
                {s.name || s.server}
                <span class="seg-tag" style="text-transform:uppercase">{s.type}</span>
                {#if s.public_key}<span class="tag" style="color:var(--accent-text)">reality</span>{/if}
              </div>
              <div class="lrow-meta">
                <span class="mono">{s.server}:{s.server_port}</span>
                {#if s.flow}<span class="tag">{s.flow}</span>{/if}
                {#if s.network}<span class="tag">{s.network}</span>{/if}
              </div>
            </div>
            <div class="lrow-actions">
              <button class="btn sm" onclick={() => { form = { ...blank(), ...s }; window.scrollTo({ top: 0 }); }}>
                <Icon name="edit" size={14} />Изменить
              </button>
              <button class="btn sm danger icon" onclick={() => confirmDelete = s} title="Удалить">
                <Icon name="trash" size={14} />
              </button>
            </div>
          </div>
        {/each}
      {/if}
      <hr class="divider" style="margin:6px 0" />
      <div class="row">
        <button class="btn primary" disabled={!!busy || servers.length === 0} onclick={() => confirmApply = true}>
          {#if busy === "apply"}<span class="btn-spinner"></span>{:else}<Icon name="restart" size={16} />{/if}
          Применить и перезапустить
        </button>
        <span class="hint-text">Пересобирает конфиг sing-box из этих серверов и перезапускает.</span>
      </div>
    </div>
  </div>

  <!-- Confirm delete -->
  {#if confirmDelete}
    <div class="modal-scrim" onmousedown={(e) => { if (e.target === e.currentTarget) confirmDelete = null; }}>
      <div class="modal">
        <div class="modal-head">
          <div class="modal-icon danger"><Icon name="trash" size={19} /></div>
          <div style="flex:1;padding-top:2px">
            <h3>Удалить «{confirmDelete.name || confirmDelete.server}»?</h3>
            <p>Сервер исчезнет из списка. Изменения вступят в силу после «Применить и перезапустить».</p>
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn ghost" onclick={() => confirmDelete = null}>Отмена</button>
          <button class="btn danger solid" onclick={() => confirmDelete && doDelete(confirmDelete)}>Удалить</button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Confirm apply -->
  {#if confirmApply}
    <div class="modal-scrim" onmousedown={(e) => { if (e.target === e.currentTarget) confirmApply = false; }}>
      <div class="modal">
        <div class="modal-head">
          <div class="modal-icon accent"><Icon name="info" size={19} /></div>
          <div style="flex:1;padding-top:2px">
            <h3>Применить и перезапустить?</h3>
            <p>Конфиг sing-box будет пересобран из текущего списка серверов и перезапущен. Сначала выполнится sing-box check.</p>
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
