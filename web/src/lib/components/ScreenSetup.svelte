<script lang="ts">
  import { api, ApiError } from "../api";
  import type { InstallStatus } from "../types";
  import Icon from "./Icon.svelte";

  let { onDone }: { onDone: () => void } = $props();

  let install = $state<InstallStatus | null>(null);
  let step = $state(1);
  let method = $state<"github" | "opkg">("github");
  let busy = $state(false);
  let installOut = $state("");
  let error = $state("");

  $effect(() => {
    api.installStatus()
      .then((s) => { install = s; })
      .catch(() => {});
  });

  const entwareOk = $derived(install?.entware ?? false);

  async function doInstall() {
    busy = true; error = "";
    try {
      const res = await api.install(method);
      installOut = JSON.stringify(res, null, 2);
      step = 4;
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = false; }
  }

  async function doCheck() {
    busy = true; error = "";
    try {
      const result = await api.configCheck();
      if (!result.ok) {
        error = (result.errors ?? []).join("\n") || result.stderr || "Конфиг не прошёл проверку";
      } else {
        step = 5;
      }
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = false; }
  }

  async function doStart() {
    busy = true; error = "";
    try {
      await api.service("start");
      onDone();
    } catch (e) {
      error = e instanceof ApiError ? e.message : String(e);
    } finally { busy = false; }
  }
</script>

