<script lang="ts">
  import { api, ApiError } from "../api";
  import type { CheckResult, BackupMeta } from "../types";

  let text = $state("");
  let original = $state("");
  let loading = $state(true);
  let loadError = $state("");

  let jsonError = $state(""); // client-side JSON parse error
  let checkResult = $state<CheckResult | null>(null);
  let busy = $state(""); // "check" | "apply" | "restart" | "backup"
  let notice = $state("");

  let backups = $state<BackupMeta[]>([]);
  let selectedBackup = $state("");

  const dirty = $derived(text !== original);

  async function load() {
    loading = true;
    loadError = "";
    try {
      text = await api.configRead();
      original = text;
      validate();
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        loadError = "No config yet. Install sing-box to generate a default config.";
      } else {
        loadError = e instanceof Error ? e.message : String(e);
      }
    } finally {
      loading = false;
    }
    loadBackups();
  }

  async function loadBackups() {
    try {
      backups = await api.configBackups();
    } catch {
      backups = [];
    }
  }

  $effect(() => {
    load();
  });

  function validate() {
    if (!text.trim()) {
      jsonError = "empty";
      return false;
    }
    try {
      JSON.parse(text);
      jsonError = "";
      return true;
    } catch (e) {
      jsonError = e instanceof Error ? e.message : "invalid JSON";
      return false;
    }
  }

  function onInput() {
    checkResult = null;
    notice = "";
    validate();
  }

  function format() {
    try {
      text = JSON.stringify(JSON.parse(text), null, 2) + "\n";
      jsonError = "";
      notice = "Formatted.";
    } catch (e) {
      jsonError = e instanceof Error ? e.message : "invalid JSON";
    }
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === "Tab") {
      e.preventDefault();
      const ta = e.target as HTMLTextAreaElement;
      const start = ta.selectionStart;
      const end = ta.selectionEnd;
      text = text.slice(0, start) + "  " + text.slice(end);
      queueMicrotask(() => ta.setSelectionRange(start + 2, start + 2));
    }
  }

  async function check() {
    if (!validate()) return;
    busy = "check";
    notice = "";
    try {
      checkResult = await api.configCheck(text);
    } catch (e) {
      checkResult = { ok: false, errors: [e instanceof Error ? e.message : String(e)] };
    } finally {
      busy = "";
    }
  }

  async function apply() {
    if (!validate()) return;
    busy = "apply";
    notice = "";
    try {
      const res = await api.configWrite(text);
      original = text;
      const bk = (res.backup as { path?: string })?.path;
      notice = bk ? `Saved. Backup: ${bk.split("/").pop()}` : "Saved.";
      loadBackups();
    } catch (e) {
      notice = "Save failed: " + (e instanceof Error ? e.message : String(e));
    } finally {
      busy = "";
    }
  }

  async function restart() {
    busy = "restart";
    try {
      await api.service("restart");
      notice = "sing-box restarted.";
    } catch (e) {
      const detail = api.serviceErrorDetail(e);
      const chk = detail?.check as CheckResult | undefined;
      notice =
        "Restart failed: " +
        (e instanceof Error ? e.message : String(e)) +
        (chk?.errors?.length ? " — " + chk.errors.join("; ") : "");
    } finally {
      busy = "";
    }
  }

  async function viewBackup() {
    if (!selectedBackup) return;
    if (dirty && !confirm("Discard current edits and load this backup?")) return;
    busy = "backup";
    try {
      text = await api.configBackupRead(selectedBackup);
      notice = `Loaded backup ${selectedBackup}. Review, then Apply to restore.`;
      validate();
    } catch (e) {
      notice = "Load failed: " + (e instanceof Error ? e.message : String(e));
    } finally {
      busy = "";
    }
  }

  function fmtTime(ts: string): string {
    const d = new Date(ts);
    return isNaN(d.getTime()) ? ts : d.toLocaleString();
  }
</script>

<div class="card">
  <div class="head">
    <h2>Configuration</h2>
    <div class="row">
      {#if jsonError}
        <span class="pill err"><span class="dot"></span>invalid JSON</span>
      {:else}
        <span class="pill ok"><span class="dot"></span>valid JSON</span>
      {/if}
      {#if dirty}<span class="pill warn"><span class="dot"></span>unsaved</span>{/if}
    </div>
  </div>

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if loadError}
    <p class="muted">{loadError}</p>
  {:else}
    <textarea
      class="editor mono"
      bind:value={text}
      oninput={onInput}
      onkeydown={onKeydown}
      spellcheck="false"
      autocomplete="off"
      autocapitalize="off"
    ></textarea>

    {#if jsonError && jsonError !== "empty"}
      <div class="msg err">JSON: {jsonError}</div>
    {/if}

    {#if checkResult}
      {#if checkResult.ok}
        <div class="msg ok">✓ sing-box check passed</div>
      {:else}
        <div class="msg err">
          ✗ sing-box check failed:
          <pre>{(checkResult.errors ?? []).join("\n") || checkResult.stderr || ""}</pre>
        </div>
      {/if}
    {/if}

    {#if notice}<div class="msg">{notice}</div>{/if}

    <div class="actions">
      <button onclick={format} disabled={!!busy}>Format</button>
      <button onclick={check} disabled={!!busy || !!jsonError}>
        {busy === "check" ? "Checking…" : "Check"}
      </button>
      <button class="primary" onclick={apply} disabled={!!busy || !!jsonError || !dirty}>
        {busy === "apply" ? "Saving…" : "Apply"}
      </button>
      <button onclick={restart} disabled={!!busy}>
        {busy === "restart" ? "Restarting…" : "Apply & Restart"}
      </button>
      <button onclick={load} disabled={!!busy}>Reload</button>
    </div>

    {#if backups.length}
      <div class="backups">
        <span class="muted">Backups:</span>
        <select bind:value={selectedBackup}>
          <option value="">— select —</option>
          {#each backups as b (b.name)}
            <option value={b.name}>{fmtTime(b.timestamp)} ({b.bytes} B)</option>
          {/each}
        </select>
        <button onclick={viewBackup} disabled={!selectedBackup || !!busy}>Load backup</button>
      </div>
    {/if}
  {/if}
</div>

<style>
  .head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 12px;
    gap: 10px;
  }
  h2 {
    margin: 0;
    font-size: 1.1rem;
  }
  .editor {
    width: 100%;
    min-height: 320px;
    resize: vertical;
    font-size: 0.82em;
    line-height: 1.45;
    tab-size: 2;
    white-space: pre;
    overflow-wrap: normal;
    background: #0a0c10;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 12px;
  }
  .msg {
    margin-top: 10px;
    font-size: 0.88em;
  }
  .msg.ok {
    color: var(--ok);
  }
  .msg.err {
    color: var(--err);
  }
  .msg pre {
    margin: 6px 0 0;
    padding: 8px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .backups {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    margin-top: 14px;
    padding-top: 12px;
    border-top: 1px solid var(--border);
    font-size: 0.9em;
  }
</style>
