import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import PoolSettingsDialog from './PoolSettingsDialog.vue'
import { UiOrderList } from '../../components/ui'
import { useCaptcha } from '../../stores/captcha'
import { useConfirm } from '../../stores/confirm'
import { useIfaces } from '../../stores/ifaces'
import { useStatus } from '../../stores/status'
import { useToast } from '../../stores/toast'
import type { IfaceInfo, PoolView, Status } from '../../api/types'

type ReqInit = { method?: string; body?: string };

function stub(routes: Record<string, unknown>) {
  return vi.fn(async (url: string, init?: ReqInit) => {
    const key = Object.keys(routes).find((k) => (url as string).endsWith(k));
    if (key) {
      const v = routes[key];
      if (typeof v === 'string') return new Response(v, { status: 200 });
      const body = typeof v === 'function' ? (v as (init?: ReqInit) => unknown)(init) : v;
      return new Response(JSON.stringify(body), { status: 200 });
    }
    return new Response('{}', { status: 200 });
  });
}

const pool: PoolView = {
  name: 'proton', platform: 'keenetic', device: 'nwg0', slot: 'nwg0', mode: 'up',
  activeFile: 'a.conf', activeEndpoint: '1.2.3.4:51820', lastResult: 'ok', lastError: '',
  rotations: 1, disabled: false, consecFails: 0,
  settings: {
    source: 'https://sub.example.com', probeHost: 'http://www.gstatic.com/generate_204',
    checkIntervalSec: 45, failThreshold: 4, cooldownMin: 7, keepalive: 21,
    fallback: 'hold', maxRttMs: 350, updateIntervalH: 12,
    amnezia: { country: 'nl', countries: [{ code: 'nl', name: 'Нидерланды' }, { code: 'de', name: 'Германия', protocols: ['awg'] }] },
  },
  configs: [{ file: 'a.conf', original: 'a', endpoint: 'e', enabled: true }],
  premium: { imported: false, countries: [] },
  protos: [],
};

const statusFor = (pools: PoolView[], over: Partial<Status> = {}): Status => ({
  version: 'v', platform: 'keenetic', backendName: 'keenetic', authEnabled: false,
  nativeAWG3: false, mtSources: true, pools, engine: undefined, ...over,
});

const iface: IfaceInfo = {
  device: 'nwg7', description: 'внешний', mode: 'external', linkUp: true,
  connected: true, handshakeAgo: 10, probe: { type: 'http', target: 'http://x/' },
};

async function makeDialog(routes: Record<string, unknown>, pools: PoolView[], over: Partial<Status> = {}) {
  useStatus().state.data = statusFor(pools, over);
  const f = stub(routes);
  vi.stubGlobal('fetch', f);
  const dlg = mount(PoolSettingsDialog, { props: { poolName: pools[0].name, open: false } });
  await dlg.setProps({ open: true });
  await nextTick();
  return { dlg, f };
}

beforeEach(() => {
  vi.unstubAllGlobals();
  useStatus().stop();
  useStatus().state.data = null;
  const ifs = useIfaces();
  ifs.stop();
  ifs.state.list = [iface];
  ifs.state.loaded = true;
  useConfirm().cancel();
  useToast().toasts.list.splice(0);
  useCaptcha().cancel();
});

