<script lang="ts">
  import { api, ApiError } from "./lib/api";
  import Login from "./lib/components/Login.svelte";
  import Shell from "./lib/components/Shell.svelte";

  let authed = $state(false);
  let checking = $state(true);

  async function probe() {
    checking = true;
    try {
      await api.whoami();
      authed = true;
    } catch (e) {
      authed = !(e instanceof ApiError && e.status === 401);
      if (e instanceof ApiError && e.status === 401) authed = false;
    } finally {
      checking = false;
    }
  }

  $effect(() => { probe(); });

  async function onLogout() {
    try { await api.logout(); } catch { /* ignore */ }
    authed = false;
  }
</script>

{#if checking}
  <div class="auth-stage">
    <div style="display:flex;flex-direction:column;align-items:center;gap:16px;color:var(--text-faint)">
      <div class="btn-spinner" style="width:22px;height:22px;color:var(--accent)"></div>
      <span class="mono" style="font-size:12.5px">проверка сессии · /api/whoami</span>
    </div>
  </div>
{:else if authed}
  <Shell {onLogout} />
{:else}
  <Login onLoggedIn={() => (authed = true)} />
{/if}
