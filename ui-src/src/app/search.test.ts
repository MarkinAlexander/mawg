import { describe, expect, it } from 'vitest'
import { filterPlaces, SEARCH_PLACES, type SearchPlace } from './search'

const FIX: SearchPlace[] = [
  { title: 'Журнал событий', keywords: ['события', 'лог', 'система'], route: '/next/system', anchor: 'card-events' },
  { title: 'Каскады: трафик одного подключения через другое', keywords: ['каскад', 'cascade'], route: '/next/groups' },
  { title: 'Система: зависимости и обновление', keywords: ['система', 'обновление'], route: '/next/system' },
  { title: 'Проверка канала провайдера (WAN-probe)', keywords: ['wan', 'проба'], route: '/next/interfaces' },
]

describe('filterPlaces', () => {
  it('пустой и пробельный запрос - весь список в авторском порядке', () => {
    expect(filterPlaces('', FIX)).toEqual(FIX)
    expect(filterPlaces('   ', FIX)).toEqual(FIX)
    expect(filterPlaces('', SEARCH_PLACES)).toEqual(SEARCH_PLACES)
  })

  it('подстрока в title без регистра (кириллица)', () => {
    const r = filterPlaces('КАСКАД', FIX)
    expect(r).toHaveLength(1)
    expect(r[0].title).toBe('Каскады: трафик одного подключения через другое')
  })

  it('подстрока в keywords (латинский алиас)', () => {
    expect(filterPlaces('wan', FIX).map((p) => p.title)).toEqual(['Проверка канала провайдера (WAN-probe)'])
  })

  it('совпадение в title ранжируется выше совпадения в keywords', () => {
    // 'система' есть в title «Система: ...» и в keywords «Журнал событий»
    const r = filterPlaces('система', FIX)
    expect(r).toHaveLength(2)
    expect(r[0].title).toBe('Система: зависимости и обновление')
    expect(r[1].title).toBe('Журнал событий')
  })

  it('внутри одного ранга - авторский порядок реестра', () => {
    // 'общее' только в keywords - порядок следования в реестре сохраняется
    const FIX2: SearchPlace[] = [
      { title: 'А', keywords: ['общее'], route: '/next/system' },
      { title: 'Б', keywords: ['другое'], route: '/next/groups' },
      { title: 'В', keywords: ['общее'], route: '/next/settings' },
    ]
    expect(filterPlaces('общее', FIX2).map((p) => p.title)).toEqual(['А', 'В'])
  })

  it('нет совпадений - пусто', () => {
    expect(filterPlaces('несуществующее', FIX)).toEqual([])
  })

  it('реестр: route в /next/, якоря - card-*, у записей есть keywords', () => {
    expect(SEARCH_PLACES.length).toBeGreaterThanOrEqual(15)
    for (const p of SEARCH_PLACES) {
      expect(p.route.startsWith('/next/')).toBe(true)
      expect(p.keywords.length).toBeGreaterThan(0)
      if (p.anchor) expect(p.anchor).toMatch(/^card-/)
    }
  })
})