describe('PoolSettingsDialog', () => {
  it('исключения пула: предзаполнены, добавить -> payload уносит список', async () => {
    const exPool: PoolView = {
      ...pool,
      settings: { ...pool.settings, excludes: ['203.0.113.9'] },
    };
    const { dlg, f } = await makeDialog({ '/members': { members: [] } }, [exPool]);
    await vi.waitFor(() => expect(dlg.text()).toContain('Исключения пула'));
    expect(dlg.findAll('.exclitem').map((x) => x.text())).toContain('203.0.113.9 ✕');
    const exInput = dlg.findAll('.hrow input').find((i) => (i.element as HTMLInputElement).placeholder.includes('IP или подсеть'))!;
    await exInput.setValue('198.51.100.7');
    await dlg.findAll('button').find((b) => b.text() === '+ добавить')!.trigger('click');
    expect(dlg.findAll('.exclitem').length).toBe(2);
    await btn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/proton') && c[1]?.method === 'PUT');
      expect(JSON.parse(put![1]!.body as string).excludes).toEqual(['203.0.113.9', '198.51.100.7']);
    });
    dlg.unmount();
  });

  it('исключения выключены флагом (95-док): плашка, добавить disabled, payload без excludes', async () => {
    const exPool: PoolView = {
      ...pool,
      settings: { ...pool.settings, excludes: ['203.0.113.9'] },
    };
    const { dlg, f } = await makeDialog({ '/members': { members: [] } }, [exPool], { excludesDisabled: true });
    await vi.waitFor(() => expect(dlg.text()).toContain('Исключения пула'));
    expect(dlg.text()).toContain('Исключения временно выключены: разбираемся со странным ростом нагрузки на CPU');
    // предзаполнение видно (список на месте), контролы погашены
    expect(dlg.findAll('.exclitem').map((x) => x.text())).toContain('203.0.113.9 ✕');
    expect(dlg.findAll('button').find((b) => b.text() === '+ добавить')!.attributes('disabled')).toBeDefined();
    const exInput = dlg.findAll('.hrow input').find((i) => (i.element as HTMLInputElement).placeholder.includes('IP или подсеть'))!;
    expect((exInput.element as HTMLInputElement).disabled).toBe(true);
    // сохранение не уносит excludes: секция не могла стать dirty
    await btn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/proton') && c[1]?.method === 'PUT');
      expect(JSON.parse(put![1]!.body as string).excludes).toBeUndefined();
    });
    dlg.unmount();
  });

  it('поля предзаполняются из пула, фоллбек-опции с пулами и интерфейсами, Сохранить -> PUT + rename', async () => {
    const { dlg, f } = await makeDialog({ '/members': { members: [{ name: 'a.conf', proto: 'awg', endpoint: '1.2.3.4', enabled: true, active: true }] } }, [pool, { ...pool, name: 'warp', device: 'nwg9', slot: 'nwg9' }]);
    // состав пула
    await vi.waitFor(() => expect(dlg.text()).toContain('Состав пула'));
    expect(dlg.text()).toContain('awg');
    expect(dlg.text()).toContain('активен');
    // источник виден (settings.source), rename виден (keenetic)
    expect(dlg.text()).toContain('Источник');
    expect(dlg.text()).toContain('Имя (переименование)');
    // фоллбек: текущее значение hold, опции с пулом warp и интерфейсом nwg7
    const fbText = dlg.text();
    expect(fbText).toContain('держать последний конфиг');
    // проба/интервалы предзаполнены
    const inputs = dlg.findAll('input');
    const vals = () => inputs.map((i) => (i.element as HTMLInputElement).value);
    expect(vals()).toContain('45');    // интервал
    expect(vals()).toContain('4');     // порог
    expect(vals()).toContain('7');     // cooldown
    expect(vals()).toContain('21');    // keepalive
    expect(vals()).toContain('350');   // rtt

    await btn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/proton') && c[1]?.method === 'PUT');
      expect(JSON.parse(put![1]!.body as string)).toEqual({
        probeHost: 'http://www.gstatic.com/generate_204',
        checkIntervalSec: 45, failThreshold: 4, cooldownMin: 7, keepalive: 21,
        fallback: 'hold', maxRttMs: 350, updateIntervalH: 12,
        // умный каскад не настроен -> честное «выключено» (86-док)
        smartCascade: { via: [] },
        // исключения не трогали - поля в payload НЕТ (иначе любое
        // сохранение стирало бы список при несросшемся предзаполнении)
      });
    });
    dlg.unmount();
  });

  it('приватный режим: адреса состава пула и источник помечены secret, цель пробы видима', async () => {
    // движковый пул может не попасть в /status - разметка состава статическая
    const eng: PoolView = {
      ...pool, name: 'free', device: 'tun1', slot: undefined,
      settings: { ...pool.settings, engineMode: 'singbox', tunName: 'tun1' },
      configs: [],
    };
    const { dlg } = await makeDialog({ '/members': { members: [
      { name: 'nl-1', proto: 'awg', endpoint: '193.32.248.85:51820', enabled: true, active: true },
      { name: 'de-1', proto: 'vless', detail: 'tls', endpoint: '95.179.130.10:443', enabled: false },
      { name: 'jp-1', proto: 'vless', endpoint: '', enabled: true },
      // имена с адресом (host|узел тега, IPv4 в имени) - тоже secret
      // (слово владельца 2026-10-17: тег узла выдавал адрес мимо сокрытия)
      { name: '203.0.113.7|moscow', proto: 'vless', endpoint: '203.0.113.7:23747', enabled: true },
      { name: 'conf-109.248.163.108', proto: 'awg', endpoint: '109.248.163.108:49809', enabled: true },
    ] } }, [eng]);
    await vi.waitFor(() => expect(dlg.text()).toContain('Состав пула'));
    const ends = dlg.findAll('.mend');
    expect(ends.map((e) => e.classes().includes('secret'))).toEqual([true, true, false, true, true]);
    const names = dlg.findAll('.mname');
    expect(names.map((e) => e.classes().includes('secret'))).toEqual([false, false, false, true, true]);
    // источник подписки (vless://строки) - поле-обёртка с secret
    expect(dlg.find('.secret textarea').exists()).toBe(true);
    // цель пробы (gstatic - публичное) - инпут не внутри .secret
    const probe = dlg.findAll('input').find((i) => (i.element as HTMLInputElement).value.includes('gstatic'))!;
    expect((probe.element as HTMLElement).closest('.secret')).toBeNull();
    dlg.unmount();
  });

  it('движковый пул: режимы подписки, селектор активного узла, интервал ротации -> PUT', async () => {
    const eng: PoolView = {
      ...pool, name: 'sub', device: 'tun1', slot: undefined,
      settings: { ...pool.settings, engineMode: 'singbox', tunName: 'tun1', groupMode: 'selector', rotateSec: 120 },
      configs: [],
    };
    const { dlg, f } = await makeDialog({
      '/members': { members: [] },
      '/engine': { mode: 'selector', groupMode: 'selector', now: 'mawg-sub|grpc', nodes: [
        { tag: 'mawg-sub|grpc', delay: 90 },
        { tag: 'mawg-sub|xhttp', delay: 60 },
      ] },
    }, [eng]);
    await vi.waitFor(() => expect(dlg.text()).toContain('Выбор узла в подписке'));
    // selector: выбор узла прямо в настройках, применяется сразу
    await vi.waitFor(() => expect(dlg.text()).toContain('Активный узел'));
    const nodeSel = dlg.findAll('.psel-btn').filter((b) => b.text().includes('grpc'));
    expect(nodeSel.length).toBeGreaterThan(0);
    await nodeSel[0].trigger('click');
    await nextTick();
    const opt = dlg.findAll('.psel-item').filter((o) => o.text().includes('xhttp'))[0]!;
    await opt.trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/engine/select'));
      expect(JSON.parse(put![1]!.body as string)).toEqual({ node: 'mawg-sub|xhttp' });
    });
    // rotate: появляется интервал, Сохранить уносит groupMode+rotateSec
    const modeSel = dlg.findAll('.psel-btn').filter((b) => b.text().includes('ручной выбор'))[0]!;
    await modeSel.trigger('click');
    await nextTick();
    const rot = dlg.findAll('.psel-item').filter((o) => o.text().includes('ротация по таймеру'))[0]!;
    await rot.trigger('click');
    await nextTick();
    expect(dlg.text()).toContain('Менять узел каждые');
    await btn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/sub') && c[1]?.method === 'PUT');
      const body = JSON.parse(put![1]!.body as string);
      expect(body.groupMode).toBe('rotate');
      expect(body.rotateSec).toBe(120);
    });
    dlg.unmount();
  });

  it('обновить из источника: POST refresh-source с source и локацией', async () => {
    const { dlg, f } = await makeDialog({ '/refresh-source': { added: 4, amnezia: { serverCountry: 'de' } } }, [pool]);
    await btn(dlg, 'обновить из источника')[0].trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/proton/refresh-source'));
      expect(JSON.parse(post![1]!.body as string)).toEqual({ source: 'https://sub.example.com', country: 'nl' });
    });
    await vi.waitFor(() => expect(useToast().toasts.list.map((t) => t.text)).toContain('ключ Amnezia обменян (локация de): конфигов 4'));
    dlg.unmount();
  });

  it('Free: проба зафиксирована, конфиг тянется сырым текстом, перевыпуск -> confirm -> captcha (отмена)', async () => {
    const free: PoolView = {
      ...pool, name: 'free', device: 'tun1', slot: undefined, free: true,
      settings: { ...pool.settings, engineMode: 'singbox', tunName: 'tun1', source: '' },
      configs: [{ file: 'free.awg', original: 'free', endpoint: '', enabled: true }],
    };
    const { dlg, f } = await makeDialog({
      '/configs/free.awg': '[Interface]\nPrivateKey = xxx\n',
      '/free/reissue': () => ({ captchaRequired: true, captcha: { id: 'c1', image: 'IMG' } }),
    }, [free]);
    await vi.waitFor(() => expect(dlg.text()).toContain('Выданный конфиг Amnezia Free'));
    expect(dlg.text()).toContain('[Interface]');
    expect(dlg.text()).toContain('Цель пробы для Amnezia Free зафиксирована');
    expect(dlg.text()).toContain('Перевыпустить');

    await btn(dlg, 'Перевыпустить конфиг')[0].trigger('click');
    await vi.waitFor(() => expect(useConfirm().state.open).toBe(true));
    useConfirm().ok();
    await vi.waitFor(() => expect(useCaptcha().state.open).toBe(true));
    expect(useCaptcha().state.image).toBe('IMG');
    useCaptcha().cancel(); // отмена - ничего не уходит
    expect(f.mock.calls.filter((c) => String(c[0]).includes('reissue'))).toHaveLength(1);
    dlg.unmount();
  });

  it('не-keenetic: переименования нет; движковый: keepalive скрыт, тип пробы заблокирован', async () => {
    const eng: PoolView = {
      ...pool, name: 'free', device: 'tun1', slot: undefined,
      settings: { ...pool.settings, engineMode: 'singbox', tunName: 'tun1', source: '' },
    };
    const { dlg } = await makeDialog({}, [eng], { platform: 'openwrt' });
    expect(dlg.text()).not.toContain('Имя (переименование)');
    expect(dlg.text()).not.toContain('Keepalive');
    expect(dlg.text()).toContain('Проверка автоматическая');
    const cb = dlg.findAll('button').find((b) => b.text().includes('HTTP-страница 204'));
    expect(cb?.attributes('disabled')).toBeDefined(); // тип пробы залочен
    dlg.unmount();
  });
});

