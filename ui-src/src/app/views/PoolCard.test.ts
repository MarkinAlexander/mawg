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
    // включён: Включить нет, остальное есть
    expect(btn(w, 'Включить')).toHaveLength(0);
    expect(btn(w, 'Сменить конфиг')).toHaveLength(1);
    expect(btn(w, 'Проверить')).toHaveLength(1);
    expect(btn(w, 'Выключить')).toHaveLength(1);
    expect(btn(w, 'удалить пул')).toHaveLength(1);
    w.unmount();
  });

  it('выключенный: «Включить» primary, ротации спрятаны, подзаголовок «ротация остановлена»', () => {
    const w = makeCard({ ...nativePool, disabled: true });
    expect(w.find('.uibadge.v-muted').text()).toBe('выключен');
    expect(w.text()).toContain('ротация остановлена');
    expect(btn(w, 'Включить')).toHaveLength(1);
    expect(btn(w, 'Сменить конфиг')).toHaveLength(0);
    expect(btn(w, 'Выключить')).toHaveLength(0);
    w.unmount();
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
    expect(w.find('.cfg').exists()).toBe(false);
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
