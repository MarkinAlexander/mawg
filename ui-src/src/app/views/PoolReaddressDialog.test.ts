import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import PoolReaddressDialog from './PoolReaddressDialog.vue'
import { useStatus } from '../../stores/status'
import { useToast } from '../../stores/toast'
import { crossPoolCollisionOf, nextFreeV4, occupiedV4 } from './poolsLogic'
import type { PoolView, Status } from '../../api/types'

// Смена ЕДИНОГО адреса всего пула (120-док + уточнение владельца 20.11:
// «в рамках пула конфиги ротируются - активен один, адрес общий; второму
// пулу провайдера беру один адрес, например 10.2.0.3, и интерфейсы
// поднимаются вместе»; НЕ +1 по конфигам - коллизия только между пулами)
const mkPool = (name: string, addrs: string[], off = false): PoolView => ({
  name, platform: 'openwrt', device: name, slot: name, mode: 'up',
  activeFile: '', activeEndpoint: '', lastResult: '', lastError: '', rotations: 0,
  disabled: false, consecFails: 0, settings: {},
  configs: addrs.map((a, i) => ({
    file: name + '-' + i + '.conf', original: name + '-' + i, enabled: !off,
    addresses: a ? [a, '2a07:b944::2:2/128'] : [],
  })),
  premium: { imported: false, countries: [] }, protos: [],
} as unknown as PoolView);

function seed(pools: PoolView[]): void {
  useStatus().state.data = {
    version: 'v', platform: 'openwrt', backendName: 'openwrt', authEnabled: false,
    nativeAWG3: false, mtSources: true, pools, engine: undefined,
  } as Status;
}

beforeEach(() => {
  vi.unstubAllGlobals();
  useStatus().stop();
  useStatus().state.data = null;
  useToast().toasts.list.splice(0);
});

const textInputs = (w: ReturnType<typeof mount>) =>
  w.findAll('input').filter((i) => i.attributes('type') !== 'checkbox');
const applyBtn = (w: ReturnType<typeof mount>) =>
  w.findAll('button').find((b) => b.text() === 'Сменить IP пула');

async function openDlg(w: ReturnType<typeof mount>): Promise<void> {
  await w.setProps({ open: true });
  await nextTick();
}

