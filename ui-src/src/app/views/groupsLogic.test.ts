import { describe, expect, it } from 'vitest'
import type { GroupView, Rule } from '../../api/types'
import {
  backupKind, cascadeOptions, cyclesValid, degradedText, dedupImportRules, dupsEntries, exportStamp, groupHasDup, importVerdict,
  groupMatchCount, groupMatches, hideOffGroups, hideOffRules, hlSegs, normRuleValue, parseSources,
  parseImportFile, presetRulesLine, showAllRules, srcBadge, srcListInitial,
  srcModeInitial, warnTitle, wireCascadeSel,
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

describe('источники (Ф3.7б)', () => {
  it('srcModeInitial: политика, фолбэк на фактические sources, каскад - all', () => {
    expect(srcModeInitial(g({ policy: { sourcesMode: 'list' } }))).toBe('list');
    expect(srcModeInitial(g({ sources: ['1.1.1.1'] }))).toBe('list');
    expect(srcModeInitial(g({}))).toBe('all');
    expect(srcModeInitial(g({ cascade: { group: 'g', source: 'a', via: 'b' } }))).toBe('all');
  });
  it('srcListInitial/parseSources: политика или фактические, split по запятым и строкам', () => {
    expect(srcListInitial(g({ policy: { sources: ['1.1.1.1'] }, sources: ['2.2.2.2'] }))).toBe('1.1.1.1');
    expect(srcListInitial(g({ sources: ['2.2.2.2'] }))).toBe('2.2.2.2');
    expect(parseSources(' 1.1.1.1, 2.2.2.0/24' + String.fromCharCode(10) + '3.3.3.3 ,')).toEqual(['1.1.1.1', '2.2.2.0/24', '3.3.3.3']);
  });
});

describe('пресеты (Ф3.7б)', () => {
  it('presetRulesLine: сводка с метками типов', () => {
    const p = { title: 'Telegram', rules: [{ type: 'domain' }, { type: 'namespace' }, { type: 'subnet' }, { type: 'subnet' }, { type: 'regex' }] };
    const line = presetRulesLine(p, 'x');
    expect(line).toContain('Telegram - правил: 5');
    expect(line).toContain('домен+поддомены: 1');
    expect(line).toContain('подсети IPv4: 2');
    expect(line).toContain('regex: 1');
    expect(presetRulesLine(null, 'p9')).toBe('Шаблон: p9');
  });
});

describe('каскады (Ф3.7б)', () => {
  it('cascadeOptions: пулы + интерфейсы без blackhole, лейбл с титулом', () => {
    const opts = cascadeOptions([{ name: 'proton' }], [{ device: 'nwg0', title: 'пул proton' }, { device: 'blackhole' }]);
    expect(opts).toEqual([
      { ref: 'pool:proton', label: 'пул proton' },
      { ref: 'iface:nwg0', label: 'интерфейс nwg0 - пул proton' },
    ]);
  });
  it('wireCascadeSel: дизейблит выбор соседа, уезжает с занятого на первый разрешённый', () => {
    const opts = [
      { ref: 'pool:a', label: 'A' },
      { ref: 'pool:b', label: 'B' },
      { ref: 'iface:x', label: 'X' },
    ];
    const r1 = wireCascadeSel(opts, 'pool:a', '');
    expect(r1.value).toBe('pool:b'); // первый не занятый
    expect(r1.opts.find((o) => o.ref === 'pool:a')!.disabled).toBe(true);
    const r2 = wireCascadeSel(opts, 'iface:x', 'iface:x');
    expect(r2.value).toBe('pool:a'); // текущее занято соседом - уехали
    const r3 = wireCascadeSel(opts, '', 'pool:b');
    expect(r3.value).toBe('pool:b'); // разрешённое текущее остаётся
  });
});

describe('импорт/выгрузка файлом (Ф3.7в)', () => {
  const good = JSON.stringify({ groups: [
    { name: 'tg', color: '#112233', interface: 'nwg0', enable: true, rules: [
      { type: 'domain', rule: 't.me', enable: true },
      { type: 'domain', rule: 'T.ME', enable: true },
      { type: 'subnet', rule: '1.2.3.4', enable: false, name: 'n1' },
      { type: 'domain', rule: '  ', enable: true },
    ] },
    { name: 'bad', interface: '', rules: [] },
    { interface: 'nwg0', rules: [] },
    { name: 'empty', interface: 'x', rules: [{ type: 'domain', rule: 'a.com', enable: true }] },
  ] });

  it('parseImportFile: mt-config отвергается, мусор/без групп - ошибки', () => {
    expect(parseImportFile('mt-config-2026.conf', '{}').error).toContain('сырой MT-конфиг');
    expect(parseImportFile('x.json', 'не json').error).toContain('не похож на mtrickle/json');
    expect(parseImportFile('x.json', '{}').error).toContain('нет групп');
    expect(parseImportFile('x.json', '{"groups":[]}').error).toContain('нет групп');
  });
  it('parseImportFile: пригодные группы, счётчик непригодных, пустые правила выкинуты', () => {
    const r = parseImportFile('groups.mtrickle', good);
    expect(r.error).toBeUndefined();
    expect(r.bad).toBe(2);
    expect(r.groups).toHaveLength(2);
    const tg = r.groups![0];
    expect(tg.name).toBe('tg');
    expect(tg.rules).toHaveLength(3); // пустой паттерн выкинут
    expect(tg.rules[2]).toEqual({ type: 'subnet', rule: '1.2.3.4', enable: false, name: 'n1' });
    expect(parseImportFile('x.json', '{"groups":[{"name":"a"}]}').error).toContain('нет групп с именем и интерфейсом');
  });
  it('dedupImportRules: ключ type+rule.toLowerCase, счётчик в отчёт', () => {
    const src = [{ name: 'tg', color: '', interface: 'i', enable: true, rules: [
      { type: 'domain', rule: 'a.com', enable: true },
      { type: 'domain', rule: 'A.COM ', enable: true },
      { type: 'domain', rule: 'b.com', enable: true },
    ] }];
    const { groups, dedup } = dedupImportRules(src);
    expect(dedup).toBe(1);
    expect(groups[0].rules.map((r) => r.rule)).toEqual(['a.com', 'b.com']);
  });
  it('importVerdict: add с пропущенными дублимами и replace', () => {
    expect(importVerdict('add', { added: 2, rules: 5, skipped: ['tg'] }, 3)).toBe('Импортировано групп: 2 (правил: 5, вычищено дубликатов: 3). Пропущены дубли имени: tg');
    expect(importVerdict('add', { added: 1, rules: 3 }, 0)).toBe('Импортировано групп: 1 (правил: 3)');
    expect(importVerdict('replace', { added: 4, rules: 9 }, 0)).toBe('Список заменён: 4 групп (правил: 9)');
  });
  it('exportStamp/backupKind', () => {
    expect(exportStamp(new Date(2026, 9, 14, 7, 5, 3))).toBe('20261014-070503');
    expect(backupKind('mt-config-2026.conf').label).toBe('MT-конфиг');
    expect(backupKind('mt-config-x.conf').restoreAsk).toContain('MagiTrickle перезапустится');
    expect(backupKind('mt-shadow-x.conf').restoreAsk).toContain('ВСЕ группы будут заменены');
  });
});

describe('чекер скрытия выключенного (80-док, бонус)', () => {
  const rules: Rule[] = [
    { id: 'r1', type: 'domain', rule: 'a.com', enable: true },
    { id: 'r2', type: 'domain', rule: 'b.com', enable: false },
    { id: 'r3', type: 'domain', rule: 'c.com', enable: true },
  ];
  const groups = [
    g({ id: 'on', name: 'включённая', enable: true, rules }),
    g({ id: 'off', name: 'выключенная', enable: false, rules }),
  ];

  it('hideOffGroups: hide=true убирает только выключенные, hide=false - как было', () => {
    expect(hideOffGroups(groups, true).map((x) => x.id)).toEqual(['on']);
    expect(hideOffGroups(groups, false)).toEqual(groups);
    expect(hideOffGroups([], true)).toEqual([]);
  });

  it('hideOffRules: hide=true убирает выключенные правила, порядок сохраняется', () => {
    expect(hideOffRules(rules, true).map((r) => r.rule)).toEqual(['a.com', 'c.com']);
    expect(hideOffRules(rules, false)).toEqual(rules);
    expect(hideOffRules([], true)).toEqual([]);
  });

  it('композиция с поиском: сначала поиск, потом скрытие выключенного', () => {
    // как во вьюхе: base = поиск, затем hideOff
    const base = groups.filter((x) => groupMatches(x, 'включённая'));
    expect(base.map((x) => x.id)).toEqual(['on']);
    const baseAll = groups.filter((x) => groupMatches(x, 'выключенная'));
    expect(baseAll.map((x) => x.id)).toEqual(['off']);
    expect(hideOffGroups(baseAll, true)).toEqual([]);
  });
});
