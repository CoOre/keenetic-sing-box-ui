<script lang="ts">
  import { api, ApiError } from "../api";

  let open = $state(false);
  let current = $state("");
  let next = $state("");
  let confirm = $state("");
  let busy = $state(false);
  let msg = $state("");
  let error = $state("");

  async function submit(e: Event) {
    e.preventDefault();
    error = "";
    msg = "";
    if (next.length < 8) {
      error = "New password must be at least 8 characters.";
      return;
    }
    if (next !== confirm) {
      error = "Passwords do not match.";
      return;
    }
    busy = true;
    try {
      await api.setPassword(next, current);
      msg = "Password changed.";
      current = next = confirm = "";
      open = false;
    } catch (err) {
      error = err instanceof ApiError ? err.message : String(err);
    } finally {
      busy = false;
    }
  }
</script>

<div class="card">
  <div class="head">
    <h2>Password</h2>
    <button onclick={() => (open = !open)}>{open ? "Cancel" : "Change"}</button>
  </div>
  {#if msg}<div class="ok-msg">{msg}</div>{/if}
  {#if open}
    <form onsubmit={submit}>
      <input
        type="password"
        placeholder="current password (or admin token)"
        bind:value={current}
        autocomplete="current-password"
      />
      <input
        type="password"
        placeholder="new password (min 8)"
        bind:value={next}
        autocomplete="new-password"
      />
      <input
        type="password"
        placeholder="confirm new password"
        bind:value={confirm}
        autocomplete="new-password"
      />
      {#if error}<div class="err">{error}</div>{/if}
      <button class="primary" type="submit" disabled={busy || !next || !confirm}>
        {busy ? "Saving…" : "Save password"}
      </button>
    </form>
  {/if}
</div>

<style>
  .head {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }
  h2 {
    margin: 0;
    font-size: 1.1rem;
  }
  form {
    display: flex;
    flex-direction: column;
    gap: 10px;
    margin-top: 12px;
  }
  .err {
    color: var(--err);
    font-size: 0.9em;
  }
  .ok-msg {
    color: var(--ok);
    font-size: 0.9em;
    margin-top: 8px;
  }
</style>
