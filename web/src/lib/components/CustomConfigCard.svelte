<script lang="ts">
  import { api, ApiError } from "../api";
  import type { ServersApplyResult } from "../types";
  import Icon from "./Icon.svelte";
  import JsonEditor, { type EditorProblem } from "./JsonEditor.svelte";

  let text = $state("");
  let original = $state("");
  let exists = $state(false);
  let path = $state("");
  let loading = $state(true);
  let loadError = $state("");
  let jsonError = $state("");
  let busy = $state(""); // "check" | "apply"
  let result = $state<ServersApplyResult | null>(null);
  let resultKind = $state<"check" | "apply">("check");
  let notice = $state("");

  const dirty = $derived(text !== original);
  const blank = $derived(!text.trim() || text.trim() === "{}");
  const errors = $derived(result && !result.check.ok ? errorLines(result) : []);
  const problems = $derived<EditorProblem[]>([
    ...(result?.syntax_error
      ? [{ line: result.syntax_error.line, col: result.syntax_error.col, message: result.syntax_error.message }]
      : errors.map((message) => ({ message }))),
    ...(result?.warnings ?? []).map((message) => ({ message, severity: "warning" as const })),
  ]);

  const example = `{
  "inbounds": [
    {
      "type": "mixed",
      "tag": "in-example",
      "listen": "192.168.1.1",
      "listen_port": 1091,
      "users": [{ "username": "user", "password": "change-me" }]
    }
  ],
  "outbounds": [
    { "type": "direct", "tag": "via-example", "bind_interface": "nwg0" }
  ],
  "route": {
    "rules": [{ "inbound": ["in-example"], "outbound": "via-example" }]
  }
}
`;

  function errorLines(r: ServersApplyResult): string[] {
    const e = r.check.errors ?? [];
    if (e.length) return e;
    return r.check.stderr ? [r.check.stderr.trim()] : ["sing-box check не прошёл"];
  }

  async function load() {
    loading = true; loadError = "";
    try {
      const c = await api.customRead();
      text = c.content; original = c.content; exists = c.exists; path = c.path;
      validate();
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    } finally { loading = false; }
  }

  $effect(() => { load(); });

  function validate(): boolean {
    if (blank) { jsonError = ""; return true; }
    try {
      const v = JSON.parse(text);
      if (v === null || typeof v !== "object" || Array.isArray(v)) {
        jsonError = "ожидается JSON-объект { … }";
        return false;
      }
      jsonError = ""; return true;
    } catch (e) {
      jsonError = e instanceof Error ? e.message : "invalid JSON"; return false;
    }
  }

  function onChange() { result = null; notice = ""; validate(); }

  function format() {
    if (blank) return;
    try { text = JSON.stringify(JSON.parse(text), null, 2) + "\n"; onChange(); }
    catch { validate(); }
  }

  function insertExample() {
    text = example; onChange();
  }

  // 400 from the API carries the same result shape (check errors, syntax
  // position) — show it like a failed check rather than a bare message.
  function asResult(e: unknown): ServersApplyResult {
    if (e instanceof ApiError && e.detail && typeof e.detail === "object" && "check" in e.detail) {
      return e.detail as ServersApplyResult;
    }
    return { check: { ok: false, errors: [e instanceof Error ? e.message : String(e)] }, applied: false, servers: 0 };
  }

  async function check() {
    if (!validate()) return;
    busy = "check"; notice = ""; resultKind = "check";
    try { result = await api.customCheck(text); }
    catch (e) { result = asResult(e); }
    finally { busy = ""; }
  }

  async function apply() {
    if (!validate()) return;
    busy = "apply"; notice = ""; resultKind = "apply";
    try {
      result = await api.customWrite(text, true);
      original = text;
      exists = !blank;
      notice = blank ? "Дополнения удалены, конфиг пересобран." : "Сохранено и применено.";
      // A failed restart doesn't void the apply: the config is already written.
      if (result.restart_error) notice += ` sing-box не перезапустился: ${result.restart_error}`;
      else if (result.restart) notice += " sing-box перезапущен.";
      if (result.firewall_error) notice += ` Фаервол: ${result.firewall_error}`;
    } catch (e) {
      result = asResult(e);
    } finally { busy = ""; }
  }
</script>