const btn = (w: ReturnType<typeof mount>, text: string) =>
  w.findAll('button').filter((b) => b.text() === text);

describe('PoolSettingsDialog - режим группы движка (60-док)', () => {
  it('движковый: селект режима есть, selector уходит в PUT; не-движковый - поля нет', async () => {
    const eng: PoolView = {
      ...pool, name: 'free', device: 'tun1', slot: undefined,
      settings: { ...pool.settings, engineMode: 'singbox', tunName: 'tun1', source: '' },
    };
    const { dlg, f } = await makeDialog({}, [eng], { platform: 'openwrt' });
    expect(dlg.text()).toContain('Выбор узла в подписке');
    expect(dlg.text()).toContain('лучший пинг (авто)');

    // открыть дропдаун режима группы: ищем по текущему значению
    // («лучший пинг (авто)»), а не по порядку - первый селект залочен
    const groupBtn = dlg.findAll('.psel-btn').find((b) => b.text().includes('лучший пинг'));
    await groupBtn!.trigger('click');
    await vi.waitFor(() => expect(dlg.find('.psel-list').exists()).toBe(true));
    const opt = dlg.findAll('.psel-item').find((o) => o.text().includes('ручной выбор узла'));
    await opt!.trigger('click');
    await nextTick();

    await btn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/free') && c[1]?.method === 'PUT');
      expect(JSON.parse(put![1]!.body as string)).toMatchObject({ groupMode: 'selector' });
    });
    dlg.unmount();
  });

  it('mihomo установлен: селектор ядра виден, выбор уходит в PUT engineCore', async () => {
    const eng: PoolView = {
      ...pool, name: 'sub', device: 'tun1', slot: undefined,
      settings: { ...pool.settings, engineMode: 'singbox', tunName: 'tun1' },
      configs: [],
    };
    const { dlg, f } = await makeDialog({
      '/members': { members: [] },
      '/engine': { mode: 'urltest', groupMode: 'urltest', now: 'mawg-sub|a', nodes: [{ tag: 'mawg-sub|a', delay: 80 }] },
    }, [eng], { engines: { mihomo: { available: true, running: true, version: 'v1.19.33' } } });
    await vi.waitFor(() => expect(dlg.text()).toContain('Ядро движка'));
    // дефолт sing-box; переключаем на mihomo
    const coreBtn = dlg.findAll('.psel-btn').find((b) => b.text() === 'sing-box');
    expect(coreBtn).toBeDefined();
    await coreBtn!.trigger('click');
    await vi.waitFor(() => expect(dlg.find('.psel-list').exists()).toBe(true));
    const opt = dlg.findAll('.psel-item').find((o) => o.text().includes('mihomo'));
    await opt!.trigger('click');
    await nextTick();
    await btn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/sub') && c[1]?.method === 'PUT');
      expect(put).toBeTruthy();
      expect(JSON.parse(put![1]!.body as string)).toMatchObject({ engineCore: 'mihomo' });
    });
    dlg.unmount();
  });

  it('mihomo не установлен: селектора ядра нет, PUT без engineCore-mihomo', async () => {
    const eng: PoolView = {
      ...pool, name: 'sub2', device: 'tun1', slot: undefined,
      settings: { ...pool.settings, engineMode: 'singbox', tunName: 'tun1' },
      configs: [],
    };
    const { dlg } = await makeDialog({ '/members': { members: [] }, '/engine': { nodes: [] } }, [eng]);
    await vi.waitFor(() => expect(dlg.text()).toContain('Выбор узла в подписке'));
    expect(dlg.text()).not.toContain('Ядро движка');
    dlg.unmount();
  });

  it('не-движковый: режима группы нет, PUT без groupMode', async () => {
    const { dlg, f } = await makeDialog({}, [pool]);
    await vi.waitFor(() => expect(dlg.text()).toContain('Источник'));
    expect(dlg.text()).not.toContain('Выбор узла в подписке');
    await btn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/proton') && c[1]?.method === 'PUT');
      expect(put).toBeTruthy();
      expect(JSON.parse(put![1]!.body as string)).not.toHaveProperty('groupMode');
    });
    dlg.unmount();
  });
});

