import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import PoolCard from './PoolCard.vue'
import { UiSwitch } from '../../components/ui'
import { useConfirm } from '../../stores/confirm'
import { useStatus } from '../../stores/status'
import { useToast } from '../../stores/toast'
import type { PoolView, Status } from '../../api/types'

function stub(statusPayload: Record<string, unknown> = {}) {
  return vi.fn(async (url: string, init?: { method?: string; body?: unknown }) => {
    if ((url as string).endsWith('/status')) {
      return new Response(JSON.stringify(statusFor(statusPayload)), { status: 200 });
    }
    if (typeof init?.body === 'string') return new Response(init.body, { status: 200 });
    return new Response('{"ok":true}', { status: 200 });
  });
}

const nativePool: PoolView = {
  name: 'proton', platform: 'openwrt', device: 'nwg0', slot: 'nwg0',
  mode: 'up', activeFile: 'a.conf', activeEndpoint: '1.2.3.4:51820',
  lastResult: 'проба: ok (30ms)', lastError: '', rotations: 7, disabled: false, consecFails: 0,
  settings: { fallback: 'direct', checkIntervalSec: 30, failThreshold: 3, cooldownMin: 5, keepalive: 25 },
  configs: [
    { file: 'a.conf', original: 'a.conf', endpoint: '1.2.3.4:51820', enabled: true },
    { file: 'b.conf', original: 'b.conf', endpoint: '5.6.7.8:443', enabled: false, coolForSec: 300 },
    { file: 'c.conf', original: 'c.conf', endpoint: '', enabled: true, conflict: 'адрес занят пулом warp' },
  ],
  premium: { imported: false, countries: [] },
  protos: [],
};

const enginePool: PoolView = {
  ...nativePool,
  name: 'free', device: 'tun1', slot: undefined, activeEndpoint: 'узлов: 4',
  settings: { ...nativePool.settings, engineMode: 'singbox', tunName: 'tun1' },
  configs: [],
  protos: [{ label: 'awg3', kind: 'awg', count: 2 }, { label: 'vless', kind: 'proxy', count: 1 }],
};

const statusFor = (over: Partial<Status> = {}): Status => ({
  version: 'v', platform: 'openwrt', backendName: 'openwrt', authEnabled: false,
  nativeAWG3: false, mtSources: true,
  pools: [], engine: { available: true, running: true, version: '1.11.0-lx', mode: 'own', tuns: [] },
  ...over,
});

function makeCard(pool: PoolView, over: Partial<Status> = {}) {
  useStatus().state.data = statusFor(over);
  return mount(PoolCard, { props: { pool } });
}

const btn = (w: ReturnType<typeof mount>, text: string) =>
  w.findAll('button').filter((b) => b.text() === text);

beforeEach(() => {
  vi.unstubAllGlobals();
  const s = useStatus();
  s.stop();
  s.state.data = null;
  useConfirm().cancel();
  useToast().toasts.list.splice(0);
});

