<script setup lang="ts">
import { onMounted } from 'vue'

// легаси-скрипт - классический (не модуль): та же семантика, что была
// в монолите; подключается один раз после монтирования разметки
let injected = false
onMounted(() => {
  if (injected) return
  injected = true
  const s = document.createElement('script')
  s.src = import.meta.env.BASE_URL + 'panel.js?v=' + __PANEL_V__
  document.body.appendChild(s)
})
</script>

<template>
<header>
  <h1>mawg</h1>
  <span class="meta" id="meta">загрузка...</span>
  <span class="badge" id="updBadge" style="display:none;cursor:pointer;border-color:var(--ok);color:var(--ok)" title="проверить обновления">обновление</span>
  <span style="flex:1"></span>
  <button class="primary" onclick="dlgPool.showModal(); loadSlots(); setPoolMode('cfg'); $('#pSource').value = '';">+ интерфейс</button>
</header>
<main>
  <div class="grid" id="pools"></div>
  <div class="card muted" id="empty" style="display:none; margin-top:0">Пулов нет. Создайте первый, например proton.</div>
  <div class="card" id="ifacescard" style="margin-top:16px">
    <h2>Интерфейсы роутера <span class="muted" id="ifacecount" style="font-size:12px;font-weight:normal"></span>
      <button id="ifShowHidden" style="font-size:11px"></button></h2>
    <div class="sub">WireGuard-интерфейсы системы. Пулы управляются mawg, внешние только показываются (статус, привязка групп), скрытые не видны нигде.</div>
    <div id="ifacelist"><div class="muted"><span class="spin"></span>загружаю интерфейсы роутера...</div></div>
  </div>
  <div class="card" id="rulescard" style="margin-top:16px">
    <h2>Правила маршрутизации <span class="muted">через MagiTrickle</span></h2>
    <div class="sub">Группы правил привязываются к интерфейсам пулов. Типы: домен, поддомены, подсеть, маска, регулярное выражение.</div>
    <div class="btnrow" style="margin-top:2px">
      <input id="rgName" placeholder="имя группы" style="width:140px">
      <input type="color" id="rgColor" value="#4a9eff" title="цвет группы">
      <select id="rgIface"></select>
      <button class="primary" id="rgCreate">+ группа</button>
      <button id="rgCascade" title="направить трафик одного подключения через другое">+ каскад</button>
    </div>
    <div class="btnrow" style="margin-top:6px">
      <div class="psel" id="presetWrap" style="flex:1;min-width:260px">
        <button type="button" class="psel-btn" id="presetBtn">выберите шаблон</button>
        <div class="psel-list" id="presetList" hidden></div>
      </div>
      <input type="hidden" id="presetSel">
      <select id="presetIface"></select>
      <button id="presetApply">шаблон на интерфейс</button>
    </div>
    <div class="muted" id="presetSrc" style="font-size:11px;margin-top:4px"></div>
    <div class="btnrow" style="align-items:center;margin-top:12px;font-size:12px;flex-wrap:wrap">
      <span class="muted">Политики отвала: переключать после</span>
      <input id="polFailCycles" type="number" min="1" max="20" style="width:56px" title="сколько циклов проверки (30с) интерфейс должен считаться мёртвым подряд" placeholder="2">
      <span class="muted">циклов отказа, возвращать после</span>
      <input id="polRestoreCycles" type="number" min="1" max="20" style="width:56px" title="сколько циклов подряд интерфейс должен быть живым перед возвратом" placeholder="2">
      <span class="muted">здоровых циклов</span>
      <button type="button" id="polCyclesSave" disabled>сохранить</button>
    </div>
    <div class="muted" style="font-size:11px;margin-top:4px">Цикл проверки - каждые 30 секунд. Меньше 2 не рекомендуется: «переключать после 1» уводит группы на запасной интерфейс из-за одной потерянной пробы (разовый таймаут или скачок пинга - ложное срабатывание), «возвращать после 1» дёргает группы обратно при первом же удачном прогоне - флапающий канал будет переключать их каждые 30-60 секунд и рвать соединения. Значения 2/2 - разумный компромисс: реакция на отвала ~1-1.5 минуты, возврат только после устойчивого восстановления.</div>
    <div id="mtgroups" style="margin-top:10px"></div>
  </div>
  <div class="card" id="bundcard" style="margin-top:16px">
    <h2>Наборы интерфейсов <span class="muted">ротация между интерфейсами</span>
      <button class="primary" id="bCreate" style="font-size:11px">+ набор</button></h2>
    <div class="sub">Группы MagiTrickle привязываются к набору: mawg держит их на первом здоровом участнике (приоритет по порядку), при отказе переключает на следующий, при восстановлении возвращает. Участники: пулы mawg и внешние интерфейсы.</div>
    <div id="bundles"></div>
  </div>
  <div class="card" id="syscard" style="margin-top:16px">
    <h2>Система <span class="muted" id="sysrefresh" style="cursor:pointer">обновить</span></h2>
    <div class="sub">Проверка зависимостей: пакеты WireGuard/AmneziaWG, MagiTrickle, ядро sing-box-lx, компоненты прошивки. Установка только по вашему подтверждению.</div>
    <table id="systable"><tr><th>компонент</th><th>версия</th><th>состояние</th><th></th></tr></table>
    <div id="rciRow" style="display:none;margin-top:12px;border-top:1px solid var(--border);padding-top:10px">
      <div style="font-size:13px;margin-bottom:4px">Токен локального API Keenetic</div>
      <div class="muted" style="font-size:11px;margin-bottom:6px">На прошивках 5.2+ локальный RCI требует токен (NDM-4515): создайте его в веб-интерфейсе Keenetic и вставьте сюда. Без токена mawg работает через CLI медленнее. На 5.1 и старее не нужен.</div>
      <div class="btnrow" style="align-items:center">
        <input id="rciToken" placeholder="X-NDMA-TKN" style="flex:1;min-width:200px;font-family:ui-monospace,monospace">
        <button id="rciSave">сохранить</button>
      </div>
    </div>
    <div style="margin-top:12px;border-top:1px solid var(--border);padding-top:10px">
      <div style="font-size:13px;margin-bottom:4px">Проверка канала провайдера</div>
      <div class="muted" style="font-size:11px;margin-bottom:6px">Перед тем как засчитать отказ интерфейса, проверяется основной канал интернета той же пробой. Канал недоступен или деградировал - отказ интерфейса не засчитывается, ротация не запускается.</div>
      <div class="btnrow" style="align-items:center">
        <label class="switch" title="включить проверку канала провайдера"><input type="checkbox" id="wanOn"><span class="knob"></span></label>
        <select id="wanType">
          <option value="http">HTTP 204</option>
          <option value="icmp">Ping IP</option>
        </select>
        <input id="wanTarget" placeholder="http://www.gstatic.com/generate_204" style="flex:1;min-width:180px">
        <span style="display:inline-flex;align-items:center;gap:6px;font-size:12px">
          <label class="switch" title="включить порог RTT"><input type="checkbox" id="wanRttOn"><span class="knob"></span></label>
          порог <input id="wanRtt" type="number" min="1" max="5000" style="width:80px"> мс
        </span>
        <button class="primary" id="wanSave">сохранить</button>
      </div>
    </div>
    <div style="margin-top:12px;border-top:1px solid var(--border);padding-top:10px">
      <div style="font-size:13px;margin-bottom:4px">Режим движка sing-box</div>
      <div class="muted" style="font-size:11px;margin-bottom:6px">«Свой экземпляр» - mawg держит отдельный процесс sing-box (tun1+, mixed 2282+, clash 2291). «Общее ядро» - пулы mawg живут фрагментом mawg-pools.json рядом с config.json основного sing-box (сам config.json не меняется), применение - горячее: lx-ядро перечитывает конфиг по SIGHUP, туннели и соединения не рвутся (на upstream-ядре - рестарт сервиса). Пробы идут через clash_api основного ядра.</div>
      <div class="btnrow" style="align-items:center">
        <select id="sbMode">
          <option value="own">свой экземпляр</option>
          <option value="shared">общее ядро (-C merge)</option>
        </select>
        <button class="primary" id="sbModeSave">применить</button>
        <span class="muted" style="font-size:11px" id="sbModeInfo"></span>
      </div>
      <div class="btnrow" id="sbCacheRow" style="display:none;align-items:center">
        <span class="muted" style="font-size:11px;flex:1" id="sbCacheText"></span>
        <button class="primary" id="sbCacheOff">выключить кэш</button>
        <button id="sbCacheTmp">в /tmp</button>
      </div>
    </div>
  </div>
  <div class="card" id="setcard" style="margin-top:16px">
    <h2>Настройки</h2>
    <div class="sub">Доступ к панели, сетевые параметры, обновление.</div>
    <div id="serverSettingsBox" style="margin-top:6px">
      <div class="btnrow" style="align-items:center">
        <b style="font-size:13px">Авторизация</b>
        <span style="flex:1"></span>
        <label class="switch" title="включить или выключить вход по паролю"><input type="checkbox" id="ssAuthOn"><span class="knob"></span></label>
      </div>
      <div class="muted" style="font-size:11px;margin-top:2px" id="ssAuthHint"></div>
      <div class="btnrow" style="align-items:center;margin-top:10px">
        <b style="font-size:13px">Сеть</b>
      </div>
      <div class="btnrow" style="align-items:center">
        <input id="ssAddr" placeholder="0.0.0.0" style="width:130px" title="адрес прослушивания">
        <span class="muted">:</span>
        <input id="ssPort" type="number" min="1" max="65535" style="width:90px" title="порт">
        <span class="muted" style="font-size:11px">адрес и порт панели</span>
      </div>
      <div class="muted" style="font-size:11px;margin-top:2px">0.0.0.0 - все интерфейсы, 127.0.0.1 - только локально (для nginx-прокси), конкретный IP - один интерфейс. Авторизация и разрешённые адреса применяются сразу, адрес и порт - после перезапуска демона (кнопка появится здесь же).</div>
      <div style="font-size:13px;margin-top:8px">Разрешённые адреса</div>
      <div id="ssChips" style="display:flex;flex-wrap:wrap;gap:6px;margin-top:4px"></div>
      <div class="btnrow" style="align-items:center;margin-top:6px">
        <input id="ssAllowed" placeholder="192.168.0.82 или 192.168.0.0/24" style="flex:1;min-width:170px">
        <button id="ssAdd">добавить</button>
      </div>
      <div class="btnrow" style="align-items:center;margin-top:4px">
        <select id="ssDevices" style="flex:1;min-width:170px"><option value="">устройства сети...</option></select>
        <button id="ssAddMine" title="добавить адрес, с которого вы открыли панель">мой адрес</button>
      </div>
      <div class="muted" style="font-size:11px;margin-top:2px">IP или подсеть, с которых открыт доступ к панели. Пусто - доступ всем. Роутер всегда может открыть панель сам. За реверс-прокси фильтруется адрес прокси, учитывайте.</div>
      <div class="btnrow" style="margin-top:8px;justify-content:flex-end">
        <button id="ssSave">сохранить</button>
      </div>
      <div class="btnrow" id="ssRestartRow" style="display:none;margin-top:6px;align-items:center;border:1px solid var(--warn);border-radius:8px;padding:8px 10px">
        <span style="font-size:12px;color:var(--warn)">адрес или порт изменён - нужен перезапуск демона</span>
        <span style="flex:1"></span>
        <button class="primary" id="ssRestart">перезапустить панель</button>
      </div>
    </div>
    <div class="btnrow" style="align-items:center;margin-top:12px;border-top:1px solid var(--border);padding-top:10px">
      <button id="setPassBtn">сменить пароль</button>
      <button id="updCheckBtn" title="сравнить версию с последним релизом на GitHub">проверить обновления</button>
      <button id="logoutBtn">выйти</button>
    </div>
    <div class="muted" id="updInfo" style="font-size:11px;margin-top:4px"></div>
  </div>
  <details open>
    <summary>Журнал событий</summary>
    <div id="log"></div>
  </details>
