import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { DOMWrapper, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createRouter, createWebHashHistory } from 'vue-router'
import InterfacesView from './InterfacesView.vue'
import { ConfirmHost, UiSwitch } from '../../components/ui'
import { useConfirm } from '../../stores/confirm'
import { useIfaces } from '../../stores/ifaces'
import { useStatus } from '../../stores/status'
import { useToast } from '../../stores/toast'
import type { IfaceInfo } from '../../api/types'

const PROBE_HTTP = 'http://www.gstatic.com/generate_204';

function apiStub(routes: Record<string, unknown | ((init?: { method?: string; body?: string }) => unknown)>) {
  return vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    const key = Object.keys(routes).find((k) => url.endsWith(k));
    const v = key ? routes[key] : {};
    const body = typeof v === 'function' ? (v as (i?: typeof init) => unknown)(init) : v;
    return new Response(JSON.stringify(body), { status: 200 });
  });
}

const fixtures: IfaceInfo[] = [
  { device: 'Awg0', slot: 'Awg0', description: 'proton', mode: 'managed', pool: 'proton', linkUp: true, connected: true, handshakeAgo: -1,
    probeOk: true, probeMs: 152, probeAt: Math.floor(Date.now() / 1000) - 42, probeStatus: 'ok (handshake 3s ago, http 152ms)' },
  { device: 'nwg7', description: 'провайдерский WG', mode: 'external', linkUp: true, connected: true, handshakeAgo: 25, probe: { type: 'http', target: PROBE_HTTP, maxRttMs: 300 }, probeStatus: 'проба: ок (45ms)' },
  { device: 'wg11', mode: 'external', linkUp: false, connected: false, handshakeAgo: -1, probe: null, probeStatus: 'проба: не прошла (dial tcp 1.1.1.1: i/o timeout)' },
  { device: 'tun1', description: 'движок', mode: 'hidden', linkUp: true, connected: true, handshakeAgo: -1 },
];

const defaultRoutes = () => ({
  '/ifaces': { interfaces: fixtures },
  '/wanprobe': { enabled: false, probe: null },
});

async function makeView(routes: Record<string, unknown | ((init?: { method?: string; body?: string }) => unknown)> = {}) {
  const f = apiStub({ ...defaultRoutes(), ...routes });
  vi.stubGlobal('fetch', f);
  // deep-link ?pool= (97-док): InterfacesView ведёт роутер сам
  const router = createRouter({
    history: createWebHashHistory(),
    routes: [{ path: '/next/:id', component: { template: '<div />' } }],
  });
  const w = mount(defineComponent(() => () => [h(InterfacesView), h(ConfirmHost)]), { global: { plugins: [router] } });
  await vi.waitFor(() => expect(useIfaces().state.loaded).toBe(true));
  // WAN-форма заполняется асинхронно из GET /wanprobe - ждём её разрешения,
  // иначе fillWan перетирает действия теста задним числом
  await vi.waitFor(() => expect(useIfaces().state.wanLoaded).toBe(true));
  await nextTick();
  return { w, f, router };
}

const ifCard = (w: ReturnType<typeof mount>) => w.findAll('.card').find((c) => c.text().includes('Интерфейсы роутера'))!;
const wanCard = (w: ReturnType<typeof mount>) => w.findAll('.card').find((c) => c.text().includes('Проверка канала провайдера'))!;
const btn = (
  scope: { findAll: (s: string) => DOMWrapper<Element>[] },
  text: string,
) => scope.findAll('button').filter((b) => b.text() === text);

beforeEach(() => {
  vi.unstubAllGlobals();
  const { state } = useIfaces();
  state.loaded = false;
  state.error = '';
  state.list = [];
  state.wan = { enabled: false, probe: null };
  state.wanLoaded = false;
  useIfaces().stop();
  useStatus().stop();
  useStatus().state.data = null;
  useConfirm().cancel();
  useToast().toasts.list.splice(0);
});

