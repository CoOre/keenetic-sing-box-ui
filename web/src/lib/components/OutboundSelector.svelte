<script lang="ts">
  import { api } from "../api";
  import type { ClashProxyNode } from "../types";

  let selectors = $state<ClashProxyNode[]>([]);
  let proxies = $state<Record<string, ClashProxyNode>>({});
  let error = $state("");
  let busy = $state("");

  async function load() {
    error = "";
    try {
      const data = await api.clashProxies();
      proxies = data.proxies ?? {};
      selectors = Object.values(proxies).filter(
        (p) => p.type === "Selector" && Array.isArray(p.all) && p.all.length > 0,
      );
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  $effect(() => {
    load();
  });

  async function pick(selector: string, name: string) {
    busy = selector;
    try {
      await api.clashSwitch(selector, name);
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = "";
    }
  }

  function delayOf(name: string): string {
    const node = proxies[name];
    return node?.type ?? "";
  }
</script>

<div class="card">
  <h2>Outbounds</h2>
  {#if error}
    <p class="hint muted">
      Selectors unavailable: {error}. Needs sing-box running with a <code>selector</code> outbound.
    </p>
  {:else if selectors.length === 0}
    <p class="muted">No selector outbounds found.</p>
  {:else}
    {#each selectors as sel (sel.name)}
      <div class="sel">
        <div class="sel-head">
          <strong>{sel.name}</strong>
          <span class="muted">→ {sel.now}</span>
        </div>
        <div class="opts">
          {#each sel.all ?? [] as opt (opt)}
            <button
              class={opt === sel.now ? "primary" : ""}
              disabled={busy === sel.name}
              onclick={() => pick(sel.name, opt)}
              title={delayOf(opt)}
            >
              {opt}
            </button>
          {/each}
        </div>
      </div>
    {/each}
  {/if}
</div>

<style>
  h2 {
    margin: 0 0 12px;
    font-size: 1.1rem;
  }
  .sel + .sel {
    margin-top: 14px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
  }
  .sel-head {
    display: flex;
    gap: 8px;
    align-items: baseline;
    margin-bottom: 8px;
  }
  .opts {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .opts button {
    font-size: 0.88em;
    padding: 6px 11px;
  }
  .hint {
    font-size: 0.84em;
  }
</style>
