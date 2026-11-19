import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PoolConfigsSection from './PoolConfigsSection.vue'
import { useStatus } from '../../stores/status'
import { useToast } from '../../stores/toast'
import type { PoolView, Status } from '../../api/types'

// Просмотр конфига как он лежит на роутере (слово владельца 20.11):
// кнопка 👁 в строке открывает модалку с raw-текстом; приватный режим
// блюрит через .secret (глобальный клик-раскрытие)
const RAW = '[Interface]\nPrivateKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=\nAddress = 10.2.0.3/32\n\n[Peer]\nPublicKey = QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=\nEndpoint = 198.51.100.7:51820\nAllowedIPs = 0.0.0.0/0\n';

const pool: PoolView = {
  name: 'protonb', platform: 'openwrt', device: 'protonb', slot: 'protonb',
  mode: 'up', activeFile: 'nl-15.conf', activeEndpoint: '198.51.100.15:51820',
  lastResult: '', lastError: '', rotations: 1, disabled: false, consecFails: 0,
  settings: {},
  configs: [
    { file: 'nl-15.conf', original: 'NL-FREE#15', endpoint: '198.51.100.15:51820', enabled: true, awg: 'awg2' },
    { file: 'nl-29.conf', original: 'NL-FREE#29', endpoint: '198.51.100.29:51820', enabled: false, awg: 'awg2' },
  ],
  premium: { imported: false, countries: [] }, protos: [],
} as unknown as PoolView;

function seed(): void {
  useStatus().state.data = {
    version: 'v', platform: 'openwrt', backendName: 'openwrt', authEnabled: false,
    nativeAWG3: false, mtSources: true, pools: [pool], engine: undefined,
  } as Status;
}

beforeEach(() => {
  vi.unstubAllGlobals();
  useStatus().stop();
  useStatus().state.data = null;
  useToast().toasts.list.splice(0);
});

describe('PoolConfigsSection - просмотр конфига', () => {
  it('кнопка 👁 открывает модалку с raw-текстом как есть, активный помечен', async () => {
    seed();
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (url: unknown) => {
      calls.push(url as string);
      if ((url as string).includes('/configs/nl-15.conf')) {
        return new Response(RAW, { status: 200 });
      }
      return new Response('{}', { status: 200 });
    }));
    const w = mount(PoolConfigsSection, { props: { pool } });
    const eye = w.findAll('button').find((b) => b.text().includes('👁'));
    expect(eye).toBeTruthy();
    await eye!.trigger('click');
    await new Promise((r) => setTimeout(r, 0));
    const dlg = [...w.element.querySelectorAll('dialog')].find((d) => d.open);
    expect(dlg).toBeTruthy();
    expect(dlg!.querySelector('h3')?.textContent).toContain('nl-15.conf');
    expect(dlg!.querySelector('h3')?.textContent).toContain('активен');
    const pre = dlg!.querySelector('.cfgview');
    expect(pre?.classList.contains('secret')).toBe(true);
    expect(pre?.textContent).toContain('PrivateKey = ');
    expect(pre?.textContent).toContain('Address = 10.2.0.3/32');
    expect(calls.some((c) => c.includes('/pools/protonb/configs/nl-15.conf'))).toBe(true);
  });

  it('копирование из шапки: execCommand-фолбэк на http, тост подтверждения', async () => {
    seed();
    const exec = vi.fn(() => true);
    (document as unknown as { execCommand: unknown }).execCommand = exec;
    vi.stubGlobal('fetch', vi.fn(async (url: unknown) => {
      if ((url as string).includes('/configs/nl-15.conf')) return new Response(RAW, { status: 200 });
      return new Response('{}', { status: 200 });
    }));
    const w = mount(PoolConfigsSection, { props: { pool } });
    await w.findAll('button').find((b) => b.text().includes('👁'))!.trigger('click');
    await new Promise((r) => setTimeout(r, 0));
    // значок копирования в шапке - ПОСЛЕ текста «активен»
    const h3 = [...w.element.querySelectorAll('dialog')].find((d) => d.open)!.querySelector('h3')!;
    expect(h3.textContent).toContain('активен');
    const copy = h3.querySelector('button');
    expect(copy).toBeTruthy();
    await copy!.click();
    expect(exec).toHaveBeenCalledWith('copy');
    expect(useToast().toasts.list.map((t) => t.text).join(' ')).toContain('скопирован');
  });

  it('второй конфиг - своя модалка без пометки «активен»', async () => {
    seed();
    vi.stubGlobal('fetch', vi.fn(async (url: unknown) => {
      if ((url as string).includes('/configs/nl-29.conf')) {
        return new Response(RAW.replace('10.2.0.3', '10.2.0.4'), { status: 200 });
      }
      return new Response('{}', { status: 200 });
    }));
    const w = mount(PoolConfigsSection, { props: { pool } });
    const eyes = w.findAll('button').filter((b) => b.text().includes('👁'));
    expect(eyes.length).toBe(2);
    await eyes[1].trigger('click');
    await new Promise((r) => setTimeout(r, 0));
    const dlg = [...w.element.querySelectorAll('dialog')].find((d) => d.open);
    expect(dlg!.querySelector('h3')?.textContent).toContain('nl-29.conf');
    expect(dlg!.querySelector('h3')?.textContent).not.toContain('активен');
    expect(dlg!.querySelector('.cfgview')?.textContent).toContain('10.2.0.4');
  });
});