describe('PoolReaddressDialog - единый адрес пула', () => {
  it('подсказка свободна и от чужих пулов, и от СВОИХ текущих адресов: protonb с .3/.4 получает .5', async () => {
    // proton держит .2, protonb - свои .3/.4: предлагать собственный адрес
    // бессмысленно («10.2.0.3 → 10.2.0.3»), берём реально новый
    seed([
      mkPool('proton', ['10.2.0.2/32']),
      mkPool('protonb', ['10.2.0.3/32', '10.2.0.4/32']),
    ]);
    const protonb = mkPool('protonb', ['10.2.0.3/32', '10.2.0.4/32']);
    const w = mount(PoolReaddressDialog, { props: { pool: protonb, open: false } });
    await openDlg(w);
    expect((textInputs(w)[0].element as HTMLInputElement).value).toBe('10.2.0.5');
    // в списке все строки получают ОДИН адрес
    expect(w.text()).toContain('10.2.0.3 → 10.2.0.5');
    expect(w.text()).toContain('10.2.0.4 → 10.2.0.5');
  });

  it('одиночный пул без соседей: подсказка не предлагает собственный адрес (172.16.0.2 → .3)', async () => {
    const warp = mkPool('warp', ['172.16.0.2/32', '172.16.0.2/32']);
    seed([warp]);
    const w = mount(PoolReaddressDialog, { props: { pool: warp, open: false } });
    await openDlg(w);
    expect((textInputs(w)[0].element as HTMLInputElement).value).toBe('172.16.0.3');
    expect(w.text()).toContain('172.16.0.2 → 172.16.0.3');
  });

  it('no-op блокирует кнопку: адрес совпадает с текущим - менять нечего', async () => {
    seed([mkPool('proton', ['10.2.0.2/32'])]);
    const p = mkPool('protonb', ['10.2.0.3/32']);
    const w = mount(PoolReaddressDialog, { props: { pool: p, open: false } });
    await openDlg(w);
    const input = textInputs(w)[0];
    await input.setValue('10.2.0.3');
    await nextTick();
    expect(applyBtn(w)!.attributes('disabled')).toBeDefined();
    await input.setValue('10.2.0.5');
    await nextTick();
    expect(applyBtn(w)!.attributes('disabled')).toBeUndefined();
  });

  it('применение: всем конфигам (с v4) один адрес, конфиги без v4 пропущены', async () => {
    const withNoV4 = mkPool('protonb', ['10.2.0.3/32', '']);
    const p = { ...withNoV4 } as PoolView;
    seed([mkPool('proton', ['10.2.0.2/32']), p]);
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (url: unknown, init?: { method?: string; body?: string }) => {
      calls.push((url as string) + ' ' + (init?.body || ''));
      if ((url as string).endsWith('/configs/readdress')) {
        return new Response(JSON.stringify({
          changed: [
            { file: 'protonb-0.conf', old: '10.2.0.3', address: '10.2.0.4' },
          ],
        }), { status: 200 });
      }
      return new Response('{}', { status: 200 });
    }));
    const w = mount(PoolReaddressDialog, { props: { pool: p, open: false } });
    await openDlg(w);
    // конфиг без v4 (движковый) не попадает в список - менять нечего
    expect(w.text()).not.toContain('protonb-1');
    const btn = applyBtn(w);
    expect(btn).toBeTruthy();
    await btn!.trigger('click');
    await new Promise((r) => setTimeout(r, 0));
    const post = calls.find((c) => c.includes('/configs/readdress'));
    expect(post).toBeTruthy();
    expect(post).toContain('"files":["protonb-0.conf"]');
    expect(post).toContain('"addresses":["10.2.0.4"]');
    expect(useToast().toasts.list.map((t) => t.text).join(' ')).toContain('весь пул на 10.2.0.4');
    expect(w.emitted()['update:open']).toBeTruthy();
  });

  it('битый адрес блокирует кнопку; серверный отказ - тост, диалог открыт', async () => {
    seed([mkPool('proton', ['10.2.0.2/32']), mkPool('protonb', ['10.2.0.3/32', '10.2.0.4/32'])]);
    const p = mkPool('protonb', ['10.2.0.3/32', '10.2.0.4/32']);
    const w = mount(PoolReaddressDialog, { props: { pool: p, open: false } });
    await openDlg(w);
    const input = textInputs(w)[0];
    await input.setValue('bogus');
    await nextTick();
    expect(applyBtn(w)!.attributes('disabled')).toBeDefined();
    vi.stubGlobal('fetch', vi.fn(async (url: unknown) => {
      if ((url as string).endsWith('/configs/readdress')) {
        return new Response(JSON.stringify({ error: 'адреса заняты: 10.2.0.5 занят (конфиг x.conf пула proton)' }), { status: 400 });
      }
      return new Response('{}', { status: 200 });
    }));
    await input.setValue('10.2.0.5');
    await nextTick();
    await applyBtn(w)!.trigger('click');
    await new Promise((r) => setTimeout(r, 0));
    expect(useToast().toasts.list.map((t) => t.text).join(' ')).toContain('заняты');
    expect(w.emitted()['update:open']).toBeFalsy();
  });
});

describe('poolsLogic - межпуловая коллизия (внутри пула общий адрес - норма)', () => {
  it('адрес совпадает с другим пулом - коллизия; только внутри пула - null', () => {
    const proton = mkPool('proton', ['10.2.0.2/32']);
    const protonb = mkPool('protonb', ['10.2.0.2/32', '10.2.0.2/32']);
    expect(crossPoolCollisionOf(protonb, [proton])).toEqual({ addr: '10.2.0.2', pool: 'proton' });
    // ротация в ОДНОМ пуле: все с .2, других пулов нет - коллизии нет
    expect(crossPoolCollisionOf(protonb, [])).toBeNull();
    expect(crossPoolCollisionOf(mkPool('x', ['10.9.9.9/32']), [proton])).toBeNull();
  });

  it('nextFreeV4 по чужим адресам: protonb со своими .3/.4 получит .3 (свой освобождается)', () => {
    const others = occupiedV4([
      mkPool('proton', ['10.2.0.2/32']),
      mkPool('protonc', ['10.2.0.5/32']),
    ]);
    expect(nextFreeV4(others, '10.2.0.')).toBe('10.2.0.3');
  });
});
