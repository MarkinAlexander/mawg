import { describe, expect, it } from 'vitest'
import { durToSec, healthFieldValid } from './dnsLogic'

// парсер Go-длительностей MT в секунды (поля политики автопроверки)
describe('durToSec', () => {
  it.each([
    ['10s', 10],
    ['2s', 2],
    ['0s', 0],
    ['45s', 45],
    ['5m0s', 300],
    ['1m30s', 90],
    ['1h0m0s', 3600],
    ['500ms', 1], // субсекундное округляется вверх до секунды
  ])('%s -> %i', (input, want) => {
    expect(durToSec(input)).toBe(want)
  })

  it.each([['', null], ['мусор', null], ['10', null]])('%s -> null', (input, want) => {
    expect(durToSec(input)).toBe(want)
  })
})

// диапазоны валидации полей политики автопроверки
describe('healthFieldValid', () => {
  it('интервал 5-300', () => {
    expect(healthFieldValid('5', 5, 300)).toBe(true)
    expect(healthFieldValid('300', 5, 300)).toBe(true)
    expect(healthFieldValid('4', 5, 300)).toBe(false)
    expect(healthFieldValid('301', 5, 300)).toBe(false)
  })
  it('пауза возврата 0-3600', () => {
    expect(healthFieldValid('0', 0, 3600)).toBe(true)
    expect(healthFieldValid('3601', 0, 3600)).toBe(false)
  })
  it.each([['', false], ['2a', false], ['-2', false], ['2.5', false], [' 2', false]])(
    'мусор %s отвергается',
    (v) => {
      expect(healthFieldValid(v, 0, 3600)).toBe(false)
    },
  )
})
