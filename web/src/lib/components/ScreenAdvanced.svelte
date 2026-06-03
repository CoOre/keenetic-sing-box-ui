<script lang="ts">
  import { api, ApiError } from "../api";
  import type { CheckResult, BackupMeta } from "../types";
  import Icon from "./Icon.svelte";

  let text = $state("");
  let original = $state("");
  let loading = $state(true);
  let loadError = $state("");
  let jsonError = $state("");
  let checkResult = $state<CheckResult | null>(null);
  let busy = $state("");
  let notice = $state("");
  let backups = $state<BackupMeta[]>([]);
  let confirmBackup = $state<BackupMeta | null>(null);

  const dirty = $derived(text !== original);
  const valid = $derived(!jsonError);

  async function load() {
    loading = true; loadError = "";
    try {
      text = await api.configRead();
      original = text;
      validate();
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        loadError = "Конфига пока нет. Установите sing-box для генерации конфигурации по умолчанию.";
      } else {
        loadError = e instanceof Error ? e.message : String(e);
      }
    } finally { loading = false; }
    loadBackups();
  }

  async function loadBackups() {
    try { backups = await api.configBackups(); } catch { backups = []; }
  }

  $effect(() => { load(); });

  function validate() {
    if (!text.trim()) { jsonError = "empty"; return false; }
    try { JSON.parse(text); jsonError = ""; return true; }
    catch (e) { jsonError = e instanceof Error ? e.message : "invalid JSON"; return false; }
  }

  function onInput() { checkResult = null; notice = ""; validate(); }

  function format() {
    try { text = JSON.stringify(JSON.parse(text), null, 2) + "\n"; jsonError = ""; notice = "Отформатировано."; }
    catch (e) { jsonError = e instanceof Error ? e.message : "invalid JSON"; }
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === "Tab") {
      e.preventDefault();
      const ta = e.target as HTMLTextAreaElement;
      const start = ta.selectionStart, end = ta.selectionEnd;
      text = text.slice(0, start) + "  " + text.slice(end);
      queueMicrotask(() => ta.setSelectionRange(start + 2, start + 2));
    }
  }

  async function check() {
    if (!validate()) return;
    busy = "check"; notice = "";
    try { checkResult = await api.configCheck(text); }
    catch (e) { checkResult = { ok: false, errors: [e instanceof Error ? e.message : String(e)] }; }
    finally { busy = ""; }
  }

  async function apply(restart: boolean) {
    if (!validate()) return;
    busy = restart ? "restart" : "apply"; notice = "";
    try {
      const res = await api.configWrite(text);
      original = text;
      const bk = (res.backup as { path?: string })?.path;
      notice = bk ? `Сохранено. Backup: ${bk.split("/").pop()}` : "Сохранено.";
      loadBackups();
      if (restart) {
        await api.service("restart");
        notice += " sing-box перезапущен.";
      }
    } catch (e) {
      notice = "Ошибка: " + (e instanceof Error ? e.message : String(e));
    } finally { busy = ""; }
  }

  async function loadBackup(b: BackupMeta) {
    confirmBackup = null;
    busy = "backup";
    try {
      text = await api.configBackupRead(b.name);
      notice = `Загружен backup ${b.name}. Проверьте и нажмите «Сохранить».`;
      validate();
    } catch (e) {
      notice = "Ошибка загрузки: " + (e instanceof Error ? e.message : String(e));
    } finally { busy = ""; }
  }

  function fmtTime(ts: string): string {
    const d = new Date(ts);
    return isNaN(d.getTime()) ? ts : d.toLocaleString("ru");
  }

  function fmtBytes(n: number): string {
    if (n < 1024) return `${n} B`;
    return `${(n / 1024).toFixed(1)} KB`;
  }
</script>

