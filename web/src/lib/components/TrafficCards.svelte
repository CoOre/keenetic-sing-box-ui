<script lang="ts">
  import { api } from "../api";

  let up = $state(0);
  let down = $state(0);
  let connected = $state(false);
  let error = $state("");

  function fmt(bytesPerSec: number): string {
    const u = ["B/s", "KB/s", "MB/s", "GB/s"];
    let n = bytesPerSec;
    let i = 0;
    while (n >= 1024 && i < u.length - 1) {
      n /= 1024;
      i++;
    }
    return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
  }

  // The Clash /traffic endpoint streams newline-delimited JSON ({"up":N,"down":N}).
  // We read it via fetch + ReadableStream so the backend Bearer injection applies.
  $effect(() => {
    const ctrl = new AbortController();
    (async () => {
      try {
        const resp = await fetch(api.clashTrafficURL(), {
          credentials: "same-origin",
          signal: ctrl.signal,
        });
        if (!resp.ok || !resp.body) {
          error = `traffic stream unavailable (HTTP ${resp.status})`;
          return;
        }
        connected = true;
        const reader = resp.body.getReader();
        const dec = new TextDecoder();
        let buf = "";
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += dec.decode(value, { stream: true });
          let nl: number;
          while ((nl = buf.indexOf("\n")) >= 0) {
            const line = buf.slice(0, nl).trim();
            buf = buf.slice(nl + 1);
            if (!line) continue;
            try {
              const t = JSON.parse(line);
              up = t.up ?? 0;
              down = t.down ?? 0;
            } catch {
              /* ignore partial/garbage line */
            }
          }
        }
      } catch (e) {
        if (!ctrl.signal.aborted) {
          error = e instanceof Error ? e.message : String(e);
        }
      } finally {
        connected = false;
      }
    })();
    return () => ctrl.abort();
  });
</script>

<div class="card">
  <h2>
    Traffic
    {#if connected}
      <span class="pill ok"><span class="dot"></span>live</span>
    {:else if error}
      <span class="pill warn" title={error}><span class="dot"></span>offline</span>
    {/if}
  </h2>
  <div class="cards">
    <div class="metric">
      <div class="arrow down">↓</div>
      <div class="num">{fmt(down)}</div>
      <div class="muted">download</div>
    </div>
    <div class="metric">
      <div class="arrow up">↑</div>
      <div class="num">{fmt(up)}</div>
      <div class="muted">upload</div>
    </div>
  </div>
  {#if error}
    <p class="hint muted">
      Traffic needs sing-box running with <code>clash_api</code> enabled on 127.0.0.1:9090.
    </p>
  {/if}
</div>

<style>
  h2 {
    margin: 0 0 12px;
    font-size: 1.1rem;
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .cards {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }
  .metric {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 14px;
    text-align: center;
  }
  .arrow {
    font-size: 1.3rem;
  }
  .arrow.down {
    color: var(--ok);
  }
  .arrow.up {
    color: var(--accent);
  }
  .num {
    font-family: var(--mono);
    font-size: 1.4rem;
    font-weight: 600;
    margin: 2px 0;
  }
  .hint {
    margin: 12px 0 0;
    font-size: 0.84em;
  }
</style>
