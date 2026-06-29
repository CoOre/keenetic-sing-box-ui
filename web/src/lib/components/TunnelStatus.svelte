<script lang="ts">
  import { api } from "../api";
  import Icon from "./Icon.svelte";

  // Ground truth for "is the VLESS tunnel up": a real latency test through the
  // active outbound via the Clash API (/proxies/<tag>/delay).
  let phase = $state<"checking" | "up" | "down" | "idle">("checking");
  let node = $state("");
  let delay = $state(0);
  let detail = $state("");

  async function check() {
    phase = "checking";
    detail = "";
    try {
      const data = await api.clashProxies();
      const sel = Object.values(data.proxies ?? {}).find(
        (p) => p.type === "Selector" && Array.isArray(p.all) && p.all.length > 0,
      );
      const tag = sel?.now ?? "";
      node = tag;
      if (!tag || tag.toLowerCase() === "direct") {
        phase = "idle";
        detail = "выбран прямой выход (direct)";
        return;
      }
      const r = await api.clashDelay(tag, { timeout: 5000 });
      delay = r.delay;
      phase = "up";
    } catch (e) {
      phase = "down";
      detail = e instanceof Error ? e.message : String(e);
    }
  }

  $effect(() => {
    check();
    const id = setInterval(check, 15000);
    return () => clearInterval(id);
  });
</script>

<div class="card">
  <div class="trow">
    <div class="tmain">
      <Icon name="activity" size={17} />
      <span class="label">Туннель VLESS</span>
      {#if phase === "up"}
        <span class="pill ok"><span class="dot"></span>установлен</span>
        <span class="mono dim">{node} · {delay} ms</span>
      {:else if phase === "checking"}
        <span class="pill warn"><span class="dot"></span>проверка…</span>
        {#if node}<span class="mono dim">{node}</span>{/if}
      {:else if phase === "idle"}
        <span class="pill warn"><span class="dot"></span>прямой выход</span>
        <span class="mono dim">{detail}</span>
      {:else}
        <span class="pill err"><span class="dot"></span>нет связи</span>
        <span class="mono dim">{node ? node + " · " : ""}{detail}</span>
      {/if}
    </div>
    <button class="btn sm" onclick={check} disabled={phase === "checking"}>
      <Icon name="refresh" size={14} /> Проверить
    </button>
  </div>
</div>

<style>
  .trow {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  .tmain {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    min-width: 0;
  }
  .dim {
    color: var(--text-dim);
    font-size: 12.5px;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
