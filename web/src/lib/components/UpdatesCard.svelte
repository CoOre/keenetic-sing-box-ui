<script lang="ts">
  import { api } from "../api";
  import type { UpdateStatus, UpdateComponent, UpdateChangelog } from "../types";
  import { renderMarkdown } from "../markdown";
  import Icon from "./Icon.svelte";

  type Target = "singbox" | "ui";

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

  // «Что нового»: release notes per component, fetched on first expand and
  // dropped whenever versions may have changed (check/install).
  let notesOpen = $state<Record<string, boolean>>({});
  let notes = $state<Record<string, UpdateChangelog | undefined>>({});
  let notesErr = $state<Record<string, string>>({});
  let notesLoading = $state<Record<string, boolean>>({});
  // Request generation per target: a response that lost the race to a newer
  // request (or to resetNotes) is dropped.
  const notesSeq: Record<string, number> = {};
  const nextSeq = (t: Target) => (notesSeq[t] = (notesSeq[t] ?? 0) + 1);

  async function loadNotes(t: Target) {
    const seq = nextSeq(t);
    notesLoading[t] = true;
    notesErr[t] = "";
    try {
      const cl = await api.updateChangelog(t);
      if (seq === notesSeq[t]) notes[t] = cl;
    } catch (e) {
      if (seq === notesSeq[t]) notesErr[t] = e instanceof Error ? e.message : String(e);
    } finally {
      if (seq === notesSeq[t]) notesLoading[t] = false;
    }
  }

  function toggleNotes(t: Target) {
    notesOpen[t] = !notesOpen[t];
    if (notesOpen[t] && !notes[t] && !notesLoading[t]) loadNotes(t);
  }

  function resetNotes() {
    notes = {};
    for (const t of ["singbox", "ui"] as Target[]) {
      if (notesOpen[t]) loadNotes(t);
      else {
        nextSeq(t);
        notesLoading[t] = false;
      }
    }
  }

  async function load() {
    loading = true;
    try {
      const r = await api.updateStatus();
      status = r.status;
      autoSingbox = r.auto_update_singbox;
      autoUI = r.auto_update_ui;
      checkHours = r.update_check_hours;
      resetNotes();
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
      resetNotes();
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

  function rowState(c?: UpdateComponent): "outdated" | "avail" | "ok" | "err" | "unknown" {
    if (!c) return "unknown";
    if (c.outdated) return "outdated";
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
        { key: "singbox" as Target, title: "Ядро sing-box", c: status?.sing_box },
        { key: "ui" as Target, title: "Веб-интерфейс", c: status?.ui },
      ] as row}
        <div class="upd-row">
          <div class="upd-text">
            <b>{row.title}</b>
            <span class="mono">
              {row.c?.current || "—"}
              {#if row.c?.latest && row.c.latest !== row.c.current} → {row.c.latest}{/if}
            </span>
          </div>
          {#if row.c?.current || row.c?.latest}
            <button
              class={"btn sm ghost" + (notesOpen[row.key] ? " on" : "")}
              onclick={() => toggleNotes(row.key)}
              aria-expanded={!!notesOpen[row.key]}
            >
              <Icon name="list" size={14} />Что нового
            </button>
          {/if}
          {#if rowState(row.c) === "outdated"}
            <span class="pill err" title={"Минимальная поддерживаемая версия — " + row.c?.min}><span class="dot"></span>нужна {row.c?.min}+</span>
            <button class="btn sm primary" disabled={!!busy} onclick={applySingbox}>
              {#if busy === "singbox"}<span class="btn-spinner"></span>{:else}<Icon name="download" size={14} />{/if}
              Обновить
            </button>
          {:else if rowState(row.c) === "err"}
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
        {#if notesOpen[row.key]}
          {@const cl = notes[row.key]}
          <div class="notes">
            {#if notesLoading[row.key] && !cl}
              <p class="hint-text">Загрузка списка изменений…</p>
            {:else if notesErr[row.key]}
              <p class="hint-text" style="color:var(--danger-text)">Не удалось загрузить: {notesErr[row.key]}</p>
            {:else if cl}
              <div class="notes-title">
                {#if cl.newer}
                  Изменения после {cl.current}
                {:else if cl.installed}
                  Установленная версия {cl.current}
                {:else if cl.current}
                  Последний описанный релиз (установлена {cl.current})
                {:else}
                  Последний релиз
                {/if}
              </div>
              {#each cl.releases as r}
                <div class="rel">
                  <div class="rel-head">
                    {#if r.url}
                      <a class="mono" href={r.url} target="_blank" rel="noopener noreferrer">{r.version}</a>
                    {:else}
                      <span class="mono">{r.version}</span>
                    {/if}
                    {#if r.date}<span class="rel-date">{r.date}</span>{/if}
                  </div>
                  {#if r.notes}
                    <div class="md">{@html renderMarkdown(r.notes)}</div>
                  {:else}
                    <p class="hint-text">Описание не опубликовано.</p>
                  {/if}
                </div>
              {:else}
                <p class="hint-text">Нет данных об изменениях.</p>
              {/each}
              <a class="notes-more" href={cl.url} target="_blank" rel="noopener noreferrer">
                Полный список изменений <Icon name="arrowRight" size={13} />
              </a>
            {/if}
          </div>
        {/if}
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
  .btn.ghost.on {
    background: var(--surface-2);
    color: var(--text);
  }
  .notes {
    margin: 0 0 8px;
    padding: 10px 14px;
    border-radius: var(--r);
    background: var(--surface-2);
    max-height: 420px;
    overflow: auto;
    font-size: 12.5px;
    line-height: 1.5;
  }
  .notes-title {
    font-size: 11.5px;
    color: var(--text-faint);
    margin-bottom: 6px;
  }
  .rel + .rel {
    margin-top: 10px;
    padding-top: 10px;
    border-top: 1px solid var(--border-soft);
  }
  .rel-head {
    display: flex;
    align-items: baseline;
    gap: 8px;
    font-weight: 600;
  }
  .rel-head a {
    color: var(--accent-text);
    text-decoration: none;
  }
  .rel-date {
    font-size: 11.5px;
    font-weight: 400;
    color: var(--text-faint);
  }
  .notes-more {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    margin-top: 10px;
    font-size: 12px;
    color: var(--accent-text);
    text-decoration: none;
  }
  .md {
    color: var(--text-dim);
    overflow-wrap: anywhere;
  }
  .md :global(h4),
  .md :global(h5),
  .md :global(h6) {
    margin: 8px 0 4px;
    font-size: 12.5px;
    font-weight: 600;
    color: var(--text);
  }
  .md :global(p) {
    margin: 4px 0;
  }
  .md :global(ul) {
    margin: 4px 0;
    padding-left: 18px;
  }
  .md :global(ul ul) {
    margin: 2px 0;
  }
  .md :global(li) {
    margin: 2px 0;
  }
  .md :global(a) {
    color: var(--accent-text);
  }
  .md :global(b) {
    color: var(--text);
    font-weight: 600;
  }
  .md :global(code) {
    font-family: var(--mono);
    font-size: 11.5px;
    padding: 1px 4px;
    border-radius: 4px;
    background: var(--surface-3);
  }
  .md :global(pre) {
    margin: 6px 0;
    padding: 8px 10px;
    border-radius: var(--r-sm);
    background: var(--surface-3);
    overflow: auto;
  }
  .md :global(.md-table) {
    margin: 6px 0;
    overflow-x: auto;
  }
  .md :global(table) {
    border-collapse: collapse;
    font-size: 11.5px;
  }
  .md :global(th),
  .md :global(td) {
    padding: 3px 8px;
    border: 1px solid var(--border-soft);
    text-align: left;
  }
  .md :global(th) {
    color: var(--text);
    font-weight: 600;
  }
  .md :global(pre code) {
    padding: 0;
    background: none;
  }
</style>