describe('PoolCard - отображение', () => {
  it('нативный включённый: бейджи/подзаголовок/kv, кнопки ротации, конфигов-секция есть', () => {
    const w = makeCard(nativePool);
    expect(w.find('.card').exists()).toBe(true);
    // UiSwitch разрешён компонентом, не <uiswitch>-тег (грабля забытого импорта)
    expect(w.findComponent(UiSwitch).exists()).toBe(true);
    expect(w.html()).not.toContain('<uiswitch');
    expect(w.find('.uibadge.v-ok').text()).toBe('работает');
    expect(w.text()).toContain('nwg0 = nwg0 | фоллбек: напрямую');
    expect(w.text()).toContain('1.2.3.4:51820');
    expect(w.text()).toContain('проба: ok (30ms)');
    expect(w.text()).toContain('7');
    expect(w.find('.cfg').exists()).toBe(true);
    expect(w.find('.cfg summary').text()).toContain('Конфиги (3)');
    // вкл/выкл - свитчем в шапке (красной кнопки «Выключить» больше нет,
    // слово владельца); рядом подпись состояния и бейдж «работает»
    expect(btn(w, 'Выключить')).toHaveLength(0);
    expect(btn(w, 'Включить')).toHaveLength(0);
    const actions = w.find('.actions');
    expect(actions.findComponent(UiSwitch).exists()).toBe(true);
    expect(actions.text()).toContain('включено');
    expect(btn(w, 'Сменить конфиг')).toHaveLength(1);
    expect(btn(w, 'Проверить')).toHaveLength(1);
    expect(btn(w, 'удалить пул')).toHaveLength(1);
    w.unmount();
  });

  it('выключенный: свитч снят с подписью «отключено», ротации спрятаны, «ротация остановлена»', () => {
    const w = makeCard({ ...nativePool, disabled: true });
    expect(w.find('.uibadge.v-muted').text()).toBe('выключен');
    expect(w.text()).toContain('ротация остановлена');
    const actions = w.find('.actions');
    expect(actions.text()).toContain('отключено');
    const sw = actions.findComponent(UiSwitch);
    expect((sw.find('input[type=checkbox]').element as HTMLInputElement).checked).toBe(false);
    expect(btn(w, 'Включить')).toHaveLength(0);
    expect(btn(w, 'Сменить конфиг')).toHaveLength(0);
    expect(btn(w, 'Выключить')).toHaveLength(0);
    expect(btn(w, 'удалить пул')).toHaveLength(1);
    w.unmount();
  });

  it('свитч в шапке: вкл -> POST enable, выкл -> POST disable', async () => {
    const f = stub();
    vi.stubGlobal('fetch', f);
    const w = makeCard(nativePool);
    const sw = w.find('.actions').findComponent(UiSwitch);
    await sw.find('input[type=checkbox]').setValue(false);
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/proton/disable'));
      expect(post).toBeTruthy();
      expect(JSON.parse(post![1]!.body as string)).toEqual({});
    });
    // включение обратно: ждём, пока busy-гвард отпустит свитч (вживую он
    // на это время disabled - гонки нет)
    await vi.waitFor(() =>
      expect(sw.find('input[type=checkbox]').attributes('disabled')).toBeUndefined());
    await sw.find('input[type=checkbox]').setValue(true);
    await vi.waitFor(() =>
      expect(f.mock.calls.some((c) => (c[0] as string).endsWith('/pools/proton/enable'))).toBe(true));
    w.unmount();
  });

  it('сделать активным: POST /activate с file, тост; на активной строке disabled', async () => {
    const f = stub();
    vi.stubGlobal('fetch', f);
    const w = makeCard(nativePool);
    const rows = w.findAll('.cfgrow');
    // активная строка (a.conf) - кнопка ⚡ disabled
    expect(rows[0].findAll('button').find((b) => b.text() === '⚡')!.attributes('disabled')).toBeDefined();
    await rows[1].findAll('button').find((b) => b.text() === '⚡')!.trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/proton/activate'));
      expect(post).toBeTruthy();
      expect(JSON.parse(post![1]!.body as string)).toEqual({ file: 'b.conf' });
    });
    await vi.waitFor(() =>
      expect(useToast().toasts.list.some((t) => t.text.includes('Конфиг активирован: b.conf'))).toBe(true));
    w.unmount();
  });

  it('Проверить: тост «запущена», POST check, ожидание нового lastResult, вердикт probeHuman', { timeout: 15000 }, async () => {
    let calls = 0;
    const f = vi.fn(async (url: string, init?: { method?: string; body?: unknown }) => {
      if ((url as string).endsWith('/status')) {
        calls++;
        // вживую родитель пересоздаёт pool из свежего /status; в тесте
        // prop статичен - мутируем объект (и восстанавливаем после)
        if (calls >= 3) nativePool.lastResult = 'ok (handshake 5s ago, icmp 20ms)';
        return new Response(JSON.stringify(statusFor()), { status: 200 });
      }
      if (typeof init?.body === 'string') return new Response(init.body, { status: 200 });
      return new Response('{"ok":true}', { status: 200 });
    });
    vi.stubGlobal('fetch', f);
    const w = makeCard(nativePool);
    await btn(w, 'Проверить')[0].trigger('click');
    await vi.waitFor(() =>
      expect(useToast().toasts.list.some((t) => t.text.includes('Проверка запущена'))).toBe(true));
    await vi.waitFor(() => {
      expect(f.mock.calls.some((c) => (c[0] as string).endsWith('/pools/proton/check'))).toBe(true);
    });
    await vi.waitFor(() =>
      expect(useToast().toasts.list.some((t) => t.text.startsWith('Проверка: туннель работает'))).toBe(true), { timeout: 8000 });
    // busy-ключ держался до результата: кнопка снова активна
    expect(btn(w, 'Проверить')[0].attributes('disabled')).toBeUndefined();
    w.unmount();
    nativePool.lastResult = 'проба: ok (30ms)'; // восстановить фикстуру
  });

  it('фоллбек: красный бейдж; ошибка пула - красной строкой; отказы подряд в проверке', () => {
    const w = makeCard({ ...nativePool, mode: 'fallback', lastError: 'dial timeout', consecFails: 2 });
    expect(w.find('.uibadge.v-err').text()).toBe('фоллбек');
    expect(w.find('.kv span.err').text()).toBe('dial timeout');
    expect(w.text()).toContain('отказов подряд: 2/3');
    w.unmount();
  });

  it('движковый: tun/ядро в подзаголовке, прото-бейджи, нет конфигов и ротаций, endpoint - узлы', () => {
    const w = makeCard(enginePool, { engine: { available: true, running: false, version: '1.11.0-lx', mode: 'own', tuns: [{ pool: 'free', nodes: 4 }] } });
    expect(w.text()).toContain('tun1 | движок sing-box 1.11.0-lx (не запущен)');
    expect(w.findAll('.protos .uibadge')).toHaveLength(2);
    expect(w.text()).toContain('awg3 ×2');
    // конфигов у движкового нет, но есть секция «Узлы» (60-док)
    expect(w.text()).not.toContain('Конфиги (');
    expect(w.text()).toContain('Узлы (');
    expect(btn(w, 'Сменить конфиг')).toHaveLength(0);
    expect(btn(w, 'Проверить')).toHaveLength(0);
    w.unmount();
  });
});

