import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import SystemView from './SystemView.vue'
import { useEvents } from '../../stores/events'

function ev(time: string, pool: string, kind: string, message: string) {
  return { time, pool, kind, message };
}

function eventsStub(list: unknown[]) {
  return vi.fn(async (url: string) => {
    expect(url).toBe('/api/v1/events');
    return new Response(JSON.stringify({ events: list }), { status: 200 });
  });
}

beforeEach(() => {
  vi.unstubAllGlobals();
  // сброс синглтон-стора: хвост и живой таймер прошлых тестов
  useEvents().state.list = [];
  useEvents().stop();
});

describe('SystemView - журнал событий', () => {
  it('новые сверху, формат строки и k-цвета пяти видов', async () => {
    vi.stubGlobal('fetch', eventsStub([
      ev('2026-10-13T10:00:01Z', 'warp', 'rotate', 'старый конфиг'),
      ev('2026-10-13T10:00:02Z', 'warp', 'applied', 'конфиг применён'),
      ev('2026-10-13T10:00:03Z', 'subs', 'fallback', 'прямой доступ'),
      ev('2026-10-13T10:00:04Z', 'magitrickle', 'magitrickle', 'группы записаны'),
      ev('2026-10-13T10:00:05Z', 'free', 'install', 'установлено'),
    ]));
    const w = mount(SystemView);
    await vi.waitFor(() => expect(w.findAll('.log > div')).toHaveLength(5));
    const rows = w.findAll('.log > div');
    // легаси slice(-80).reverse(): последняя запись сверху
    expect(rows[0].text()).toContain('install');
    expect(rows[4].text()).toContain('rotate');
    for (const [i, kind] of ['install', 'magitrickle', 'fallback', 'applied', 'rotate'].entries()) {
      expect(rows[i].find('.k-' + kind).exists()).toBe(true);
    }
    // формат [время] pool: kind message
    expect(rows[0].text()).toMatch(/^\[\d{2}:\d{2}:\d{2}\] free: install установлено$/);
    w.unmount();
  });

  it('хвост 80 записей (легаси slice(-80)), экранирование HTML из событий', async () => {
    const list = Array.from({ length: 85 }, (_, i) =>
      ev(`2026-10-13T00:00:${String(i % 60).padStart(2, '0')}Z`, 'p', 'rotate', 'msg' + i));
    list[84] = ev('2026-10-13T00:01:24Z', 'p', 'rotate', '<b>xss</b> & "quotes"');
    vi.stubGlobal('fetch', eventsStub(list));
    const w = mount(SystemView);
    await vi.waitFor(() => expect(w.findAll('.log > div')).toHaveLength(80));
    const rows = w.findAll('.log > div');
    expect(rows[0].text()).toContain('<b>xss</b>'); // events[84] - сверху
    expect(rows[1].text()).toContain('msg83');
    expect(rows[79].text()).toContain('msg5'); // events[5] - первый из хвоста
    // интерполяция Vue экранирует: тега нет, текст виден как текст
    expect(rows[0].find('b').exists()).toBe(false);
    w.unmount();
  });

  it('опрос 15с, stop при размонтировании', async () => {
    vi.useFakeTimers();
    try {
      const f = eventsStub([ev('2026-10-13T10:00:00Z', 'p', 'rotate', 'm')]);
      vi.stubGlobal('fetch', f);
      const w = mount(SystemView);
      await vi.advanceTimersByTimeAsync(0);
      expect(f).toHaveBeenCalledTimes(1);
      await vi.advanceTimersByTimeAsync(15000);
      expect(f).toHaveBeenCalledTimes(2);
      await vi.advanceTimersByTimeAsync(15000);
      expect(f).toHaveBeenCalledTimes(3);
      w.unmount();
      await vi.advanceTimersByTimeAsync(60000);
      expect(f).toHaveBeenCalledTimes(3); // после unmount тишина
    } finally {
      vi.useRealTimers();
    }
  });

  it('свёртывание карточки прячет журнал (UiCard - контролируемый, нужен v-model)', async () => {
    vi.stubGlobal('fetch', eventsStub([ev('2026-10-13T10:00:00Z', 'p', 'rotate', 'm')]));
    const w = mount(SystemView);
    await vi.waitFor(() => expect(w.findAll('.log > div')).toHaveLength(1));
    // легаси details: свёрнуто по клику, разворачивается обратно.
    // v-show гасит .body inline-стилем; isVisible() в jsdom этой среды
    // ненадёжен (врёт на предках и после снятия) - проверяем стиль
    const bodyStyle = () => w.find('.body').attributes('style') ?? '';
    expect(bodyStyle()).not.toContain('none');
    await w.find('.fold').trigger('click');
    expect(bodyStyle()).toContain('display: none');
    await w.find('.fold').trigger('click');
    expect(bodyStyle()).not.toContain('none');
    w.unmount();
  });
});