describe('InterfacesView - карточка «Интерфейсы роутера»', () => {
  it('компоненты разрешены (не кастом-теги), режимы/бейджи/handshake, скрытые по кнопке', async () => {
    const { w } = await makeView();
    const html = w.html();
    for (const tag of ['<uicard', '<uiswitch', '<uiselect', '<uidialog', '<uiinput', '<uibutton', '<uihelp']) {
      expect(html.includes(tag)).toBe(false);
    }
    expect(w.findComponent(UiSwitch).exists()).toBe(true);
    // глоссарий-проход: [?] в карточках (словарь терминов, 70-док)
    expect(w.findAll('.uihelp').length).toBeGreaterThanOrEqual(5);

    const card = ifCard(w);
    expect(card.find('.ifrow').exists()).toBe(true);
    expect(card.text()).toContain('4 шт, скрыто 1');

    const rowTexts = card.findAll('.ifrow').map((r) => r.text());
    expect(rowTexts).toHaveLength(3); // tun1 скрыт
    expect(rowTexts[0]).toContain('Awg0');
    expect(rowTexts[0]).toContain('пул proton'); // managed - бейдж, без кнопок
    expect(rowTexts[0]).toContain('проба: ок 152мс'); // плашка пробы пула - как у внешних
    // время той же пробы - в tooltip плашки
    const poolBadge = card.findAll('.ifrow')[0].findAll('.uibadge').find((b) => b.text() === 'проба: ок 152мс');
    expect(poolBadge?.attributes('title')).toMatch(/ok \(handshake 3s ago, http 152ms\) · \d+ с назад/);
    expect(rowTexts[0]).not.toContain('скрыть');
    expect(rowTexts[1]).toContain('nwg7');
    expect(rowTexts[1]).toContain('handshake 25с назад');
    expect(rowTexts[2]).toContain('wg11');

    // бейджи up/down: up - зелёный, down - красный (external)
    const badges = card.findAll('.ifrow').map((r) => r.findAll('.uibadge').map((b) => b.classes().join(' ')));
    expect(badges[0].join()).toContain('v-ok');
    expect(badges[2].join()).toContain('v-err');

    await btn(card, 'показать скрытые (1)')[0].trigger('click');
    await nextTick();
    expect(card.text()).toContain('скрыть служебные');
    expect(card.findAll('.ifrow')).toHaveLength(4);
    expect(card.text()).toContain('сделать внешним'); // у hidden
    w.unmount();
  });

  it('бейдж провальной пробы: короткий текст, клик - инфо-модалка с деталью из скобок', async () => {
    const { w } = await makeView();
    const card = ifCard(w);
    const pbadges = card.findAll('.pbadge');
    expect(pbadges).toHaveLength(3); // теперь и managed-строка несёт плашку пробы пула
    expect(pbadges[0].text()).toBe('проба: ок 152мс'); // пул Awg0 - той же пробой
    expect(pbadges[1].text()).toBe('проба: ок (45ms)'); // ок - целиком
    expect(pbadges[2].text()).toBe('проба: не прошла'); // короткий
    expect(card.text()).not.toContain('dial tcp');

    await pbadges[2].trigger('click'); // wg11 - провальная, с деталью в скобках
    await nextTick();
    expect(useConfirm().state.open).toBe(true);
    expect(useConfirm().state.title).toBe('Проверка wg11');
    expect(useConfirm().state.text).toBe('dial tcp 1.1.1.1: i/o timeout');
    useConfirm().ok();
    await nextTick();
    expect(useConfirm().state.open).toBe(false);
    w.unmount();
  });

  it('смена режима: «скрыть» -> POST hidden, «сделать внешним» -> POST external', async () => {
    const { w, f } = await makeView();
    await btn(ifCard(w), 'скрыть')[0].trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/ifaces/nwg7/mode'));
      expect(post).toBeTruthy();
      expect(JSON.parse(post![1]!.body as string)).toEqual({ mode: 'hidden' });
    });

    await btn(ifCard(w), 'показать скрытые (1)')[0].trigger('click');
    await nextTick();
    await btn(ifCard(w), 'сделать внешним')[0].trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/ifaces/tun1/mode'));
      expect(JSON.parse(post![1]!.body as string)).toEqual({ mode: 'external' });
    });
    w.unmount();
  });
});

