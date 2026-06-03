<script lang="ts">
  import { api } from "../api";
  import type { SystemInfo, CheckResult } from "../types";

  let { service, onChanged }: { service: SystemInfo["service"]; onChanged: () => void } =
    $props();

  let busy = $state("");
  let lastOut = $state("");
  let error = $state("");
  let checkErrors = $state<string[]>([]);
  let logTail = $state<string[]>([]);

  async function act(action: string) {
    busy = action;
    error = "";
    checkErrors = [];
    logTail = [];
    try {
      const res = await api.service(action);
      lastOut = [res.result?.stdout, res.result?.stderr].filter(Boolean).join("\n").trim();
      onChanged();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      const detail = api.serviceErrorDetail(e);
      if (detail) {
        const chk = detail.check as CheckResult | undefined;
        if (chk && !chk.ok) {
          checkErrors = chk.errors?.length ? chk.errors : [chk.stderr || "config check failed"];
        }
        if (detail.log?.length) logTail = detail.log;
      }
    } finally {
      busy = "";
    }
  }

  const actions = [
    { id: "start", label: "Start", primary: true },
    { id: "restart", label: "Restart", primary: false },
    { id: "stop", label: "Stop", primary: false },
  ];
</script>

<div class="card">
  <h2>Service</h2>
  <div class="row">
    {#each actions as a (a.id)}
      <button class={a.primary ? "primary" : ""} disabled={!!busy} onclick={() => act(a.id)}>
        {busy === a.id ? "…" : a.label}
      </button>
    {/each}
    <span class="sep"></span>
    {#if service.enabled}
      <button disabled={!!busy} onclick={() => act("disable")}>Disable autostart</button>
    {:else}
      <button disabled={!!busy} onclick={() => act("enable")}>Enable autostart</button>
    {/if}
  </div>
  {#if error}
    <pre class="out err">{error}</pre>
    {#if checkErrors.length}
      <div class="diag">
        <div class="diag-title">Config check failed:</div>
        <pre class="out err">{checkErrors.join("\n")}</pre>
      </div>
    {/if}
    {#if logTail.length}
      <div class="diag">
        <div class="diag-title">Last log lines:</div>
        <pre class="out">{logTail.join("\n")}</pre>
      </div>
    {/if}
  {:else if lastOut}
    <pre class="out">{lastOut}</pre>
  {/if}
</div>

<style>
  h2 {
    margin: 0 0 12px;
    font-size: 1.1rem;
  }
  .sep {
    flex: 1;
  }
  .out {
    margin: 12px 0 0;
    padding: 10px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    font-family: var(--mono);
    font-size: 0.82em;
    max-height: 200px;
    overflow: auto;
    white-space: pre-wrap;
  }
  .err {
    color: var(--err);
  }
  .diag {
    margin-top: 10px;
  }
  .diag-title {
    font-size: 0.82em;
    color: var(--fg-dim);
    margin-bottom: 4px;
  }
</style>
