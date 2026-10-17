// Реестр «мест» для поиска по настройкам (46-док §1, первый пункт Фазы 4:
// лупа в шапке + Ctrl+K, фильтрация по подстроке, переход + скролл к якорю).
// Наполняется вручную по мере появления карточек; заглушки вкладок (Обзор,
// DNS) и демо /next/kit не включаем - в выдаче пустышки мешают.
// Якоря - id на UiCard вьюх (fallthrough на section.card).
export interface SearchPlace {
  title: string
  keywords: string[]
  route: string
  anchor?: string
}

export const SEARCH_PLACES: SearchPlace[] = [
  // --- Интерфейсы ---
  {
    title: 'Интерфейсы роутера (AWG/WireGuard)',
    keywords: ['интерфейсы', 'awg', 'amnezia', 'амнезия', 'wireguard', 'туннели', 'подключения'],
    route: '/next/interfaces',
    anchor: 'card-ifaces',
  },
  {
    title: 'Пулы конфигов: загрузка, premium, слоты',
    keywords: ['пулы', 'конфиги', 'слоты', 'premium', 'премиум', 'pool'],
    route: '/next/interfaces',
  },
  {
    title: 'Наборы интерфейсов (ротация)',
    keywords: ['наборы', 'бандлы', 'ротация', 'bundles', 'порядок интерфейсов'],
    route: '/next/interfaces',
    anchor: 'card-bundles',
  },
  {
    title: 'Проверка канала провайдера (WAN-probe)',
    keywords: ['wan', 'probe', 'проба', 'канал', 'провайдер', 'интернет', 'живость'],
    route: '/next/interfaces',
    anchor: 'card-probe',
  },
  // --- Группы правил ---
  {
    title: 'Правила маршрутизации (группы MagiTrickle)',
    keywords: ['группы правил', 'правила', 'mt', 'magitrickle', 'магитрикл', 'ipset', 'маршрутизация', 'туннель'],
    route: '/next/groups',
    anchor: 'card-rules',
  },
  {
    title: 'Пресеты: шаблон правил на интерфейс',
    keywords: ['пресет', 'шаблон', 'allow-domains', 'itdoginfo', 'заготовка', 'preset'],
    route: '/next/groups',
    anchor: 'card-rules',
  },
  {
    title: 'Каскады: трафик одного подключения через другое',
    keywords: ['каскад', 'посредник', 'цепочка', 'cascade'],
    route: '/next/groups',
    anchor: 'card-rules',
  },
  {
    title: 'Политики отвала и гистерезис',
    keywords: ['гистерезис', 'политика', 'отвал', 'циклы', 'blackhole', 'черная дыра', 'чёрная дыра'],
    route: '/next/groups',
    anchor: 'card-rules',
  },
  {
    title: 'Поиск и дубликаты правил',
    keywords: ['поиск', 'дубликаты', 'повторы', 'фильтр'],
    route: '/next/groups',
    anchor: 'card-rules',
  },
  {
    title: 'Резервные копии: полный бэкап и восстановление',
    keywords: ['бэкап', 'резерв', 'копия', 'восстановить', 'backup', 'архив', 'tar', 'настройки панели', 'порт'],
    route: '/next/backups',
    anchor: 'card-backups',
  },
  {
    title: 'Импорт и выгрузка групп файлом',
    keywords: ['импорт', 'экспорт', 'выгрузка', 'файл', '.mtrickle', 'перенос'],
    route: '/next/backups',
    anchor: 'card-transfer',
  },
  // --- Система ---
  {
    title: 'Система: зависимости и обновление',
    keywords: ['система', 'зависимости', 'обновление', 'provision', 'sing-box', 'сингбокс', 'версия'],
    route: '/next/system',
    anchor: 'card-system',
  },
  {
    title: 'Журнал событий',
    keywords: ['события', 'журнал', 'лог', 'логи', 'events'],
    route: '/next/system',
    anchor: 'card-events',
  },
  // --- Настройки ---
  {
    title: 'Настройки панели',
    keywords: ['настройки', 'сервер', 'панель', 'settings'],
    route: '/next/settings',
    anchor: 'card-settings',
  },
  {
    title: 'Авторизация: вход по паролю',
    keywords: ['авторизация', 'вход', 'логин', 'учётка', 'учетка', 'auth'],
    route: '/next/settings',
    anchor: 'card-settings',
  },
  {
    title: 'Разрешённые адреса (allowlist)',
    keywords: ['allowlist', 'адреса', 'доступ', 'подсеть', 'белый список', 'разрешенные', 'разрешённые'],
    route: '/next/settings',
    anchor: 'card-settings',
  },
  {
    title: 'Сеть: адрес и порт панели',
    keywords: ['сеть', 'адрес', 'порт', 'listen', 'перезапуск'],
    route: '/next/settings',
    anchor: 'card-settings',
  },
  {
    title: 'RCI-токен Keenetic',
    keywords: ['rci', 'токен', 'keenetic', 'кинетик', 'ndm'],
    route: '/next/settings',
    anchor: 'card-settings',
  },
  {
    title: 'Смена пароля',
    keywords: ['пароль', 'смена пароля', 'password'],
    route: '/next/settings',
    anchor: 'card-settings',
  },
]

// Фильтрация по подстроке без регистра (кириллица, без фаззи - 46-док):
// пустой/пробельный запрос - весь список в авторском порядке; совпадение
// в title ранжируется выше совпадения в keywords, внутри ранга - авторский
// порядок реестра.
export function filterPlaces(qRaw: string, places: SearchPlace[] = SEARCH_PLACES): SearchPlace[] {
  const q = qRaw.trim().toLowerCase();
  if (!q) return places.slice();
  const hits: { place: SearchPlace; rank: number; idx: number }[] = [];
  places.forEach((place, idx) => {
    const inTitle = place.title.toLowerCase().includes(q);
    const inKeywords = place.keywords.some((k) => k.toLowerCase().includes(q));
    if (inTitle || inKeywords) hits.push({ place, rank: inTitle ? 0 : 1, idx });
  });
  return hits.sort((a, b) => a.rank - b.rank || a.idx - b.idx).map((h) => h.place);
}