<div class="page stack">
  <div class="callout accent">
    <Icon name="box" size={17} />
    <div class="callout-body">
      <b>Установка sing-box</b><br />
      Роутер почти готов. Пройдите шаги ниже — панель сама скачает ядро, создаст конфиг и запустит сервис. Ручная работа через SSH не нужна.
    </div>
  </div>

  <div class="card">
    <div class="card-head">
      <h3 class="card-title"><Icon name="box" size={17} />Мастер установки</h3>
      <div class="card-head-actions"><p class="card-sub" style="margin:0">5 шагов до рабочего VPN</p></div>
    </div>
    <div class="card-body">
      <div class="steps">

        <!-- Step 1 -->
        <div class={"step " + (step > 1 ? "done" : step === 1 ? "active" : "")}>
          <div class="step-rail">
            <div class="step-num">{#if step > 1}<Icon name="checkSmall" size={15} />{:else}1{/if}</div>
            <div class="step-line"></div>
          </div>
          <div class="step-content">
            <h4>Проверка Entware</h4>
            {#if step === 1}
              {#if install === null}
                <p>Проверка состояния роутера…</p>
              {:else if entwareOk}
                <div class="callout ok" style="margin-bottom:12px">
                  <Icon name="check" size={17} />
                  <div class="callout-body"><b>Entware на месте</b><br />Пакетная среда установлена — можно использовать любой способ установки.</div>
                </div>
                <button class="btn primary" onclick={() => step = 2}>Дальше <Icon name="arrowRight" size={14} /></button>
              {:else}
                <div class="callout err">
                  <Icon name="alert" size={17} />
                  <div class="callout-body"><b>Entware не найден</b><br />Установите Entware на роутер — без него недоступен opkg и запуск ядра.</div>
                </div>
              {/if}
            {/if}
          </div>
        </div>

        <!-- Step 2 -->
        <div class={"step " + (step > 2 ? "done" : step === 2 ? "active" : "")}>
          <div class="step-rail">
            <div class="step-num">{#if step > 2}<Icon name="checkSmall" size={15} />{:else}2{/if}</div>
            <div class="step-line"></div>
          </div>
          <div class="step-content">
            <h4>Способ установки</h4>
            {#if step === 2}
              <div class="seg" style="margin-bottom:12px">
                <div class={"seg-card" + (method === "github" ? " on" : "")} onclick={() => method = "github"} role="radio" aria-checked={method === "github"} tabindex="0" onkeydown={(e) => e.key === "Enter" && (method = "github")}>
                  <div class="seg-radio"></div>
                  <div class="seg-main">
                    <b>GitHub release <span class="seg-tag">рекоменд.</span></b>
                    <div class="seg-desc">Свежий бинарь напрямую с релизов sing-box. Не зависит от репозиториев opkg.</div>
                  </div>
                </div>
                <div class={"seg-card" + (method === "opkg" ? " on" : "")} onclick={() => entwareOk && (method = "opkg")} style={!entwareOk ? "opacity:.5;pointer-events:none" : ""} role="radio" aria-checked={method === "opkg"} tabindex="0">
                  <div class="seg-radio"></div>
                  <div class="seg-main">
                    <b>opkg <span class="seg-tag">{entwareOk ? "Entware" : "недоступно"}</span></b>
                    <div class="seg-desc">Установка через пакетный менеджер Entware. Требует настроенный репозиторий.</div>
                  </div>
                </div>
              </div>
              <button class="btn primary" onclick={() => step = 3}>Дальше <Icon name="arrowRight" size={14} /></button>
            {/if}
          </div>
        </div>

        <!-- Step 3 -->
        <div class={"step " + (step > 3 ? "done" : step === 3 ? "active" : "")}>
          <div class="step-rail">
            <div class="step-num">{#if step > 3}<Icon name="checkSmall" size={15} />{:else}3{/if}</div>
            <div class="step-line"></div>
          </div>
          <div class="step-content">
            <h4>Установка</h4>
            {#if step === 3}
              <p>Будет выполнена установка <span class="tag">{method === "github" ? "из GitHub release" : "через opkg"}</span>. Это займёт несколько секунд.</p>
              {#if error}<div class="callout err" style="margin-bottom:12px"><Icon name="alert" size={17} /><div class="callout-body">{error}</div></div>{/if}
              <button class="btn primary" disabled={busy} onclick={doInstall}>
                {#if busy}<span class="btn-spinner"></span>{:else}<Icon name="download" size={16} />{/if}
                Установить sing-box
              </button>
            {/if}
          </div>
        </div>

        <!-- Step 4 -->
        <div class={"step " + (step > 4 ? "done" : step === 4 ? "active" : "")}>
          <div class="step-rail">
            <div class="step-num">{#if step > 4}<Icon name="checkSmall" size={15} />{:else}4{/if}</div>
            <div class="step-line"></div>
          </div>
          <div class="step-content">
            <h4>Создание и проверка конфигурации</h4>
            {#if step === 4}
              {#if installOut}
                <div class="terminal" style="margin-bottom:12px;max-height:120px"><span class="l-info">{installOut}</span></div>
              {/if}
              <p>Создадим стартовый <span class="tag">config.json</span> и прогоним <span class="tag">sing-box check</span>.</p>
              {#if error}<div class="callout err" style="margin-bottom:12px"><Icon name="alert" size={17} /><div class="callout-body">{error}</div></div>{/if}
              <button class="btn primary" disabled={busy} onclick={doCheck}>
                {#if busy}<span class="btn-spinner"></span>{:else}<Icon name="check" size={16} />{/if}
                Создать и проверить
              </button>
            {/if}
          </div>
        </div>

        <!-- Step 5 -->
        <div class={"step " + (step === 5 ? "active" : "")}>
          <div class="step-rail">
            <div class="step-num">5</div>
          </div>
          <div class="step-content">
            <h4>Запуск сервиса</h4>
            {#if step === 5}
              <div class="callout ok" style="margin-bottom:12px">
                <Icon name="check" size={17} />
                <div class="callout-body"><b>Готово к запуску</b><br />Ядро установлено, конфиг валиден. Запустите сервис — дальше можно добавлять серверы и настраивать маршрутизацию.</div>
              </div>
              {#if error}<div class="callout err" style="margin-bottom:12px"><Icon name="alert" size={17} /><div class="callout-body">{error}</div></div>{/if}
              <button class="btn primary" disabled={busy} onclick={doStart}>
                {#if busy}<span class="btn-spinner"></span>{:else}<Icon name="play" size={16} />{/if}
                Запустить sing-box
              </button>
            {/if}
          </div>
        </div>
      </div>
    </div>
  </div>
</div>
