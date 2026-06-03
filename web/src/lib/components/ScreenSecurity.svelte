<script lang="ts">
  import { api, ApiError } from "../api";
  import Icon from "./Icon.svelte";

  let { onLogout }: { onLogout: () => void } = $props();

  let cur = $state("");
  let next = $state("");
  let conf = $state("");
  let busy = $state(false);
  let error = $state("");
  let success = $state(false);

  const tooShort = $derived(next.length > 0 && next.length < 8);
  const mismatch = $derived(conf.length > 0 && next !== conf);
  const canSave = $derived(!!cur && next.length >= 8 && next === conf);

  async function save(e: Event) {
    e.preventDefault();
    busy = true; error = ""; success = false;
    try {
      await api.setPassword(next, cur);
      cur = next = conf = "";
      success = true;
    } catch (err) {
      error = err instanceof ApiError ? err.message : String(err);
    } finally { busy = false; }
  }
</script>

<div class="page stack">
  <!-- Password -->
  <div class="card">
    <div class="card-head">
      <div>
        <h3 class="card-title"><Icon name="key" size={17} />Пароль администратора</h3>
        <p class="card-sub">Защищает доступ к панели управления</p>
      </div>
    </div>
    <form class="card-body stack" style="max-width:460px" onsubmit={save}>
      {#if success}
        <div class="callout ok">
          <Icon name="check" size={17} />
          <div class="callout-body"><b>Пароль изменён</b><br />Используйте новый пароль при следующем входе.</div>
        </div>
      {/if}
      <div class="field">
        <label>Текущий пароль <span class="hint">или admin token</span></label>
        <input class="input" type="password" bind:value={cur} autocomplete="current-password" placeholder="••••••••" />
      </div>
      <hr class="divider" />
      <div class="field">
        <label>Новый пароль <span class="hint">минимум 8 символов</span></label>
        <input class="input" type="password" bind:value={next} autocomplete="new-password" placeholder="••••••••" />
      </div>
      {#if tooShort}
        <span class="hint-text" style="color:var(--danger-text)">Пароль слишком короткий — нужно минимум 8 символов.</span>
      {/if}
      <div class="field">
        <label>Подтверждение</label>
        <input class="input" type="password" bind:value={conf} autocomplete="new-password" placeholder="••••••••" />
      </div>
      {#if mismatch}
        <span class="hint-text" style="color:var(--danger-text)">Пароли не совпадают.</span>
      {/if}
      {#if error}
        <div class="callout err"><Icon name="alert" size={17} /><div class="callout-body">{error}</div></div>
      {/if}
      <div class="row" style="margin-top:4px">
        <button class="btn primary" type="submit" disabled={!canSave || busy}>
          {#if busy}<span class="btn-spinner"></span>{:else}<Icon name="check" size={16} />{/if}
          Сменить пароль
        </button>
      </div>
    </form>
  </div>

  <!-- Session -->
  <div class="card">
    <div class="card-head">
      <div>
        <h3 class="card-title"><Icon name="shield" size={17} />Сессия</h3>
        <p class="card-sub">Текущий вход и безопасность</p>
      </div>
    </div>
    <div class="card-body stack-sm">
      <div class="lrow">
        <div class="lrow-main">
          <div class="lrow-title">Эта сессия</div>
          <div class="lrow-meta">cookie-сессия · CSRF активен · LAN</div>
        </div>
        <span class="pill ok"><span class="dot ok"></span>активна</span>
      </div>
      <div class="callout">
        <Icon name="info" size={17} />
        <div class="callout-body">
          Панель доступна только внутри локальной сети и защищена паролем и CSRF. Это не публичный сервис.
        </div>
      </div>
      <div class="row">
        <button class="btn danger" onclick={onLogout}>
          <Icon name="logout" size={16} />Выйти из сессии
        </button>
      </div>
    </div>
  </div>
</div>
