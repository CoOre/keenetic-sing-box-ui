<script lang="ts">
  import { api, ApiError } from "../api";
  import type { ClashConnection, ClashConnectionsSnapshot } from "../types";
  import Icon from "./Icon.svelte";

  const POLL_MS = 2000;

  let snap = $state<ClashConnectionsSnapshot | null>(null);
  let online = $state(false);
  let error = $state("");
  let paused = $state(false);
  let search = $state("");
  let outFilter = $state<"all" | "proxy" | "direct">("all");
  let sortKey = $state<"download" | "upload" | "start" | "rate">("rate");
  let killing = $state<Record<string, boolean>>({});
  let confirmKillAll = $state(false);
  let killingAll = $state(false);
  // тик раз в секунду, чтобы колонка «Время» жила между опросами
  let nowTick = $state(Date.now());

  // Скорость на соединение считаем по дельте байт между опросами.
  let rates: Record<string, { down: number; up: number }> = {};
  let prev: Record<string, { download: number; upload: number; t: number }> = {};

  async function poll() {
    try {
      const s = await api.clashConnections();
      const t = Date.now();
      const next: typeof prev = {};
      const nextRates: typeof rates = {};
      for (const c of s.connections ?? []) {
        const p = prev[c.id];
        if (p && t > p.t) {
          const dt = (t - p.t) / 1000;
          nextRates[c.id] = {
            down: Math.max(0, (c.download - p.download) / dt),
            up: Math.max(0, (c.upload - p.upload) / dt),
          };
        }
        next[c.id] = { download: c.download, upload: c.upload, t };
      }
      prev = next;
      rates = nextRates;
      snap = s;
      online = true;
      error = "";
    } catch (e) {
      online = false;
      error = e instanceof ApiError ? e.message : String(e);
    }
  }

  $effect(() => {
    if (paused) return;
    poll();
    const id = setInterval(poll, POLL_MS);
    return () => clearInterval(id);
  });

  $effect(() => {
    const id = setInterval(() => (nowTick = Date.now()), 1000);
    return () => clearInterval(id);
  });

  function outboundKind(c: ClashConnection): "proxy" | "direct" | "reject" {
    const final = c.chains[0] ?? "";
    if (final === "direct") return "direct";
    if (final === "block" || final === "reject" || final === "dns-out") return "reject";
    return "proxy";
  }

  function chainTitle(c: ClashConnection): string {
    return [...c.chains].reverse().join(" → ") + (c.rule ? `\nправило: ${c.rule}` : "");
  }

  function fmtBytes(n: number): string {
    if (n < 1024) return `${n.toFixed(0)} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
    return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
  }

  function fmtRate(n: number): string {
    if (n < 512) return ""; // шум не показываем
    return `${fmtBytes(n)}/s`;
  }

  function fmtDur(start: string, now: number): string {
    const s = Math.max(0, Math.floor((now - Date.parse(start)) / 1000));
    if (s < 60) return `${s}с`;
    if (s < 3600) return `${Math.floor(s / 60)}м ${s % 60}с`;
    return `${Math.floor(s / 3600)}ч ${Math.floor((s % 3600) / 60)}м`;
  }

  function matches(c: ClashConnection, q: string): boolean {
    if (!q) return true;
    const m = c.metadata;
    return [m.host, m.sourceIP, m.destinationIP, c.rule, ...c.chains]
      .filter(Boolean)
      .some((v) => String(v).toLowerCase().includes(q));
  }

  const conns = $derived.by(() => {
    const q = search.trim().toLowerCase();
    const list = (snap?.connections ?? []).filter((c) => {
      if (outFilter !== "all" && outboundKind(c) !== outFilter) return false;
      return matches(c, q);
    });
    const key = sortKey;
    return list.sort((a, b) => {
      switch (key) {
        case "download": return b.download - a.download;
        case "upload": return b.upload - a.upload;
        case "start": return Date.parse(b.start) - Date.parse(a.start);
        case "rate": {
          const ra = rates[a.id], rb = rates[b.id];
          const va = ra ? ra.down + ra.up : 0;
          const vb = rb ? rb.down + rb.up : 0;
          return vb - va || b.download - a.download;
        }
      }
    });
  });

  const total = $derived(snap?.connections?.length ?? 0);
  const directCount = $derived((snap?.connections ?? []).filter((c) => outboundKind(c) === "direct").length);

  async function kill(id: string) {
    killing = { ...killing, [id]: true };
    try {
      await api.clashConnectionClose(id);
      await poll();
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally {
      const { [id]: _, ...rest } = killing;
      killing = rest;
    }
  }

  async function killAll() {
    killingAll = true;
    try {
      await api.clashConnectionsCloseAll();
      confirmKillAll = false;
      await poll();
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally {
      killingAll = false;
    }
  }
</script>

<div class="page wide stack">
  {#if snap}
    <div class="tiles conn-tiles">
      <div class="tile">
        <span class="tile-label">Соединений</span>
        <span class="tile-val">{total}</span>
      </div>
      <div class="tile">
        <span class="tile-label">Напрямую (direct)</span>
        <span class={"tile-val " + (directCount > 0 ? "warn" : "")}>{directCount}</span>
      </div>
      <div class="tile">
        <span class="tile-label">Скачано за сессию</span>
        <span class="tile-val">{fmtBytes(snap.downloadTotal)}</span>
      </div>
      <div class="tile">
        <span class="tile-label">Отдано за сессию</span>
        <span class="tile-val">{fmtBytes(snap.uploadTotal)}</span>
      </div>
    </div>
  {/if}

  <div class="card flush">
    <div class="card-head conn-head">
      <div>
        <h3 class="card-title">
          <Icon name="connections" size={17} />Живые соединения
          {#if online}
            <span class="pill ok"><span class="dot live"></span>live</span>
          {:else}
            <span class="pill warn">offline</span>
          {/if}
        </h3>
        <p class="card-sub">Кто из LAN куда ходит и через какой outbound</p>
      </div>
      <div class="card-head-actions conn-actions">
        <input
          class="input sm-input"
          type="search"
          placeholder="Фильтр: домен, IP, правило…"
          bind:value={search}
        />
        <select class="select sm-input" bind:value={outFilter}>
          <option value="all">Все outbound</option>
          <option value="proxy">Через прокси</option>
          <option value="direct">Напрямую</option>
        </select>
        <button
          class="btn sm icon"
          title={paused ? "Продолжить обновление" : "Приостановить обновление"}
          onclick={() => (paused = !paused)}
        >
          <Icon name={paused ? "play" : "pause"} size={14} />
        </button>
        <button
          class="btn sm danger"
          disabled={!online || total === 0}
          onclick={() => (confirmKillAll = true)}
        >
          <Icon name="x" size={14} />Разорвать все
        </button>
      </div>
    </div>

    {#if error && !snap}
      <div class="card-body">
        <p class="hint-text" style="display:flex;gap:7px;align-items:center">
          <Icon name="info" size={14} />Таблица доступна при запущенном sing-box с
          <span class="tag">clash_api</span> на <span class="tag">127.0.0.1:9090</span>.
        </p>
      </div>
    {:else if conns.length === 0}
      <div class="empty">
        <div class="empty-icon"><Icon name="connections" size={20} /></div>
        <h4>{total === 0 ? "Нет активных соединений" : "Ничего не найдено"}</h4>
        <p>
          {total === 0
            ? "Через sing-box сейчас ничего не идёт. Трафик вне route-ipset сюда не попадает — он минует прокси на уровне iptables."
            : "Под текущий фильтр не подходит ни одно соединение."}
        </p>
      </div>
    {:else}
      <div class="tbl-wrap">
        <table>
          <thead>
            <tr>
              <th>Клиент</th>
              <th>Назначение</th>
              <th>Правило</th>
              <th>Outbound</th>
              <th class={"sort" + (sortKey === "rate" ? " on" : "")} onclick={() => (sortKey = "rate")}>Скорость</th>
              <th class={"sort" + (sortKey === "download" ? " on" : "")} onclick={() => (sortKey = "download")}>↓</th>
              <th class={"sort" + (sortKey === "upload" ? " on" : "")} onclick={() => (sortKey = "upload")}>↑</th>
              <th class={"sort" + (sortKey === "start" ? " on" : "")} onclick={() => (sortKey = "start")}>Время</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {#each conns as c (c.id)}
              {@const kind = outboundKind(c)}
              {@const r = rates[c.id]}
              <tr>
                <td class="mono nowrap">
                  {c.metadata.sourceIP}<span class="dim">:{c.metadata.sourcePort}</span>
                </td>
                <td class="dest">
                  {#if c.metadata.host}
                    <span class="host" title={c.metadata.host}>{c.metadata.host}</span>
                    <span class="sub mono">{c.metadata.destinationIP}:{c.metadata.destinationPort} · {c.metadata.network}</span>
                  {:else}
                    <span class="host mono">{c.metadata.destinationIP}:{c.metadata.destinationPort}</span>
                    <span class="sub mono">{c.metadata.network}</span>
                  {/if}
                </td>
                <td class="rule mono" title={c.rule}>{c.rule || "—"}</td>
                <td>
                  <span
                    class={"pill mono " + (kind === "proxy" ? "ok" : kind === "direct" ? "warn" : "err")}
                    title={chainTitle(c)}
                  >{c.chains[0] ?? "?"}</span>
                </td>
                <td class="mono nowrap rate">{r ? fmtRate(r.down + r.up) : ""}</td>
                <td class="mono nowrap">{fmtBytes(c.download)}</td>
                <td class="mono nowrap">{fmtBytes(c.upload)}</td>
                <td class="mono nowrap dim">{fmtDur(c.start, nowTick)}</td>
                <td class="act">
                  <button
                    class="btn xs icon danger"
                    title="Разорвать соединение"
                    disabled={!!killing[c.id]}
                    onclick={() => kill(c.id)}
                  >
                    {#if killing[c.id]}<span class="btn-spinner"></span>{:else}<Icon name="x" size={13} />{/if}
                  </button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </div>

  {#if error && snap}
    <p class="hint-text" style="display:flex;gap:7px;align-items:center">
      <Icon name="warn" size={14} />{error}
    </p>
  {/if}

  {#if confirmKillAll}
    <div class="modal-scrim" onmousedown={(e) => { if (e.target === e.currentTarget) confirmKillAll = false; }}>
      <div class="modal">
        <div class="modal-head">
          <div class="modal-icon danger"><Icon name="alert" size={19} /></div>
          <div style="flex:1;padding-top:2px">
            <h3>Разорвать все соединения?</h3>
            <p>sing-box закроет {total} активных соединений. Клиенты переподключатся сами, но загрузки и стримы прервутся.</p>
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn ghost" onclick={() => (confirmKillAll = false)}>Отмена</button>
          <button class="btn danger solid" disabled={killingAll} onclick={killAll}>
            {#if killingAll}<span class="btn-spinner"></span>{/if}
            Разорвать все
          </button>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  .conn-tiles {
    grid-template-columns: repeat(4, 1fr);
  }
  .conn-head {
    flex-wrap: wrap;
    gap: 10px;
  }
  .conn-actions {
    flex-wrap: wrap;
  }
  .sm-input {
    height: 32px;
    font-size: 12.5px;
  }
  input.sm-input {
    width: 210px;
  }
  select.sm-input {
    width: auto;
  }
  .tbl-wrap {
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12.5px;
  }
  th {
    text-align: left;
    font-size: 10.5px;
    text-transform: uppercase;
    letter-spacing: 0.07em;
    font-weight: 600;
    color: var(--text-faint);
    padding: 9px 12px;
    border-bottom: 1px solid var(--border-soft);
    white-space: nowrap;
    user-select: none;
  }
  th.sort {
    cursor: pointer;
  }
  th.sort:hover {
    color: var(--text-dim);
  }
  th.sort.on {
    color: var(--accent-text);
  }
  td {
    padding: 8px 12px;
    border-bottom: 1px solid var(--border-soft);
    vertical-align: middle;
  }
  tbody tr:last-child td {
    border-bottom: none;
  }
  tbody tr:hover td {
    background: var(--surface-2);
  }
  .nowrap {
    white-space: nowrap;
  }
  .dim {
    color: var(--text-faint);
  }
  .dest {
    max-width: 260px;
  }
  .dest .host {
    display: block;
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .dest .sub {
    display: block;
    font-size: 11px;
    color: var(--text-faint);
    margin-top: 1px;
  }
  .rule {
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text-dim);
    font-size: 11.5px;
  }
  .rate {
    color: var(--ok-text);
    font-size: 11.5px;
  }
  .act {
    text-align: right;
  }
</style>
