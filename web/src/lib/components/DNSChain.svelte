<script lang="ts" module>
  // One server in a chain, as the user sees it: what to query and whether
  // through the VPN. Tags/registry are derived on save (see ScreenDNS).
  export type DNSEntry = { address: string; proxy: boolean; custom?: boolean };

  export const CUSTOM = "__custom";
  export const LOCAL = "local";

  export function canProxy(address: string): boolean {
    const a = address.trim().toLowerCase();
    return a !== LOCAL && !a.startsWith("dhcp:");
  }
</script>

<script lang="ts">
  // Ordered DNS failover chain: primary + backups tried in order when the
  // previous one doesn't answer (sing-box ≥ 1.14).
  import type { DNSPreset } from "../types";
  import Icon from "./Icon.svelte";

  let {
    id,
    presets,
    entries,
    emptyLabel = "",
    onchange,
  }: {
    id: string;
    presets: DNSPreset[];
    entries: DNSEntry[];
    emptyLabel?: string; // when set, the chain may be empty (shown as this option)
    onchange: (entries: DNSEntry[]) => void;
  } = $props();

  const groups = $derived(
    presets.reduce<[string, DNSPreset[]][]>((acc, p) => {
      if (p.address === LOCAL) return acc;
      const g = acc.find(([c]) => c === p.category);
      if (g) g[1].push(p); else acc.push([p.category, [p]]);
      return acc;
    }, []),
  );

  function selValue(e: DNSEntry): string {
    if (e.custom) return CUSTOM;
    if (e.address === LOCAL) return LOCAL;
    return presets.some((p) => p.address === e.address) ? e.address : CUSTOM;
  }

  function pick(i: number, v: string) {
    if (v === "") { onchange([]); return; } // emptyLabel chosen
    const cur = entries[i];
    let next: DNSEntry;
    if (v === CUSTOM) next = { address: cur && selValue(cur) === CUSTOM ? cur.address : "", proxy: cur?.proxy ?? false, custom: true };
    else if (v === LOCAL) next = { address: LOCAL, proxy: false };
    else {
      const p = presets.find((x) => x.address === v);
      next = { address: v, proxy: p?.proxy ? true : (cur?.proxy ?? false) };
    }
    onchange(i < entries.length ? entries.map((e, j) => (j === i ? next : e)) : [...entries, next]);
  }

  function update(i: number, patch: Partial<DNSEntry>) {
    onchange(entries.map((e, j) => (j === i ? { ...e, ...patch } : e)));
  }

  function add() {
    onchange([...entries, { address: "https://1.1.1.1/dns-query", proxy: false }]);
  }

  function remove(i: number) {
    onchange(entries.filter((_, j) => j !== i));
  }

  // Rows to render: an empty optional chain still shows its selector.
  const rows = $derived(entries.length ? entries : emptyLabel ? [null] : []);
</script>

<div class="chain">
  {#each rows as e, i}
    <div class="chain-row">
      <span class="chain-label">{e === null ? "" : i === 0 ? "Основной" : "Резервный"}</span>
      <div class="chain-main">
        <select id={i === 0 ? id : undefined} class="select" value={e ? selValue(e) : ""}
          aria-label={i === 0 ? "Основной DNS-сервер" : "Резервный DNS-сервер " + i}
          onchange={(ev) => pick(i, (ev.target as HTMLSelectElement).value)}>
          {#if emptyLabel && i === 0}<option value="">{emptyLabel}</option>{/if}
          {#each groups as [cat, list] (cat)}
            <optgroup label={cat}>
              {#each list as p (p.address)}
                <option value={p.address}>{p.name}{p.note ? " — " + p.note : ""}</option>
              {/each}
            </optgroup>
          {/each}
          <optgroup label="Другое">
            <option value={LOCAL}>Системный DNS роутера</option>
            <option value={CUSTOM}>Свой адрес…</option>
          </optgroup>
        </select>
        {#if e && selValue(e) === CUSTOM}
          <input class="input mono" value={e.address} placeholder="https://dns.example/dns-query · tls://… · quic://… · 8.8.8.8"
            aria-label="Адрес DNS-сервера"
            oninput={(ev) => update(i, { address: (ev.target as HTMLInputElement).value, custom: true })} />
        {/if}
      </div>
      {#if e && canProxy(e.address)}
        <button class={"vpn" + (e.proxy ? " on" : "")} onclick={() => update(i, { proxy: !e.proxy })}
          role="switch" aria-checked={e.proxy} title="Запросы к этому серверу идут через VPN — провайдер их не видит и не подменяет">
          <span class="vpn-dot"></span>VPN
        </button>
      {:else}
        <span class="vpn-spacer"></span>
      {/if}
      {#if i > 0}
        <button class="btn sm ghost icon chain-x" onclick={() => remove(i)} title="Убрать резервный"><Icon name="x" size={14} /></button>
      {:else}
        <span class="x-spacer"></span>
      {/if}
    </div>
  {/each}
  {#if entries.length}
    <button class="btn sm ghost chain-add" onclick={add}><Icon name="plus" size={13} />резервный</button>
  {/if}
</div>

<style>
  .chain { display: flex; flex-direction: column; gap: 8px; }
  .chain-row { display: flex; align-items: flex-start; gap: 8px; }
  .chain-label { width: 76px; flex: none; padding-top: 9px; font-size: 12.5px; color: var(--text-dim); }
  .chain-main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 6px; }
  .vpn {
    flex: none; width: 64px; margin-top: 5px; height: 30px; padding: 0;
    display: inline-flex; align-items: center; justify-content: center; gap: 6px;
    font: 600 11px var(--mono); letter-spacing: .04em;
    border-radius: 999px; border: 1px dashed var(--border);
    background: transparent; color: var(--text-dim); cursor: pointer; opacity: .75;
  }
  .vpn:hover { opacity: 1; }
  .vpn-dot { width: 7px; height: 7px; border-radius: 50%; border: 1.5px solid currentColor; }
  .vpn.on { background: var(--accent); color: #fff; border: 1px solid transparent; opacity: 1; }
  .vpn.on .vpn-dot { background: currentColor; }
  .vpn-spacer { width: 64px; flex: none; }
  .x-spacer, .chain-x { width: 30px; flex: none; }
  .chain-x { margin-top: 5px; padding: 0; justify-content: center; }
  .chain-add { align-self: flex-start; margin-left: 84px; }
  @media (max-width: 560px) {
    .chain-label { width: 100%; padding-top: 0; }
    .chain-row { flex-wrap: wrap; }
    .chain-add { margin-left: 0; }
  }
</style>
