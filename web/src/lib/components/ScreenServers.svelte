<script lang="ts">
  import { api, ApiError } from "../api";
  import type { Server, Chain, CheckResult } from "../types";
  import { labelOf, hopsLabel } from "../pick";
  import Icon from "./Icon.svelte";
  import ServerEditor from "./ServerEditor.svelte";
  import ChainEditor from "./ChainEditor.svelte";
  import SubsCard from "./SubsCard.svelte";
  import DeleteImpact from "./DeleteImpact.svelte";

  const TYPE_LABEL: Record<string, string> = {
    vless: "VLESS", trojan: "Trojan", shadowsocks: "SS", vmess: "VMess", hysteria2: "HY2", tuic: "TUIC", anytls: "AnyTLS",
  };

  let servers = $state<Server[]>([]);
  let chains = $state<Chain[]>([]);
  let maxChainHops = $state(5);
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
  let warnings = $state<string[]>([]);
  // null = closed, "new" = add, Chain = edit that chain.
  let chainEditor = $state<Chain | "new" | null>(null);
  let confirmDeleteChain = $state<Chain | null>(null);
  // chain id → last latency check: ms, or an error text.
  let chainDelay = $state<Record<string, number | string>>({});
  let confirmApply = $state(false);
  // sub_id → subscription name, for the "from subscription" tag.
  let subNames = $state<Record<string, string>>({});

  async function loadList() {
    try {
      const [st, subs] = await Promise.all([api.serverState(), api.subList().catch(() => [])]);
      subNames = Object.fromEntries(subs.map((x) => [x.id, x.name || x.url]));
      servers = st.servers;
      chains = st.chains;
      maxChainHops = st.max_chain_hops;
      selected = st.selected ?? "";
      autoNow = st.auto_now ?? "";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => { loadList(); });

  // "auto" (urltest) exists only with 2+ plain servers: chains never join it.
  const hasAuto = $derived(servers.length > 1);
  // There's something to pick between: servers and intact chains together.
  const canChoose = $derived(servers.length + chains.filter((c) => !c.broken).length > 1);
  const primary = $derived<Server | Chain | undefined>(servers.find((s) => s.primary) ?? chains.find((c) => c.primary && !c.broken));
  // Live state wins; the stored flag is the fallback when sing-box is down.
  const isMain = (x: Server | Chain) => selected ? selected === x.tag : !!x.primary;
  const nameOf = (tag: string) => {
    const x = servers.find((s) => s.tag === tag) ?? chains.find((c) => c.tag === tag);
    return x ? labelOf(x) : tag;
  };
  const serverName = (id: string) => {
    const s = servers.find((x) => x.id === id);
    return s ? labelOf(s) : "";
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

  async function onChainSaved(c: Chain) {
    const wasEdit = chainEditor !== "new";
    chainEditor = null;
    error = "";
    if (c.id) delete chainDelay[c.id]; // measured over the old hops
    await loadList();
    notice = `${wasEdit ? "Цепочка обновлена" : "Цепочка создана"}: ${c.name || c.hops.map(serverName).join(" → ")}. Нажмите «Применить», чтобы активировать.`;
  }

  async function doDeleteChain(c: Chain) {
    if (!c.id || busy) return;
    confirmDeleteChain = null;
    busy = "delete"; error = ""; notice = ""; warnings = [];
    try {
      warnings = (await api.chainDelete(c.id)).warnings ?? [];
      await loadList();
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  async function checkChain(c: Chain) {
    if (!c.id || !c.tag) return;
    busy = "delay:" + c.id;
    try {
      chainDelay[c.id] = (await api.clashDelay(c.tag, { timeout: 8000 })).delay;
    } catch (e) {
      chainDelay[c.id] = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  async function onSaved(s: Server) {
    const wasEdit = editor !== "new";
    editor = null;
    error = "";
    await loadList();
    notice = `${wasEdit ? "Сервер обновлён" : "Сервер добавлен"}: ${labelOf(s)}. Нажмите «Применить», чтобы активировать.`;
  }

  async function doDelete(s: Server) {
    if (!s.id || busy) return;
    confirmDelete = null;
    busy = "delete"; error = ""; notice = ""; warnings = [];
    try {
      warnings = (await api.serverDelete(s.id)).warnings ?? [];
      await loadList();
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  async function applyAndRestart() {
    busy = "apply"; error = ""; notice = ""; applyCheck = null; warnings = []; confirmApply = false;
    try {
      const res = await api.serversApply(true);
      applyCheck = res.check;
      warnings = res.warnings ?? [];
      if (res.applied) {
        notice = `Применено (${res.servers} серверов), sing-box перезапущен.`;
        chainDelay = {}; // measured against the previous config
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
          <button class="btn sm" disabled={!!busy} onclick={() => setPrimary(primary?.id ?? "", primary ? labelOf(primary) : (hasAuto ? "авто" : nameOf(servers[0].tag ?? "")))}>
            Вернуть на VPN
          </button>
        </div>
      {:else if canChoose}
        <div class="mode-strip">
          {#if selected === "auto" || (!selected && !primary && hasAuto)}
            <span class="mode-label"><Icon name="restart" size={14} />Авто</span>
            <span class="hint-text">самый быстрый по задержке{#if autoNow} · сейчас <b>{nameOf(autoNow)}</b>{/if}</span>
          {:else}
            <span class="mode-label"><Icon name="check" size={14} />Основной</span>
            <span class="hint-text"><b>{selected ? nameOf(selected) : primary ? labelOf(primary) : nameOf(servers[0]?.tag ?? "")}</b> — весь трафик через него</span>
            {#if hasAuto}
              <button class="btn sm ghost" style="margin-left:auto" disabled={!!busy} onclick={() => setPrimary("", "авто (самый быстрый)")}>
                {#if busy === "primary:"}<span class="btn-spinner"></span>{/if}Авто
              </button>
            {/if}
          {/if}
        </div>
      {/if}
      {#if servers.length === 0}
        <div class="empty">
          <div class="empty-icon"><Icon name="server" size={20} /></div>
          <h4>Серверов пока нет</h4>
          <p>Добавьте первый сервер по share-ссылке (vless, trojan, ss, vmess, hy2, tuic, anytls), вручную или через подписку ниже.</p>
          <button class="btn primary" style="margin-top:12px" onclick={() => (editor = "new")}><Icon name="plus" size={16} />Добавить сервер</button>
        </div>
      {:else}
        {#each servers as s (s.id)}
          <div class="lrow">
            <div class="lrow-main">
              <div class="lrow-title">
                {labelOf(s)}
                <span class="seg-tag">{TYPE_LABEL[s.type] ?? s.type}</span>
                {#if s.public_key}<span class="tag" style="color:var(--accent-text)">reality</span>{/if}
                {#if s.obfs_password}<span class="tag" style="color:var(--accent-text)">obfs</span>{/if}
                {#if s.sub_id}<span class="tag" title="Сервер из подписки: обновляется автоматически"><Icon name="link" size={11} />{subNames[s.sub_id] ?? "подписка"}</span>{/if}
                {#if canChoose && isMain(s)}<span class="pill accent">основной</span>{/if}
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
              {#if canChoose && !isMain(s)}
                <button class="btn sm ghost" disabled={!!busy} onclick={() => s.id && setPrimary(s.id, labelOf(s))}>
                  {#if busy === "primary:" + s.id}<span class="btn-spinner"></span>{:else}<Icon name="check" size={14} />{/if}
                  Сделать основным
                </button>
              {/if}
              {#if !s.sub_id}
                <button class="btn sm" onclick={() => (editor = s)}>
                  <Icon name="edit" size={14} />Изменить
                </button>
                <button class="btn sm danger icon" onclick={() => confirmDelete = s} title="Удалить">
                  <Icon name="trash" size={14} />
                </button>
              {/if}
            </div>
          </div>
        {/each}
      {/if}
      {#if error}<div class="callout err"><Icon name="alert" size={17} /><div class="callout-body">{error}</div></div>{/if}
      {#if notice}<div class="callout ok"><Icon name="check" size={17} /><div class="callout-body">{notice}</div></div>{/if}
      {#if warnings.length}
        <div class="callout warn">
          <Icon name="alert" size={17} />
          <div class="callout-body">{#each warnings as w}<div>{w}</div>{/each}</div>
        </div>
      {/if}
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

  <!-- Chains -->
  <div class="card">
    <div class="card-head">
      <div>
        <h3 class="card-title"><Icon name="shuffle" size={17} />Цепочки <span class="pill mono">{chains.length}</span></h3>
        <p class="card-sub">Двойной (и больше) хоп на роутере: каждый следующий сервер подключается через предыдущий. Цепочка выбирается как отдельный выход, в «Авто» не участвует.</p>
      </div>
      <div class="card-head-actions">
        <button class="btn sm" disabled={servers.length < 2} onclick={() => (chainEditor = "new")} title={servers.length < 2 ? "Нужно минимум два сервера" : ""}>
          <Icon name="plus" size={14} />Создать
        </button>
      </div>
    </div>
    <div class="card-body stack-sm">
      {#if chains.length === 0}
        <p class="hint-text">{servers.length < 2 ? "Добавьте хотя бы два сервера, чтобы собрать цепочку." : "Цепочек нет. Например: вход через ближний сервер, выход — через дальний."}</p>
      {:else}
        {#each chains as c (c.id)}
          <div class="lrow">
            <div class="lrow-main">
              <div class="lrow-title">
                {labelOf(c)}
                <span class="seg-tag">{hopsLabel(c.hops.length)}</span>
                {#if c.broken}<span class="tag" style="color:var(--danger-text)" title="Серверы цепочки удалены — она не попадает в конфиг"><Icon name="alert" size={11} />сломана</span>{/if}
                {#if canChoose && !c.broken && isMain(c)}<span class="pill accent">основной</span>{/if}
                {#if c.id && typeof chainDelay[c.id] === "number"}<span class="pill">{chainDelay[c.id]} мс</span>{/if}
              </div>
              <div class="lrow-meta">
                <span class="chain-path">
                  {#each c.hops as id, i (id + i)}
                    {#if i > 0}<Icon name="arrowRight" size={12} />{/if}
                    {#if c.missing?.includes(id)}<span class="missing">удалён</span>{:else}<span>{serverName(id)}</span>{/if}
                  {/each}
                </span>
                {#if c.id && typeof chainDelay[c.id] === "string"}<span class="tag" style="color:var(--danger-text)">{chainDelay[c.id]}</span>{/if}
              </div>
            </div>
            <div class="lrow-actions">
              {#if !c.broken}
                <button class="btn sm ghost" disabled={!!busy} onclick={() => checkChain(c)} title="Замерить задержку через цепочку (после «Применить»)">
                  {#if busy === "delay:" + c.id}<span class="btn-spinner"></span>{:else}<Icon name="activity" size={14} />{/if}
                  Проверить
                </button>
              {/if}
              {#if canChoose && !c.broken && !isMain(c)}
                <button class="btn sm ghost" disabled={!!busy} onclick={() => c.id && setPrimary(c.id, labelOf(c))}>
                  {#if busy === "primary:" + c.id}<span class="btn-spinner"></span>{:else}<Icon name="check" size={14} />{/if}
                  Сделать основным
                </button>
              {/if}
              <button class="btn sm" onclick={() => (chainEditor = c)}>
                <Icon name="edit" size={14} />Изменить
              </button>
              <button class="btn sm danger icon" onclick={() => confirmDeleteChain = c} title="Удалить">
                <Icon name="trash" size={14} />
              </button>
            </div>
          </div>
        {/each}
      {/if}
    </div>
  </div>

  <SubsCard onchanged={loadList} />

  {#if chainEditor}
    <ChainEditor
      initial={chainEditor === "new" ? null : chainEditor}
      {servers}
      maxHops={maxChainHops}
      onclose={() => (chainEditor = null)}
      onsaved={onChainSaved} />
  {/if}

  <!-- Confirm chain delete -->
  {#if confirmDeleteChain}
    <div class="modal-scrim" onmousedown={(e) => { if (e.target === e.currentTarget) confirmDeleteChain = null; }}>
      <div class="modal">
        <div class="modal-head">
          <div class="modal-icon danger"><Icon name="trash" size={19} /></div>
          <div style="flex:1;padding-top:2px">
            <h3>Удалить цепочку «{labelOf(confirmDeleteChain)}»?</h3>
            <p>Серверы останутся. Изменения вступят в силу после «Применить и перезапустить».</p>
            {#if confirmDeleteChain.id}
              {@const id = confirmDeleteChain.id}
              <DeleteImpact load={() => api.chainDelete(id, true)} />
            {/if}
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn ghost" onclick={() => confirmDeleteChain = null}>Отмена</button>
          <button class="btn danger solid" disabled={!!busy} onclick={() => confirmDeleteChain && doDeleteChain(confirmDeleteChain)}>Удалить</button>
        </div>
      </div>
    </div>
  {/if}

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
            {#if confirmDelete.id}
              {@const id = confirmDelete.id}
              <DeleteImpact load={() => api.serverDelete(id, true)} />
            {/if}
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn ghost" onclick={() => confirmDelete = null}>Отмена</button>
          <button class="btn danger solid" disabled={!!busy} onclick={() => confirmDelete && doDelete(confirmDelete)}>Удалить</button>
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
  .chain-path {
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 4px;
  }
  .chain-path .missing { color: var(--danger-text); text-decoration: line-through; }
</style>
