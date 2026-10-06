<script lang="ts">
  import { api, ApiError } from "../api";
  import type { Subscription, SubInput, SubRefreshResult } from "../types";
  import Icon from "./Icon.svelte";
  import FormSection from "./FormSection.svelte";

  // onchanged: the server set changed (refresh, add, delete) — reload the list.
  let { onchanged }: { onchanged: () => void } = $props();

  const INTERVALS = [
    { v: 60, label: "каждый час" },
    { v: 360, label: "каждые 6 часов" },
    { v: 720, label: "каждые 12 часов" },
    { v: 1440, label: "раз в сутки" },
  ];

  let subs = $state<Subscription[]>([]);
  // null = closed, "new" = add, Subscription = edit that one.
  let editor = $state<Subscription | "new" | null>(null);
  let form = $state<SubInput>(blank());
  let uaOpen = $state(false);
  let busy = $state("");
  let error = $state("");
  let notice = $state("");
  let formError = $state("");
  let confirmDelete = $state<Subscription | null>(null);

  function blank(): SubInput {
    return { name: "", url: "", interval: 720, enabled: true, auto_apply: true, user_agent: "" };
  }

  async function load() {
    try {
      subs = await api.subList();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => { load(); });

  function openEditor(s: Subscription | "new") {
    formError = "";
    if (s === "new") {
      form = blank();
      uaOpen = false;
    } else {
      form = { name: s.name, url: s.url, interval: s.interval || 720, enabled: s.enabled, auto_apply: s.auto_apply, user_agent: s.user_agent ?? "" };
      uaOpen = !!s.user_agent;
    }
    editor = s;
  }

  function report(r: SubRefreshResult) {
    const name = r.subscription.name || hostOf(r.subscription.url);
    if (!r.changed) {
      notice = `«${name}»: без изменений (${r.subscription.last_count} серв.).`;
    } else if (r.apply_error) {
      error = `«${name}»: серверы обновлены, но применить не удалось: ${r.apply_error}`;
    } else if (r.apply?.applied) {
      notice = `«${name}»: серверы обновлены и применены, sing-box перезапущен.`;
    } else {
      notice = `«${name}»: серверы обновлены (${r.subscription.last_count}). Нажмите «Применить и перезапустить».`;
    }
  }

  async function save() {
    busy = "save"; formError = "";
    try {
      if (editor === "new") {
        const r = await api.subAdd(form);
        editor = null;
        error = ""; notice = "";
        report(r);
        onchanged();
      } else if (editor) {
        await api.subUpdate(editor.id, form);
        editor = null;
        onchanged(); // the name shows on the servers' tags
      }
      await load();
    } catch (e) {
      formError = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  async function refresh(s: Subscription) {
    busy = "refresh:" + s.id; error = ""; notice = "";
    try {
      const r = await api.subRefresh(s.id);
      report(r);
      if (r.changed) onchanged();
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally {
      busy = "";
      await load();
    }
  }

  async function doDelete(s: Subscription) {
    confirmDelete = null;
    busy = "delete:" + s.id; error = ""; notice = "";
    try {
      await api.subDelete(s.id);
      notice = `Подписка «${s.name || hostOf(s.url)}» удалена вместе с серверами. Нажмите «Применить и перезапустить».`;
      onchanged();
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally {
      busy = "";
      await load();
    }
  }

  function hostOf(u: string): string {
    try { return new URL(u).host; } catch { return u; }
  }
  function fmtBytes(n: number): string {
    const u = ["Б", "КБ", "МБ", "ГБ", "ТБ"];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return `${n.toFixed(i >= 3 ? 1 : 0)} ${u[i]}`;
  }
  function fmtAgo(ts?: string): string {
    if (!ts) return "ещё не обновлялась";
    const m = Math.round((Date.now() - new Date(ts).getTime()) / 60000);
    if (m < 1) return "только что";
    if (m < 60) return `${m} мин назад`;
    if (m < 60 * 24) return `${Math.round(m / 60)} ч назад`;
    return new Date(ts).toLocaleDateString("ru-RU");
  }
  // Days until expiry; negative = expired, null = not reported.
  function daysLeft(s: Subscription): number | null {
    const exp = s.info?.expire;
    if (!exp) return null;
    return Math.floor((exp * 1000 - Date.now()) / 86400000);
  }
  const intervalLabel = (m: number) => INTERVALS.find((i) => i.v === m)?.label ?? `каждые ${m} мин`;
</script>

<div class="card">
  <div class="card-head">
    <div>
      <h3 class="card-title"><Icon name="link" size={17} />Подписки <span class="pill mono">{subs.length}</span></h3>
      <p class="card-sub">Ссылка провайдера со списком серверов. Список обновляется сам по расписанию.</p>
    </div>
    <div class="card-head-actions">
      <button class="btn sm" onclick={() => openEditor("new")}><Icon name="plus" size={14} />Добавить</button>
    </div>
  </div>
  <div class="card-body stack-sm">
    {#if subs.length === 0}
      <p class="hint-text">Подписок нет. Добавьте ссылку вида <span class="mono">https://…/sub/…</span> от Marzban, Remnawave, 3x-ui или другого провайдера.</p>
    {/if}
    {#each subs as s (s.id)}
      {@const left = daysLeft(s)}
      {@const used = (s.info?.upload ?? 0) + (s.info?.download ?? 0)}
      <div class="lrow">
        <div class="lrow-main">
          <div class="lrow-title">
            {s.name || hostOf(s.url)}
            <span class="pill mono">{s.last_count} серв.</span>
            {#if !s.enabled}<span class="pill">выключена</span>{/if}
            {#if s.auto_apply}<span class="tag">автоприменение</span>{/if}
            {#if s.via === "proxy"}<span class="tag" title="Напрямую провайдер недоступен, подписка скачана через sing-box">через прокси</span>{/if}
          </div>
          <div class="lrow-meta">
            <span class="mono">{hostOf(s.url)}</span>
            <span>{fmtAgo(s.last_fetch)} · {intervalLabel(s.interval)}</span>
            {#if s.skipped}<span class="tag" title="Нераспознанные ссылки и служебные записи провайдера">пропущено {s.skipped}</span>{/if}
            {#if s.info?.total}
              <span class="tag mono">{fmtBytes(used)} / {fmtBytes(s.info.total)}</span>
            {:else if used}
              <span class="tag mono">{fmtBytes(used)}</span>
            {/if}
            {#if left !== null}
              <span class="tag" class:warn-text={left < 7}>
                {left < 0 ? "истекла" : `до ${new Date((s.info?.expire ?? 0) * 1000).toLocaleDateString("ru-RU")}`}
              </span>
            {/if}
          </div>
          {#if s.last_error}
            <div class="sub-err">{s.last_error}</div>
          {/if}
        </div>
        <div class="lrow-actions">
          <button class="btn sm ghost" disabled={!!busy} onclick={() => refresh(s)} title="Обновить сейчас">
            {#if busy === "refresh:" + s.id}<span class="btn-spinner"></span>{:else}<Icon name="refresh" size={14} />{/if}
            Обновить
          </button>
          <button class="btn sm" disabled={!!busy} onclick={() => openEditor(s)}><Icon name="edit" size={14} />Изменить</button>
          <button class="btn sm danger icon" disabled={!!busy} onclick={() => (confirmDelete = s)} title="Удалить">
            <Icon name="trash" size={14} />
          </button>
        </div>
      </div>
    {/each}
    {#if error}<div class="callout err"><Icon name="alert" size={17} /><div class="callout-body">{error}</div></div>{/if}
    {#if notice}<div class="callout ok"><Icon name="check" size={17} /><div class="callout-body">{notice}</div></div>{/if}
  </div>
</div>

{#if editor}
  <div class="modal-scrim">
    <div class="modal editor" role="dialog" aria-modal="true" aria-labelledby="sub-editor-title">
      <div class="modal-head">
        <div class="modal-icon accent"><Icon name="link" size={19} /></div>
        <div style="flex:1;min-width:0;padding-top:2px">
          <h3 id="sub-editor-title">{editor === "new" ? "Новая подписка" : "Изменить подписку"}</h3>
          <p>{editor === "new" ? "Подписка будет скачана сразу, серверы появятся в списке" : (editor.name || hostOf(editor.url))}</p>
        </div>
        <button class="btn sm ghost icon" onclick={() => (editor = null)} title="Закрыть"><Icon name="x" size={16} /></button>
      </div>
      <div class="modal-body stack">
        <div class="field">
          <label for="sub-url">Ссылка подписки</label>
          <!-- svelte-ignore a11y_autofocus -->
          <input id="sub-url" class="input mono" bind:value={form.url} autofocus={editor === "new"} placeholder="https://panel.example.com/sub/…"
            onkeydown={(e) => e.key === "Enter" && form.url.trim() && save()} />
        </div>
        <div class="grid-2">
          <div class="field">
            <label for="sub-name">Название <span class="hint">необязательно</span></label>
            <input id="sub-name" class="input" bind:value={form.name} placeholder="из ответа провайдера" />
          </div>
          <div class="field">
            <label for="sub-interval">Обновлять</label>
            <select id="sub-interval" class="select" bind:value={form.interval}>
              {#if !INTERVALS.some((i) => i.v === form.interval)}<option value={form.interval}>каждые {form.interval} мин</option>{/if}
              {#each INTERVALS as i (i.v)}<option value={i.v}>{i.label}</option>{/each}
            </select>
          </div>
        </div>
        <div class="toggle-row">
          <div class="toggle-text"><b>Применять автоматически</b><span>Если список серверов изменился — пересобрать конфиг и перезапустить sing-box</span></div>
          <button class={"toggle" + (form.auto_apply ? " on" : "")} onclick={() => (form.auto_apply = !form.auto_apply)} role="switch" aria-checked={form.auto_apply} aria-label="Применять автоматически"></button>
        </div>
        {#if editor !== "new"}
          <div class="toggle-row">
            <div class="toggle-text"><b>Обновлять по расписанию</b><span>Выключенная подписка оставляет серверы как есть</span></div>
            <button class={"toggle" + (form.enabled ? " on" : "")} onclick={() => (form.enabled = !form.enabled)} role="switch" aria-checked={!!form.enabled} aria-label="Обновлять по расписанию"></button>
          </div>
        {/if}
        <FormSection title="Дополнительно" hint={form.user_agent?.trim() ? `User-Agent: ${form.user_agent.trim()}` : ""} bind:open={uaOpen}>
          <div class="field">
            <label for="sub-ua">User-Agent</label>
            <input id="sub-ua" class="input mono" bind:value={form.user_agent} placeholder="keenetic-sing-box-ui" />
            <span class="hint">Панели выбирают формат ответа по User-Agent. Если провайдер отдаёт JSON или Clash вместо ссылок, укажите <span class="mono">v2rayN/7.0</span>.</span>
          </div>
        </FormSection>
        {#if formError}<div class="callout err"><Icon name="alert" size={17} /><div class="callout-body">{formError}</div></div>{/if}
      </div>
      <div class="modal-foot">
        <button class="btn ghost" onclick={() => (editor = null)}>Отмена</button>
        <button class="btn primary" disabled={!!busy || !form.url.trim()} onclick={save}>
          {#if busy === "save"}<span class="btn-spinner"></span>{:else}<Icon name="check" size={16} />{/if}
          {editor === "new" ? "Добавить и загрузить" : "Сохранить"}
        </button>
      </div>
    </div>
  </div>
{/if}

{#if confirmDelete}
  <div class="modal-scrim" onmousedown={(e) => { if (e.target === e.currentTarget) confirmDelete = null; }}>
    <div class="modal">
      <div class="modal-head">
        <div class="modal-icon danger"><Icon name="trash" size={19} /></div>
        <div style="flex:1;padding-top:2px">
          <h3>Удалить «{confirmDelete.name || hostOf(confirmDelete.url)}»?</h3>
          <p>Вместе с подпиской удалятся её серверы ({confirmDelete.last_count}). Изменения вступят в силу после «Применить и перезапустить».</p>
        </div>
      </div>
      <div class="modal-foot">
        <button class="btn ghost" onclick={() => (confirmDelete = null)}>Отмена</button>
        <button class="btn danger solid" onclick={() => confirmDelete && doDelete(confirmDelete)}>Удалить</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .sub-err {
    margin-top: 4px;
    font-size: 12px;
    color: var(--danger-text);
    word-break: break-word;
  }
  .warn-text { color: var(--warn-text); }
</style>
