import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import PoolReaddressDialog from './PoolReaddressDialog.vue'
import { useStatus } from '../../stores/status'
import { useToast } from '../../stores/toast'
import type { PoolView, Status } from '../../api/types'

// Перенумерация/смена локальных адресов (120-док + слово владельца 20.11
// «менять прям на этапе загрузки, как по одному так и массово»): база
// генерирует план, каждый адрес правится руками, галочка снимает строку -
// уходит только выбранное (явные addresses 1:1 к files)
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

const textInputs = (w: ReturnType<typeof mount>) =>
  w.findAll('input').filter((i) => i.attributes('type') !== 'checkbox');
const checkboxes = (w: ReturnType<typeof mount>) =>
  w.findAll('input[type=checkbox]');
const applyBtn = (w: ReturnType<typeof mount>) =>
  w.findAll('button').find((b) => b.text() === 'Сменить адреса');

async function openDlg(w: ReturnType<typeof mount>): Promise<void> {
  await w.setProps({ open: true });
  await nextTick();
}

describe('PoolReaddressDialog', () => {
  it('база генерирует план по строкам, массовое применение шлёт явные addresses 1:1', async () => {
    seed();
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (url: unknown, init?: { method?: string; body?: string }) => {
      const u = url as string;
      calls.push(u + ' ' + (init?.method || '') + ' ' + (init?.body || ''));
      if (u.endsWith('/configs/readdress')) {
        return new Response(JSON.stringify({
          changed: [
            { file: 'nl-15.conf', old: '10.2.0.2', address: '10.2.0.3' },
            { file: 'nl-29.conf', old: '10.2.0.2', address: '10.2.0.4' },
            { file: 'off.conf', old: '10.2.0.2', address: '10.2.0.5' },
          ],
        }), { status: 200 });
      }
      return new Response('{}', { status: 200 });
    }));
    const w = mount(PoolReaddressDialog, { props: { pool, open: false } });
    await openDlg(w);
    // подсказка базы: первый свободный адрес занятой подсети
    const inputs = textInputs(w);
    expect((inputs[0].element as HTMLInputElement).value).toBe('10.2.0.3');
    expect(w.text()).toContain('Proton');
    // план: каждому конфигу +1, выключенный тоже
    expect((inputs[1].element as HTMLInputElement).value).toBe('10.2.0.3');
    expect((inputs[2].element as HTMLInputElement).value).toBe('10.2.0.4');
    expect((inputs[3].element as HTMLInputElement).value).toBe('10.2.0.5');
    expect(w.text()).toContain('выключенный');
    expect(checkboxes(w).length).toBe(3);
    const btn = applyBtn(w);
    expect(btn).toBeTruthy();
    await btn!.trigger('click');
    await new Promise((r) => setTimeout(r, 0));
    const post = calls.find((c) => c.includes('/configs/readdress'));
    expect(post).toBeTruthy();
    expect(post).toContain('"files":["nl-15.conf","nl-29.conf","off.conf"]');
    expect(post).toContain('"addresses":["10.2.0.3","10.2.0.4","10.2.0.5"]');
    expect(useToast().toasts.list.map((t) => t.text).join(' ')).toContain('Сменено');
    expect(w.emitted()['update:open']).toBeTruthy();
  });

  it('правка по одному: снятые галочки не уходят, адрес правится руками', async () => {
    seed();
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (url: unknown, init?: { method?: string; body?: string }) => {
      calls.push((url as string) + ' ' + (init?.body || ''));
      if ((url as string).endsWith('/configs/readdress')) {
        return new Response(JSON.stringify({ changed: [{ file: 'nl-29.conf', old: '10.2.0.2', address: '10.2.0.9' }] }), { status: 200 });
      }
      return new Response('{}', { status: 200 });
    }));
    const w = mount(PoolReaddressDialog, { props: { pool, open: false } });
    await openDlg(w);
    // снимаем первую и третью галочки, второй ставим свой адрес
    await checkboxes(w)[0].setValue(false);
    await checkboxes(w)[2].setValue(false);
    const inputs = textInputs(w);
    await inputs[2].setValue('10.2.0.9');
    await nextTick();
    await applyBtn(w)!.trigger('click');
    await new Promise((r) => setTimeout(r, 0));
    const post = calls.find((c) => c.includes('/configs/readdress'));
    expect(post).toContain('"files":["nl-29.conf"]');
    expect(post).toContain('"addresses":["10.2.0.9"]');
  });

  it('битый адрес блокирует кнопку, пустой - строка пропускается', async () => {
    seed();
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 200 })));
    const w = mount(PoolReaddressDialog, { props: { pool, open: false } });
    await openDlg(w);
    const inputs = textInputs(w);
    await inputs[1].setValue('not-an-ip');
    await nextTick();
    expect(applyBtn(w)!.attributes('disabled')).toBeDefined();
    // пустой адрес = строка пропускается, кнопка снова жива
    await inputs[1].setValue('');
    await inputs[2].setValue('');
    await nextTick();
    expect(applyBtn(w)!.attributes('disabled')).toBeUndefined();
    // занятость других пулов входит в подсказку
    useStatus().state.data!.pools!.push({
      ...pool, name: 'proton2', configs: [
        { file: 'x.conf', enabled: true, addresses: ['10.2.0.3/32'] },
      ],
    } as PoolView);
    await w.setProps({ open: false });
    await w.setProps({ open: true });
    await nextTick();
    expect((textInputs(w)[0].element as HTMLInputElement).value).toBe('10.2.0.4');
  });

  it('серверный отказ - тост с ошибкой, диалог не закрылся', async () => {
    seed();
    vi.stubGlobal('fetch', vi.fn(async (url: unknown) => {
      if ((url as string).endsWith('/configs/readdress')) {
        return new Response(JSON.stringify({ error: 'адреса заняты: 10.2.0.3 занят' }), { status: 400 });
      }
      return new Response('{}', { status: 200 });
    }));
    const w = mount(PoolReaddressDialog, { props: { pool, open: false } });
    await openDlg(w);
    await applyBtn(w)!.trigger('click');
    await new Promise((r) => setTimeout(r, 0));
    expect(useToast().toasts.list.map((t) => t.text).join(' ')).toContain('заняты');
    expect(w.emitted()['update:open']).toBeFalsy();
  });
});
