<script lang="ts">
  import { api, ApiError } from "../api";
  import type { SystemInfo, InstallStatus } from "../types";
  import Icon from "./Icon.svelte";

  let { onNav }: { onNav: (r: string) => void } = $props();

  let info = $state<SystemInfo | null>(null);
  let install = $state<InstallStatus | null>(null);
  let busy = $state("");
  let confirmAction = $state("");
  let error = $state("");

  // Traffic via Clash API stream
  let trafficConnected = $state(false);
  let downHistory = $state<number[]>(Array(48).fill(0));
  let upHistory = $state<number[]>(Array(48).fill(0));
  // EMA-сглаживание (байт/с): clash отдаёт мгновенную скорость раз в секунду,
  // без сглаживания бёрстовый трафик рисует «расчёску» из нулей
  let downAvg = $state(0);
  let upAvg = $state(0);
  const EMA_ALPHA = 0.35;

  async function load() {
    try {
      const [sys, ins] = await Promise.all([api.system(), api.installStatus()]);
      info = sys;
      install = ins;
      error = "";
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    }
  }

  $effect(() => { load(); });

  // Traffic stream
  $effect(() => {
    const ctrl = new AbortController();
    (async () => {
      try {
        const resp = await fetch(api.clashTrafficURL(), { credentials: "same-origin", signal: ctrl.signal });
        if (!resp.ok || !resp.body) return;
        trafficConnected = true;
        const reader = resp.body.getReader();
        const dec = new TextDecoder();
        let buf = "";
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += dec.decode(value, { stream: true });
          let nl: number;
          while ((nl = buf.indexOf("\n")) >= 0) {
            const line = buf.slice(0, nl).trim();
            buf = buf.slice(nl + 1);
            if (!line) continue;
            try {
              const t = JSON.parse(line);
              downAvg += ((t.down ?? 0) - downAvg) * EMA_ALPHA;
              upAvg += ((t.up ?? 0) - upAvg) * EMA_ALPHA;
              downHistory = [...downHistory.slice(1), downAvg / 1024 / 1024];
              upHistory = [...upHistory.slice(1), upAvg / 1024 / 1024];
            } catch { /* ignore */ }
          }
        }
      } catch { /* ignore */ }
      finally { trafficConnected = false; }
    })();
    return () => ctrl.abort();
  });

  async function svcAction(action: "start" | "stop" | "restart" | "enable" | "disable") {
    busy = action;
    error = "";
    confirmAction = "";
    try {
      await api.service(action);
      await load();
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  function fmtBytes(n: number): [string, string] {
    if (n < 1024) return [n.toFixed(0), "B/s"];
    if (n < 1024 * 1024) return [(n / 1024).toFixed(1), "KB/s"];
    return [(n / 1024 / 1024).toFixed(2), "MB/s"];
  }

  // Гладкая кривая по точкам (Catmull-Rom → кубические Безье)
  function smoothPath(data: number[], w: number, h: number, max: number): string {
    const n = data.length;
    const px = (i: number) => (i / (n - 1)) * w;
    const py = (i: number) => h - (data[i] / max) * (h - 6) - 3;
    const cy = (y: number) => Math.min(Math.max(y, 2), h - 1);
    let d = `M ${px(0)},${py(0)}`;
    for (let i = 0; i < n - 1; i++) {
      const y0 = py(Math.max(i - 1, 0)), y1 = py(i), y2 = py(i + 1), y3 = py(Math.min(i + 2, n - 1));
      const x1 = px(i), x2 = px(i + 1);
      const dx = (x2 - x1) / 3;
      d += ` C ${x1 + dx},${cy(y1 + (y2 - y0) / 6)} ${x2 - dx},${cy(y2 - (y3 - y1) / 6)} ${x2},${y2}`;
    }
    return d;
  }

  const CHART_W = 100;
  const CHART_H = 48;
  // Общая шкала для обеих серий — один график
  const chartMax = $derived(Math.max(...downHistory, ...upHistory, 0.01));
  const downLine = $derived(smoothPath(downHistory, CHART_W, CHART_H, chartMax));
  const upLine = $derived(smoothPath(upHistory, CHART_W, CHART_H, chartMax));
  const downArea = $derived(`${downLine} L ${CHART_W},${CHART_H} L 0,${CHART_H} Z`);
  const upArea = $derived(`${upLine} L ${CHART_W},${CHART_H} L 0,${CHART_H} Z`);

  const installed = $derived(install?.installed ?? false);
  const svcPresent = $derived(info?.service?.present ?? false);
  const svcEnabled = $derived(info?.service?.enabled ?? false);
  const running = $derived(installed && svcPresent && (info?.service?.running ?? false));

  const [d, du] = $derived(fmtBytes(downAvg));
  const [u, uu] = $derived(fmtBytes(upAvg));
</script>

<div class="page stack">
  <!-- attention callout -->
  {#if !installed}
    <div class="callout warn">
      <Icon name="warn" size={17} />
      <div class="callout-body">
        <b>sing-box не установлен</b><br />
        Роутер готов (Entware на месте), но ядро sing-box ещё не установлено.
        <div class="callout-actions">
          <button class="btn sm primary" onclick={() => onNav("setup")}>
            Перейти к установке <Icon name="arrowRight" size={14} />
          </button>
        </div>
      </div>
    </div>
  {:else if !running}
    <div class="callout warn">
      <Icon name="warn" size={17} />
      <div class="callout-body">
        <b>Сервис остановлен</b><br />
        Трафик идёт напрямую — VPN-маршрутизация сейчас не работает.
        <div class="callout-actions">
          <button class="btn sm primary" disabled={busy === "start"} onclick={() => svcAction("start")}>
            {#if busy === "start"}<span class="btn-spinner"></span>{:else}<Icon name="play" size={14} />{/if}
            Запустить сервис
          </button>
        </div>
      </div>
    </div>
  {:else}
    <div class="callout ok">
      <Icon name="check" size={17} />
      <div class="callout-body">
        <b>Всё работает</b><br />
        sing-box запущен, маршрутизация активна. Трафик к выбранным доменам и подсетям идёт через VPN.
      </div>
    </div>
  {/if}

  {#if error}
    <div class="callout err"><Icon name="alert" size={17} /><div class="callout-body">{error}</div></div>
  {/if}

  <!-- status tiles -->
  {#if info && install}
    <div>
      <div class="row" style="margin-bottom:12px">
        <h2 class="section-title" style="margin:0">Состояние окружения</h2>
      </div>
      <div class="tiles">
        <div class="tile">
          <span class="tile-label">Платформа</span>
          <span class="tile-val mono">{info.os}/{info.arch}</span>
        </div>
        <div class="tile">
          <span class="tile-label">Entware</span>
          <span class={"tile-val " + (info.entware ? "ok" : "err")}>
            <span class={"dot " + (info.entware ? "ok" : "err")}></span>
            {info.entware ? "present" : "missing"}
          </span>
        </div>
        <div class="tile">
          <span class="tile-label">sing-box</span>
          <span class={"tile-val " + (installed ? "ok" : "err")}>
            <span class={"dot " + (installed ? "ok" : "err")}></span>
            {installed ? (install.version ?? "installed") : "не установлен"}
          </span>
        </div>
        <div class="tile">
          <span class="tile-label">Сервис</span>
          <span class={"tile-val " + (running ? "ok" : (installed ? "warn" : "err"))}>
            <span class={"dot " + (running ? "ok" : (installed ? "warn" : "err"))}></span>
            {running ? "активен" : (installed ? "остановлен" : "—")}
          </span>
        </div>
      </div>
    </div>

    <div class="grid-2">
      <!-- service control -->
      <div class="card">
        <div class="card-head">
          <div>
            <h3 class="card-title"><Icon name="power" size={17} />Сервис</h3>
            <p class="card-sub">Управление ядром sing-box</p>
          </div>
        </div>
        <div class="card-body stack-sm">
          <div class="row" style="gap:12px">
            <span class={"pill " + (running ? "ok" : "warn") + " mono"}>
              <span class={"dot " + (running ? "live" : "warn")}></span>
              {running ? "running" : "stopped"}
            </span>
            <span class="hint-text">{svcEnabled ? "autostart включён" : "autostart выключен"}</span>
          </div>
          <div class="row-wrap" style="margin-top:4px">
            {#if !running}
              <button class="btn primary" disabled={busy === "start" || !installed} onclick={() => svcAction("start")}>
                {#if busy === "start"}<span class="btn-spinner"></span>{:else}<Icon name="play" size={16} />{/if}
                Запустить
              </button>
            {:else}
              <button class="btn" disabled={!!busy} onclick={() => { confirmAction = "restart"; }}>
                <Icon name="restart" size={16} />Перезапустить
              </button>
              <button class="btn danger" disabled={!!busy} onclick={() => { confirmAction = "stop"; }}>
                <Icon name="stop" size={16} />Остановить
              </button>
            {/if}
          </div>
          <hr class="divider" style="margin:6px 0" />
          <div class="toggle-row">
            <div class="toggle-text">
              <b>Автозапуск</b>
              <span>Стартовать sing-box при загрузке роутера</span>
            </div>
            <button
              class={"toggle" + (svcEnabled ? " on" : "")}
              onclick={() => svcAction(svcEnabled ? "disable" : "enable")}
              disabled={!!busy || !installed}
              role="switch"
              aria-checked={svcEnabled}
            ></button>
          </div>
          <p class="hint-text" style="display:flex;gap:7px;align-items:center">
            <Icon name="info" size={14} />Остановка и перезапуск разрывают активные соединения.
          </p>
        </div>
      </div>

      <!-- traffic -->
      <div class="card card-fill">
        <div class="card-head">
          <div>
            <h3 class="card-title"><Icon name="activity" size={17} />Трафик</h3>
          </div>
          <div class="card-head-actions">
            {#if trafficConnected}
              <span class="pill ok"><span class="dot live"></span>live</span>
            {:else}
              <span class="pill warn">offline</span>
            {/if}
          </div>
        </div>
        <div class="card-body">
          <div class="traffic-chart">
            {#if trafficConnected}
              <svg class="spark" viewBox="0 0 {CHART_W} {CHART_H}" preserveAspectRatio="none">
                <defs>
                  <linearGradient id="gd" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stop-color="var(--ok)" stop-opacity="0.22"/>
                    <stop offset="100%" stop-color="var(--ok)" stop-opacity="0"/>
                  </linearGradient>
                  <linearGradient id="gu" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stop-color="var(--accent)" stop-opacity="0.22"/>
                    <stop offset="100%" stop-color="var(--accent)" stop-opacity="0"/>
                  </linearGradient>
                </defs>
                <path d={downArea} fill="url(#gd)"/>
                <path d={upArea} fill="url(#gu)"/>
                <path d={downLine} fill="none" stroke="var(--ok)" stroke-width="1.6" stroke-linejoin="round" vector-effect="non-scaling-stroke"/>
                <path d={upLine} fill="none" stroke="var(--accent)" stroke-width="1.6" stroke-linejoin="round" vector-effect="non-scaling-stroke"/>
              </svg>
            {:else}
              <span class="spark-base"></span>
            {/if}
            <div class="chart-overlay">
              <span class="chart-stat"><Icon name="download" size={13} style="color:var(--ok)" />{d}<small>{du}</small></span>
              <span class="chart-stat"><Icon name="upload" size={13} style="color:var(--accent)" />{u}<small>{uu}</small></span>
            </div>
          </div>
          {#if !trafficConnected}
            <p class="hint-text" style="margin-top:14px;display:flex;gap:7px">
              <Icon name="info" size={14} />Метрики доступны при запущенном sing-box с
              <span class="tag">clash_api</span> на <span class="tag">127.0.0.1:9090</span>.
            </p>
          {/if}
        </div>
      </div>
    </div>

    <!-- quick links -->
    <div class="card">
      <div class="card-head noborder">
        <h3 class="card-title"><Icon name="arrowRight" size={17} />Дальше</h3>
      </div>
      <div class="card-body" style="padding-top:8px">
        <div class="grid-3">
          {#each [
            { ic: "route", t: "Маршрутизация", d: "Режим перехвата, домены и подсети", n: "routing" },
            { ic: "server", t: "VPN-серверы", d: "Настройка outbound-подключений", n: "servers" },
            { ic: "diagnostics", t: "Диагностика", d: "Логи и проверка конфигурации", n: "diagnostics" },
          ] as q}
            <button class="lrow" style="cursor:pointer;flex-direction:column;align-items:flex-start;gap:8px;background:none;border:1px solid var(--border-soft);text-align:left;font-family:inherit;font-size:inherit;color:inherit" onclick={() => onNav(q.n)}>
              <div class="row" style="width:100%">
                <Icon name={q.ic} size={18} style="color:var(--accent)" />
                <span class="spacer"></span>
                <Icon name="arrowRight" size={15} style="color:var(--text-faint)" />
              </div>
              <div>
                <div class="lrow-title" style="font-size:13.5px">{q.t}</div>
                <div class="hint-text" style="margin-top:2px">{q.d}</div>
              </div>
            </button>
          {/each}
        </div>
      </div>
    </div>
  {/if}

  <!-- confirm modals -->
  {#if confirmAction === "restart"}
    <div class="modal-scrim" onmousedown={(e) => { if (e.target === e.currentTarget) confirmAction = ""; }}>
      <div class="modal">
        <div class="modal-head">
          <div class="modal-icon danger"><Icon name="alert" size={19} /></div>
          <div style="flex:1;padding-top:2px">
            <h3>Перезапустить сервис?</h3>
            <p>Активные VPN-соединения разорвутся на 1–2 секунды, пока sing-box перечитывает конфигурацию.</p>
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn ghost" onclick={() => confirmAction = ""}>Отмена</button>
          <button class="btn danger solid" disabled={busy === "restart"} onclick={() => svcAction("restart")}>
            {#if busy === "restart"}<span class="btn-spinner"></span>{/if}
            Перезапустить
          </button>
        </div>
      </div>
    </div>
  {/if}
  {#if confirmAction === "stop"}
    <div class="modal-scrim" onmousedown={(e) => { if (e.target === e.currentTarget) confirmAction = ""; }}>
      <div class="modal">
        <div class="modal-head">
          <div class="modal-icon danger"><Icon name="alert" size={19} /></div>
          <div style="flex:1;padding-top:2px">
            <h3>Остановить сервис?</h3>
            <p>После остановки весь трафик пойдёт напрямую, в обход VPN, до следующего запуска.</p>
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn ghost" onclick={() => confirmAction = ""}>Отмена</button>
          <button class="btn danger solid" disabled={busy === "stop"} onclick={() => svcAction("stop")}>
            {#if busy === "stop"}<span class="btn-spinner"></span>{/if}
            Остановить
          </button>
        </div>
      </div>
    </div>
  {/if}
</div>
