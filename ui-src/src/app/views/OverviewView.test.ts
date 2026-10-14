import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createRouter, createWebHashHistory } from 'vue-router'
import OverviewView from './OverviewView.vue'
import { useGroups } from '../../stores/groups'
import { useIfaces } from '../../stores/ifaces'
import { useBundles } from '../../stores/bundles'
import { useStatus } from '../../stores/status'
import type { Status } from '../../api/types'

// Обзор - короткая выжимка (44-дока §6): состояние MT и плитки-счётчики
// со ссылками на вкладки. Пулы - на «Интерфейсах» (49-док), LAN-клиенты -
// в Настройках, мастера встанут сюда в Ф5 (46-дока §2).

// стаб fetch по путям (группы/интерфейсы/наборы/бэкапы/статус)
function apiStub() {
  return vi.fn(async (url: string) => {
    const u = url;
    if (u.endsWith('/mt/groups')) {
      return new Response(JSON.stringify({ available: true, groups: [
        { id: 'g1', name: 'telegram', interface: 'nwg0', enable: true, rules: [{ id: 'r1' }, { id: 'r2' }] },
        { id: 'g2', name: 'ai', interface: 'tun1', enable: true, rules: [] },
        { id: 'g3', name: 'каскад', interface: 'nwg7', enable: true, cascade: { group: 'g3' }, rules: [] },
      ] }), { status: 200 });
    }
    if (u.endsWith('/mt/duplicates')) return new Response(JSON.stringify({ duplicates: {} }), { status: 200 });
    if (u.endsWith('/ifaces')) {
      return new Response(JSON.stringify({ interfaces: [{ device: 'nwg0' }, { device: 'nwg7' }] }), { status: 200 });
    }
    if (u.endsWith('/bundles')) return new Response(JSON.stringify({ bundles: [{ name: 'b1' }] }), { status: 200 });
    if (u.endsWith('/mt/backups')) {
      return new Response(JSON.stringify({ backups: [{ name: 'mawg-full-1.tar.gz', kind: 'full' }] }), { status: 200 });
    }
    return new Response(JSON.stringify({}), { status: 200 });
  });
}

beforeEach(() => {
  vi.unstubAllGlobals();
  vi.stubGlobal('fetch', apiStub());
  const g = useGroups();
  g.stop();
  g.state.groups = [];
  g.state.loaded = false;
  g.state.backups = { list: [], loaded: false };
  const i = useIfaces();
  i.stop();
  i.state.list = [];
  i.state.loaded = false;
  const b = useBundles();
  b.stop();
  b.state.list = [];
  b.state.loaded = false;
  useStatus().state.data = null;
  useStatus().state.mt = 'checking';
});

describe('OverviewView', () => {
  it('сводка: карточка разрешена, плитки-ссылки со счётчиками (группы/правила/каскады, интерфейсы, бэкапы)', async () => {
    const router = createRouter({
      history: createWebHashHistory(),
      routes: [{ path: '/next/:id', component: { template: '<div />' } }],
    })
    const w = mount(OverviewView, { global: { plugins: [router] } });
    await vi.waitFor(() => expect(useGroups().state.loaded).toBe(true));
    await vi.waitFor(() => expect(useIfaces().state.loaded).toBe(true));
    await vi.waitFor(() => expect(useBundles().state.loaded).toBe(true));
    await nextTick();
    // грабля забытого импорта
    expect(w.find('.card').exists()).toBe(true);
    expect(w.html()).not.toContain('<uicard');

    const tiles = w.findAll('.tile');
    expect(tiles).toHaveLength(4);
    // слово владельца: интерфейсы раньше групп - сначала подключения
    expect(tiles[0].attributes('href')).toBe('#/next/interfaces');
    expect(tiles[0].text()).toContain('Интерфейсы');
    expect(tiles[0].text()).toContain('2'); // интерфейсы
    expect(tiles[1].text()).toContain('Пулы');
    expect(tiles[2].attributes('href')).toBe('#/next/groups');
    expect(tiles[2].text()).toContain('Группы правил');
    expect(tiles[2].text()).toContain('3'); // группы
    expect(tiles[2].text()).toContain('2 правила');
    expect(tiles[2].text()).toContain('1 каскад');
    expect(tiles[3].attributes('href')).toBe('#/next/backups');
    expect(tiles[3].text()).toContain('1'); // бэкапы
    w.unmount();
  });

  it('статус MT бейджем: недоступен - err, доступен - ok', async () => {
    const { state } = useStatus();
    state.mt = 'no';
    const w = mount(OverviewView);
    await nextTick();
    const badge = w.find('.mtrow .uibadge');
    expect(badge.text()).toContain('magitrickle: недоступен');
    expect(badge.classes().join(' ')).toContain('v-err');
    state.mt = 'yes';
    await nextTick();
    expect(w.find('.mtrow .uibadge').classes().join(' ')).toContain('v-ok');
    w.unmount();
  });

  it('мета: платформа и версия из статуса, пока данные нет - загрузка/ошибка', async () => {
    const w = mount(OverviewView);
    await nextTick();
    expect(w.text()).toContain('загрузка...');
    const { state } = useStatus();
    state.data = {
      version: 'v0.6.11', platform: 'openwrt', backendName: 'openwrt', authEnabled: false,
      nativeAWG3: false, mtSources: true, pools: [],
    } as Status;
    await nextTick();
    expect(w.text()).toContain('openwrt · mawg v0.6.11');
    w.unmount();
  });
});