</main>

<dialog id="dlgPool">
  <form method="dialog" style="display:grid; gap:4px">
    <h3 style="margin:0 0 6px">Новый интерфейс (пул)</h3>
    <div class="btnrow" style="margin:0 0 2px">
      <button type="button" id="pModeCfg" class="primary">конфиги .conf / .zip</button>
      <button type="button" id="pModeLink">ссылка или подписка</button>
      <button type="button" id="pModeFree">Amnezia Free без аккаунта</button>
    </div>
    <label class="f">Имя (латиница, цифры, дефис)
      <input id="pName" placeholder="proton" pattern="[a-z][a-z0-9-]{0,14}" title="строчная латиница, цифры и дефисы, начинается с буквы, до 15 символов" required>
    </label>
    <label class="f" id="slotRow" style="display:none">Слот Keenetic
      <div class="btnrow" style="margin-top:3px">
        <select id="pSlot" style="flex:1;min-width:180px"></select>
        <button type="button" id="slotCreate" title="создать следующий слот WireguardN через CLI">+ слот</button>
      </div>
      <div id="slotHint" style="display:none;font-size:11px;color:var(--warn);margin-top:4px"></div>
    </label>
    <label class="f" id="protoRow" style="display:none">Протокол (OpenWrt)
      <select id="pProto">
        <option value="wireguard">wireguard</option>
        <option value="amneziawg">amneziawg</option>
      </select>
    </label>
    <label class="f">Если все конфиги недоступны
      <select id="pFallback">
        <option value="direct">пустить трафик напрямую</option>
        <option value="hold">держать последний конфиг</option>
      </select>
    </label>
    <label class="f" id="pSourceRow" style="display:none">Ссылка, URL подписки или несколько ссылок строками
      <textarea id="pSource" rows="6" placeholder="vpn://... / wireguard://... / amneziawg://...&#10;vless://... / trojan://...&#10;https://example.com/sub" style="font-family:ui-monospace,monospace;font-size:12px"></textarea>
    </label>
    <div id="pAmneziaRow" style="display:none;margin-top:6px;border:1px solid var(--border);border-radius:6px;padding:8px">
      <div style="font-size:12px;margin-bottom:4px">Это ключ Amnezia Premium/Free API - mawg обменяет его у gateway Амнезии на конфиг <b id="pAmneziaProto">AWG</b> и создаст пул.</div>
      <div class="muted" style="font-size:11px;margin-bottom:4px">Транспорт к gateway: напрямую или через socks5 уже работающего пула (если провайдер режет gw.amnezia.org). Пробы идут строго по HTTPS, ответы валидируются как JSON.</div>
      <div class="btnrow" style="align-items:center">
        <select id="pAmneziaVia" style="flex:1;min-width:200px">
          <option value="">напрямую (https)</option>
        </select>
        <select id="pAmneziaCountry" style="display:none"></select>
      </div>
    </div>
    <div class="muted" id="pInspect" style="display:none;font-size:11px;margin-top:2px"></div>
    <div class="muted" id="pLinkHint" style="display:none;font-size:11px">wireguard://, amneziawg:// и vpn:// превращаются в обычные конфиги пула (нативно, слот). Узлы vless/trojan и т.п. становятся tun-интерфейсом движка sing-box (tun1, tun2...), который виден в MagiTrickle как обычный интерфейс. Если на роутере движка нет - узлы будут показаны списком с пометкой.</div>
    <div class="muted" id="pFreeHint" style="display:none;font-size:12px">Аккаунт, подписка и ключ не нужны. Доступность и регион определяет официальный gateway. Конфиг Free - AmneziaWG 3.x: нативные интерфейсы его не поднимают, пул работает через ядро sing-box-lx (если ядра нет - установите его в «Система -> Зависимости», пул можно создать и до установки). Gateway может потребовать капчу - она появится для ввода. Пул создаётся выключенным.</div>
    <div id="pPlanResult" style="display:none;font-size:12px;border-top:1px solid var(--border);padding-top:8px"></div>
    <div class="btnrow" style="justify-content:end" id="pActions">
      <button value="cancel" id="pCancel">Отмена</button>
      <button class="primary" id="pCreate">Создать</button>
    </div>
  </form>
