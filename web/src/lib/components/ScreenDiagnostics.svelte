<script lang="ts">
  import { api, ApiError } from "../api";
  import type { CheckResult, ClashProxyNode, TraceReport, TraceRuleMatch, TraceVerdict } from "../types";
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

  // --- MTU probe / clamp ---
  let mtuBusy = $state(false);
  let mtuRes = $state<{ ip: string; pmtu: number; mss: number } | null>(null);
  let mtuError = $state("");
  let mtuApplied = $state("");

  async function probeMTU() {
    mtuBusy = true; mtuError = ""; mtuApplied = ""; mtuRes = null;
    try { mtuRes = await api.probeMTU(); }
    catch (e) { mtuError = e instanceof Error ? e.message : String(e); }
    finally { mtuBusy = false; }
  }
  async function applyClamp() {
    if (!mtuRes) return;
    mtuBusy = true; mtuError = "";
    try { const r = await api.applyMSSClamp(mtuRes.mss); mtuApplied = `MSS ${r.mss} → ${r.ip}`; }
    catch (e) { mtuError = e instanceof Error ? e.message : String(e); }
    finally { mtuBusy = false; }
  }
  async function clearClamp() {
    mtuBusy = true; mtuError = "";
    try { await api.clearMSSClamp(); mtuApplied = "снят"; }
    catch (e) { mtuError = e instanceof Error ? e.message : String(e); }
    finally { mtuBusy = false; }
  }

  // --- route trace ---
  let traceTarget = $state("");
  let traceBusy = $state(false);
  let traceError = $state("");
  let traceRep = $state<TraceReport | null>(null);

  async function runTrace() {
    const t = traceTarget.trim();
    if (!t || traceBusy) return;
    traceBusy = true; traceError = ""; traceRep = null;
    try { traceRep = await api.diagTrace(t); }
    catch (e) { traceError = e instanceof Error ? e.message : String(e); }
    finally { traceBusy = false; }
  }

  const verdictText: Record<TraceVerdict, string> = {
    proxy: "через прокси",
    direct: "напрямую (не в route-set)",
    bypass: "напрямую (exclude)",
    reject: "блокируется (reject-set, порт 443)",
    capture_missing: "в route-set, но перехват СНЯТ — уходит напрямую!",
    no_capture: "прозрачный перехват не активен",
    unknown: "неизвестно",
  };

  function verdictClass(v: TraceVerdict): string {
    switch (v) {
      case "proxy": return "ok";
      case "capture_missing": case "reject": return "err";
      default: return "";
    }
  }

  function matchLabel(m: TraceRuleMatch): string {
    switch (m.source) {
      case "route_domains": return `route_domains: ${m.entry}` + (m.match === "suffix" ? " (родительский домен)" : "");
      case "route_cidr": return `route_cidr: ${m.entry}`;
      case "exclude_cidr": return `exclude_cidr: ${m.entry}`;
      case "reject_cidr": return `reject_cidr: ${m.entry}`;
      case "list": return `список ${shortURL(m.list_url ?? "")}: ${m.entry}`;
    }
  }

  function matchNote(m: TraceRuleMatch): string {
    if (m.effective) return "";
    if (m.source === "list" && m.list_kind === "domain")
      return "домены из URL-списков не резолвятся в ipset — не влияет на маршрут";
    if (m.source === "route_domains" && m.match === "suffix")
      return "резолвится только указанное имя; IP поддомена могут не попасть в set";
    return "не влияет";
  }

  function shortURL(u: string): string {
    try {
      const p = new URL(u);
      const last = p.pathname.split("/").filter(Boolean).pop();
      return last ? `${p.hostname}/…/${last}` : p.hostname;
    } catch { return u; }
  }

  function logClass(line: string): string {
    if (line.includes("ERR") || line.includes("ERRO") || line.includes("error")) return "l-err";
    if (line.includes("WARN")) return "l-warn";
    if (line.includes("outbound/vless") || line.includes("outbound/trojan")) return "l-accent";
    return "l-info";
  }
</script>