describe('PoolCard - мутации', () => {
  it('Сменить конфиг -> POST /pools/{name}/rotate {}', async () => {
    const f = stub();
    vi.stubGlobal('fetch', f);
    const w = makeCard(nativePool);
    await btn(w, 'Сменить конфиг')[0].trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/proton/rotate'));
      expect(post).toBeTruthy();
      expect(JSON.parse(post![1]!.body as string)).toEqual({});
    });
    w.unmount();
  });

  it('удалить пул: confirm -> DELETE', async () => {
    const f = stub();
    vi.stubGlobal('fetch', f);
    const card = makeCard(nativePool);
    await btn(card, 'удалить пул')[0].trigger('click');
    await nextTick();
    expect(useConfirm().state.open).toBe(true);
    expect(useConfirm().state.text).toContain('Удалить пул proton?');
    useConfirm().ok();
    await vi.waitFor(() => {
      const del = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/proton') && c[1]?.method === 'DELETE');
      expect(del).toBeTruthy();
    });
    card.unmount();
  });

  it('конфиги: тумблер/стрелки/удаление шлют правильные тела; активная строка зелёная, cooldown-бейдж', async () => {
    const f = stub();
    vi.stubGlobal('fetch', f);
    const w = makeCard(nativePool);
    await w.find('.cfg summary').trigger('click');
    await nextTick();
    const rows = w.findAll('.cfgrow');
    expect(rows).toHaveLength(3);
    expect(rows[0].classes()).toContain('active');
    expect(rows[0].text()).toContain('активен');
    expect(rows[1].classes()).toContain('dimmed');
    expect(rows[1].text()).toContain('cooldown 5м');
    expect(rows[2].find('.uibadge[title*="адрес занят"]').exists()).toBe(true); // conflict в tooltip

    // up первой строки задизейблен, down последней тоже
    const up0 = rows[0].findAll('button').find((b) => b.attributes('title') === 'выше в ротации')!;
    expect(up0.attributes('disabled')).toBeDefined();
    const downBtn = () =>
      w.findAll('.cfgrow')[1].findAll('button').find((b) => b.attributes('title') === 'ниже в ротации')!;
    await downBtn().trigger('click');
    await vi.waitFor(() => {
      const m = f.mock.calls.find((c) => (c[0] as string).endsWith('/configs/b.conf/move'));
      expect(JSON.parse(m![1]!.body as string)).toEqual({ delta: 1 });
    });
    // ждём сброс busy, иначе клик по тумблеру придётся на disabled-состояние
    await vi.waitFor(() => expect(downBtn().attributes('disabled')).toBeUndefined());

    // строки перезапрашиваем - после refresh DOM мог перерисоваться
    await w.findAll('.cfgrow')[0].find('input[type=checkbox]').setValue(false);
    await vi.waitFor(() => {
      const t = f.mock.calls.find((c) => (c[0] as string).endsWith('/configs/a.conf/enable'));
      expect(JSON.parse(t![1]!.body as string)).toEqual({ enabled: false });
    });
    // busy общий на карточку - ждём сброса перед следующим кликом
    await vi.waitFor(() =>
      expect(w.findAll('.cfgrow')[0].find('input[type=checkbox]').attributes('disabled')).toBeUndefined());

    await w.findAll('.cfgrow')[2].findAll('button').find((b) => b.attributes('title') === 'удалить конфиг')!.trigger('click');
    await vi.waitFor(() => {
      expect(f.mock.calls.find((c) => (c[0] as string).endsWith('/configs/c.conf') && c[1]?.method === 'DELETE')).toBeTruthy();
    });
    w.unmount();
  });
});

