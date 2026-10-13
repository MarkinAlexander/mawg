import { describe, expect, it } from 'vitest'
import type { GroupView } from '../../api/types'
import {
  cyclesValid, degradedText, dupsEntries, groupHasDup, groupMatchCount,
  groupMatches, hlSegs, normRuleValue, showAllRules, srcBadge, warnTitle,
} from './groupsLogic'

function g(over: Partial<GroupView>): GroupView {
  return { id: 'g', name: 'n', interface: 'i', enable: true, rules: [], ...over };
}

describe('hlSegs', () => {
  it('режет до/совпадение/после без регистрозависимости', () => {
    expect(hlSegs('Telegram Rules', 'gram')).toEqual([
      { text: 'Tele', hit: false },
      { text: 'gram', hit: true },
      { text: ' Rules', hit: false },
    ]);
  });
  it('без совпадения и без запроса - один сегмент', () => {
    expect(hlSegs('abc', 'zz')).toEqual([{ text: 'abc', hit: false }]);
    expect(hlSegs('abc', '')).toEqual([{ text: 'abc', hit: false }]);
  });
});

describe('поиск', () => {
  const rl = (over: Partial<{ rule: string; name: string }>) => ({ id: 'r', type: 'domain', rule: '', enable: true, ...over });
  it('ruleMatches по правилу и имени, groupMatches по имени группы', () => {
    expect(groupMatches(g({ name: 'tg', rules: [rl({ rule: 'x.com' })] }), 'x.co')).toBe(true);
    expect(groupMatches(g({ name: 'tg' }), 't')).toBe(true);
    expect(groupMatches(g({ name: 'tg' }), 'zz')).toBe(false);
    expect(groupMatchCount(g({ name: 'tg', rules: [rl({ rule: 'tg.com' }), rl({ rule: 'zz' })] }), 'tg')).toBe(2);
  });
  it('showAllRules: имя поймано, но правил не нашлось - группа целиком', () => {
    const grp = g({ name: 'tg', rules: [rl({ rule: 'x.com' })] });
    expect(showAllRules(grp, 'tg')).toBe(true); // совпало только имя
    expect(showAllRules(grp, 'x.co')).toBe(false); // есть совпадение правила
    expect(showAllRules(grp, '')).toBe(true); // без поиска - всё
  });
});

describe('normRuleValue', () => {
  it('subnet без маски получает /32, остальное не трогается', () => {
    expect(normRuleValue('subnet', '1.2.3.4')).toBe('1.2.3.4/32');
    expect(normRuleValue('subnet', '1.2.3.0/24')).toBe('1.2.3.0/24');
    expect(normRuleValue('domain', '1.2.3.4')).toBe('1.2.3.4');
  });
});

describe('warnTitle/groupHasDup', () => {
  it('тултип перечисляет другие группы с типами', () => {
    const t = warnTitle([
      { groupName: 'ai', ruleType: 'domain' },
      { group: 'backup', type: 'subnet' },
    ]);
    expect(t).toContain('ai (домен)');
    expect(t).toContain('backup (подсеть)');
    expect(t).toContain('применит только первое');
  });
  it('groupHasDup - только по включённым правилам', () => {
    const dups = { 'x.com': [{ groupName: 'ai' }] };
    expect(groupHasDup(g({ rules: [{ id: 'r', type: 'domain', rule: 'x.com', enable: true }] }), dups)).toBe(true);
    expect(groupHasDup(g({ rules: [{ id: 'r', type: 'domain', rule: 'x.com', enable: false }] }), dups)).toBe(false);
  });
});

describe('srcBadge', () => {
  it('режимы политики и фолбэк на фактические sources', () => {
    expect(srcBadge(g({ policy: { sourcesMode: 'all+router' } }))!.text).toBe('источники: локалка + роутер');
    expect(srcBadge(g({ policy: { sourcesMode: 'list', sources: ['1.1.1.1'], sourcesRouter: true }, sources: ['1.1.1.1'] }))!.text).toBe('источники: IP-список (1) + роутер');
    expect(srcBadge(g({ sources: ['1.1.1.1', '2.2.2.2'] }))!.text).toBe('источники: IP-список (2)');
    expect(srcBadge(g({ policy: { sourcesMode: 'all' } }))).toBeNull();
  });
});

describe('degradedText', () => {
  it('тексты деградации и фолбэк для iface:xxx', () => {
    expect(degradedText(g({ degraded: 'direct' }))).toContain('идет напрямую');
    expect(degradedText(g({ degraded: 'direct-pending' }))).toContain('ждет возврата');
    expect(degradedText(g({ degraded: 'iface:nwg7' }))).toBe('временно переключена на запасной интерфейс');
    expect(degradedText(g({ degraded: 'что-то новое' }))).toBe('состояние: что-то новое');
    expect(degradedText(g({}))).toBe('');
  });
});

describe('dupsEntries', () => {
  const groups = [
    g({ id: '1', name: 'tg', rules: [{ id: 'r1', type: 'domain', rule: 't.me', enable: true }] }),
    g({ id: '2', name: 'ai', rules: [{ id: 'r2', type: 'domain', rule: 't.me', enable: true }, { id: 'r3', type: 'wildcard', rule: '*.openai.com', enable: true }] }),
  ];
  it('без поиска - межгрупповые из /mt/duplicates, с бейджем дубликата', () => {
    const list = dupsEntries(groups, { 't.me': [{ groupName: 'tg', ruleType: 'domain' }, { groupName: 'ai', ruleType: 'domain' }] }, '');
    expect(list).toHaveLength(1);
    expect(list[0]).toEqual({ pat: 't.me', types: 'домен', groups: ['tg', 'ai'], dup: true });
  });
  it('с поиском - все вхождения найденных паттернов по группам', () => {
    const list = dupsEntries(groups, {}, 't.me');
    expect(list).toEqual([{ pat: 't.me', types: 'домен', groups: ['tg', 'ai'], dup: true }]);
    expect(dupsEntries(groups, {}, 'openai')[0].dup).toBe(false); // одна группа - не дубликат
    expect(dupsEntries(groups, {}, '')).toEqual([]); // без дубликатов - пусто
  });
});

describe('cyclesValid', () => {
  it('целые 1..20, иначе false', () => {
    expect(cyclesValid('2', '2')).toBe(true);
    expect(cyclesValid('20', '1')).toBe(true);
    expect(cyclesValid('0', '2')).toBe(false);
    expect(cyclesValid('21', '2')).toBe(false);
    expect(cyclesValid('2a', '2')).toBe(false);
    expect(cyclesValid('', '2')).toBe(false);
  });
});
