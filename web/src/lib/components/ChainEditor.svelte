<script lang="ts">
  import { untrack } from "svelte";
  import { api, ApiError } from "../api";
  import { labelOf } from "../pick";
  import type { Chain, Server, ServerType } from "../types";
  import Icon from "./Icon.svelte";

  let { initial = null, servers, maxHops, onclose, onsaved }: {
    initial?: Chain | null;
    servers: Server[];
    maxHops: number; // servers.MaxChainHops, from the server list response
    onclose: () => void;
    onsaved: (c: Chain) => void;
  } = $props();

  const QUIC_TYPES: ServerType[] = ["hysteria2", "tuic"];

  // initial and servers are fixed for the lifetime of the modal.
  const init = untrack(() => ({ chain: initial, known: new Set(servers.map((s) => s.id)) }));
  const editing = !!init.chain?.id;
  let name = $state(init.chain?.name ?? "");
  // Hops of a broken chain may point at deleted servers: drop them so the
  // user picks a replacement instead of saving a dangling ID.
  const startHops = (init.chain?.hops ?? []).filter((id) => init.known.has(id));
  const removed = (init.chain?.hops.length ?? 0) - startHops.length;
  let hops = $state<string[]>(startHops);
  let pick = $state("");
  let busy = $state(false);
  let error = $state("");

  const byID = $derived(new Map(servers.map((s) => [s.id ?? "", s])));
  const label = (id: string) => {
    const s = byID.get(id);
    return s ? labelOf(s) : id;
  };
  const available = $derived(servers.filter((s) => s.id && !hops.includes(s.id)));
  const preview = $derived(hops.map(label).join(" → "));
  // A QUIC server past the entry rides UDP through the previous hop.
  const quicInside = $derived(hops.slice(1).some((id) => QUIC_TYPES.includes(byID.get(id)?.type as ServerType)));

  function add() {
    if (!pick || hops.includes(pick) || hops.length >= maxHops) return;
    hops = [...hops, pick];
    pick = "";
  }
  function move(i: number, d: number) {
    const j = i + d;
    if (j < 0 || j >= hops.length) return;
    const next = [...hops];
    [next[i], next[j]] = [next[j], next[i]];
    hops = next;
  }
  function remove(i: number) {
    hops = hops.filter((_, k) => k !== i);
  }

  async function save() {
    busy = true; error = "";
    try {
      onsaved(await api.chainSave({ id: init.chain?.id, name: name.trim(), hops }));
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = false; }
  }
</script>

<div class="modal-scrim">
  <div class="modal editor" role="dialog" aria-modal="true" aria-labelledby="chain-editor-title">
    <div class="modal-head">
      <div class="modal-icon accent"><Icon name="shuffle" size={19} /></div>
      <div style="flex:1;min-width:0;padding-top:2px">
        <h3 id="chain-editor-title">{editing ? "Изменить цепочку" : "Новая цепочка"}</h3>
        <p>Трафик входит через первый сервер, каждый следующий подключается через предыдущий. Выход — последний.</p>
      </div>
      <button class="btn sm ghost icon" onclick={onclose} title="Закрыть"><Icon name="x" size={16} /></button>
    </div>

    <div class="modal-body editor-body stack">
      <div class="field">
        <label for="chain-name">Название</label>
        <input id="chain-name" class="input" bind:value={name} placeholder="Например, via-NL" />
      </div>

      {#if removed > 0}
        <div class="callout warn">
          <Icon name="alert" size={17} />
          <div class="callout-body">Из цепочки убраны удалённые серверы ({removed}). Выберите замену.</div>
        </div>
      {/if}

      <div class="field">
        <span class="field-label">Серверы цепочки</span>
        {#if hops.length === 0}
          <p class="hint-text">Добавьте минимум два сервера.</p>
        {:else}
          <ol class="hops">
            {#each hops as id, i (id)}
              <li class="hop">
                <span class="hop-n mono">{i + 1}</span>
                <span class="hop-name">{label(id)}</span>
                {#if i === 0}<span class="tag">вход</span>{/if}
                {#if i === hops.length - 1 && hops.length > 1}<span class="tag">выход</span>{/if}
                <span class="hop-actions">
                  <button class="btn sm ghost icon" disabled={i === 0} onclick={() => move(i, -1)} title="Выше">
                    <span class="up"><Icon name="chevDown" size={14} /></span>
                  </button>
                  <button class="btn sm ghost icon" disabled={i === hops.length - 1} onclick={() => move(i, 1)} title="Ниже">
                    <Icon name="chevDown" size={14} />
                  </button>
                  <button class="btn sm ghost icon" onclick={() => remove(i)} title="Убрать"><Icon name="x" size={14} /></button>
                </span>
              </li>
            {/each}
          </ol>
        {/if}
        {#if hops.length < maxHops && available.length > 0}
          <div class="row" style="gap:8px">
            <select class="select" bind:value={pick} aria-label="Сервер">
              <option value="">Выберите сервер…</option>
              {#each available as s (s.id)}
                <option value={s.id}>{labelOf(s)}</option>
              {/each}
            </select>
            <button class="btn" disabled={!pick} onclick={add}><Icon name="plus" size={16} />Добавить</button>
          </div>
        {/if}
      </div>

      {#if hops.length > 1}
        <div class="preview mono">{preview}</div>
      {/if}
      {#if quicInside}
        <div class="callout">
          <Icon name="info" size={17} />
          <div class="callout-body">QUIC-сервер (Hysteria 2 / TUIC) не на входе: его UDP идёт через предыдущий сервер, и тот должен пропускать UDP.</div>
        </div>
      {/if}

      {#if error}<div class="callout err"><Icon name="alert" size={17} /><div class="callout-body">{error}</div></div>{/if}
    </div>

    <div class="modal-foot">
      <button class="btn ghost" onclick={onclose}>Отмена</button>
      <button class="btn primary" disabled={busy || hops.length < 2} onclick={save}>
        {#if busy}<span class="btn-spinner"></span>{:else}<Icon name="check" size={16} />{/if}
        {editing ? "Сохранить" : "Создать"}
      </button>
    </div>
  </div>
</div>

<style>
  .editor {
    max-width: 560px;
    max-height: calc(100vh - 48px);
    display: flex;
    flex-direction: column;
  }
  .editor-body {
    overflow-y: auto;
    min-height: 0;
  }
  .field-label {
    font-size: 12.5px;
    font-weight: 500;
    color: var(--text-dim);
  }
  .hops {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .hop {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 8px;
    border: 1px solid var(--border-soft);
    border-radius: var(--r);
    background: var(--bg-soft);
  }
  .hop-n {
    font-size: 12px;
    color: var(--text-faint);
    width: 14px;
    text-align: right;
  }
  .hop-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .hop-actions {
    display: inline-flex;
    gap: 2px;
  }
  .up {
    display: inline-flex;
    transform: rotate(180deg);
  }
  .preview {
    font-size: 12.5px;
    color: var(--text-dim);
    word-break: break-word;
  }
</style>
