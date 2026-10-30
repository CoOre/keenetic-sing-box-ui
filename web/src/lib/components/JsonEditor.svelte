<script lang="ts" module>
  export type { EditorProblem } from "../cm";
</script>

<script lang="ts">
  import { onMount } from "svelte";
  import type { EditorProblem, JsonEditorHandle } from "../cm";

  let {
    value = $bindable(""),
    onchange,
    problems = [],
    placeholder = "",
    minHeight = "320px",
  }: {
    value?: string;
    onchange?: (v: string) => void;
    problems?: EditorProblem[];
    placeholder?: string;
    minHeight?: string;
  } = $props();

  let host: HTMLDivElement;
  let editor = $state<JsonEditorHandle | null>(null);
  let loadError = $state("");

  onMount(() => {
    let alive = true;
    import("../cm")
      .then(({ createJsonEditor }) => {
        if (!alive) return;
        editor = createJsonEditor(host, {
          value,
          placeholder,
          minHeight,
          onChange: (v) => { value = v; onchange?.(v); },
        });
      })
      .catch((e) => { loadError = e instanceof Error ? e.message : String(e); });
    return () => { alive = false; editor?.destroy(); };
  });

  // Parent replaced the text (load, reset, format): push it into the editor.
  $effect(() => { editor?.setValue(value); });
  $effect(() => { editor?.setProblems(problems); });
</script>

<div class="json-editor" bind:this={host} style:min-height={editor ? undefined : minHeight}>
  {#if loadError}
    <p class="hint-text" style="padding:16px 18px;color:var(--danger-text)">Редактор не загрузился: {loadError}</p>
  {:else if !editor}
    <p class="hint-text" style="padding:16px 18px">Загрузка редактора…</p>
  {/if}
</div>

<style>
  .json-editor {
    width: 100%;
    overflow: hidden;
    background: #070a0e;
  }
</style>
