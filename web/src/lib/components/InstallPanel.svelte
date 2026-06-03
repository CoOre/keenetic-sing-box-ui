<script lang="ts">
  import { api } from "../api";
  import type { InstallStatus } from "../types";

  let { install, onChanged }: { install: InstallStatus | null; onChanged: () => void } =
    $props();

  let busy = $state(false);
  let log = $state("");
  let error = $state("");

  async function run(source: "opkg" | "github") {
    busy = true;
    error = "";
    log = `Installing from ${source}…`;
    try {
      const res = await api.install(source);
      log = JSON.stringify(res, null, 2);
      onChanged();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      log = "";
    } finally {
      busy = false;
    }
  }
</script>

<div class="card">
  <h2>Install sing-box</h2>
  <p class="muted">
    sing-box is not installed. Choose a source. GitHub downloads the official release for your
    architecture and verifies its checksum.
  </p>
  <div class="row">
    <button class="primary" disabled={busy} onclick={() => run("github")}>
      Install from GitHub
    </button>
    <button disabled={busy || !install?.entware} onclick={() => run("opkg")}>
      Install via opkg
    </button>
    {#if !install?.entware}
      <span class="muted">opkg needs Entware</span>
    {/if}
  </div>
  {#if error}
    <pre class="out err">{error}</pre>
  {:else if log}
    <pre class="out">{log}</pre>
  {/if}
</div>

<style>
  h2 {
    margin: 0 0 6px;
    font-size: 1.1rem;
  }
  p {
    margin: 0 0 12px;
  }
  .out {
    margin: 12px 0 0;
    padding: 10px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    font-family: var(--mono);
    font-size: 0.82em;
    max-height: 240px;
    overflow: auto;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .err {
    color: var(--err);
  }
</style>
