<script lang="ts">
  import type { DeleteResult } from "../types";

  // What a deletion breaks, from the backend's dry run (?dry_run=1): the same
  // check the deletion itself and a subscription refresh report, so the
  // confirmation never predicts one thing while apply does another.
  let { load }: { load: () => Promise<DeleteResult> } = $props();

  let warnings = $state<string[] | null>(null); // null = loading
  let error = $state("");
  let seq = 0;

  $effect(() => {
    const n = ++seq;
    warnings = null; error = "";
    load().then(
      (r) => { if (n === seq) warnings = r.warnings ?? []; },
      (e) => { if (n === seq) { warnings = []; error = e instanceof Error ? e.message : String(e); } },
    );
  });
</script>

{#if warnings === null}
  <p class="muted">Проверяю, что перестанет работать…</p>
{:else if error}
  <p class="muted">Не удалось проверить последствия: {error}</p>
{:else if warnings.length}
  <p><b>После «Применить и перезапустить»:</b></p>
  {#each warnings as w}<p>{w}</p>{/each}
{/if}
