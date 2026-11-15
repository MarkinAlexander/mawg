import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import PoolReaddressDialog from './PoolReaddressDialog.vue'
import { useStatus } from '../../stores/status'
import { useToast } from '../../stores/toast'
import type { PoolView, Status } from '../../api/types'

// Перенумерация локальных адресов (120-док): однотипная куча Proton с
// одним адресом - подсказка первого свободного + массовый POST readdress
const pool: PoolView = {
  name: 'proton', platform: 'keenetic', device: 'nwg0', slot: 'nwg0', mode: 'up',
  activeFile: '', activeEndpoint: '', lastResult: '', lastError: '', rotations: 0,
  disabled: false, consecFails: 0, settings: {},
  configs: [
    { file: 'nl-15.conf', original: 'NL-FREE#15', enabled: true, addresses: ['10.2.0.2/32', '2a07:b944::2:2/128'] },
    { file: 'nl-29.conf', original: 'NL-FREE#29', enabled: true, addresses: ['10.2.0.2/32', '2a07:b944::2:2/128'] },
    { file: 'off.conf', original: 'выключенный', enabled: false, addresses: ['10.2.0.2/32'] },
  ],
  premium: { imported: false, countries: [] }, protos: [],
} as unknown as PoolView;

function seed(): void {
  useStatus().state.data = {
    version: 'v', platform: 'keenetic', backendName: 'keenetic', authEnabled: false,
    nativeAWG3: false, mtSources: true, pools: [pool], engine: undefined,
  } as Status;
}

beforeEach(() => {
  vi.unstubAllGlobals();
  useStatus().stop();
  useStatus().state.data = null;
  useToast().toasts.list.splice(0);
});

describe('PoolReaddressDialog', () => {
  it('подсказка базы - первый свободный адрес занятой подсети; применение шлёт base и показывает занятость', async () => {
    seed();
    const calls: string[] = [];
    const f = vi.fn(async (url: unknown, init?: { method?: string; body?: string }) => {
      const u = url as string;
      calls.push(u + ' ' + (init?.method || '') + ' ' + (init?.body || ''));
      if (u.endsWith('/configs/readdress')) {
        return new Response(JSON.stringify({
          changed: [
            { file: 'nl-15.conf', old: '10.2.0.2', address: '10.2.0.3' },
            { file: 'nl-29.conf', old: '10.2.0.2', address: '10.2.0.4' },
          ],
        }), { status: 200 });
      }
      return new Response('{}', { status: 200 });
    });
    vi.stubGlobal('fetch', f);
    // open с false -> true: подсказка базы прилетает watch'ем на открытие
    const w = mount(PoolReaddressDialog, { props: { pool, open: false } });
    await w.setProps({ open: true });
    await nextTick();
    const input = w.find('input');
    expect((input.element as HTMLInputElement).value).toBe('10.2.0.3');
    expect(w.text()).toContain('Proton');
    // перенумеруется весь пул: выключенный тоже - куча обычно выключена
    // конфликтом адресов ещё до первой ротации (120-док)
    expect(w.text()).toContain('10.2.0.2 → 10.2.0.3');
    expect(w.text()).toContain('10.2.0.2 → 10.2.0.4');
    expect(w.text()).toContain('выключенный');
    const btn = w.findAll('button').find((b) => b.text() === 'Перенумеровать');
    expect(btn).toBeTruthy();
    await btn!.trigger('click');
    await new Promise((r) => setTimeout(r, 0));
    const post = calls.find((c) => c.includes('/configs/readdress'));
    expect(post).toBeTruthy();
    expect(post).toContain('"base":"10.2.0.3"');
    expect(useToast().toasts.list.map((t) => t.text).join(' ')).toContain('Перенумеровано');
    expect(w.emitted()['update:open']).toBeTruthy();
  });

  it('занятость других пулов входит в подсказку; переполнение /24 блокирует кнопку', async () => {
    seed();
    useStatus().state.data!.pools!.push({
      ...pool, name: 'proton2', configs: [
        { file: 'x.conf', enabled: true, addresses: ['10.2.0.3/32'] },
      ],
    } as PoolView);
    const w = mount(PoolReaddressDialog, { props: { pool, open: false } });
    await w.setProps({ open: true });
    await nextTick();
    const input = w.find('input');
    expect((input.element as HTMLInputElement).value).toBe('10.2.0.4');
    // база у края: второму адресу не хватает - применение задизейблено
    await input.setValue('10.2.0.255');
    await nextTick();
    expect(w.text()).toContain('не влезает');
    const btn = w.findAll('button').find((b) => b.text() === 'Перенумеровать');
    expect(btn!.attributes('disabled')).toBeDefined();
    // серверный отказ (адрес занят) - тост с ошибкой, диалог не закрылся
    vi.stubGlobal('fetch', vi.fn(async (url: unknown) => {
      if ((url as string).endsWith('/configs/readdress')) {
        return new Response(JSON.stringify({ error: 'адреса заняты: 10.2.0.4 занят' }), { status: 400 });
      }
      return new Response('{}', { status: 200 });
    }));
    await input.setValue('10.2.0.4');
    await nextTick();
    const ok = w.findAll('button').find((b) => b.text() === 'Перенумеровать');
    await ok!.trigger('click');
    await new Promise((r) => setTimeout(r, 0));
    expect(useToast().toasts.list.map((t) => t.text).join(' ')).toContain('заняты');
    expect(w.emitted()['update:open']).toBeFalsy();
  });
});
