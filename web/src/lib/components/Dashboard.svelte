<script lang="ts">
  import { api } from "../api";
  import type { SystemInfo, InstallStatus } from "../types";
  import SystemCard from "./SystemCard.svelte";
  import InstallPanel from "./InstallPanel.svelte";
  import ServiceControls from "./ServiceControls.svelte";
  import TrafficCards from "./TrafficCards.svelte";
  import OutboundSelector from "./OutboundSelector.svelte";
  import LogsPanel from "./LogsPanel.svelte";
  import ServersPanel from "./ServersPanel.svelte";
  import RoutingPanel from "./RoutingPanel.svelte";
  import ConfigEditor from "./ConfigEditor.svelte";
  import PasswordCard from "./PasswordCard.svelte";

  let showAdvanced = $state(false);

  let { onLogout }: { onLogout: () => void } = $props();

  let info = $state<SystemInfo | null>(null);
  let install = $state<InstallStatus | null>(null);
  let loadError = $state("");
  let refreshing = $state(false);

  async function refresh() {
    refreshing = true;
    loadError = "";
    try {
      const [sys, ins] = await Promise.all([api.system(), api.installStatus()]);
      info = sys;
      install = ins;
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    } finally {
      refreshing = false;
    }
  }

  $effect(() => {
    refresh();
  });

  const installed = $derived(install?.installed ?? false);
</script>

<header>
  <div class="row">
    <strong>sing-box</strong>
    <span class="muted">· Keenetic</span>
  </div>
  <div class="row">
    <button onclick={refresh} disabled={refreshing}>
      {refreshing ? "…" : "Refresh"}
    </button>
    <button class="danger" onclick={onLogout}>Sign out</button>
  </div>
</header>

<main>
  {#if loadError}
    <div class="card err">Failed to load: {loadError}</div>
  {/if}

  {#if info}
    <SystemCard {info} {install} />

    {#if !installed}
      <InstallPanel {install} onChanged={refresh} />
    {:else}
      <ServiceControls service={info.service} onChanged={refresh} />
      <TrafficCards />
      <RoutingPanel onApplied={refresh} />
      <ServersPanel onApplied={refresh} />
      <OutboundSelector />
    {/if}

    <LogsPanel />

    {#if installed}
      <div class="advanced">
        <button class="toggle" onclick={() => (showAdvanced = !showAdvanced)}>
          {showAdvanced ? "▾" : "▸"} Advanced: raw config editor
        </button>
        {#if showAdvanced}<ConfigEditor />{/if}
      </div>
    {/if}

    <PasswordCard />
  {/if}
</main>

<style>
  header {
    position: sticky;
    top: 0;
    z-index: 10;
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 12px;
    padding: 12px 16px;
    background: color-mix(in srgb, var(--bg) 88%, transparent);
    backdrop-filter: blur(8px);
    border-bottom: 1px solid var(--border);
  }
  main {
    max-width: 880px;
    margin: 0 auto;
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  .err {
    color: var(--err);
  }
  .advanced {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .toggle {
    align-self: flex-start;
    background: none;
    border: none;
    color: var(--fg-dim);
    padding: 4px 0;
  }
  .toggle:hover {
    color: var(--fg);
  }
</style>