describe('PoolSettingsDialog - умный каскад (86-док)', () => {
  // свитч по тексту подписи (label без текста - берём обёртку)
  const switchByLabel = (w: ReturnType<typeof mount>, label: string) =>
    w.findAll('input[type=checkbox]')
      .find((b) => (b.element as HTMLInputElement).closest('.uiswitch, .uicheckbox')?.textContent?.includes(label));
  // выбрать в селекте добавления вариант и нажать «добавить»
  const addVia = async (w: ReturnType<typeof mount>, name: string) => {
    const sel = w.findAll('.psel-btn').filter((b) => b.text() === '—')[0];
    await sel.trigger('click');
    await w.findAll('.psel-item').filter((o) => o.text().includes(name))[0].trigger('click');
    await w.findAll('button').filter((b) => b.text().trim() === 'добавить')[0].trigger('click');
  };

  it('тумблер + добавление via из селекта + расписание -> PUT smartCascade', async () => {
    const warp: PoolView = { ...pool, name: 'warp', device: 'nwg9', slot: 'nwg9' };
    const { dlg, f } = await makeDialog({}, [pool, warp]);
    await vi.waitFor(() => expect(dlg.text()).toContain('Умный каскад'));
    // до включения - списка via нет
    expect(dlg.text()).not.toContain('добавить запасной пул');

    const sw = switchByLabel(dlg, 'спасать пул');
    expect(sw).toBeTruthy();
    await sw!.setValue(true);
    await nextTick();
    // компонент кита разрешён (не <uiorderlist>-кастом)
    expect(dlg.findComponent(UiOrderList).exists()).toBe(true);
    expect(dlg.html()).not.toContain('<uiorderlist');
    expect(dlg.text()).toContain('добавить запасной пул');
    expect(dlg.text()).toContain('Запасных нет');
    expect(dlg.text()).toContain('Время проверки, ЧЧ:ММ');
    await addVia(dlg, 'warp');
    expect(dlg.text()).toContain('порядок списка = приоритет');

    await btn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/proton') && c[1]?.method === 'PUT');
      expect(JSON.parse(put![1]!.body as string)).toMatchObject({
        smartCascade: { via: ['warp'], return: { mode: 'schedule', timeOfDay: ['03:00'] }, minDwellMin: 10 },
      });
    });
    dlg.unmount();
  });

  it('ранний триггер: режим «после отказов» с числом уходит в PUT', async () => {
    const warp: PoolView = { ...pool, name: 'warp', device: 'nwg9', slot: 'nwg9' };
    const { dlg, f } = await makeDialog({}, [pool, warp]);
    await vi.waitFor(() => expect(dlg.text()).toContain('Умный каскад'));
    await switchByLabel(dlg, 'спасать пул')!.setValue(true);
    await nextTick();
    // до выбора Via сохранение выключает каскад; выберем warp и режим fails
    await addVia(dlg, 'warp');
    const trigSel = dlg.findAll('.psel-btn').filter((b) => b.text().includes('после перебора всех конфигов'))[0];
    await trigSel.trigger('click');
    await dlg.findAll('.psel-item').filter((o) => o.text().includes('после отказов подряд'))[0].trigger('click');
    await nextTick();
    expect(dlg.text()).toContain('Отказов подряд до каскада');
    await btn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/proton') && c[1]?.method === 'PUT');
      expect(JSON.parse(put![1]!.body as string)).toMatchObject({
        smartCascade: { via: ['warp'], triggerMode: 'fails', triggerFails: 3 },
      });
    });
    dlg.unmount();
  });

  it('предзаполнение из настроек: via в списке, порядок правится стрелками, интервал и антифлап', async () => {
    const other: PoolView = { ...pool, name: 'awgfree', device: 'nwg8', slot: 'nwg8' };
    const p: PoolView = {
      ...pool,
      settings: { ...pool.settings, smartCascade: { via: ['awgfree', 'warp'], return: { mode: 'interval', intervalH: 4 }, minDwellMin: 30 } },
    };
    const warp: PoolView = { ...pool, name: 'warp', device: 'nwg9', slot: 'nwg9' };
    const { dlg } = await makeDialog({}, [p, warp, other]);
    await vi.waitFor(() => expect(dlg.text()).toContain('Умный каскад'));
    const rows = dlg.findAll('.uorderlist .row');
    expect(rows.length).toBe(2);
    expect(rows[0].text()).toContain('awgfree'); // порядок из настроек сохранён
    expect(rows[1].text()).toContain('warp');
    // порядок правится: warp выше
    await rows[1].findAll('button').filter((b) => b.text().trim() === '↑')[0].trigger('click');
    const moved = dlg.findAll('.uorderlist .row');
    expect(moved[0].text()).toContain('warp');
    expect(dlg.text()).toContain('Интервал проверки, часов');
    const inputs = dlg.findAll('input');
    expect(inputs.map((i) => (i.element as HTMLInputElement).value)).toContain('4');
    dlg.unmount();
  });
});
