<script lang="ts">
  import { api } from "../api";
  import type { SystemInfo, InstallStatus } from "../types";
  import Icon from "./Icon.svelte";
  import ScreenOverview from "./ScreenOverview.svelte";
  import ScreenRouting from "./ScreenRouting.svelte";
  import ScreenServers from "./ScreenServers.svelte";
  import ScreenDiagnostics from "./ScreenDiagnostics.svelte";
  import ScreenAdvanced from "./ScreenAdvanced.svelte";
  import ScreenSecurity from "./ScreenSecurity.svelte";
  import ScreenSetup from "./ScreenSetup.svelte";

  let { onLogout }: { onLogout: () => void } = $props();

  type Route = "overview" | "routing" | "servers" | "diagnostics" | "advanced" | "security" | "setup";

  let route = $state<Route>("overview");
  let sidebarOpen = $state(false);
  let refreshing = $state(false);
  let refreshKey = $state(0);

  let info = $state<SystemInfo | null>(null);
  let install = $state<InstallStatus | null>(null);
  let serverCount = $state(0);

  async function loadSidebar() {
    try {
      const [sys, ins] = await Promise.all([api.system(), api.installStatus()]);
      info = sys;
      install = ins;
    } catch { /* ignore */ }
    try {
      const list = await api.serverList();
      serverCount = list.length;
    } catch { /* ignore */ }
  }

  $effect(() => { loadSidebar(); });

  async function refresh() {
    refreshing = true;
    await loadSidebar();
    refreshKey++;
    refreshing = false;
  }

  function go(r: Route) {
    route = r;
    sidebarOpen = false;
    const sc = document.querySelector(".scroll");
    if (sc) sc.scrollTop = 0;
  }

  const NAV_MANAGE = [
    { id: "overview" as Route, label: "Обзор", icon: "overview" },
    { id: "routing" as Route, label: "Маршрутизация", icon: "route" },
    { id: "servers" as Route, label: "Серверы", icon: "server" },
    { id: "diagnostics" as Route, label: "Диагностика", icon: "diagnostics" },
  ];
  const NAV_SYSTEM = [
    { id: "advanced" as Route, label: "Дополнительно", icon: "advanced" },
    { id: "security" as Route, label: "Безопасность", icon: "shield" },
  ];

  const TITLES: Record<string, [string, string]> = {
    overview:    ["Обзор", "Состояние системы и сервиса"],
    routing:     ["Маршрутизация", "Режим перехвата, домены и подсети"],
    servers:     ["VPN-серверы", "Outbound-подключения sing-box"],
    diagnostics: ["Диагностика", "Логи, проверка конфига, outbounds"],
    advanced:    ["Дополнительно", "Raw config и резервные копии"],
    security:    ["Безопасность", "Пароль и сессия"],
    setup:       ["Установка sing-box", "Пошаговый мастер"],
  };

  const installed = $derived(install?.installed ?? false);
  const svcEnabled = $derived(info?.service?.enabled ?? false);
  const svcPresent = $derived(info?.service?.present ?? false);
  const running = $derived(installed && svcPresent && svcEnabled);

  const svcKind = $derived(running ? "live" : (installed ? "warn" : "err"));
  const svcText = $derived(running ? "Сервис активен" : (installed ? "Сервис остановлен" : "Не установлен"));
  const svcSub = $derived(installed
    ? (install?.version ? "sing-box " + install.version : "sing-box")
    : (info?.entware ? "Entware present" : "Entware missing"));

  const [title, sub] = $derived(TITLES[route] ?? ["sing-box", ""]);

  const now = new Date().toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
</script>

<div class="app">
  {#if sidebarOpen}
    <div class="scrim-mob" onclick={() => sidebarOpen = false} role="presentation"></div>
  {/if}

  <aside class={"sidebar" + (sidebarOpen ? " open" : "")}>
    <!-- Brand -->
    <div class="sb-brand">
      <div class="sb-mark"><Icon name="box" size={18} /></div>
      <div class="sb-brand-text"><b>sing-box</b><span>Keenetic</span></div>
    </div>

    <!-- Status badge -->
    <div class="sb-status" onclick={() => go(installed ? "overview" : "setup")} role="button" tabindex="0" onkeydown={(e) => e.key === "Enter" && go(installed ? "overview" : "setup")}>
      <span class={"dot " + svcKind}></span>
      <div class="sb-status-text">
        <b>{svcText}</b>
        <span class="mono">{svcSub}</span>
      </div>
    </div>

    <!-- Nav -->
    <nav class="sb-nav">
      <div class="sb-nav-label">Управление</div>
      {#each NAV_MANAGE as n}
        <div
          class={"nav-item" + (route === n.id ? " active" : "")}
          onclick={() => go(n.id)}
          role="button"
          tabindex="0"
          onkeydown={(e) => e.key === "Enter" && go(n.id)}
        >
          <Icon name={n.icon} size={17} />{n.label}
          {#if n.id === "overview" && !running && installed}
            <span class="badge">!</span>
          {/if}
          {#if n.id === "servers"}
            <span class="badge neutral">{serverCount}</span>
          {/if}
        </div>
      {/each}

      <div class="sb-nav-label">Система</div>
      {#each NAV_SYSTEM as n}
        <div
          class={"nav-item" + (route === n.id ? " active" : "")}
          onclick={() => go(n.id)}
          role="button"
          tabindex="0"
          onkeydown={(e) => e.key === "Enter" && go(n.id)}
        >
          <Icon name={n.icon} size={17} />{n.label}
        </div>
      {/each}
    </nav>

    <!-- Footer -->
    <div class="sb-foot">
      <div class="sb-avatar">AD</div>
      <div class="sb-foot-text">
        <b>admin</b>
        <span>LAN · 192.168.1.1</span>
      </div>
      <button class="btn sm ghost icon" title="Выйти" onclick={onLogout}>
        <Icon name="logout" size={16} />
      </button>
    </div>
  </aside>

  <div class="main">
    <!-- Topbar -->
    <header class="topbar">
      <button class="btn ghost icon menu-btn" onclick={() => sidebarOpen = true}>
        <Icon name="menu" size={18} />
      </button>
      <div style="min-width:0">
        <h1>{title}</h1>
        <div class="crumb-sub">{sub}</div>
      </div>
      <span class="topbar-spacer"></span>
      <span class="topbar-meta"><Icon name="clock" size={13} />{now}</span>
      <button class="btn sm" disabled={refreshing} onclick={refresh}>
        {#if refreshing}<span class="btn-spinner"></span>{:else}<Icon name="refresh" size={14} />{/if}
        Обновить
      </button>
    </header>

    <!-- Screen content -->
    <div class="scroll">
      {#key refreshKey}
        {#if route === "overview"}
          <ScreenOverview onNav={(r) => go(r as Route)} />
        {:else if route === "routing"}
          <ScreenRouting />
        {:else if route === "servers"}
          <ScreenServers />
        {:else if route === "diagnostics"}
          <ScreenDiagnostics />
        {:else if route === "advanced"}
          <ScreenAdvanced />
        {:else if route === "security"}
          <ScreenSecurity onLogout={onLogout} />
        {:else if route === "setup"}
          <ScreenSetup onDone={() => { loadSidebar(); go("overview"); }} />
        {/if}
      {/key}
    </div>
  </div>
</div>