describe('PoolCard - premium', () => {
  it('без nativeAWG3 и без импорта панель скрыта; импортированный - смена страны', async () => {
    const w = makeCard(nativePool);
    expect(w.find('.prem').exists()).toBe(false);
    w.unmount();

    const f = stub({ nativeAWG3: true });
    vi.stubGlobal('fetch', f);
    const imported: PoolView = {
      ...nativePool,
      premium: { imported: true, country: 'nl', countries: [{ code: 'nl', name: 'Нидерланды' }, { code: 'de', name: 'Германия' }] },
    };
    const w2 = makeCard(imported, { nativeAWG3: true });
    expect(w2.find('.prem').exists()).toBe(true);
    expect(w2.find('.prem .psel-btn').exists()).toBe(true);
    // страна предвыбрана текущей (легаси)
    expect(w2.find('.prem .psel-btn').text()).toContain('Нидерланды');
    // смена сразу POST'ит предвыбранную
    const switchBtn = () => btn(w2, 'Сменить страну')[0];
    await switchBtn().trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/premium/country'));
      expect(JSON.parse(post![1]!.body as string)).toEqual({ country: 'nl' });
    });
    // ждём сброс busy, иначе дропдаун ещё задизейблен
    await vi.waitFor(() => expect(switchBtn().attributes('disabled')).toBeUndefined());
    // выбор другой страны и повторная смена
    await w2.find('.prem .psel-btn').trigger('click');
    await nextTick();
    const de = w2.findAll('.psel-item').find((o) => o.text().includes('Германия'))!;
    await de.trigger('click');
    await nextTick();
    await switchBtn().trigger('click');
    await vi.waitFor(() => {
      const posts = f.mock.calls.filter((c) => (c[0] as string).endsWith('/premium/country'));
      expect(posts).toHaveLength(2);
      expect(JSON.parse(posts[1][1]!.body as string)).toEqual({ country: 'de' });
    });
    w2.unmount();
  });

  it('импорт: пустой ключ - тост; с ключом - POST premium', async () => {
    const f = stub();
    vi.stubGlobal('fetch', f);
    // нативный импорт: платформа keenetic (или openwrtProto amneziawg) + nativeAWG3
    const w = makeCard({ ...nativePool, configs: [], platform: 'keenetic' }, { nativeAWG3: true });
    expect(w.find('.prem').exists()).toBe(true);
    expect(w.text()).toContain('Amnezia Premium');
    await btn(w, 'Импортировать')[0].trigger('click');
    expect(useToast().toasts.list.map((t) => t.text)).toContain('Введите Premium vpn:// ключ');
    expect(f.mock.calls.filter((c) => (c[0] as string).includes('/premium'))).toHaveLength(0);

    const inp = w.find('.prem input[type=password]');
    await inp.setValue('vpn://abc');
    await btn(w, 'Импортировать')[0].trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/proton/premium'));
      expect(JSON.parse(post![1]!.body as string)).toEqual({ key: 'vpn://abc' });
    });
    w.unmount();
  });
});

