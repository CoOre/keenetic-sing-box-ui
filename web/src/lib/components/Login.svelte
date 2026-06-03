<script lang="ts">
  import { api, ApiError } from "../api";
  import Icon from "./Icon.svelte";

  let { onLoggedIn }: { onLoggedIn: () => void } = $props();

  let passwordSet = $state<boolean | null>(null);
  let password = $state("");
  let confirm = $state("");
  let busy = $state(false);
  let error = $state("");

  $effect(() => {
    api.authStatus()
      .then((s) => (passwordSet = s.password_set))
      .catch(() => (passwordSet = true));
  });

  const tooShort = $derived(password.length > 0 && password.length < 8);
  const mismatch = $derived(confirm.length > 0 && password !== confirm);
  const canSetup = $derived(password.length >= 8 && password === confirm);

  async function doLogin(e: Event) {
    e.preventDefault();
    if (!password) return;
    busy = true; error = "";
    try {
      await api.login(password);
      onLoggedIn();
    } catch (err) {
      error = err instanceof ApiError ? err.message : String(err);
    } finally { busy = false; }
  }

  async function doSetup(e: Event) {
    e.preventDefault();
    if (!canSetup) return;
    busy = true; error = "";
    try {
      await api.setPassword(password);
      onLoggedIn();
    } catch (err) {
      error = err instanceof ApiError ? err.message : String(err);
    } finally { busy = false; }
  }
</script>

<div class="auth-stage">
  {#if passwordSet === null}
    <div style="display:flex;flex-direction:column;align-items:center;gap:16px;color:var(--text-faint)">
      <div class="btn-spinner" style="width:22px;height:22px;color:var(--accent)"></div>
      <span class="mono" style="font-size:12.5px">проверка сессии…</span>
    </div>
  {:else if passwordSet}
    <div class="auth-card">
      <div class="auth-brand">
        <div class="sb-mark"><Icon name="box" size={24} /></div>
        <div style="text-align:center">
          <h2>sing-box</h2>
          <p>Keenetic · панель управления</p>
        </div>
      </div>
      <div class="card">
        <form class="card-body stack" onsubmit={doLogin}>
          <div class="field">
            <label>Пароль</label>
            <!-- svelte-ignore a11y_autofocus -->
            <input
              class="input"
              type="password"
              bind:value={password}
              autocomplete="current-password"
              placeholder="Введите пароль для входа"
              autofocus
            />
          </div>
          {#if error}
            <div class="callout err">
              <Icon name="alert" size={17} />
              <div class="callout-body"><b>Неверный пароль</b><br />{error}</div>
            </div>
          {/if}
          <button class="btn primary block" type="submit" disabled={busy || !password}>
            {#if busy}<span class="btn-spinner"></span>{/if}
            Войти
          </button>
          <p class="hint-text" style="text-align:center">Забыли пароль? Войдите через admin token, затем задайте новый.</p>
        </form>
      </div>
    </div>
  {:else}
    <div class="auth-card">
      <div class="auth-brand">
        <div class="sb-mark"><Icon name="box" size={24} /></div>
        <div style="text-align:center">
          <h2>Добро пожаловать</h2>
          <p>Задайте пароль для защиты панели</p>
        </div>
      </div>
      <div class="card">
        <form class="card-body stack" onsubmit={doSetup}>
          <div class="callout accent">
            <Icon name="shield" size={17} />
            <div class="callout-body">
              <b>Trust-on-first-use</b><br />
              Это первый запуск. Пароль, который вы зададите, будет защищать доступ к панели в локальной сети.
            </div>
          </div>
          <div class="field">
            <label>Новый пароль <span class="hint">минимум 8 символов</span></label>
            <!-- svelte-ignore a11y_autofocus -->
            <input class="input" type="password" bind:value={password} autocomplete="new-password" placeholder="••••••••" autofocus />
          </div>
          {#if tooShort}
            <span class="hint-text" style="color:var(--danger-text)">Пароль слишком короткий — нужно минимум 8 символов.</span>
          {/if}
          <div class="field">
            <label>Подтверждение</label>
            <input class="input" type="password" bind:value={confirm} autocomplete="new-password" placeholder="••••••••" />
          </div>
          {#if mismatch}
            <span class="hint-text" style="color:var(--danger-text)">Пароли не совпадают.</span>
          {/if}
          {#if error}
            <span class="hint-text" style="color:var(--danger-text)">{error}</span>
          {/if}
          <button class="btn primary block" type="submit" disabled={busy || !canSetup}>
            {#if busy}<span class="btn-spinner"></span>{/if}
            Задать пароль
          </button>
        </form>
      </div>
    </div>
  {/if}
</div>
