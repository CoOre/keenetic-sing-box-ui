<script lang="ts">
  import { api } from "../api";
  import type { UpdateStatus, UpdateComponent } from "../types";
  import Icon from "./Icon.svelte";

  let status = $state<UpdateStatus | null>(null);
  let autoSingbox = $state(false);
  let autoUI = $state(false);
  let checkHours = $state(6);
  let loading = $state(true);
  let busy = $state(""); // "check" | "singbox" | "ui" | "settings"
  let notice = $state("");
  let error = $state("");
  let uiRestarting = $state(false);

  const INTERVALS = [1, 3, 6, 12, 24, 48];

  async function load() {
    loading = true;
    try {
      const r = await api.updateStatus();
      status = r.status;
      autoSingbox = r.auto_update_singbox;
      autoUI = r.auto_update_ui;
      checkHours = r.update_check_hours;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  $effect(() => { load(); });

  async function check() {
    busy = "check"; notice = ""; error = "";
    try {
      status = await api.updateCheck();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally { busy = ""; }
  }

  const sleep = (ms: number) => new Promise((res) => setTimeout(res, ms));

  // Installs run detached on the server (a dropped connection must not kill
  // the download), so after POST we poll status until `updating` clears.
  // Returns the final status, or null if the server stopped answering (the
  // expected outcome of a successful UI self-update).
  async function waitInstallDone(): Promise<UpdateStatus | null> {
    const deadline = Date.now() + 30 * 60_000;
    while (Date.now() < deadline) {
      await sleep(2000);
      try {
        const r = await api.updateStatus();
        if (!r.status.updating) return r.status;
      } catch {
        return null;
      }
    }
    return null;
  }

  async function applySingbox() {
    busy = "singbox"; notice = ""; error = "";
    try {
      await api.updateApply("singbox");
      const st = await waitInstallDone();
      if (st?.last_error) error = st.last_error;
      else notice = st?.last_result ? "Готово: " + st.last_result : "Установка завершена.";
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally { busy = ""; }
  }

  // UI self-update: after the detached install succeeds the server restarts
  // itself. Poll /healthz until the reported version changes, then reload
  // the page to pick up the new frontend assets.
  async function applyUI() {
    busy = "ui"; notice = ""; error = "";
    let before = "";
    try {
      before = (await (await fetch("/healthz")).json()).version ?? "";
    } catch { /* proceed anyway */ }
    try {
      await api.updateApply("ui");
      const st = await waitInstallDone();
      if (st?.last_error) { error = st.last_error; return; }
      uiRestarting = true;
      notice = "Установлено, веб-интерфейс перезапускается…";
      const deadline = Date.now() + 90_000;
      while (Date.now() < deadline) {
        await sleep(1500);
        try {
          const h = await (await fetch("/healthz")).json();
          if (h.version && h.version !== before) { location.reload(); return; }
        } catch { /* still restarting */ }
      }
      error = "Перезапуск затянулся — обновите страницу вручную.";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally { busy = ""; uiRestarting = false; }
  }

  // Persist toggles via the shared settings endpoint (server merges over the
  // stored settings, so this doesn't clobber routing fields).
  async function saveAuto(patch: Record<string, unknown>) {
    busy = "settings"; error = "";
    try {
      await api.settingsSave({
        auto_update_singbox: autoSingbox,
        auto_update_ui: autoUI,
        update_check_hours: checkHours,
        ...patch,
      } as never);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally { busy = ""; }
  }

  function fmtChecked(ts?: string): string {
    if (!ts) return "ещё не проверялось";
    const d = new Date(ts);
    if (isNaN(d.getTime()) || d.getTime() <= 0) return "ещё не проверялось";
    return d.toLocaleString("ru");
  }

  function rowState(c?: UpdateComponent): "avail" | "ok" | "err" | "unknown" {
    if (!c) return "unknown";
    if (c.error) return "err";
    if (c.available) return "avail";
    if (c.latest) return "ok";
    return "unknown";
  }
</script>

<div class="card flush">
  <div class="card-head">
    <h3 class="card-title"><Icon name="download" size={17} />Обновления</h3>
    <div class="card-head-actions">
      <span class="hint-text">Проверено: {fmtChecked(status?.checked_at)}</span>
      <button class="btn sm" disabled={!!busy} onclick={check}>
        {#if busy === "check"}<span class="btn-spinner"></span>{:else}<Icon name="refresh" size={14} />{/if}
        Проверить сейчас
      </button>
    </div>
  </div>

  <div class="card-body">
    {#if loading}
      <p class="hint-text">Загрузка…</p>
    {:else}
      {#each [
        { key: "singbox", title: "Ядро sing-box", c: status?.sing_box },
        { key: "ui", title: "Веб-интерфейс", c: status?.ui },
      ] as row}
        <div class="upd-row">
          <div class="upd-text">
            <b>{row.title}</b>
            <span class="mono">
              {row.c?.current || "—"}
              {#if row.c?.latest && row.c.latest !== row.c.current} → {row.c.latest}{/if}
            </span>
          </div>
          {#if rowState(row.c) === "err"}
            <span class="pill err" title={row.c?.error}><span class="dot"></span>ошибка проверки</span>
          {:else if rowState(row.c) === "avail"}
            <span class="pill warn"><span class="dot"></span>доступно {row.c?.latest}</span>
            {#if row.key === "singbox"}
              <button class="btn sm primary" disabled={!!busy} onclick={applySingbox}>
                {#if busy === "singbox"}<span class="btn-spinner"></span>{:else}<Icon name="download" size={14} />{/if}
                Обновить
              </button>
            {:else}
              <button class="btn sm primary" disabled={!!busy} onclick={applyUI}>
                {#if busy === "ui"}<span class="btn-spinner"></span>{:else}<Icon name="download" size={14} />{/if}
                Обновить
              </button>
            {/if}
          {:else if rowState(row.c) === "ok"}
            <span class="pill ok"><span class="dot"></span>актуально</span>
          {:else}
            <span class="pill"><span class="dot"></span>нет данных</span>
          {/if}
        </div>
      {/each}

      {#if uiRestarting}
        <div class="callout warn" style="margin-top:10px">
          <Icon name="info" size={17} />
          <div class="callout-body">Веб-интерфейс перезапускается — страница обновится автоматически.</div>
        </div>
      {/if}
      {#if notice}<p class="hint-text" style="color:var(--ok-text);margin-top:8px">{notice}</p>{/if}
      {#if error}<p class="hint-text" style="color:var(--danger-text);margin-top:8px">{error}</p>{/if}

      <hr class="divider" style="margin:10px 0" />

      <div class="toggle-row">
        <div class="toggle-text">
          <b>Автообновление sing-box</b>
          <span>Устанавливать новые релизы ядра автоматически (с перезапуском сервиса)</span>
        </div>
        <button
          class={"toggle" + (autoSingbox ? " on" : "")}
          onclick={() => { autoSingbox = !autoSingbox; saveAuto({ auto_update_singbox: autoSingbox }); }}
          disabled={!!busy}
          role="switch"
          aria-checked={autoSingbox}
          aria-label="Автообновление sing-box"
        ></button>
      </div>
      <div class="toggle-row">
        <div class="toggle-text">
          <b>Автообновление веб-интерфейса</b>
          <span>Устанавливать новые релизы этой панели автоматически (с её перезапуском)</span>
        </div>
        <button
          class={"toggle" + (autoUI ? " on" : "")}
          onclick={() => { autoUI = !autoUI; saveAuto({ auto_update_ui: autoUI }); }}
          disabled={!!busy}
          role="switch"
          aria-checked={autoUI}
          aria-label="Автообновление веб-интерфейса"
        ></button>
      </div>
      <div class="toggle-row">
        <div class="toggle-text">
          <b>Интервал проверки</b>
          <span>Как часто опрашивать GitHub Releases в фоне</span>
        </div>
        <select
          class="select"
          style="width:auto"
          bind:value={checkHours}
          onchange={() => saveAuto({ update_check_hours: checkHours })}
          disabled={!!busy}
        >
          {#each INTERVALS as h}
            <option value={h}>{h} ч</option>
          {/each}
        </select>
      </div>
      <p class="hint-text" style="display:flex;gap:7px;align-items:center">
        <Icon name="info" size={14} />Обновление ядра перезапускает sing-box и разрывает активные соединения.
      </p>
    {/if}
  </div>
</div>

<style>
  .upd-row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 9px 0;
  }
  .upd-text {
    flex: 1;
    min-width: 0;
  }
  .upd-text b {
    font-size: 13.5px;
    font-weight: 500;
    display: block;
  }
  .upd-text span {
    font-size: 11.5px;
    color: var(--text-faint);
  }
</style>
