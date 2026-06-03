<script lang="ts">
  import type { SystemInfo, InstallStatus } from "../types";

  let { info, install }: { info: SystemInfo; install: InstallStatus | null } = $props();

  const entware = $derived(info.entware != null);
  const running = $derived(info.service.present && info.service.enabled);
</script>

<div class="card">
  <div class="grid">
    <div>
      <div class="label">Platform</div>
      <div class="val mono">{info.os}/{info.arch}</div>
    </div>
    <div>
      <div class="label">Entware</div>
      {#if entware}
        <span class="pill ok"><span class="dot"></span>present</span>
      {:else}
        <span class="pill err"><span class="dot"></span>missing</span>
      {/if}
    </div>
    <div>
      <div class="label">sing-box</div>
      {#if install?.installed}
        <span class="pill ok"><span class="dot"></span>{install.version || "installed"}</span>
      {:else}
        <span class="pill warn"><span class="dot"></span>not installed</span>
      {/if}
    </div>
    <div>
      <div class="label">Service</div>
      {#if !info.service.present}
        <span class="pill warn"><span class="dot"></span>no init</span>
      {:else if running}
        <span class="pill ok"><span class="dot"></span>enabled</span>
      {:else}
        <span class="pill err"><span class="dot"></span>disabled</span>
      {/if}
    </div>
  </div>
  {#if !entware}
    <p class="note">
      Entware not detected at <code>{info.paths.opkg}</code>. Install OPKG/Entware via
      Keenetic web admin first.
    </p>
  {/if}
</div>

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
    gap: 14px;
  }
  .label {
    font-size: 0.78em;
    color: var(--fg-dim);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    margin-bottom: 4px;
  }
  .val {
    font-size: 0.95em;
  }
  .note {
    margin: 14px 0 0;
    font-size: 0.88em;
    color: var(--warn);
  }
</style>
