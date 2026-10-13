import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import OverviewView from './OverviewView.vue'
import { useStatus } from '../../stores/status'
import type { PoolView } from '../../api/types'

const pool: PoolView = {
  name: 'proton', platform: 'openwrt', device: 'nwg0', mode: 'up',
  activeFile: '', activeEndpoint: '', lastResult: '', lastError: '',
  rotations: 0, disabled: false, consecFails: 0,
  settings: {}, configs: [], premium: { imported: false, countries: [] }, protos: [],
};

beforeEach(() => {
  vi.unstubAllGlobals();
  const s = useStatus();
  s.stop();
  s.state.data = null;
});

describe('OverviewView', () => {
  it('без пулов - подсказка «Пулов нет»', () => {
    useStatus().state.data = {
      version: 'v', platform: 'openwrt', backendName: 'openwrt', authEnabled: false,
      nativeAWG3: false, mtSources: true, pools: [],
    };
    const w = mount(OverviewView);
    expect(w.text()).toContain('Пулов нет. Создайте первый, например proton.');
    w.unmount();
  });

  it('карточка на каждый пул (PoolCard разрешён, не кастом-тег)', () => {
    useStatus().state.data = {
      version: 'v', platform: 'openwrt', backendName: 'openwrt', authEnabled: false,
      nativeAWG3: false, mtSources: true, pools: [pool, { ...pool, name: 'warp' }],
    };
    const w = mount(OverviewView);
    expect(w.findAll('.card')).toHaveLength(2);
    expect(w.text()).toContain('proton');
    expect(w.text()).toContain('warp');
    expect(w.html()).not.toContain('<poolcard');
    w.unmount();
  });
});
