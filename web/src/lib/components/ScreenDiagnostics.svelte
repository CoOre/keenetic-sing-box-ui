<script lang="ts">
  import { api, ApiError } from "../api";
  import type { CheckResult, ClashProxyNode } from "../types";
  import Icon from "./Icon.svelte";

  let lines = $state<string[]>([]);
  let logPath = $state("");
  let logError = $state("");
  let autoRefresh = $state(true);
  let termRef = $state<HTMLDivElement | null>(null);

  let checkBusy = $state(false);
  let checkResult = $state<CheckResult | null>(null);

  let selectors = $state<ClashProxyNode[]>([]);
  let proxies = $state<Record<string, ClashProxyNode>>({});
  let clashError = $state("");
  let clashBusy = $state("");

  async function loadLogs() {
    try {
      const res = await api.logs(200);
      lines = res.lines ?? [];
      logPath = res.path;
      logError = "";
      setTimeout(() => { if (termRef) termRef.scrollTop = termRef.scrollHeight; }, 50);
    } catch (e) {
      logError = e instanceof Error ? e.message : String(e);
    }
  }

  async function loadClash() {
    clashError = "";
    try {
      const data = await api.clashProxies();
      proxies = data.proxies ?? {};
      selectors = Object.values(proxies).filter(
        (p) => p.type === "Selector" && Array.isArray(p.all) && p.all.length > 0
      );
    } catch (e) {
      clashError = e instanceof Error ? e.message : String(e);
    }
  }

  $effect(() => { loadLogs(); loadClash(); });

  $effect(() => {
    if (!autoRefresh) return;
    const id = setInterval(loadLogs, 3000);
    return () => clearInterval(id);
  });

  async function runCheck() {
    checkBusy = true; checkResult = null;
    try { checkResult = await api.configCheck(); }
    catch (e) { checkResult = { ok: false, errors: [e instanceof Error ? e.message : String(e)] }; }
    finally { checkBusy = false; }
  }

  async function pick(selector: string, name: string) {
    clashBusy = selector;
    try { await api.clashSwitch(selector, name); await loadClash(); }
    catch (e) { clashError = e instanceof Error ? e.message : String(e); }
    finally { clashBusy = ""; }
  }

  function logClass(line: string): string {
    if (line.includes("ERR") || line.includes("ERRO") || line.includes("error")) return "l-err";
    if (line.includes("WARN")) return "l-warn";
    if (line.includes("outbound/vless") || line.includes("outbound/trojan")) return "l-accent";
    return "l-info";
  }
</script>

