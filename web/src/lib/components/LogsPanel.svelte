<script lang="ts">
  import { api } from "../api";

  let lines = $state<string[]>([]);
  let path = $state("");
  let error = $state("");
  let auto = $state(true);
  let tail = $state(200);
  let timer: ReturnType<typeof setInterval> | null = null;
  let box: HTMLPreElement | null = $state(null);

  async function load() {
    try {
      const res = await api.logs(tail);
      lines = res.lines ?? [];
      path = res.path;
      error = "";
      queueMicrotask(() => {
        if (box) box.scrollTop = box.scrollHeight;
      });
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  $effect(() => {
    load();
  });

  $effect(() => {
    if (auto) {
      timer = setInterval(load, 3000);
    }
    return () => {
      if (timer) clearInterval(timer);
      timer = null;
    };
  });
</script>

<div class="card">
  <h2>
    Logs
    <label class="auto">
      <input type="checkbox" bind:checked={auto} /> auto-refresh
    </label>
    <button onclick={load}>Refresh</button>
  </h2>
  {#if error}
    <pre class="logbox err">{error}</pre>
  {:else if lines.length === 0}
    <p class="muted">No log lines yet ({path || "log file not found"}).</p>
  {:else}
    <pre class="logbox" bind:this={box}>{lines.join("\n")}</pre>
  {/if}
</div>

<style>
  h2 {
    margin: 0 0 12px;
    font-size: 1.1rem;
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .auto {
    font-size: 0.82em;
    color: var(--fg-dim);
    display: inline-flex;
    align-items: center;
    gap: 5px;
    margin-left: auto;
  }
  .logbox {
    margin: 0;
    padding: 10px;
    background: #0a0c10;
    border: 1px solid var(--border);
    border-radius: 8px;
    font-family: var(--mono);
    font-size: 0.8em;
    line-height: 1.4;
    max-height: 320px;
    overflow: auto;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .err {
    color: var(--err);
  }
</style>
