import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PoolNodesSection from './PoolNodesSection.vue'
import { useStatus } from '../../stores/status'
import { useToast } from '../../stores/toast'
import type { PoolView, Status } from '../../api/types'

// дровер «Узлы» (60-док) с управлением режима/порядка (118 TASK 0,
// слово владельца 14.11): управление должно быть видно ПРЯМО в дровере,
// стрелки порядка - во всех режимах, включая дефолтный urltest
const enginePool: PoolView = {
  name: 'free', platform: 'openwrt', device: 'tun1', slot: undefined,
  mode: 'up', activeFile: '',
  activeEndpoint: 'узлов: 3', lastResult: '', lastError: '', rotations: 0,
  disabled: false, consecFails: 0,
  settings: { fallback: 'direct', checkIntervalSec: 30, failThreshold: 3, cooldownMin: 5, keepalive: 25, engineMode: 'singbox', tunName: 'tun1' },
  configs: [], premium: { imported: false, countries: [] }, protos: [],
};

// name - ключ ручного порядка (nodeOrder), тег - то, что в clash_api
const nodes = [
  { tag: 'узел-1', name: 'n1', delay: 250, type: 'VLESS', transport: 'grpc' },
  { tag: 'узел-2', name: 'n2', delay: 120, type: 'VLESS' },
  { tag: 'узел-3', name: 'n3', delay: 0, type: 'TROJAN', transport: 'ws' },
];
const payloadUrltest = { mode: 'urltest', groupMode: 'urltest', now: 'узел-2', nodes };

const statusFor = (): Status => ({
  version: 'v', platform: 'openwrt', backendName: 'openwrt', authEnabled: false,
  nativeAWG3: false, mtSources: true,
  pools: [], engine: { available: true, running: true, version: '1.11.0-lx', mode: 'own', tuns: [] },
});

// движковый бэкенд-стаб: groupMode в payload мутирует от PUT /pools/free -
// перечитывание /engine после смены режима видит новый режим (как вживую)
function engineStub(payload: Record<string, unknown>) {
  const state = JSON.parse(JSON.stringify(payload));
  const puts: { url: string; body: Record<string, unknown> }[] = [];
  const f = vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    const u = url as string;
    if (u.endsWith('/status')) return new Response(JSON.stringify(statusFor()), { status: 200 });
    if (u.endsWith('/engine') && (!init?.method || init.method === 'GET')) {
      return new Response(JSON.stringify(state), { status: 200 });
    }
    if (u.endsWith('/pools/free') && init?.method === 'PUT') {
      const body = JSON.parse(init.body || '{}') as Record<string, unknown>;
      puts.push({ url: u, body });
      if (typeof body.groupMode === 'string') state.groupMode = body.groupMode;
      return new Response('{"ok":"updated"}', { status: 200 });
    }
    if (typeof init?.body === 'string') {
      puts.push({ url: u, body: JSON.parse(init.body) as Record<string, unknown> });
    }
    return new Response('{"ok":true}', { status: 200 });
  });
  return { f, state, puts };
}

async function openSection(payload: Record<string, unknown> = payloadUrltest) {
  const stub = engineStub(payload);
  vi.stubGlobal('fetch', stub.f);
  const w = mount(PoolNodesSection, { props: { pool: { ...enginePool } } });
  const d = w.find('details').element as HTMLDetailsElement;
  d.open = true;
  await w.find('details').trigger('toggle');
  await vi.waitFor(() => expect(w.text()).toContain('узел-1'));
  return { w, ...stub };
}

const btnByTitle = (w: ReturnType<typeof mount>, title: string) =>
  w.findAll('button').filter((b) => b.attributes('title') === title);

beforeEach(() => {
  vi.unstubAllGlobals();
  useStatus().stop();
  useStatus().state.data = null;
  useToast().toasts.list.splice(0);
});

describe('PoolNodesSection - стрелки порядка видимы всегда (118 TASK 0)', () => {
  it('urltest (дефолт): стрелки в строках узлов, клик шлёт PUT /node-order', async () => {
    const { w, f, puts } = await openSection();
    // раньше: template v-if="engMode !== 'urltest'" - стрелок не было вовсе
    const down = btnByTitle(w, 'опустить в порядке');
    expect(down.length).toBe(3); // у каждого узла, у последнего disabled
    expect((down[2].element as HTMLButtonElement).disabled).toBe(true);
    expect((btnByTitle(w, 'поднять в порядке')[0].element as HTMLButtonElement).disabled).toBe(true);
    await down[0].trigger('click');
    await vi.waitFor(() => {
      const put = puts.find((p) => p.url.endsWith('/node-order'));
      expect(put).toBeTruthy();
      expect(put!.body).toEqual({ order: ['n2', 'n1', 'n3'] });
    });
    expect(f.mock.calls.some((c) => (c[0] as string).endsWith('/node-order'))).toBe(true);
    w.unmount();
  });

  it('urltest: подсказка - порядок сейчас ни на что не влияет, станет решающим в других режимах', async () => {
    const { w } = await openSection();
    expect(w.text()).toContain('ни на что не влияют');
    expect(w.text()).toContain('«по порядку», «ручной выбор» и «ротация»');
    w.unmount();
  });

  it('fallback: стрелки видимы, подсказка «первый живой» с возвратом на старшего', async () => {
    const { w } = await openSection({ ...payloadUrltest, groupMode: 'fallback' });
    expect(btnByTitle(w, 'опустить в порядке').length).toBe(3);
    expect(w.text()).toContain('активен первый живой узел');
    w.unmount();
  });
});