describe('InterfacesView - диалог пробы интерфейса', () => {
  it('предзаполнен из пробы, смена типа подставляет дефолт, Сохранить -> PUT', async () => {
    const { w, f } = await makeView();
    const gear = ifCard(w).find('button[title="проверка настроена"]');
    expect(gear.exists()).toBe(true);
    expect(gear.text()).toContain('*'); // проба настроена - зелёная звёздочка
    await gear.trigger('click');
    await nextTick();

    const dlg = w.findAll('dialog').find((d) => d.find('h3').text() === 'Проверка интерфейса nwg7')!;
    expect(dlg.text()).toContain('Проверка идёт через этот интерфейс');
    expect(dlg.find('.psel-btn').text()).toBe('HTTP-страница 204');
    const target = dlg.findAll('input').find((i) => i.attributes('type') !== 'number' && i.attributes('type') !== 'checkbox')!;
    expect((target.element as HTMLInputElement).value).toBe(PROBE_HTTP);
    expect((dlg.find('input[type=checkbox]').element as HTMLInputElement).checked).toBe(true);
    const rtt = dlg.find('input[type=number]');
    expect((rtt.element as HTMLInputElement).value).toBe('300');

    // смена http -> icmp: цель была дефолтом http - заменяется дефолтом icmp
    await dlg.find('.psel-btn').trigger('click');
    await nextTick();
    await dlg.findAll('.psel-item').find((o) => o.text().includes('Ping IP'))!.trigger('click');
    await nextTick();
    expect((target.element as HTMLInputElement).value).toBe('1.1.1.1');

    await target.setValue('9.9.9.9');
    await dlg.findAll('button').filter((b) => b.text() === 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/ifaces/nwg7/probe'));
      expect(put).toBeTruthy();
      expect(JSON.parse(put![1]!.body as string)).toEqual({ type: 'icmp', target: '9.9.9.9', maxRttMs: 300 });
    });
    await vi.waitFor(() => expect(useIfaces().state.list[1].mode).toBe('external')); // перечитан
    w.unmount();
  });

  it('пустая цель - тост без PUT; «убрать проверку» - PUT {target:""}', async () => {
    const { w, f } = await makeView();
    await ifCard(w).find('button[title="проверка настроена"]').trigger('click');
    await nextTick();
    const dlg = w.findAll('dialog').find((d) => d.find('h3').text().includes('Проверка интерфейса'))!;
    const target = dlg.findAll('input').find((i) => i.attributes('type') !== 'number' && i.attributes('type') !== 'checkbox')!;
    await target.setValue('  ');

    const puts = () => f.mock.calls.filter((c) => (c[0] as string).endsWith('/probe') && c[1]?.method === 'PUT');
    await dlg.findAll('button').filter((b) => b.text() === 'Сохранить')[0].trigger('click');
    await nextTick();
    expect(useToast().toasts.list.map((t) => t.text)).toContain('Укажите цель пробы');
    expect(puts()).toHaveLength(0);

    await dlg.findAll('button').find((b) => b.text() === 'убрать проверку')!.trigger('click');
    await vi.waitFor(() => {
      expect(puts()).toHaveLength(1);
      expect(JSON.parse(puts()[0][1]!.body as string)).toEqual({ target: '' });
    });
    w.unmount();
  });

  it('без пробы: шестерёнка «настроить проверку», без звёздочки, rtt-поле задизейблено', async () => {
    const { w } = await makeView();
    const gear = ifCard(w).find('button[title="настроить проверку"]');
    expect(gear.exists()).toBe(true);
    expect(gear.text()).not.toContain('*');
    await gear.trigger('click');
    await nextTick();
    const dlg = w.findAll('dialog').find((d) => d.find('h3').text() === 'Проверка интерфейса wg11')!;
    expect((dlg.find('input[type=checkbox]').element as HTMLInputElement).checked).toBe(false);
    expect((dlg.find('input[type=number]').element as HTMLInputElement).disabled).toBe(true);
    expect((dlg.find('input[type=number]').element as HTMLInputElement).value).toBe('300');
    w.unmount();
  });
});

