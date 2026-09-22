<script lang="ts">
  import type { Snippet } from "svelte";
  import Icon from "./Icon.svelte";

  let { title, hint = "", open = $bindable(false), children }: {
    title: string;
    hint?: string;
    open?: boolean;
    children: Snippet;
  } = $props();
</script>

<div class="fsec" class:open>
  <button type="button" class="fsec-head" onclick={() => (open = !open)} aria-expanded={open}>
    <Icon name={open ? "chevDown" : "chevRight"} size={15} />
    <span class="fsec-title">{title}</span>
    {#if hint}<span class="fsec-hint">{hint}</span>{/if}
  </button>
  {#if open}
    <div class="fsec-body stack-sm">{@render children()}</div>
  {/if}
</div>

<style>
  .fsec {
    border: 1px solid var(--border-soft);
    border-radius: var(--r);
    background: var(--bg-soft);
  }
  .fsec-head {
    all: unset;
    box-sizing: border-box;
    width: 100%;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 10px 12px;
    cursor: pointer;
    color: var(--text-dim);
    border-radius: var(--r);
  }
  .fsec-head:hover { color: var(--text); }
  .fsec-head:focus-visible { outline: 2px solid var(--accent-border); }
  .fsec-title { font-size: 13px; font-weight: 600; color: var(--text); }
  .fsec-hint {
    font-size: 11.5px;
    color: var(--text-faint);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .fsec-body { padding: 2px 12px 14px; }
</style>