</dialog>

<dialog id="dlgGroup">
  <form method="dialog" style="display:grid; gap:4px">
    <h3 style="margin:0 0 6px">Изменить группу</h3>
    <label class="f">Имя
      <input id="egName" required>
    </label>
    <label class="f">Цвет
      <input type="color" id="egColor" style="width:100%;height:30px">
    </label>
    <label class="f">Интерфейс
      <select id="egIface"></select>
    </label>
    <label class="f">Если основной интерфейс недоступен
      <select id="egPolicy">
        <option value="">по умолчанию (как у пула)</option>
        <option value="direct">прямой ход (проверив интернет)</option>
        <option value="iface">переключить на запасной интерфейс</option>
      </select>
    </label>
    <label class="f" id="egPolicyIfaceWrap" style="display:none">Запасной интерфейс
      <select id="egPolicyIface"></select>
    </label>
    <div class="muted" id="egPolicyHint" style="font-size:11px"></div>
    <div class="btnrow" style="justify-content:end">
      <button value="cancel">Отмена</button>
      <button class="primary" id="egSave">Сохранить</button>
    </div>
  </form>
</dialog>

<dialog id="dlgCascade">
  <form method="dialog" style="display:grid; gap:4px; min-width:420px">
    <h3 style="margin:0 0 4px">Каскад</h3>
    <div class="muted" style="font-size:12px;margin-bottom:6px">Создается служебная группа: трафик к эндпоинтам источника направляется через путь-посредник. Включается и выключается обычным тумблером группы; при ротации источника адреса добавляются сами.</div>
    <label class="f">Что гоняем (источник эндпоинтов)
      <select id="csSource"></select>
    </label>
    <label class="f" id="csHostsWrap" style="display:none">Адреса или домены его эндпоинтов (по одному в строке)
      <textarea id="csHosts" rows="3" placeholder="8.39.125.1&#10;engage.cloudflareclient.com"></textarea>
    </label>
    <label class="f">Через что (путь)
      <select id="csVia"></select>
    </label>
    <div class="btnrow" style="justify-content:end">
      <button value="cancel">Отмена</button>
      <button class="primary" id="csCreate">Создать каскад</button>
    </div>
  </form>