describe('PoolCard - приватный режим по адресам конфигов (62-док + слово владельца)', () => {
  it('пул есть в /status: активный эндпоинт и cfgend получают .secret, «проверка» и счётчик - нет', () => {
    const w = makeCard(nativePool, { pools: [nativePool] });
    const kvSpans = w.findAll('.kv span');
    const cfgSpan = kvSpans.find((s) => s.text() === '1.2.3.4:51820')!;
    expect(cfgSpan.classes()).toContain('secret');
    const probe = kvSpans.find((s) => s.text().includes('проба: ok'))!;
    expect(probe.classes()).not.toContain('secret');
    const rot = kvSpans.find((s) => s.text() === '7')!;
    expect(rot.classes()).not.toContain('secret');
    const ends = w.findAll('.cfgend');
    expect(ends.map((e) => e.classes().includes('secret'))).toEqual([true, true, false]);
    w.unmount();
  });

  it('статус-стор пуст (пулов в /status нет): разметка без .secret - скрывать нечего', () => {
    const w = makeCard(nativePool);
    expect(w.findAll('.kv .secret').length).toBe(0);
    expect(w.findAll('.cfgend.secret').length).toBe(0);
    w.unmount();
  });

  it('lastError с адресом из конфигов скрывается, прочий текст - виден', () => {
    const errPool = { ...nativePool, lastError: 'dial tcp 1.2.3.4:51820: timeout' };
    const w = makeCard(errPool, { pools: [errPool] });
    expect(w.find('.kv .err.secret').exists()).toBe(true);
    const w2 = makeCard({ ...nativePool, lastError: 'handshake не прошёл' }, { pools: [nativePool] });
    expect(w2.find('.kv .err.secret').exists()).toBe(false);
    w.unmount();
    w2.unmount();
  });
});