<div class="page stack">
  <!-- Config check -->
  <div class="card">
    <div class="card-head">
      <div>
        <h3 class="card-title"><Icon name="check" size={17} />Проверка конфигурации</h3>
        <p class="card-sub">Запускает sing-box check на текущем конфиге</p>
      </div>
      <div class="card-head-actions">
        <button class="btn sm primary" disabled={checkBusy} onclick={runCheck}>
          {#if checkBusy}<span class="btn-spinner"></span>{:else}<Icon name="check" size={14} />{/if}
          Проверить конфиг
        </button>
      </div>
    </div>
    <div class="card-body">
      {#if checkBusy}
        <div class="callout">
          <Icon name="clock" size={17} />
          <div class="callout-body">Выполняется <span class="tag">sing-box check</span>…</div>
        </div>
      {:else if checkResult}
        <div class={"callout " + (checkResult.ok ? "ok" : "err")}>
          <Icon name={checkResult.ok ? "check" : "alert"} size={17} />
          <div class="callout-body">
            <b>{checkResult.ok ? "Конфигурация валидна" : "Ошибка конфигурации"}</b><br />
            {#if !checkResult.ok}
              <span class="mono" style="font-size:12px">{(checkResult.errors ?? []).join("\n") || checkResult.stderr || ""}</span>
            {:else}
              <span class="mono" style="font-size:12px">configuration OK</span>
            {/if}
          </div>
        </div>
      {:else}
        <p class="hint-text" style="display:flex;gap:7px">
          <Icon name="info" size={14} />Нажмите «Проверить конфиг», чтобы прогнать
          <span class="tag">sing-box check</span> перед применением.
        </p>
      {/if}
    </div>
  </div>

  <!-- Outbound selectors -->
  <div class="card">
    <div class="card-head">
      <div>
        <h3 class="card-title"><Icon name="shuffle" size={17} />Исходящие подключения</h3>
        <p class="card-sub">Переключение selector outbound через Clash API</p>
      </div>
      <div class="card-head-actions">
        {#if !clashError && selectors.length > 0}
          <span class="pill ok"><span class="dot live"></span>Clash API</span>
        {:else}
          <span class="pill warn">недоступно</span>
        {/if}
      </div>
    </div>
    <div class="card-body">
      {#if clashError || selectors.length === 0}
        <div class="empty">
          <div class="empty-icon"><Icon name="shuffle" size={20} /></div>
          <h4>Selectors недоступны</h4>
          <p>Нужен запущенный sing-box с <span class="tag">selector</span> outbound и включённым <span class="tag">clash_api</span>.</p>
        </div>
      {:else}
        <div class="stack-sm">
          {#each selectors as sel (sel.name)}
            <div class="stack-sm">
              <p class="hint-text">Группа <span class="tag">{sel.name}</span> · текущий: <b style="color:var(--accent-text)">{sel.now}</b></p>
              <div class="seg" style="grid-template-columns:repeat(auto-fill,minmax(140px,1fr))">
                {#each sel.all ?? [] as opt (opt)}
                  <div
                    class={"seg-card" + (opt === sel.now ? " on" : "")}
                    onclick={() => pick(sel.name, opt)}
                    style="align-items:center"
                    role="radio"
                    aria-checked={opt === sel.now}
                    tabindex="0"
                    onkeydown={(e) => e.key === "Enter" && pick(sel.name, opt)}
                  >
                    <div class="seg-radio"></div>
                    <div class="seg-main">
                      <b class="mono" style="font-size:13px">{opt}</b>
                      {#if proxies[opt]}
                        <div class="seg-desc">{proxies[opt].type}</div>
                      {/if}
                    </div>
                  </div>
                {/each}
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  </div>

  <!-- Logs -->
  <div class="card flush">
    <div class="card-head">
      <div>
        <h3 class="card-title"><Icon name="diagnostics" size={17} />Логи</h3>
        <p class="card-sub">Последние строки журнала sing-box</p>
      </div>
      <div class="card-head-actions">
        <label class="row" style="gap:8px;font-size:12.5px;color:var(--text-dim);cursor:pointer">
          <button
            class={"toggle" + (autoRefresh ? " on" : "")}
            onclick={() => autoRefresh = !autoRefresh}
            role="switch"
            aria-checked={autoRefresh}
          ></button>
          auto-refresh
        </label>
        <button class="btn sm" onclick={loadLogs}><Icon name="refresh" size={14} />Обновить</button>
      </div>
    </div>
    <div class="card-body">
      {#if logError}
        <div class="callout err"><Icon name="alert" size={17} /><div class="callout-body">{logError}</div></div>
      {:else if lines.length === 0}
        <div class="empty">
          <div class="empty-icon"><Icon name="diagnostics" size={20} /></div>
          <h4>Логов нет</h4>
          <p>Журнал появится после запуска сервиса.</p>
        </div>
      {:else}
        <div class="terminal" bind:this={termRef} style="max-height:360px">
          {#each lines as line, i (i)}
            <div class={logClass(line)}>{line}</div>
          {/each}
        </div>
      {/if}
      <p class="hint-text mono" style="margin-top:10px">{logPath || "/opt/var/log/sing-box.log"} {autoRefresh ? "· обновление каждые 3с" : ""}</p>
    </div>
  </div>
</div>
