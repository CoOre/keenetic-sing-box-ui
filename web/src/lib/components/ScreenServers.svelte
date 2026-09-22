<script lang="ts">
  import { api, ApiError } from "../api";
  import type { Server, CheckResult } from "../types";
  import Icon from "./Icon.svelte";
  import ServerEditor from "./ServerEditor.svelte";

  const TYPE_LABEL: Record<string, string> = {
    vless: "VLESS", trojan: "Trojan", shadowsocks: "SS", vmess: "VMess", hysteria2: "HY2",
  };

  let servers = $state<Server[]>([]);
  // Live "proxy" selector: server tag, "auto" or "direct"; "" = sing-box unreachable.
  let selected = $state("");
  let autoNow = $state("");
  // null = closed, "new" = add, Server = edit that entry.
  let editor = $state<Server | "new" | null>(null);
  let busy = $state("");
  let error = $state("");
  let notice = $state("");
  let applyCheck = $state<CheckResult | null>(null);
  let confirmDelete = $state<Server | null>(null);
  let confirmApply = $state(false);

  async function loadList() {
    try {
      const st = await api.serverState();
      servers = st.servers;
      selected = st.selected ?? "";
      autoNow = st.auto_now ?? "";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => { loadList(); });

  const multi = $derived(servers.length > 1);
  const primary = $derived(servers.find((s) => s.primary));
  // Live state wins; the stored flag is the fallback when sing-box is down.
  const isMain = (s: Server) => selected ? selected === s.tag : !!s.primary;
  const nameOf = (tag: string) => {
    const s = servers.find((x) => x.tag === tag);
    return s ? (s.name || s.server) : tag;
  };

  async function setPrimary(id: string, label: string) {
    busy = "primary:" + id; error = ""; notice = "";
    try {
      const r = await api.serverSetPrimary(id);
      await loadList();
      notice = r.live
        ? `Трафик переключён: ${label}.`
        : `Сохранено: ${label}. Вступит в силу после «Применить и перезапустить».`;
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  async function onSaved(s: Server) {
    const wasEdit = editor !== "new";
    editor = null;
    error = "";
    await loadList();
    notice = `${wasEdit ? "Сервер обновлён" : "Сервер добавлен"}: ${s.name || s.server}. Нажмите «Применить», чтобы активировать.`;
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
      if (res.applied) {
        notice = `Применено (${res.servers} серверов), sing-box перезапущен.`;
        // The backend re-selects the primary once the Clash API is back up.
        setTimeout(loadList, 2500);
      }
      else { error = "Конфиг не прошёл проверку sing-box."; }
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }
</script>

<div class="page stack">
  <!-- Server list -->
  <div class="card">
    <div class="card-head">
      <div>
        <h3 class="card-title"><Icon name="database" size={17} />Список серверов <span class="pill mono">{servers.length}</span></h3>
        <p class="card-sub">Outbound'ы sing-box. Изменения вступают в силу после «Применить».</p>
      </div>
      <div class="card-head-actions">
        <button class="btn sm primary" onclick={() => (editor = "new")}><Icon name="plus" size={14} />Добавить</button>
      </div>
    </div>
    <div class="card-body stack-sm">
      {#if selected === "direct" && servers.length > 0}
        <div class="callout warn">
          <Icon name="alert" size={17} />
          <div class="callout-body">
            <b>Выбран прямой выход (direct)</b><br />
            Трафик из маршрутизации сейчас идёт мимо VPN.
          </div>
          <button class="btn sm" disabled={!!busy} onclick={() => setPrimary(primary?.id ?? "", primary ? (primary.name || primary.server) : (multi ? "авто" : nameOf(servers[0].tag ?? "")))}>
            Вернуть на VPN
          </button>
        </div>
      {:else if multi}
        <div class="mode-strip">
          {#if selected === "auto" || (!selected && !primary)}
            <span class="mode-label"><Icon name="restart" size={14} />Авто</span>
            <span class="hint-text">самый быстрый по задержке{#if autoNow} · сейчас <b>{nameOf(autoNow)}</b>{/if}</span>
          {:else}
            <span class="mode-label"><Icon name="check" size={14} />Основной</span>
            <span class="hint-text"><b>{selected ? nameOf(selected) : (primary?.name || primary?.server)}</b> — весь трафик через него</span>
            <button class="btn sm ghost" style="margin-left:auto" disabled={!!busy} onclick={() => setPrimary("", "авто (самый быстрый)")}>
              {#if busy === "primary:"}<span class="btn-spinner"></span>{/if}Авто
            </button>
          {/if}
        </div>
      {/if}
      {#if servers.length === 0}
        <div class="empty">
          <div class="empty-icon"><Icon name="server" size={20} /></div>
          <h4>Серверов пока нет</h4>
          <p>Добавьте первый сервер по share-ссылке (vless, trojan, ss, vmess, hy2) или вручную.</p>
          <button class="btn primary" style="margin-top:12px" onclick={() => (editor = "new")}><Icon name="plus" size={16} />Добавить сервер</button>
        </div>
      {:else}
        {#each servers as s (s.id)}
          <div class="lrow">
            <div class="lrow-main">
              <div class="lrow-title">
                {s.name || s.server}
                <span class="seg-tag">{TYPE_LABEL[s.type] ?? s.type}</span>
                {#if s.public_key}<span class="tag" style="color:var(--accent-text)">reality</span>{/if}
                {#if s.obfs_password}<span class="tag" style="color:var(--accent-text)">obfs</span>{/if}
                {#if multi && isMain(s)}<span class="pill accent">основной</span>{/if}
                {#if selected === "auto" && autoNow === s.tag}<span class="pill">авто · сейчас</span>{/if}
              </div>
              <div class="lrow-meta">
                <span class="mono">{s.server}{s.server_port ? `:${s.server_port}` : ""}</span>
                {#if s.server_ports?.length}<span class="tag mono">hop {s.server_ports.map((p) => p.replace(":", "-")).join(",")}</span>{/if}
                {#if s.flow}<span class="tag">{s.flow}</span>{/if}
                {#if s.network}<span class="tag">{s.network}</span>{/if}
              </div>
            </div>
            <div class="lrow-actions">
              {#if multi && !isMain(s)}
                <button class="btn sm ghost" disabled={!!busy} onclick={() => s.id && setPrimary(s.id, s.name || s.server)}>
                  {#if busy === "primary:" + s.id}<span class="btn-spinner"></span>{:else}<Icon name="check" size={14} />{/if}
                  Сделать основным
                </button>
              {/if}
              <button class="btn sm" onclick={() => (editor = s)}>
                <Icon name="edit" size={14} />Изменить
              </button>
              <button class="btn sm danger icon" onclick={() => confirmDelete = s} title="Удалить">
                <Icon name="trash" size={14} />
              </button>
            </div>
          </div>
        {/each}
      {/if}
      {#if error}<div class="callout err"><Icon name="alert" size={17} /><div class="callout-body">{error}</div></div>{/if}
      {#if notice}<div class="callout ok"><Icon name="check" size={17} /><div class="callout-body">{notice}</div></div>{/if}
      {#if applyCheck && !applyCheck.ok}
        <div class="callout err">
          <Icon name="alert" size={17} />
          <div class="callout-body">
            <b>sing-box check не пройден</b><br />
            <span class="mono" style="font-size:12px;white-space:pre-wrap">{(applyCheck.errors ?? []).join("\n") || applyCheck.stderr || ""}</span>
          </div>
        </div>
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

  {#if editor}
    <ServerEditor
      initial={editor === "new" ? null : editor}
      onclose={() => (editor = null)}
      onsaved={onSaved} />
  {/if}

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

<style>
  .mode-strip {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    padding: 8px 12px;
    border: 1px solid var(--border-soft);
    border-radius: var(--r);
    background: var(--bg-soft);
    min-height: 44px;
  }
  .mode-label {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    font-weight: 600;
    color: var(--accent-text);
  }
  .mode-strip b { color: var(--text); font-weight: 500; }
</style>
