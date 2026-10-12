// Вкладки нового UI. Информационная архитектура - Фаза 4 в
// local-docs/44-ui-rewrite-plan.md; до паритетного переноса карточек
// (Фаза 3) вкладки - заглушки, рабочая панель - legacy на /.
export interface TabDef {
  id: string
  label: string
}

export const TABS: TabDef[] = [
  { id: 'overview', label: 'Обзор' },
  { id: 'groups', label: 'Группы правил' },
  { id: 'interfaces', label: 'Интерфейсы' },
  { id: 'backups', label: 'Резервные копии' },
  { id: 'dns', label: 'DNS' },
  { id: 'system', label: 'Система' },
  { id: 'settings', label: 'Настройки' },
]
