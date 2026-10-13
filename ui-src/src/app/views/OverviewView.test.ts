import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import OverviewView from './OverviewView.vue'
import { useStatus } from '../../stores/status'

// Слово владельца (2026-10-14): пулы - на вкладке «Интерфейсы», Обзор -
// заглушка под будущую выжимку/мастеров (Ф4/Ф5)
beforeEach(() => {
  vi.unstubAllGlobals();
  useStatus().stop();
  useStatus().state.data = null;
});

describe('OverviewView', () => {
  it('заглушка: текст на переносе, карточек пулов нет (даже когда пулы есть)', () => {
    useStatus().state.data = {
      version: 'v', platform: 'openwrt', backendName: 'openwrt', authEnabled: false,
      nativeAWG3: false, mtSources: true,
      pools: [{
        name: 'proton', platform: 'openwrt', device: 'nwg0', mode: 'up',
        activeFile: '', activeEndpoint: '', lastResult: '', lastError: '',
        rotations: 0, disabled: false, consecFails: 0,
        settings: {}, configs: [], premium: { imported: false, countries: [] }, protos: [],
      }],
    };
    const w = mount(OverviewView);
    expect(w.text()).toContain('Обзор');
    expect(w.text()).toContain('Вкладка на переносе');
    expect(w.text()).not.toContain('proton'); // карточек пулов нет
    expect(w.text()).not.toContain('+ интерфейс');
    expect(w.html()).not.toContain('<poolcard');
    w.unmount();
  });
});