<div class="page wide stack">
  <div class="callout warn">
    <Icon name="warn" size={17} />
    <div class="callout-body">
      <b>Экспертный режим</b><br />
      Прямое редактирование <span class="tag">/opt/etc/sing-box/config.json</span>.
      Ошибка здесь может уронить сервис — используйте только если понимаете формат sing-box.
      Перед применением выполняется <span class="tag">sing-box check</span>.
    </div>
  </div>

  <div class="card flush">
    <div class="card-head">
      <h3 class="card-title"><Icon name="advanced" size={17} />Raw config editor</h3>
      <div class="card-head-actions">
        {#if jsonError && jsonError !== "empty"}
          <span class="pill err"><span class="dot err"></span>invalid JSON</span>
        {:else if !loading && !loadError}
          <span class="pill ok"><span class="dot ok"></span>valid JSON</span>
        {/if}
        {#if dirty}<span class="pill warn"><span class="dot warn"></span>unsaved</span>{/if}
      </div>
    </div>

    {#if loading}
      <div class="card-body"><p class="hint-text">Загрузка…</p></div>
    {:else if loadError}
      <div class="card-body">
        <div class="callout warn"><Icon name="info" size={17} /><div class="callout-body">{loadError}</div></div>
      </div>
    {:else}
      <div style="position:relative">
        <textarea
          class="textarea mono"
          spellcheck="false"
          autocomplete="off"
          bind:value={text}
          oninput={onInput}
          onkeydown={onKeydown}
          style="border:none;border-radius:0;background:#070a0e;min-height:380px;font-size:12.5px;line-height:1.7;padding:16px 18px;width:100%;resize:vertical"
        ></textarea>
      </div>

      {#if checkResult}
        <div style="padding:0 var(--pad) 14px">
          <div class={"callout " + (checkResult.ok ? "ok" : "err")}>
            <Icon name={checkResult.ok ? "check" : "alert"} size={17} />
            <div class="callout-body">
              <b>{checkResult.ok ? "Конфигурация валидна" : "Ошибка конфигурации"}</b>
              {#if !checkResult.ok}
                <br /><span class="mono" style="font-size:12px">{(checkResult.errors ?? []).join("\n") || checkResult.stderr || ""}</span>
              {/if}
            </div>
          </div>
        </div>
      {/if}

      {#if notice}
        <div style="padding:0 var(--pad) 14px">
          <p class="hint-text" style="color:var(--ok-text)">{notice}</p>
        </div>
      {/if}

      <div class="card-head" style="border-top:1px solid var(--border-soft);border-bottom:none;flex-wrap:wrap;gap:8px">
        <button class="btn sm" onclick={format}><Icon name="copy" size={14} />Форматировать</button>
        <button class="btn sm" disabled={!!busy || !valid} onclick={check}>
          {#if busy === "check"}<span class="btn-spinner"></span>{:else}<Icon name="check" size={14} />{/if}
          Проверить
        </button>
        <button class="btn sm" onclick={() => { load(); notice = ""; }}>
          <Icon name="restart" size={14} />Сбросить
        </button>
        <span class="spacer"></span>
        <button class="btn sm" disabled={!dirty || !valid || !!busy} onclick={() => apply(false)}>
          {#if busy === "apply"}<span class="btn-spinner"></span>{:else}<Icon name="save" size={14} />{/if}
          Сохранить
        </button>
        <button class="btn sm primary" disabled={!valid || !!busy} onclick={() => apply(true)}>
          {#if busy === "restart"}<span class="btn-spinner"></span>{:else}<Icon name="restart" size={14} />{/if}
          Сохранить и перезапустить
        </button>
      </div>
    {/if}
  </div>

  <!-- Backups -->
  {#if backups.length > 0}
    <div class="card">
      <div class="card-head">
        <h3 class="card-title"><Icon name="history" size={17} />Резервные копии</h3>
        <div class="card-head-actions">
          <p class="card-sub" style="margin:0">Предыдущие версии config.json</p>
        </div>
      </div>
      <div class="card-body stack-sm">
        {#each backups as b (b.name)}
          <div class="lrow">
            <div class="lrow-main">
              <div class="lrow-title mono" style="font-size:13px">{b.name}</div>
              <div class="lrow-meta">{fmtTime(b.timestamp)} · {fmtBytes(b.bytes)}</div>
            </div>
            <button class="btn sm" onclick={() => dirty ? (confirmBackup = b) : loadBackup(b)}>
              <Icon name="history" size={14} />Загрузить
            </button>
          </div>
        {/each}
      </div>
    </div>
  {/if}

  <!-- Confirm load backup -->
  {#if confirmBackup}
    <div class="modal-scrim" onmousedown={(e) => { if (e.target === e.currentTarget) confirmBackup = null; }}>
      <div class="modal">
        <div class="modal-head">
          <div class="modal-icon danger"><Icon name="alert" size={19} /></div>
          <div style="flex:1;padding-top:2px">
            <h3>Загрузить backup?</h3>
            <p>В редакторе есть несохранённые изменения. Загрузка {confirmBackup.name} перезапишет их.</p>
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn ghost" onclick={() => confirmBackup = null}>Отмена</button>
          <button class="btn danger solid" onclick={() => confirmBackup && loadBackup(confirmBackup)}>Загрузить и потерять правки</button>
        </div>
      </div>
    </div>
  {/if}
</div>