describe('InterfacesView - проверка канала провайдера', () => {
  it('форма заполняется из GET: тип/цель/порог', async () => {
    const { w } = await makeView({
      '/wanprobe': { enabled: true, probe: { type: 'icmp', target: '8.8.8.8', maxRttMs: 500 } },
    });
    const card = wanCard(w);
    expect(card.find('.psel-btn').text()).toBe('Ping IP');
    const target = card.findAll('input').find((i) => i.attributes('type') === 'text')!;
    expect((target.element as HTMLInputElement).value).toBe('8.8.8.8');
    const boxes = card.findAll('input[type=checkbox]');
    expect((boxes[0].element as HTMLInputElement).checked).toBe(true); // включена
    expect((boxes[1].element as HTMLInputElement).checked).toBe(true); // порог RTT
    expect((card.find('input[type=number]').element as HTMLInputElement).value).toBe('500');
    w.unmount();
  });

  it('выключенная проверка: пустая цель, у http добирается дефолт (легаси refreshWAN)', async () => {
    const { w } = await makeView({ '/wanprobe': { enabled: false, probe: null } });
    const card = wanCard(w);
    expect((card.findAll('input[type=checkbox]')[0].element as HTMLInputElement).checked).toBe(false);
    const target = card.findAll('input').find((i) => i.attributes('type') === 'text')!;
    expect((target.element as HTMLInputElement).value).toBe(PROBE_HTTP); // легаси: http добирает дефолт
    expect(target.attributes('placeholder')).toBe(PROBE_HTTP);
    expect((card.find('input[type=number]').element as HTMLInputElement).value).toBe('300');
    w.unmount();
  });

  it('сохранение: включённая - полная проба, выключенная - {target:""}, тосты', async () => {
    const { w, f } = await makeView();
    const card = wanCard(w);
    const puts = () => f.mock.calls.filter((c) => (c[0] as string).endsWith('/wanprobe') && c[1]?.method === 'PUT');

    // GET вернул enabled:false - включаем галку (цель уже дефолтная http)
    await card.findAll('input[type=checkbox]')[0].setValue(true);
    await btn(card, 'сохранить')[0].trigger('click');
    await vi.waitFor(() => expect(puts()).toHaveLength(1));
    expect(JSON.parse(puts()[0][1]!.body as string)).toEqual({
      type: 'http', target: PROBE_HTTP, maxRttMs: 0,
    });
    await vi.waitFor(() =>
      expect(useToast().toasts.list.map((t) => t.text)).toContain('Проверка канала сохранена'));

    // выключаем: PUT {target:""} и тост про выключение
    await card.findAll('input[type=checkbox]')[0].setValue(false);
    await btn(card, 'сохранить')[0].trigger('click');
    await vi.waitFor(() => expect(puts()).toHaveLength(2));
    expect(JSON.parse(puts()[1][1]!.body as string)).toEqual({ target: '' });
    await vi.waitFor(() =>
      expect(useToast().toasts.list.map((t) => t.text)).toContain('Проверка канала выключена'));
    w.unmount();
  });

  it('включено без цели - тост «Укажите цель проверки канала», PUT не уходит (легаси: http пустую добирает дефолтом - нужен icmp)', async () => {
    const { w, f } = await makeView();
    const card = wanCard(w);
    // icmp: смена типа подставит дефолт 1.1.1.1 - очищаем и его
    await card.find('.psel-btn').trigger('click');
    await nextTick();
    await card.findAll('.psel-item').find((o) => o.text().includes('Ping IP'))!.trigger('click');
    await nextTick();
    const target = card.findAll('input').find((i) => i.attributes('type') === 'text')!;
    expect((target.element as HTMLInputElement).value).toBe('1.1.1.1');
    await target.setValue('');
    await card.findAll('input[type=checkbox]')[0].setValue(true);
    await btn(card, 'сохранить')[0].trigger('click');
    await nextTick();
    expect(useToast().toasts.list.map((t) => t.text)).toContain('Укажите цель проверки канала');
    expect(f.mock.calls.filter((c) => (c[0] as string).endsWith('/wanprobe') && c[1]?.method === 'PUT')).toHaveLength(0);
    w.unmount();
  });
});

describe('InterfacesView - deep-link ?pool= (97-док)', () => {
  it('скролл к карточке пула, запрос чистится (F5 не переоткроет)', async () => {
    const scroll = vi.fn();
    (Element.prototype as unknown as { scrollIntoView: unknown }).scrollIntoView = scroll;
    const poolView = {
      name: 'proton', platform: 'openwrt', device: 'nwg0', mode: 'up', activeFile: '',
      activeEndpoint: '', lastResult: '', lastError: '', rotations: 0, disabled: false,
      consecFails: 0, settings: {}, configs: [], premium: { imported: false, countries: [] }, protos: [],
    };
    const { w, router } = await makeView({
      '/status': { version: 'v', platform: 'openwrt', backendName: 'openwrt', authEnabled: false, nativeAWG3: false, mtSources: true, pools: [poolView] },
    });
    // getElementById видит только присоединённое дерево (грабля jsdom):
    // переносим отрисованный DOM в документ
    document.body.appendChild(w.element);
    await router.push({ path: '/next/interfaces', query: { pool: 'proton' } });
    // пулы приходят из /status: перечитываем - стор увидит пул,
    // deep-link заскроллит и почистит запрос
    await useStatus().refresh();
    await vi.waitFor(() => expect(router.currentRoute.value.query.pool).toBeUndefined());
    expect(scroll).toHaveBeenCalled();
    // карточка пула реально в DOM с якорем
    expect(w.find('#card-pool-proton').exists()).toBe(true);
    const host = w.element;
    w.unmount();
    host.remove();
  });
});
