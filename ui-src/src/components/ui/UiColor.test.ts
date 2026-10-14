import { beforeEach, describe, expect, it } from 'vitest'
import { nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import UiColor from './UiColor.vue'

const mk = (modelValue = '#4a9eff', bare = true) =>
  mount(UiColor, { props: { modelValue, bare, 'onUpdate:modelValue': (v: string) => w.setProps({ modelValue: v }) } });
let w = mk();

beforeEach(() => {
  w = mk();
});

describe('UiColor - кнопка-свотч', () => {
  it('заливка текущим цветом, дропдаун закрыт', () => {
    const btn = w.find('.cbtn');
    expect(btn.attributes('style')).toContain('rgb(74, 158, 255)'); // jsdom сериализует в rgb
    expect(w.find('.cpop').exists()).toBe(false);
    w.unmount();
  });

  it('bare-режим - без UiField, labeled - с подписью', () => {
    expect(w.find('.uifield').exists()).toBe(false);
    const labeled = mount(UiColor, { props: { modelValue: '#4a9eff', label: 'Цвет' } });
    expect(labeled.find('.uifield').exists()).toBe(true);
    expect(labeled.find('.lbl').text()).toBe('Цвет');
    labeled.unmount();
    w.unmount();
  });

  it('пресет: update:modelValue + закрытие дропдауна', async () => {
    await w.find('.cbtn').trigger('click');
    expect(w.find('.cpop').exists()).toBe(true);
    const swatches = w.findAll('.swatch');
    expect(swatches.length).toBe(16);
    await swatches[2].trigger('click'); // #2AABEE
    expect(w.emitted('update:modelValue')![0]).toEqual(['#2aabee']);
    expect(w.find('.cpop').exists()).toBe(false);
    w.unmount();
  });
});

describe('UiColor - пикер', () => {
  it('SV-квадрат: фон из текущего оттенка, драг меняет значение', async () => {
    await w.setProps({ modelValue: '#ff0000' }); // h=0, s=1, v=1
    await w.find('.cbtn').trigger('click');
    await nextTick();
    const sv = w.find('.sv');
    expect(sv.exists()).toBe(true);
    // драг в левый нижний угол (кликом - pointerdown сразу применяет)
    const rect = { left: 0, top: 0, width: 100, height: 100 };
    vi_spyRect(sv, rect);
    await sv.trigger('pointerdown', { pointerId: 1, clientX: 0, clientY: 100 });
    const emitted = w.emitted('update:modelValue')!;
    expect(emitted[emitted.length - 1]).toEqual(['#000000']); // s=0, v=0
    w.unmount();
  });

  it('полоса оттенка: клик меняет hue с сохранением s/v', async () => {
    await w.setProps({ modelValue: '#4a9eff' }); // h=210 s=.71 v=1
    await w.find('.cbtn').trigger('click');
    await nextTick();
    vi_spyRect(w.find('.hue'), { left: 0, top: 0, width: 360, height: 14 });
    await w.find('.hue').trigger('pointerdown', { pointerId: 1, clientX: 120, clientY: 7 });
    const emitted = w.emitted('update:modelValue')!;
    expect(emitted[emitted.length - 1][0]).toMatch(/^#[0-9a-f]{6}$/);
    // hue = 120 (зелёный), s/v прежние
    expect(emitted[emitted.length - 1][0]).toBe('#4aff4a');
    w.unmount();
  });

  it('hex-поле: валидный ввод эмитит, мусор - только подсветка', async () => {
    await w.find('.cbtn').trigger('click');
    const hex = w.find('.hex');
    await hex.setValue('#e0564b');
    expect(w.emitted('update:modelValue')!.at(-1)).toEqual(['#e0564b']);
    await hex.setValue('мусор');
    await nextTick();
    expect(hex.classes()).toContain('bad');
    expect(w.emitted('update:modelValue')).toHaveLength(1); // мусор не эмитит
    w.unmount();
  });
});

// jsdom getBoundingClientRect даёт нули - подменяем на заданный прямоугольник
function vi_spyRect(wrapper: { element: Element }, rect: { left: number; top: number; width: number; height: number }): void {
  wrapper.element.getBoundingClientRect = () => rect as DOMRect;
}