describe('PoolNodesSection - селект режима в дровере (118 TASK 0)', () => {
  it('виден при urltest, выбор «по порядку» шлёт PUT /pools/free {groupMode}, перечитывает узлы', async () => {
    const { w, puts } = await openSection();
    expect(w.find('.moderow .psel-btn').text()).toBe('лучший пинг (авто)');
    await w.find('.moderow .psel-btn').trigger('click');
    const item = w.findAll('.psel-item').find((o) => o.text().includes('по порядку'))!;
    expect(item).toBeDefined();
    expect(item.text()).toContain('активен первый живой'); // hint в выпадающем списке
    await item.trigger('click');
    await vi.waitFor(() => {
      const put = puts.find((p) => p.url.endsWith('/pools/free'));
      expect(put).toBeTruthy();
      expect(put!.body).toEqual({ groupMode: 'fallback' });
    });
    // подсказка сменилась на режим по порядку (перечитали /engine)
    await vi.waitFor(() => expect(w.text()).toContain('активен первый живой узел'));
    w.unmount();
  });

  it('ротация: сразу с валидным rotateSec (RotateSec<10 не тикает), интервал виден и применяется', async () => {
    const { w, puts } = await openSection({ ...payloadUrltest, groupMode: 'fallback' });
    await w.find('.moderow .psel-btn').trigger('click');
    await w.findAll('.psel-item').find((o) => o.text().includes('ротация'))!.trigger('click');
    await vi.waitFor(() => {
      const put = puts.find((p) => p.url.endsWith('/pools/free'));
      expect(put!.body).toEqual({ groupMode: 'rotate', rotateSec: 300 });
    });
    await vi.waitFor(() => expect(w.find('.rotsec input').exists()).toBe(true));
    await w.find('.rotsec input').setValue('60');
    await btnByTitle(w, 'применить интервал ротации')[0].trigger('click');
    await vi.waitFor(() => {
      const secs = puts.filter((p) => p.url.endsWith('/pools/free') && p.body.rotateSec);
      expect(secs[secs.length - 1]!.body).toEqual({ rotateSec: 60 });
    });
    w.unmount();
  });

  it('тост о смене режима - с короткой подписью выбранного варианта', async () => {
    const { w } = await openSection();
    await w.find('.moderow .psel-btn').trigger('click');
    await w.findAll('.psel-item').find((o) => o.text().includes('по порядку'))!.trigger('click');
    await vi.waitFor(() =>
      expect(useToast().toasts.list.some((t) => t.text.includes('Режим группы: по порядку'))).toBe(true));
    w.unmount();
  });

  it('пока движок пересобирается - оверлей «идёт применение» на дровере (118, слово владельца 14.11)', async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => { release = r; });
    const stub = engineStub(payloadUrltest);
    const base = stub.f;
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
      if ((url as string).endsWith('/pools/free') && init?.method === 'PUT') {
        await gate; // пересборка движка висит - ответ не спешит
        stub.state.groupMode = 'fallback'; // как сделал бы настоящий бэкенд
        return new Response('{"ok":"updated"}', { status: 200 });
      }
      return base(url, init);
    }));
    const w = mount(PoolNodesSection, { props: { pool: { ...enginePool } } });
    const d = w.find('details').element as HTMLDetailsElement;
    d.open = true;
    await w.find('details').trigger('toggle');
    await vi.waitFor(() => expect(w.text()).toContain('узел-1'));
    await w.find('.moderow .psel-btn').trigger('click');
    await w.findAll('.psel-item').find((o) => o.text().includes('по порядку'))!.trigger('click');
    // селект уже «по порядку», но ответа нет - оверлей объясняет, что живо
    expect(w.text()).toContain('идёт применение настроек…');
    release();
    await vi.waitFor(() => expect(w.text()).toContain('активен первый живой узел'));
    await vi.waitFor(() => expect(w.text()).not.toContain('идёт применение настроек…'));
    w.unmount();
  });
});
