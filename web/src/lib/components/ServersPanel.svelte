<script lang="ts">
  import { api, ApiError } from "../api";
  import type { Server, CheckResult } from "../types";

  let { onApplied }: { onApplied?: () => void } = $props();

  function emptyServer(): Server {
    return { name: "", type: "vless", server: "", server_port: 443, tls: true, network: "" };
  }

  let servers = $state<Server[]>([]);
  let draft = $state<Server>(emptyServer());
  let link = $state("");
  let busy = $state("");
  let error = $state("");
  let notice = $state("");
  let applyCheck = $state<CheckResult | null>(null);

  async function loadList() {
    try {
      servers = await api.serverList();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => { loadList(); });

  async function parse() {
    if (!link.trim()) return;
    busy = "parse";
    error = "";
    notice = "";
    try {
      const s = await api.serverParse(link.trim());
      draft = { ...emptyServer(), ...s };
      notice = "Ссылка разобрана. Проверьте поля и нажмите «Добавить сервер».";
      link = "";
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally {
      busy = "";
    }
  }

  async function save() {
    if (!draft.server || !draft.type) {
      error = "Адрес и тип сервера обязательны.";
      return;
    }
    busy = "save";
    error = "";
    notice = "";
    try {
      await api.serverSave(draft);
      draft = emptyServer();
      await loadList();
      notice = "Сохранено. Нажмите «Применить и перезапустить» чтобы активировать.";
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally {
      busy = "";
    }
  }

  function edit(s: Server) {
    draft = { ...s };
    notice = `Редактирование "${s.name || s.server}".`;
  }

  async function remove(s: Server) {
    if (!s.id) return;
    if (!confirm(`Удалить сервер "${s.name || s.server}"?`)) return;
    busy = "del";
    try {
      await api.serverDelete(s.id);
      await loadList();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = "";
    }
  }

  async function applyAndRestart() {
    busy = "apply";
    error = "";
    notice = "";
    applyCheck = null;
    try {
      const res = await api.serversApply(true);
      applyCheck = res.check;
      if (res.applied) {
        notice = `Применено (${res.servers} сервер(ов)), sing-box перезапущен.`;
        onApplied?.();
      } else {
        error = "Конфиг не прошёл проверку sing-box.";
      }
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally {
      busy = "";
    }
  }

  const showUUID = $derived(draft.type === "vless" || draft.type === "vmess");
  const showPassword = $derived(draft.type === "trojan" || draft.type === "shadowsocks");
  const showTLS = $derived(draft.type !== "shadowsocks");
  const reset = () => { draft = emptyServer(); notice = ""; error = ""; };
</script>

<div class="card">
  <h2>VPN-серверы</h2>

  <!-- Paste a share link -->
  <div class="paste">
    <input
      type="text"
      placeholder="Вставьте vless:// trojan:// ss:// vmess:// ссылку"
      bind:value={link}
      spellcheck="false"
    />
    <button onclick={parse} disabled={busy === "parse" || !link.trim()}>
      {busy === "parse" ? "…" : "Разобрать"}
    </button>
  </div>

  <!-- Form -->
  <div class="form">
    <label class="f"><span>Название</span><input bind:value={draft.name} placeholder="Мой сервер" /></label>
    <label class="f">
      <span>Тип</span>
      <select bind:value={draft.type}>
        <option value="vless">VLESS</option>
        <option value="trojan">Trojan</option>
        <option value="shadowsocks">Shadowsocks</option>
        <option value="vmess">VMess</option>
      </select>
    </label>
    <label class="f wide"><span>Адрес</span><input bind:value={draft.server} placeholder="1.2.3.4 или хост" /></label>
    <label class="f"><span>Порт</span><input type="number" bind:value={draft.server_port} /></label>

    {#if showUUID}
      <label class="f wide"><span>UUID</span><input bind:value={draft.uuid} spellcheck="false" /></label>
    {/if}
    {#if showPassword}
      <label class="f wide"><span>Пароль</span><input bind:value={draft.password} spellcheck="false" /></label>
    {/if}
    {#if draft.type === "shadowsocks"}
      <label class="f"><span>Метод</span><input bind:value={draft.method} placeholder="aes-256-gcm" /></label>
    {/if}
    {#if draft.type === "vmess"}
      <label class="f"><span>AlterID</span><input type="number" bind:value={draft.alter_id} /></label>
    {/if}
    {#if draft.type === "vless"}
      <label class="f"><span>Flow</span><input bind:value={draft.flow} placeholder="xtls-rprx-vision" /></label>
    {/if}

    {#if showTLS}
      <label class="f chk"><input type="checkbox" bind:checked={draft.tls} /><span>TLS</span></label>
      {#if draft.tls}
        <label class="f"><span>SNI</span><input bind:value={draft.sni} /></label>
        <label class="f"><span>Fingerprint</span><input bind:value={draft.fingerprint} placeholder="chrome" /></label>
        <label class="f wide"><span>Reality public key</span><input bind:value={draft.public_key} spellcheck="false" /></label>
        <label class="f"><span>Reality short ID</span><input bind:value={draft.short_id} spellcheck="false" /></label>
      {/if}
    {/if}

    <label class="f">
      <span>Транспорт</span>
      <select bind:value={draft.network}>
        <option value="">TCP</option>
        <option value="ws">WebSocket</option>
        <option value="grpc">gRPC</option>
        <option value="http">HTTP</option>
      </select>
    </label>
    {#if draft.network === "ws"}
      <label class="f"><span>WS path</span><input bind:value={draft.ws_path} placeholder="/path" /></label>
      <label class="f"><span>WS host</span><input bind:value={draft.ws_host} /></label>
    {/if}
    {#if draft.network === "grpc"}
      <label class="f"><span>gRPC service</span><input bind:value={draft.grpc_service_name} /></label>
    {/if}
  </div>

  <div class="row">
    <button class="primary" onclick={save} disabled={!!busy}>
      {draft.id ? "Обновить сервер" : "Добавить сервер"}
    </button>
    {#if draft.id || draft.server}
      <button onclick={reset} disabled={!!busy}>Сбросить</button>
    {/if}
  </div>

  {#if error}<div class="msg err">{error}</div>{/if}
  {#if notice}<div class="msg">{notice}</div>{/if}
  {#if applyCheck && !applyCheck.ok}
    <div class="msg err">
      sing-box check:
      <pre>{(applyCheck.errors ?? []).join("\n") || applyCheck.stderr || ""}</pre>
    </div>
  {/if}

  <!-- Saved servers list -->
  {#if servers.length}
    <div class="list">
      {#each servers as s (s.id)}
        <div class="item">
          <div class="info">
            <strong>{s.name || s.server}</strong>
            <span class="muted">{s.type} · {s.server}:{s.server_port}</span>
          </div>
          <div class="row">
            <button onclick={() => edit(s)} disabled={!!busy}>Изменить</button>
            <button class="danger" onclick={() => remove(s)} disabled={!!busy}>Удалить</button>
          </div>
        </div>
      {/each}
    </div>
    <div class="apply">
      <button class="primary" onclick={applyAndRestart} disabled={!!busy}>
        {busy === "apply" ? "Применение…" : "Применить и перезапустить"}
      </button>
      <span class="muted">Пересобирает конфиг sing-box из этих серверов и перезапускает.</span>
    </div>
  {:else}
    <p class="muted empty">Серверов нет. Вставьте ссылку или заполните форму выше.</p>
  {/if}
</div>

<style>
  h2 { margin: 0 0 12px; font-size: 1.1rem; }
  .paste { display: flex; gap: 8px; margin-bottom: 14px; }
  .paste input { flex: 1; }
  .form {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
    gap: 10px;
  }
  .f { display: flex; flex-direction: column; gap: 4px; font-size: 0.85em; }
  .f.wide { grid-column: span 2; }
  .f > span { color: var(--fg-dim); }
  .f.chk { flex-direction: row; align-items: center; gap: 6px; align-self: end; }
  .row { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 12px; }
  .msg { margin-top: 10px; font-size: 0.88em; }
  .msg.err { color: var(--err); }
  .msg pre {
    margin: 6px 0 0; padding: 8px;
    background: var(--bg); border: 1px solid var(--border);
    border-radius: 6px; white-space: pre-wrap; word-break: break-word;
  }
  .list { margin-top: 16px; display: flex; flex-direction: column; gap: 8px; }
  .item {
    display: flex; justify-content: space-between; align-items: center;
    gap: 10px; padding: 10px 12px;
    background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
  }
  .info { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
  .info span { font-size: 0.82em; }
  .apply {
    display: flex; align-items: center; gap: 12px; flex-wrap: wrap;
    margin-top: 14px; padding-top: 14px; border-top: 1px solid var(--border);
  }
  .apply .muted { font-size: 0.82em; }
  .empty { margin: 14px 0 0; }
</style>