<div class="page stack">
  <!-- Route trace -->
  <div class="card">
    <div class="card-head">
      <div>
        <h3 class="card-title"><Icon name="route" size={17} />Трассировка маршрута</h3>
        <p class="card-sub">Почему этот сайт (не) идёт через прокси: резолвинг, ipset, conntrack, outbound</p>
      </div>
    </div>
    <div class="card-body stack-sm">
      <form class="row" style="gap:8px" onsubmit={(e) => { e.preventDefault(); runTrace(); }}>
        <input
          class="input mono"
          style="flex:1"
          placeholder="домен, IP или URL — например web.telegram.org"
          bind:value={traceTarget}
          disabled={traceBusy}
        />
        <button class="btn sm primary" type="submit" disabled={traceBusy || !traceTarget.trim()}>
          {#if traceBusy}<span class="btn-spinner"></span>{:else}<Icon name="route" size={14} />{/if}
          Трассировать
        </button>
      </form>

      {#if traceError}
        <div class="callout err"><Icon name="alert" size={17} /><div class="callout-body">{traceError}</div></div>
      {:else if traceRep}
        {@const r = traceRep}
        <p class="hint-text">
          <span class="tag">{r.target}</span> · режим <span class="tag">{r.mode}</span>
          {#if r.capture_installed === false}
            · <b style="color:var(--err-text,#e5484d)">перехват PREROUTING не установлен</b>
          {:else if r.capture_installed}
            · перехват активен
          {/if}
          {#if r.outbound}
            · outbound: {#if r.outbound.error}<span class="tag">{r.outbound.selector}</span> недоступен{:else}<span class="tag">{r.outbound.selector} → {r.outbound.now}</span>{/if}
          {/if}
        </p>

        {#if r.resolve_error}
          <div class="callout err">
            <Icon name="alert" size={17} />
            <div class="callout-body">Не резолвится: <span class="mono" style="font-size:12px">{r.resolve_error}</span></div>
          </div>
        {/if}

        {#if (r.domain_matches ?? []).length > 0}
          <div class="stack-sm">
            {#each r.domain_matches ?? [] as m}
              <p class="hint-text" style="display:flex;gap:7px;align-items:baseline">
                <Icon name={m.effective ? "check" : "info"} size={13} />
                <span><span class="mono" style="font-size:12px">{matchLabel(m)}</span>{#if matchNote(m)} — {matchNote(m)}{/if}</span>
              </p>
            {/each}
          </div>
        {:else if r.kind === "domain"}
          <p class="hint-text">Домен не найден ни в route_domains, ни в URL-списках.</p>
        {/if}

        {#if r.ips.length === 0 && !r.resolve_error}
          <p class="hint-text">Нет IPv4-адресов для проверки.</p>
        {/if}

        {#each r.ips as ipr (ipr.ip)}
          <div class={"callout " + verdictClass(ipr.verdict)}>
            <Icon name={ipr.verdict === "proxy" ? "check" : ipr.verdict === "capture_missing" || ipr.verdict === "reject" ? "alert" : "info"} size={17} />
            <div class="callout-body">
              <b class="mono">{ipr.ip}</b> → <b>{verdictText[ipr.verdict]}</b>
              <span class="hint-text" style="font-size:11.5px"> ({ipr.verdict_source === "live" ? "по живому ipset" : "по настройкам"})</span>
              {#if ipr.sets}
                <br /><span class="mono" style="font-size:12px">
                  route:{ipr.sets.route ? "✓" : "—"} exclude:{ipr.sets.exclude ? "✓" : "—"} reject:{ipr.sets.reject ? "✓" : "—"}
                  {#if ipr.sets.err} · {ipr.sets.err}{/if}
                </span>
              {/if}
              {#each ipr.matches ?? [] as m}
                <br /><span class="mono" style="font-size:12px">{matchLabel(m)}</span>{#if matchNote(m)}<span class="hint-text" style="font-size:11.5px"> — {matchNote(m)}</span>{/if}
              {/each}
              {#if ipr.conntrack_total > 0}
                <br /><span class="hint-text" style="font-size:11.5px">активных соединений: {ipr.conntrack_total}</span>
                {#each ipr.conntrack ?? [] as f}
                  <br /><span class="mono" style="font-size:12px">
                    {f.proto} {f.src}:{f.sport} → :{f.dport}
                    {#if f.state} {f.state}{/if}
                    {#if f.redirected} · REDIRECT{/if}
                    {#if f.mark} · mark={f.mark}{/if}
                  </span>
                {/each}
              {:else}
                <br /><span class="hint-text" style="font-size:11.5px">активных соединений нет{#if r.conntrack_error} (conntrack: {r.conntrack_error}){/if}</span>
              {/if}
            </div>
          </div>
        {/each}
      {:else}
        <p class="hint-text" style="display:flex;gap:7px">
          <Icon name="info" size={14} />Введите домен или IP: покажет, как он резолвится, из какого правила
          попал (или не попал) в route-set и что с ним сделает фаервол.
        </p>
      {/if}
    </div>
  </div>

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

  <!-- MTU probe -->
  <div class="card">
    <div class="card-head">
      <div>
        <h3 class="card-title"><Icon name="activity" size={17} />Подбор MTU</h3>
        <p class="card-sub">Пробивает path MTU до сервера (ICMP + DF) и рекомендует TCP MSS</p>
      </div>
      <div class="card-head-actions">
        <button class="btn sm primary" disabled={mtuBusy} onclick={probeMTU}>
          {#if mtuBusy}<span class="btn-spinner"></span>{:else}<Icon name="activity" size={14} />{/if}
          Подобрать MTU
        </button>
      </div>
    </div>
    <div class="card-body">
      {#if mtuError}
        <div class="callout err"><Icon name="alert" size={17} /><div class="callout-body"><span class="mono" style="font-size:12px">{mtuError}</span></div></div>
      {:else if mtuRes}
        <div class="callout ok">
          <Icon name="check" size={17} />
          <div class="callout-body">
            До <span class="tag">{mtuRes.ip}</span> path MTU = <b>{mtuRes.pmtu}</b>, рекомендуемый TCP MSS = <b>{mtuRes.mss}</b>.
            {#if mtuApplied}<br /><span class="mono" style="font-size:12px">clamp: {mtuApplied}</span>{/if}
          </div>
        </div>
        <div class="row" style="gap:8px;margin-top:10px">
          <button class="btn sm" disabled={mtuBusy} onclick={applyClamp}><Icon name="save" size={14} />Применить MSS {mtuRes.mss}</button>
          <button class="btn sm ghost" disabled={mtuBusy} onclick={clearClamp}><Icon name="trash" size={14} />Снять clamp</button>
        </div>
        <p class="hint-text" style="margin-top:8px;display:flex;gap:7px">
          <Icon name="info" size={14} />Clamp временный (не переживёт перезагрузку/переприменение фаервола) — пока без персистентности.
        </p>
      {:else}
        <p class="hint-text" style="display:flex;gap:7px">
          <Icon name="info" size={14} />Нажмите «Подобрать MTU», если соединение с сервером тормозит на больших пакетах (признак PMTU-blackhole).
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