describe('PoolCard - узлы движкового пула (60-док)', () => {
  const enginePayload = {
    mode: 'urltest', now: 'узел-2',
    nodes: [
      { tag: 'узел-1', delay: 250, type: 'VLESS', transport: 'grpc' },
      { tag: 'узел-2', delay: 120, type: 'VLESS' },
      { tag: 'узел-3', delay: 0, type: 'TROJAN', transport: 'ws' },
    ],
  };

  function engineStub(payload = enginePayload) {
    return vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
      if ((url as string).endsWith('/engine')) {
        if (init?.method === 'PUT') return new Response('{"ok":true,"now":"узел-1"}', { status: 200 });
        if (init?.method === 'POST') return new Response('{"ok":true,"delays":{"узел-1":111}}', { status: 200 });
        return new Response(JSON.stringify(payload), { status: 200 });
      }
      if ((url as string).endsWith('/status')) {
        return new Response(JSON.stringify(statusFor()), { status: 200 });
      }
      return new Response('{"ok":true}', { status: 200 });
    });
  }

  async function openNodes(payload = enginePayload) {
    const f = engineStub(payload);
    vi.stubGlobal('fetch', f);
    const w = makeCard(enginePool);
    const d = w.find('details').element as HTMLDetailsElement;
    d.open = true;
    await w.find('details').trigger('toggle');
    await vi.waitFor(() => expect(w.text()).toContain('узел-1'));
    return { w, f };
  }

  it('открытие грузит узлы из /engine: пинги, активный бейдж, «Перемерить пинги» в urltest', async () => {
    const { w, f } = await openNodes();
    expect(f.mock.calls.some((c) => (c[0] as string).endsWith('/engine'))).toBe(true);
    expect(w.text()).toContain('250 мс');
    expect(w.text()).toContain('120 мс');
    expect(w.text()).toContain('—'); // не измерен
    const rows = w.findAll('.cfgrow');
    expect(rows[1].classes()).toContain('active'); // now = узел-2
    expect(w.text()).toContain('Перемерить пинги');
    expect(w.text()).not.toContain('выбрать этот узел');
    // тип/транспорт у каждого узла (слово владельца 2026-10-17)
    expect(w.text()).toContain('VLESS/grpc');
    expect(w.text()).toContain('TROJAN/ws');
    // «конфиг» движковому пулу не показываем (нет конфигов)
    expect(w.find('.kv').text()).not.toContain('конфиг');
    w.unmount();
  });

  it('selector-режим: ⚡ у узлов, клик шлёт PUT /engine/select {node}; перемер доступен всем режимам', async () => {
    const { w, f } = await openNodes({ ...enginePayload, mode: 'selector' });
    expect(w.text()).toContain('Перемерить пинги');
    const pick = w.findAll('.cfgctl button').filter((b) => b.text().includes('⚡'));
    expect(pick.length).toBe(3);
    expect((pick[1].element as HTMLButtonElement).disabled).toBe(true); // now = узел-2
    await pick[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/engine/select'));
      expect(JSON.parse(put![1]!.body as string)).toEqual({ node: 'узел-1' });
    });
    w.unmount();
  });

  it('тумблер узла: POST /engine/toggle, исключённый узел приглушён с бейджем', async () => {
    const payload = {
      ...enginePayload,
      nodes: [...enginePayload.nodes, { tag: 'узел-4', delay: 0, type: 'VLESS', transport: 'ws', off: true }],
    };
    const { w, f } = await openNodes(payload);
    const rows = w.findAll('.cfgrow');
    expect(rows[3].classes()).toContain('dimmed');
    expect(w.text()).toContain('исключён');
    // включение выключенного -> POST toggle
    const sw = w.findAllComponents(UiSwitch).filter((x) => x.props('modelValue') === false);
    expect(sw.length).toBe(1);
    sw[0].vm.$emit('update:model-value', true);
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/engine/toggle'));
      expect(JSON.parse(post![1]!.body as string)).toEqual({ node: 'узел-4' });
    });
    w.unmount();
  });

  it('перемер: кнопка дергает POST /engine/retest и перечитывает список', async () => {
    const { w, f } = await openNodes();
    await w.findAll('button').filter((b) => b.text() === 'Перемерить пинги')[0].trigger('click');
    await vi.waitFor(() => {
      expect(f.mock.calls.some((c) => (c[0] as string).endsWith('/engine/retest'))).toBe(true);
    });
    w.unmount();
  });

  it('движок не отвечает - честная ошибка в секции, узлов нет', async () => {
    const f = vi.fn(async (url: string) => {
      if ((url as string).endsWith('/engine')) {
        return new Response(JSON.stringify({ error: 'движок не отвечает: connection refused' }), { status: 502 });
      }
      if ((url as string).endsWith('/status')) {
        return new Response(JSON.stringify(statusFor()), { status: 200 });
      }
      return new Response('{"ok":true}', { status: 200 });
    });
    vi.stubGlobal('fetch', f);
    const w = makeCard(enginePool);
    const d = w.find('details').element as HTMLDetailsElement;
    d.open = true;
    await w.find('details').trigger('toggle');
    await vi.waitFor(() => expect(w.text()).toContain('движок не отвечает'));
    w.unmount();
  });
});

