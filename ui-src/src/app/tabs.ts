// Вкладки нового UI. Информационная архитектура - Фаза 4 в
// local-docs/44-ui-rewrite-plan.md; с Ф3.9 новое UI - единственная панель.
export interface TabDef {
  id: string
  label: string
}

export const TABS: TabDef[] = [
  { id: 'overview', label: 'Обзор' },
  // слово владельца: интерфейсы раньше групп - сначала подключения,
  // потом правила к ним
  { id: 'interfaces', label: 'Интерфейсы' },
  { id: 'groups', label: 'Группы правил' },
  { id: 'backups', label: 'Резервные копии' },
  { id: 'dns', label: 'DNS' },
  { id: 'system', label: 'Система' },
  { id: 'settings', label: 'Настройки' },
]
