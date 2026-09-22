<script lang="ts">
  import { api, ApiError } from "../api";
  import type { Server, ServerType } from "../types";
  import Icon from "./Icon.svelte";
  import FormSection from "./FormSection.svelte";

  let { initial = null, onclose, onsaved }: {
    initial?: Server | null;
    onclose: () => void;
    onsaved: (s: Server) => void;
  } = $props();

  const PROTOCOLS: { v: ServerType; label: string }[] = [
    { v: "vless", label: "VLESS" },
    { v: "trojan", label: "Trojan" },
    { v: "shadowsocks", label: "Shadowsocks" },
    { v: "vmess", label: "VMess" },
    { v: "hysteria2", label: "Hysteria 2" },
  ];
  const SS_METHODS = [
    "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305",
    "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305",
  ];

  const FINGERPRINTS = ["chrome", "firefox", "safari", "ios", "edge", "random", "randomized"];

  function blank(): Server {
    return { name: "", type: "vless", server: "", server_port: 443, tls: true, network: "" };
  }

  // Form state. Composite fields (lists, reality switch) are edited as plain
  // text/booleans and folded back into the Server in normalized().
  let form = $state<Server>(blank());
  let reality = $state(false);
  let alpnText = $state("");
  let portsText = $state("");
  let link = $state("");
  let parsed = $state(false);
  let manual = $state(false);
  let busy = $state("");
  let error = $state("");
  let tlsOpen = $state(false);
  let transportOpen = $state(false);
  let hy2Open = $state(false);

  const editing = $derived(!!form.id);
  const t = $derived(form.type);
  const hasTLS = $derived(t !== "shadowsocks");
  const tlsForced = $derived(t === "trojan" || t === "hysteria2");
  const tlsOn = $derived(tlsForced || !!form.tls);
  const hasUTLS = $derived(t === "vless" || t === "vmess" || t === "trojan");
  const hasTransport = $derived(t === "vless" || t === "vmess" || t === "trojan");
  const isHy2 = $derived(t === "hysteria2");

  function load(s: Server) {
    form = { ...blank(), ...s };
    reality = !!s.public_key?.trim();
    alpnText = (s.alpn ?? []).join(", ");
    portsText = (s.server_ports ?? []).map((p) => p.replace(":", "-")).join(", ");
    // Open only the sections that carry non-default values.
    tlsOpen = !!(s.sni || s.alpn?.length || s.insecure || s.fingerprint || reality);
    transportOpen = !!s.network;
    hy2Open = !!(s.obfs_password || s.server_ports?.length || s.up_mbps || s.down_mbps);
  }

  $effect.pre(() => {
    // Runs once: initial is fixed for the lifetime of the modal.
    if (initial) { load(initial); manual = true; }
  });

  function setType(v: ServerType) {
    form.type = v;
    if (v === "trojan" || v === "hysteria2") form.tls = true;
    if (v === "shadowsocks" && !form.method) form.method = SS_METHODS[0];
  }

  async function parseLink(raw = link) {
    raw = raw.trim();
    if (!raw) return;
    busy = "parse"; error = "";
    try {
      const s = await api.serverParse(raw);
      load(s);
      parsed = true;
      manual = true;
      link = "";
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  function onPaste(e: ClipboardEvent) {
    const text = e.clipboardData?.getData("text") ?? "";
    if (text.includes("://")) {
      e.preventDefault();
      link = text.trim();
      parseLink(link);
    }
  }

  // Build the payload with only the fields relevant to the chosen protocol,
  // so switching protocols never leaves stale fields behind.
  function normalized(): Server {
    const f = form;
    const out: Server = {
      id: f.id, name: f.name.trim(), type: f.type,
      server: f.server.trim(), server_port: Number(f.server_port) || 0,
    };
    const str = (v?: string) => (v ?? "").trim() || undefined;
    const num = (v?: number) => Number(v) > 0 ? Number(v) : undefined;

    if (t === "vless" || t === "vmess") out.uuid = str(f.uuid);
    if (t === "vless") out.flow = str(f.flow);
    if (t === "vmess") out.alter_id = num(f.alter_id);
    if (t === "trojan" || t === "shadowsocks" || t === "hysteria2") out.password = f.password || undefined;
    if (t === "shadowsocks") out.method = f.method;

    if (hasTLS && tlsOn) {
      out.tls = true;
      out.sni = str(f.sni);
      const alpn = alpnText.split(",").map((a) => a.trim()).filter(Boolean);
      if (alpn.length) out.alpn = alpn;
      if (f.insecure) out.insecure = true;
      if (hasUTLS) out.fingerprint = str(f.fingerprint);
      if (t === "vless" && reality) {
        out.public_key = str(f.public_key);
        out.short_id = str(f.short_id);
      }
    }
    if (hasTransport && f.network) {
      out.network = f.network;
      if (f.network === "ws") { out.ws_path = str(f.ws_path); out.ws_host = str(f.ws_host); }
      if (f.network === "grpc") out.grpc_service_name = str(f.grpc_service_name);
    }
    if (isHy2) {
      const ports = portsText.split(/[,\s]+/).map((p) => p.trim().replace("-", ":")).filter(Boolean);
      if (ports.length) {
        out.server_ports = ports;
        out.hop_interval = str(f.hop_interval);
      }
      out.up_mbps = num(f.up_mbps);
      out.down_mbps = num(f.down_mbps);
      out.obfs_password = f.obfs_password || undefined;
    }
    return out;
  }

  async function save() {
    busy = "save"; error = "";
    try {
      onsaved(await api.serverSave(normalized()));
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = ""; }
  }

  const tlsHint = $derived.by(() => {
    if (!tlsOn) return "выключен";
    const parts = [form.sni && `SNI ${form.sni}`, t === "vless" && reality && "Reality", form.insecure && "insecure"];
    return parts.filter(Boolean).join(" · ") || "включён";
  });
  const transportHint = $derived(
    ({ "": "TCP", ws: "WebSocket", grpc: "gRPC", http: "HTTP" } as Record<string, string>)[form.network ?? ""] ?? form.network ?? "TCP",
  );
  const hy2Hint = $derived.by(() => {
    const parts = [
      form.obfs_password && "obfs salamander",
      portsText.trim() && `порты ${portsText.trim()}`,
      (form.up_mbps || form.down_mbps) ? `${form.up_mbps || "–"}/${form.down_mbps || "–"} Мбит/с` : "BBR",
    ];
    return parts.filter(Boolean).join(" · ");
  });
</script>

<div class="modal-scrim">
  <div class="modal editor" role="dialog" aria-modal="true" aria-labelledby="srv-editor-title">
    <div class="modal-head">
      <div class="modal-icon accent"><Icon name="server" size={19} /></div>
      <div style="flex:1;min-width:0;padding-top:2px">
        <h3 id="srv-editor-title">{editing ? "Изменить сервер" : "Новый сервер"}</h3>
        <p>{editing ? (form.name || form.server) : "Вставьте share-ссылку — поля заполнятся сами"}</p>
      </div>
      <button class="btn sm ghost icon" onclick={onclose} title="Закрыть"><Icon name="x" size={16} /></button>
    </div>

    <div class="modal-body editor-body stack">
      {#if !editing}
        <div class="row" style="gap:8px">
          <!-- svelte-ignore a11y_autofocus -->
          <input class="input mono" bind:value={link} autofocus
            placeholder="vless:// · trojan:// · ss:// · vmess:// · hy2://"
            onpaste={onPaste}
            onkeydown={(e) => e.key === "Enter" && parseLink()} />
          <button class="btn" disabled={!link.trim() || busy === "parse"} onclick={() => parseLink()}>
            {#if busy === "parse"}<span class="btn-spinner"></span>{:else}<Icon name="link" size={16} />{/if}
            Разобрать
          </button>
        </div>
        {#if parsed}
          <div class="callout ok"><Icon name="check" size={17} /><div class="callout-body">Ссылка разобрана. Проверьте поля и сохраните.</div></div>
        {/if}
        {#if !manual}
          <button class="or-manual" onclick={() => { manual = true; setType(form.type); }}>
            <span>или заполнить вручную</span><Icon name="chevDown" size={14} />
          </button>
        {/if}
      {/if}

      {#if manual}
        <div class="proto" role="radiogroup" aria-label="Протокол">
          {#each PROTOCOLS as p (p.v)}
            <button type="button" role="radio" aria-checked={t === p.v} class:on={t === p.v} onclick={() => setType(p.v)}>{p.label}</button>
          {/each}
        </div>

        <div class="field"><label for="srv-name">Название</label><input id="srv-name" class="input" bind:value={form.name} placeholder="Мой сервер" /></div>
        <div class="addr">
          <div class="field"><label for="srv-host">Адрес</label><input id="srv-host" class="input mono" bind:value={form.server} placeholder="1.2.3.4 или host.example.net" /></div>
          <div class="field"><label for="srv-port">Порт</label><input id="srv-port" class="input mono" type="number" min="1" max="65535" bind:value={form.server_port} /></div>
        </div>

        {#if t === "vless" || t === "vmess"}
          <div class="field"><label for="srv-uuid">UUID</label><input id="srv-uuid" class="input mono" bind:value={form.uuid} placeholder="00000000-0000-…" /></div>
        {/if}
        {#if t === "shadowsocks"}
          <div class="grid-2">
            <div class="field">
              <label for="srv-method">Метод</label>
              <select id="srv-method" class="select" bind:value={form.method}>
                {#if form.method && !SS_METHODS.includes(form.method)}<option>{form.method}</option>{/if}
                {#each SS_METHODS as m (m)}<option>{m}</option>{/each}
              </select>
            </div>
            <div class="field"><label for="srv-pass">Пароль</label><input id="srv-pass" class="input mono" bind:value={form.password} /></div>
          </div>
        {:else if t === "trojan" || t === "hysteria2"}
          <div class="field">
            <label for="srv-pass">Пароль {#if isHy2}<span class="hint">auth</span>{/if}</label>
            <input id="srv-pass" class="input mono" bind:value={form.password} />
          </div>
        {/if}
        {#if t === "vless"}
          <div class="field">
            <label for="srv-flow">Flow</label>
            <select id="srv-flow" class="select" bind:value={form.flow}>
              <option value="">— нет —</option>
              <option value="xtls-rprx-vision">xtls-rprx-vision</option>
              {#if form.flow && form.flow !== "xtls-rprx-vision"}<option>{form.flow}</option>{/if}
            </select>
          </div>
        {/if}
        {#if t === "vmess"}
          <div class="field"><label for="srv-aid">AlterID <span class="hint">обычно 0</span></label><input id="srv-aid" class="input mono" type="number" min="0" bind:value={form.alter_id} /></div>
        {/if}

        <div class="stack-sm">
          {#if isHy2}
            <FormSection title="Hysteria 2" hint={hy2Hint} bind:open={hy2Open}>
              <div class="field"><label for="hy-obfs">Obfs-пароль <span class="hint">salamander, пусто — без obfs</span></label><input id="hy-obfs" class="input mono" bind:value={form.obfs_password} /></div>
              <div class="grid-2">
                <div class="field"><label for="hy-ports">Port hopping <span class="hint">через запятую</span></label><input id="hy-ports" class="input mono" bind:value={portsText} placeholder="20000-30000" /></div>
                <div class="field"><label for="hy-hop">Интервал смены</label><input id="hy-hop" class="input mono" bind:value={form.hop_interval} placeholder="30s" disabled={!portsText.trim()} /></div>
              </div>
              <div class="grid-2">
                <div class="field"><label for="hy-up">Upload, Мбит/с</label><input id="hy-up" class="input mono" type="number" min="0" bind:value={form.up_mbps} placeholder="пусто — BBR" /></div>
                <div class="field"><label for="hy-down">Download, Мбит/с</label><input id="hy-down" class="input mono" type="number" min="0" bind:value={form.down_mbps} placeholder="пусто — BBR" /></div>
              </div>
            </FormSection>
          {/if}

          {#if hasTLS}
            <FormSection title={t === "vless" ? "TLS и Reality" : "TLS"} hint={tlsHint} bind:open={tlsOpen}>
              {#if !tlsForced}
                <div class="toggle-row">
                  <div class="toggle-text"><b>TLS</b><span>Шифрование транспорта</span></div>
                  <button class={"toggle" + (form.tls ? " on" : "")} onclick={() => (form.tls = !form.tls)} role="switch" aria-checked={!!form.tls} aria-label="TLS"></button>
                </div>
              {/if}
              {#if tlsOn}
                <div class="grid-2">
                  <div class="field"><label for="tls-sni">SNI</label><input id="tls-sni" class="input mono" bind:value={form.sni} placeholder={form.server || "server name"} /></div>
                  {#if hasUTLS}
                    <div class="field">
                      <label for="tls-fp">Fingerprint <span class="hint">uTLS</span></label>
                      <select id="tls-fp" class="select" bind:value={form.fingerprint}>
                        <option value="">— нет —</option>
                        {#each FINGERPRINTS as fp (fp)}<option>{fp}</option>{/each}
                        {#if form.fingerprint && !FINGERPRINTS.includes(form.fingerprint)}<option>{form.fingerprint}</option>{/if}
                      </select>
                    </div>
                  {:else}
                    <div class="field"><label for="tls-alpn">ALPN</label><input id="tls-alpn" class="input mono" bind:value={alpnText} placeholder="h3" /></div>
                  {/if}
                </div>
                {#if hasUTLS}
                  <div class="field"><label for="tls-alpn">ALPN <span class="hint">через запятую</span></label><input id="tls-alpn" class="input mono" bind:value={alpnText} placeholder="h2, http/1.1" /></div>
                {/if}
                <div class="toggle-row">
                  <div class="toggle-text"><b>Не проверять сертификат</b><span>Для самоподписанных сертификатов</span></div>
                  <button class={"toggle" + (form.insecure ? " on" : "")} onclick={() => (form.insecure = !form.insecure)} role="switch" aria-checked={!!form.insecure} aria-label="Insecure"></button>
                </div>
                {#if t === "vless"}
                  <div class="toggle-row">
                    <div class="toggle-text"><b>Reality</b><span>TLS-маскировка под чужой сайт</span></div>
                    <button class={"toggle" + (reality ? " on" : "")} onclick={() => (reality = !reality)} role="switch" aria-checked={reality} aria-label="Reality"></button>
                  </div>
                  {#if reality}
                    <div class="grid-2">
                      <div class="field"><label for="re-pbk">Public key</label><input id="re-pbk" class="input mono" bind:value={form.public_key} /></div>
                      <div class="field"><label for="re-sid">Short ID</label><input id="re-sid" class="input mono" bind:value={form.short_id} /></div>
                    </div>
                  {/if}
                {/if}
              {/if}
            </FormSection>
          {/if}

          {#if hasTransport}
            <FormSection title="Транспорт" hint={transportHint} bind:open={transportOpen}>
              <div class="grid-3">
                <div class="field">
                  <label for="tr-net">Тип</label>
                  <select id="tr-net" class="select" bind:value={form.network}>
                    <option value="">TCP</option>
                    <option value="ws">WebSocket</option>
                    <option value="grpc">gRPC</option>
                    <option value="http">HTTP</option>
                  </select>
                </div>
                {#if form.network === "ws"}
                  <div class="field"><label for="tr-path">Path</label><input id="tr-path" class="input mono" bind:value={form.ws_path} placeholder="/path" /></div>
                  <div class="field"><label for="tr-host">Host</label><input id="tr-host" class="input mono" bind:value={form.ws_host} placeholder="host header" /></div>
                {/if}
                {#if form.network === "grpc"}
                  <div class="field" style="grid-column:span 2"><label for="tr-svc">Service name</label><input id="tr-svc" class="input mono" bind:value={form.grpc_service_name} /></div>
                {/if}
              </div>
            </FormSection>
          {/if}
        </div>
      {/if}

      {#if error}<div class="callout err"><Icon name="alert" size={17} /><div class="callout-body">{error}</div></div>{/if}
    </div>

    <div class="modal-foot">
      <button class="btn ghost" onclick={onclose}>Отмена</button>
      <button class="btn primary" disabled={!manual || !!busy || !form.server.trim()} onclick={save}>
        {#if busy === "save"}<span class="btn-spinner"></span>{:else}<Icon name="check" size={16} />{/if}
        {editing ? "Сохранить" : "Добавить"}
      </button>
    </div>
  </div>
</div>

<style>
  .editor {
    max-width: 600px;
    max-height: calc(100vh - 48px);
    display: flex;
    flex-direction: column;
  }
  .editor-body {
    overflow-y: auto;
    min-height: 0;
  }
  .proto {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    padding: 4px;
    background: var(--bg-soft);
    border: 1px solid var(--border-soft);
    border-radius: var(--r);
  }
  .proto button {
    all: unset;
    box-sizing: border-box;
    flex: 1 1 auto;
    text-align: center;
    padding: 7px 10px;
    font-size: 13px;
    font-weight: 500;
    color: var(--text-dim);
    border-radius: var(--r-sm);
    cursor: pointer;
    white-space: nowrap;
  }
  .proto button:hover { color: var(--text); background: var(--surface-2); }
  .proto button.on { color: var(--accent-text); background: var(--accent-soft); box-shadow: inset 0 0 0 1px var(--accent-border); }
  .proto button:focus-visible { outline: 2px solid var(--accent-border); }
  .addr {
    display: grid;
    grid-template-columns: 1fr 110px;
    gap: var(--gap);
  }
  .or-manual {
    all: unset;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    font-size: 12.5px;
    color: var(--text-faint);
    cursor: pointer;
    padding: 4px 0;
  }
  .or-manual:hover { color: var(--text-dim); }
  .or-manual::before, .or-manual::after {
    content: "";
    flex: 1;
    height: 1px;
    background: var(--border-soft);
  }
  @media (max-width: 560px) {
    .addr { grid-template-columns: 1fr 90px; }
  }
</style>