describe('PoolCard - генератор WARP (77-док)', () => {
  const warpPool: PoolView = {
    ...nativePool,
    name: 'warp',
    activeEndpoint: '162.159.192.7:2408',
    settings: { ...nativePool.settings, warp: { mode: 'random' } },
    configs: [{ file: 'w1.conf', original: 'w1', endpoint: '162.159.192.7:2408', enabled: true }],
  };

  it('у варп-пула есть кнопка «Генерация нового конфига», у обычного нет', () => {
    const w = makeCard(warpPool);
    expect(btn(w, 'Генерация нового конфига').length).toBe(1);
    w.unmount();
    const w2 = makeCard(nativePool);
    expect(btn(w2, 'Генерация нового конфига').length).toBe(0);
    w2.unmount();
  });

  it('чужой варп-пул (конфиг в сетях Cloudflare, без меты) тоже узнаётся', () => {
    const w = makeCard({
      ...warpPool,
      settings: { ...nativePool.settings },
    });
    expect(btn(w, 'Генерация нового конфига').length).toBe(1);
    w.unmount();
  });

  it('клик шлёт POST /pools/{name}/warp и тостит вердикт пробы', async () => {
    const captured: { url?: string; body?: string } = {};
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: { method?: string; body?: unknown }) => {
      const u = url as string;
      if (u.endsWith('/status')) {
        return new Response(JSON.stringify(statusFor()), { status: 200 });
      }
      if (u.endsWith('/pools/warp/warp')) {
        captured.url = u;
        captured.body = typeof init?.body === 'string' ? init.body : '';
        return new Response(JSON.stringify({
          pool: 'warp', added: 1,
          results: [{ file: 'w2.conf', endpoint: '8.39.204.2:934', ok: true, detail: 'ok' }],
        }), { status: 200 });
      }
      return new Response('{"ok":true}', { status: 200 });
    }));
    const w = makeCard(warpPool);
    await btn(w, 'Генерация нового конфига')[0].trigger('click');
    await vi.waitFor(() => expect(captured.url).toBeTruthy());
    expect(captured.body).toBe('{}');
    await vi.waitFor(() =>
      expect(useToast().toasts.list.some((t) => t.text.includes('проба прошла'))).toBe(true));
    w.unmount();
  });
});

describe('PoolCard - скачивание по уровню конфига (антиобфускация)', () => {
  const levelsPool: PoolView = {
    ...nativePool,
    name: 'mix',
    configs: [
      { file: 'a.conf', endpoint: '1.2.3.4:51820', enabled: true, awg: 'plain' },
      { file: 'b.conf', endpoint: '5.6.7.8:51820', enabled: true, awg: 'awg2' },
      { file: 'c.conf', endpoint: '9.9.9.9:51820', enabled: true, awg: 'awg3' },
    ],
  };

  it('plain -> только AWG, awg2 -> только WG (снять поля), awg3 -> ничего', () => {
    const w = makeCard(levelsPool);
    const html = w.html();
    expect(html.match(/variant=awg/g)?.length).toBe(1);
    expect(html).toContain('configs/a.conf?variant=awg');
    expect(html.match(/variant=plain/g)?.length).toBe(1);
    expect(html).toContain('configs/b.conf?variant=plain');
    expect(html).not.toContain('c.conf?variant');
    w.unmount();
  });
});