<div class="card flush">
  <div class="card-head">
    <h3 class="card-title"><Icon name="advanced" size={17} />Дополнения к конфигу</h3>
    <div class="card-head-actions">
      {#if jsonError}
        <span class="pill err"><span class="dot err"></span>ошибка JSON</span>
      {:else if exists && !dirty}
        <span class="pill ok"><span class="dot ok"></span>активны</span>
      {/if}
      {#if dirty}<span class="pill warn"><span class="dot warn"></span>не сохранено</span>{/if}
    </div>
  </div>

  <div class="card-body stack-sm" style="padding-bottom:12px">
    <p class="hint-text">
      Фрагмент конфига sing-box{#if path}&nbsp;<span class="tag">{path.split("/").pop()}</span>{/if}, который
      вливается в каждый сгенерированный конфиг. Объекты сливаются, массивы (<span class="mono">inbounds</span>,
      <span class="mono">outbounds</span>, <span class="mono">route.rules</span>…) дополняются в конец — ваши
      правила идут после служебных и перед <span class="mono">route.final</span>; простые значения заменяют
      сгенерированные. Пустой редактор — дополнений нет.
    </p>
  </div>

  {#if loading}
    <div class="card-body"><p class="hint-text">Загрузка…</p></div>
  {:else if loadError}
    <div class="card-body">
      <div class="callout warn"><Icon name="info" size={17} /><div class="callout-body">{loadError}</div></div>
    </div>
  {:else}
    <JsonEditor bind:value={text} onchange={onChange} {problems} minHeight="240px"
      placeholder={'{ "inbounds": [ … ], "outbounds": [ … ], "route": { "rules": [ … ] } }'} />

    {#if jsonError}
      <div style="padding:12px var(--pad) 0">
        <p class="hint-text mono" style="color:var(--danger-text);font-size:12px">{jsonError}</p>
      </div>
    {/if}

    {#if result}
      <div style="padding:12px var(--pad) 0" class="stack-sm">
        {#if errors.length}
          <div class="callout err">
            <Icon name="alert" size={17} />
            <div class="callout-body">
              <b>{resultKind === "apply" ? "Не применено — ничего не сохранено" : "Ошибки"}</b>
              {#each errors as e}<br /><span class="mono" style="font-size:12px">{e}</span>{/each}
            </div>
          </div>
        {:else if resultKind === "check"}
          <div class="callout ok">
            <Icon name="check" size={17} />
            <div class="callout-body">
              <b>Итоговый конфиг валиден</b><br />
              Проверено: JSON, слияние, ссылки на теги, порты, <span class="mono">sing-box check</span>.
            </div>
          </div>
        {/if}
        {#if result.warnings?.length}
          <div class="callout warn">
            <Icon name="warn" size={17} />
            <div class="callout-body">
              <b>Предупреждения</b>
              {#each result.warnings as w}<br /><span style="font-size:12.5px">{w}</span>{/each}
            </div>
          </div>
        {/if}
      </div>
    {/if}

    {#if notice}
      <div style="padding:12px var(--pad) 0">
        <p class="hint-text" style="color:var(--ok-text)">{notice}</p>
      </div>
    {/if}

    <div class="card-head" style="border-top:1px solid var(--border-soft);border-bottom:none;flex-wrap:wrap;gap:8px;margin-top:14px">
      <button class="btn sm" disabled={blank || !!jsonError} onclick={format}><Icon name="copy" size={14} />Форматировать</button>
      {#if blank}
        <button class="btn sm" onclick={insertExample}><Icon name="plus" size={14} />Пример</button>
      {/if}
      <button class="btn sm" disabled={!dirty || !!busy} onclick={() => { text = original; onChange(); }}>
        <Icon name="restart" size={14} />Сбросить
      </button>
      <span class="spacer"></span>
      <button class="btn sm" disabled={!!busy || !!jsonError} onclick={check}>
        {#if busy === "check"}<span class="btn-spinner"></span>{:else}<Icon name="check" size={14} />{/if}
        Проверить
      </button>
      <button class="btn sm primary" disabled={!!busy || !!jsonError || (!dirty && !exists)} onclick={apply}>
        {#if busy === "apply"}<span class="btn-spinner"></span>{:else}<Icon name="save" size={14} />{/if}
        {blank && exists ? "Удалить и применить" : "Сохранить и применить"}
      </button>
    </div>
  {/if}
</div>