</dialog>

<dialog id="dlgSettings">
  <form method="dialog" style="display:grid; gap:4px">
    <h3 style="margin:0 0 6px">Настройки пула <span class="muted" id="spName"></span></h3>
    <div class="f" id="spSourceRow" style="display:none;border-bottom:1px solid var(--border);padding-bottom:10px;margin-bottom:4px">
      <div style="font-size:13px;margin-bottom:2px">Источник (ссылка, vpn:// или URL подписки)</div>
      <textarea id="spSource" rows="3" style="width:100%;font-family:ui-monospace,monospace;font-size:12px"></textarea>
      <div class="btnrow" style="margin-top:6px;justify-content:flex-end;align-items:center">
        <select id="spAmneziaCountry" style="display:none;max-width:220px" title="локация у gateway Амнезии"></select>
        <button type="button" id="spRefreshSource" title="заново разобрать источник и пересобрать пул (конфиги заменяются)">обновить из источника</button>
      </div>
      <label class="f" style="margin-top:6px">Автообновление источника, ч (0 = из заголовка подписки, без него сутки; при деградации пула mawg перепроверит источник и сам)
        <input id="spUpdateInt" type="number" min="0" max="8760">
      </label>
      <div class="muted" style="font-size:11px;margin-top:2px">Сервер сменил IP или ссылка протухла - вставьте новую и нажмите. Старые конфиги пула будут заменены разобранными. Для ключа Amnezia mawg повторит обмен (тем же устройством, без новой выдачи); можно сменить локацию списком.</div>
    </div>
    <label class="f" id="spRenameRow">Имя (переименование)
      <input id="spRename" pattern="[a-z][a-z0-9-]{0,14}" title="строчная латиница, цифры и дефисы, начинается с буквы, до 15 символов">
    </label>
    <div class="f" id="spMembersRow" style="display:none">
      <div style="font-size:13px;margin-bottom:2px">Состав пула (протоколы и адреса)</div>
      <div id="spMembers" style="max-height:180px;overflow:auto;font-size:12px;display:grid;gap:4px"></div>
    </div>
    <label class="f" id="spProbeTypeRow">Тип пробы через туннель
      <select id="spProbeType">
        <option value="http">HTTP-страница 204 (надежнее, ICMP режут)</option>
        <option value="icmp">Ping публичного IP</option>
      </select>
    </label>
    <label class="f">Цель пробы
      <input id="spProbe" placeholder="http://www.gstatic.com/generate_204">
    </label>
    <div class="muted" id="spEngineNote" style="display:none;font-size:11px;margin:-2px 0 4px">Проверка автоматическая: mawg получает целевую страницу через узел (строгий HTTP-тест, ping не используется). После порога отказов узел подписки переключается на следующий, неудачный уходит в cooldown.</div>
    <div class="f" id="spRttRow" style="display:flex;align-items:center;gap:10px;flex-wrap:wrap">
      <label class="switch" title="включить порог RTT"><input type="checkbox" id="spRttOn"><span class="knob"></span></label>
      <span>считать нерабочим при RTT выше</span>
      <input id="spRtt" type="number" min="1" max="5000" style="width:90px"> мс
    </div>
    <label class="f">Интервал проверки, сек
      <input id="spInterval" type="number" min="10" max="3600">
    </label>
    <label class="f" id="spThresholdRow">Отказов подряд до ротации
      <input id="spThreshold" type="number" min="1" max="10">
    </label>
    <label class="f" id="spCooldownRow">Cooldown неудачного конфига, мин
      <input id="spCooldown" type="number" min="1" max="720">
    </label>
    <label class="f" id="spKeepaliveRow">Keepalive, сек (0 = из конфига)
      <input id="spKeepalive" type="number" min="0" max="300">
    </label>
    <label class="f">Если все конфиги недоступны
      <select id="spFallback">
        <option value="direct">пустить трафик напрямую</option>
        <option value="hold">держать последний конфиг</option>
      </select>
    </label>
    <div class="muted" id="fbHint" style="font-size:11px;margin-top:2px"></div>
    <div class="btnrow" style="justify-content:end">
      <button value="cancel">Отмена</button>
      <button class="primary" id="spSave">Сохранить</button>
    </div>
  </form>
</dialog>

<dialog id="dlgBundle">
  <form method="dialog" style="display:grid; gap:4px; min-width:420px">
    <h3 style="margin:0 0 6px" id="bdTitle">Новый набор</h3>
    <label class="f">Имя
      <input id="bdName" pattern="[a-zа-яё0-9][a-zа-яё0-9-]{0,23}" maxlength="24" title="Латиница или кириллица, цифры и дефисы, до 24 символов" required>
    </label>
    <label class="f">Участники по приоритету (первый = основной)
      <div style="display:flex;gap:6px;margin-top:3px">
        <select id="bdAddSel" style="flex:1"></select>
        <button type="button" id="bdAdd">добавить</button>
      </div>
    </label>
    <div id="bdMembers" style="margin-top:2px"></div>
    <div class="f" style="margin-top:10px">Группы MagiTrickle в наборе
      <div id="bdGroups" style="border:1px solid var(--border);border-radius:6px;padding:6px;margin-top:3px"></div>
    </div>
    <div class="btnrow" style="justify-content:end">
      <button value="cancel">Отмена</button>
      <button class="primary" id="bdSave">Сохранить</button>
    </div>
  </form>
</dialog>

<dialog id="dlgIfaceProbe">
  <form method="dialog" style="display:grid; gap:4px; min-width:400px">
    <h3 style="margin:0 0 6px">Проверка интерфейса <span class="muted" id="ipDevice"></span></h3>
    <div class="muted" style="font-size:11px">Проба идет через этот интерфейс. Используется для здоровья в наборах и показывается в панели интерфейсов.</div>
    <label class="f">Тип пробы
      <select id="ipType">
        <option value="http">HTTP-страница 204</option>
        <option value="icmp">Ping IP</option>
      </select>
    </label>
    <label class="f">Цель пробы
      <input id="ipTarget" placeholder="http://www.gstatic.com/generate_204">
    </label>
    <div class="f" id="spRttRow" style="display:flex;align-items:center;gap:10px;flex-wrap:wrap">
      <label class="switch" title="включить порог RTT"><input type="checkbox" id="ipRttOn"><span class="knob"></span></label>
      <span>считать нерабочим при RTT выше</span>
      <input id="ipRtt" type="number" min="1" max="5000" style="width:90px"> мс
    </div>
    <div class="btnrow" style="justify-content:space-between">
      <button type="button" id="ipRemove" class="danger">убрать проверку</button>
      <span style="display:flex;gap:6px">
        <button value="cancel">Отмена</button>
        <button class="primary" id="ipSave">Сохранить</button>
      </span>
    </div>
  </form>
</dialog>

<dialog id="dlgConfirm">
  <form method="dialog" style="display:grid; gap:10px; min-width:380px">
    <h3 style="margin:0" id="cfTitle">Подтверждение</h3>
    <div id="cfText" style="font-size:13px;white-space:pre-line;line-height:1.5"></div>
    <div class="btnrow" style="justify-content:end;margin:0">
      <button value="cancel" id="cfCancel">Отмена</button>
      <button class="primary" id="cfOk">Установить</button>
    </div>
  </form>
</dialog>

<dialog id="dlgLx">
  <form method="dialog" style="display:grid; gap:8px; min-width:440px">
    <h3 style="margin:0" id="lxTitle">Ядро sing-box-lx</h3>
    <div id="lxText" style="font-size:13px;white-space:pre-line;line-height:1.5"></div>
    <label class="f" id="lxFlavorLabel">Профиль сборки
      <select id="lxFlavor">
        <option value="plain">plain - обычный бинарь: быстрый старт, много места (на mips ~90 МБ)</option>
        <option value="upx">upx - сжатый: в ~4 раза меньше на диске, старт дольше и RAM больше (opt-in)</option>
      </select>
    </label>
    <div class="f" id="lxBackupRow" style="display:none;align-items:center;gap:8px">
      <label class="switch" title="сохранить старое ядро рядом перед заменой"><input type="checkbox" id="lxBackup"><span class="knob"></span></label>
      <span id="lxBackupText" style="font-size:12px"></span>
    </div>
    <div class="muted" id="lxNote" style="font-size:11px"></div>
    <div class="f" id="lxProgress" style="display:none;align-items:center;gap:10px;font-size:13px;line-height:1.5">
      <span class="spin"></span>
      <span>Идёт установка: качается релиз (~30-90 МБ), проверяется контрольная сумма, прогоняется тест-запуск. Это занимает до пары минут - не закрывайте панель.</span>
    </div>
    <div class="btnrow" style="justify-content:end;margin:0">
      <button value="cancel" id="lxCancel">Отмена</button>
      <button class="primary" id="lxGo">Установить</button>
    </div>
  </form>
</dialog>

<dialog id="dlgCaptcha" style="max-width:min(92vw,420px)">
  <div style="display:grid; gap:10px; min-width:320px">
    <h3 style="margin:0">Проверка Amnezia Free</h3>
    <div class="muted" style="font-size:12px" id="capHint">Введите цифры с картинки, чтобы продолжить получение бесплатного конфига.</div>
    <div style="display:flex;align-items:center;gap:8px">
      <img id="capImage" alt="капча" style="max-width:100%;border:1px solid var(--border);border-radius:8px;background:#fff;min-height:60px">
      <button type="button" id="capRefresh" title="обновить картинку" style="font-size:16px;line-height:1;padding:6px 8px">&#8635;</button>
    </div>
    <input id="capInput" inputmode="numeric" autocomplete="off" placeholder="_ _ _ _ _" style="font:16px/1.4 ui-monospace,monospace;letter-spacing:4px;text-align:center">
    <div class="btnrow" style="justify-content:end;margin:0">
      <button type="button" id="capCancel">Отмена</button>
      <button type="button" class="primary" id="capSend">Отправить</button>
    </div>
  </div>
</dialog>

<dialog id="dlgResult" style="max-width:min(92vw,660px)">
  <form method="dialog" style="display:grid; gap:10px; min-width:460px">
    <h3 style="margin:0" id="rsTitle">Результат</h3>
    <pre id="rsOut" style="background:var(--bg);border:1px solid var(--border);border-radius:8px;padding:10px;font:11.5px/1.55 ui-monospace,monospace;margin:0;white-space:pre-wrap;word-break:break-word;max-height:55vh;overflow:auto"></pre>
    <div class="btnrow" style="justify-content:end;margin:0">
      <button class="primary" value="ok">Закрыть</button>
    </div>
  </form>
</dialog>

<dialog id="dlgUpdate">
  <div style="display:grid;gap:10px;min-width:380px">
    <h3 style="margin:0">Обновление mawg</h3>
    <div class="btnrow" style="justify-content:flex-start;align-items:center;margin:0">
      <span class="spin" id="updWaitSpin"></span>
      <span id="updWaitText">скачиваем и устанавливаем; панель перезапустится, страница обновится сама</span>
    </div>
    <div class="muted" style="font-size:11px">прошло <span id="updWaitSec">0</span> с; скачивание и установка на роутере могут занять пару минут</div>
    <div class="btnrow" style="justify-content:end;margin:0">
      <button id="updWaitClose" style="display:none">закрыть</button>
    </div>
  </div>
</dialog>

<dialog id="dlgImport">
  <form method="dialog" style="display:grid; gap:6px; min-width:420px; max-width:560px">
    <h3 style="margin:0 0 4px" id="imTitle">Импорт правил</h3>
    <label class="f">Тип для доменов
      <select id="imType">
        <option value="auto">авто (IP и CIDR как подсети, остальное как домен+поддомены)</option>
        <option value="namespace">домен+поддомены</option>
        <option value="domain">домен (без поддоменов)</option>
        <option value="wildcard">маска (* ?)</option>
        <option value="subnet">подсеть</option>
        <option value="regex">регулярка (по одной на строку)</option>
      </select>
    </label>
    <div class="muted" style="font-size:11px">Ссылки всегда очищаются до домена: https://www.ozon.ru/path -> www.ozon.ru, ftp://files.example.com/dir -> files.example.com</div>
    <div class="f" style="display:flex;align-items:center;justify-content:space-between;gap:12px">
      <span style="flex:1">Усекать до второго уровня
        <span class="muted" style="display:block;font-size:11px">www.ozon.ru -> ozon.ru, зоны вроде bbc.co.uk не трогаются</span>
      </span>
      <label class="switch" title="усекать до второго уровня"><input type="checkbox" id="imSecond"><span class="knob"></span></label>
    </div>
    <div class="f" style="display:flex;align-items:center;justify-content:space-between;gap:12px">
      <span style="flex:1">Импортировать включёнными
        <span class="muted" style="display:block;font-size:11px">выключенное правило можно включить позже тумблером</span>
      </span>
      <label class="switch" title="импортировать включёнными"><input type="checkbox" id="imEnable" checked><span class="knob"></span></label>
    </div>
    <label class="f">Список: по одному в строке, пробелы/запятые/точки с запятой тоже разделяют
      <textarea id="imText" rows="10" spellcheck="false" style="font:12px/1.5 ui-monospace,monospace;width:100%;box-sizing:border-box;margin-top:3px"></textarea>
    </label>
    <div class="muted" style="font-size:11px">Дубликаты внутри списка и совпадения с существующими правилами пропускаются. Кириллические домены вставляйте в punycode.</div>
    <div class="btnrow" style="justify-content:end;margin:0">
      <button type="button" id="imCancel">отмена</button>
      <button type="button" class="primary" id="imGo">импортировать</button>
    </div>
  </form>
</dialog>

<dialog id="dlgCopy">
  <form method="dialog" style="display:grid; gap:8px; min-width:420px; max-width:560px">
    <h3 style="margin:0" id="cpTitle">Список правил</h3>
    <textarea id="cpText" rows="12" readonly spellcheck="false" style="font:12px/1.5 ui-monospace,monospace;width:100%;box-sizing:border-box"></textarea>
    <div class="muted" style="font-size:11px">Если не скопировалось само - выделено, нажмите Ctrl+C.</div>
    <div class="btnrow" style="justify-content:end;margin:0">
      <button class="primary" value="close">Закрыть</button>
    </div>
  </form>
</dialog>

<dialog id="dlgAuth">
  <form method="dialog" style="display:grid;gap:10px;min-width:380px">
    <h3 style="margin:0">Вход в панель</h3>
    <label class="f">логин<input id="authLogin" autocomplete="username"></label>
    <label class="f">пароль<div class="pwrap"><input type="password" id="authPass" autocomplete="current-password"><button type="button" class="peye" data-for="authPass" title="показать пароль"></button></div></label>
    <div class="muted" id="authErr" style="display:none;font-size:12px;color:var(--err)"></div>
    <div class="btnrow" style="justify-content:end;margin:0">
      <button class="primary" id="authGo">Войти</button>
    </div>
    <div class="muted" style="font-size:11px">Пароль первого входа выводится в консоль при установке и лежит в first-auth.txt. Сброс из терминала роутера: mawg -reset-auth</div>
  </form>
</dialog>

<dialog id="dlgPass">
  <form method="dialog" style="display:grid;gap:10px;min-width:380px">
    <h3 style="margin:0">Смена пароля</h3>
    <label class="f">старый пароль<div class="pwrap"><input type="password" id="pwOld" autocomplete="current-password"><button type="button" class="peye" data-for="pwOld" title="показать пароль"></button></div></label>
    <label class="f">новый пароль<div class="pwrap"><input type="password" id="pwNew" autocomplete="new-password"><button type="button" class="peye" data-for="pwNew" title="показать пароль"></button></div></label>
    <label class="f">повторите новый<div class="pwrap"><input type="password" id="pwNew2" autocomplete="new-password"><button type="button" class="peye" data-for="pwNew2" title="показать пароль"></button></div></label>
    <div class="muted" style="font-size:11px">Минимум 8 символов. Подсказка с паролем первого входа после смены удаляется.</div>
    <div class="muted" id="pwErr" style="display:none;font-size:12px;color:var(--err)"></div>
    <div class="btnrow" style="justify-content:end;margin:0">
      <button value="cancel">Отмена</button>
      <button class="primary" id="pwSave">Сохранить</button>
    </div>
  </form>
</dialog>

<div id="topbar"></div>
<div class="msg" id="msg"></div>

</template>
