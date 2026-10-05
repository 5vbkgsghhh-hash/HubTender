// =============================================
// Типы для таблицы users (пользователи портала)
// =============================================

export type UserRole = 'Руководитель' | 'Администратор' | 'Разработчик' | 'Старший группы' | 'Инженер';
export type AccessStatus = 'pending' | 'approved' | 'blocked';

export interface UserInsert {
  id: string; // UUID from auth.users
  full_name: string;
  email: string;
  role: UserRole; // Русское название роли (для отображения)
  role_code: string; // Связь с roles.code (administrator, developer, director, engineer, senior_group, general_director)
  access_status?: AccessStatus;
  allowed_pages?: string[]; // Массив путей страниц. Пустой массив = полный доступ. Синхронизируется из roles.allowed_pages
  approved_by?: string | null;
  approved_at?: string | null;
  password?: string | null; // ВНИМАНИЕ: хранится в открытом виде (только для справки администраторов)
  access_enabled?: boolean; // Флаг доступа: true - может войти, false - доступ закрыт
}

export interface User extends UserInsert {
  role_code: string;
  access_status: AccessStatus;
  allowed_pages: string[];
  registration_date: string;
  created_at: string;
  updated_at: string;
  password: string | null;
  access_enabled: boolean;
  tender_deadline_extensions?: TenderDeadlineExtension[];
}

// Упрощенный тип пользователя для AuthContext
export interface AuthUser {
  id: string;
  email: string;
  full_name: string;
  role: UserRole;
  role_code?: string;
  role_color?: string;
  access_status: AccessStatus;
  allowed_pages: string[];
  access_enabled: boolean;
}

// =============================================
// Типы для системы управления дедлайнами
// =============================================

export interface TenderDeadlineExtension {
  tender_id: string;
  extended_deadline: string; // ISO 8601 timestamp
}

export interface DeadlineCheckResult {
  isExpired: boolean;      // Истек ли дедлайн
  canEdit: boolean;        // Может ли редактировать
  deadline: Date | null;   // Эффективный дедлайн
  isExtended: boolean;     // Продлен ли вручную
}

// =============================================
// Константы прав доступа по ролям
// =============================================

// Все страницы портала (для Transfer component и проверки доступа)
export const ALL_PAGES = [
  '/dashboard',
  '/tenders',
  '/tender-timeline',
  '/tasks',
  '/admin/nomenclatures',
  '/admin/construction_cost',
  '/admin/tenders',
  '/admin/markup_constructor',
  '/admin/markup',
  '/library',
  '/library/templates',
  '/positions',
  '/positions/:positionId/items',
  '/commerce',
  '/commerce/proposal',
  '/commerce/redistribution',
  '/costs',
  '/bsm',
  '/analytics/comparison',
  '/analytics/quality',
  '/analytics/price-benchmark',
  '/analytics/price-sources',
  '/analytics/action-plan',
  '/analytics/change-impact',
  '/analytics/review-pack',
  '/financial-indicators',
  '/projects',
  '/projects/:projectId',
  '/settings',
  '/settings/agents',
  '/users',
  '/admin/import-log',
  '/admin/insurance',
  '/admin/ai-settings',
  '/admin/api-access',
  '/data-quality',
] as const;

// Страницы по умолчанию для каждой роли
// Пустой массив = полный доступ (для Администратора, Руководителя и Разработчика)
export const DEFAULT_ROLE_PAGES: Record<UserRole, string[]> = {
  'Руководитель': [], // Полный доступ
  'Администратор': [], // Полный доступ
  'Разработчик': [], // Полный доступ (для отладки и разработки)
  'Старший группы': [
    '/dashboard',
    '/tender-timeline',
    '/tasks',
    '/positions',
    '/positions/:positionId/items',
    '/commerce',
    '/commerce/proposal',
    '/library',
    '/library/templates',
    '/costs',
    '/bsm',
    '/analytics/comparison',
    '/financial-indicators',
    '/settings',
    '/settings/agents',
  ],
  'Инженер': [
    '/dashboard',
    '/tender-timeline',
    '/tasks',
    '/positions',
    '/positions/:positionId/items',
    '/library',
    '/library/templates',
    '/bsm',
    '/settings',
    '/settings/agents',
  ],
};

// Названия страниц (соответствуют левому боковому меню)
export const PAGE_LABELS: Record<string, string> = {
  '/dashboard': 'Дашборд',
  '/tenders': 'Перечень тендеров',
  '/tender-timeline': 'Хронология расчёта тендеров',
  '/tasks': 'Список задач',
  '/positions': 'Позиции заказчика',
  '/commerce/proposal': 'Форма КП',
  '/commerce/redistribution': 'Перераспределение',
  '/library': 'Материалы и работы',
  '/library/templates': 'Шаблоны',
  '/bsm': 'Базовая стоимость',
  '/costs': 'Затраты на строительство',
  '/financial-indicators': 'Финансовые показатели',
  '/analytics/comparison': 'Сравнение объектов',
  '/projects': 'Текущие объекты',
  '/projects/:projectId': 'Детали объекта',
  '/admin/nomenclatures': 'Номенклатуры',
  '/admin/construction_cost': 'Справочник затрат',
  '/admin/tenders': 'Тендеры',
  '/admin/markup': 'Проценты наценок',
  '/admin/markup_constructor': 'Конструктор наценок',
  '/admin/import-log': 'Журнал импортов строк',
  '/admin/insurance': 'Страхование от судимостей',
  '/admin/api-access': 'Доступ к API',
  '/users': 'Пользователи',
  '/settings': 'Настройки',
  '/settings/agents': 'Подключённые агенты',
  '/positions/:positionId/items': 'Работы и материалы',
  '/commerce': 'Форма КП', // Старый путь, оставлен для совместимости
  '/data-quality': 'Проверка данных',
  '/analytics/quality': 'Качество расчёта',
  '/analytics/price-benchmark': 'Ценовые отклонения',
  '/analytics/price-sources': 'Источники цен',
  '/analytics/action-plan': 'План действий',
  '/analytics/change-impact': 'Изменения расчёта',
  '/analytics/review-pack': 'Отчёт для проверки',
  '/admin/ai-settings': 'AI и нейросети',
};

// Структура страниц с группировкой (для UI модального окна)
export const PAGES_STRUCTURE = [
  {
    title: null, // Без группы
    pages: ['/dashboard', '/positions'],
  },
  {
    title: 'Данные по тендерам',
    pages: ['/tenders', '/tender-timeline'],
  },
  {
    title: null, // Без группы
    pages: ['/tasks'],
  },
  {
    title: 'Коммерция',
    pages: ['/commerce/proposal', '/commerce/redistribution'],
  },
  {
    title: 'Библиотеки',
    pages: ['/library', '/library/templates', '/admin/nomenclatures', '/admin/construction_cost'],
  },
  {
    title: null, // Без группы
    pages: ['/financial-indicators'],
  },
  {
    title: 'Аналитика',
    pages: ['/costs', '/analytics/comparison', '/bsm', '/projects'],
  },
  {
    title: 'Администрирование',
    pages: ['/admin/tenders', '/admin/markup', '/admin/markup_constructor', '/admin/ai-settings'],
  },
  {
    title: null, // Без группы
    pages: ['/users'],
  },
  {
    title: 'Настройки',
    pages: ['/settings/agents', '/admin/import-log', '/admin/insurance', '/admin/api-access'],
  },
] as const;

// =============================================
// Вспомогательные функции для работы с пользователями
// =============================================

/**
 * Проверка, может ли пользователь управлять другими пользователями
 * (одобрять регистрации, блокировать, редактировать права)
 */
export const canManageUsers = (role: UserRole): boolean => {
  return role === 'Администратор' || role === 'Руководитель' || role === 'Разработчик';
};

/**
 * Проверка доступа пользователя к странице
 * @param user - Авторизованный пользователь
 * @param pagePath - Путь страницы (например, '/dashboard' или '/positions/123/items')
 * @returns true если пользователь имеет доступ к странице
 */
export const hasPageAccess = (user: AuthUser, pagePath: string): boolean => {
  // Администраторы и Руководители имеют полный доступ
  if (canManageUsers(user.role)) {
    return true;
  }

  // Пустой массив allowed_pages = полный доступ
  if (user.allowed_pages.length === 0) {
    return true;
  }

  // Специальная логика: если есть доступ к /positions, автоматически разрешен доступ к /positions/:positionId/items
  // Эти страницы являются одним целым - просмотр позиций и их элементов (работ и материалов)
  if (pagePath.match(/^\/positions\/[^/]+\/items$/)) {
    // Проверяем, есть ли доступ к родительской странице /positions
    if (user.allowed_pages.includes('/positions')) {
      return true;
    }
  }

  if (pagePath === '/settings/agents' && user.allowed_pages.includes('/settings')) {
    return true;
  }

  // Специальная логика: если есть доступ к /projects, автоматически разрешен доступ к /projects/:projectId
  // Эти страницы являются одним целым - просмотр списка объектов и деталей конкретного объекта
  if (pagePath.match(/^\/projects\/[^/]+$/)) {
    // Проверяем, есть ли доступ к родительской странице /projects
    if (user.allowed_pages.includes('/projects')) {
      return true;
    }
  }

  // Проверяем, соответствует ли текущий путь хотя бы одному разрешенному
  return user.allowed_pages.some((allowedPath) => {
    // Преобразуем паттерн маршрута в regex
    // Например, /positions/:positionId/items -> /positions/[^/]+/items
    const pattern = '^' + allowedPath.replace(/:[^/]+/g, '[^/]+') + '$';
    const regex = new RegExp(pattern);
    return regex.test(pagePath);
  });
};

/**
 * Проверка, является ли пользователь администратором
 */
export const isAdmin = (role: UserRole): boolean => {
  return role === 'Администратор';
};

/**
 * Проверка, является ли пользователь руководителем
 */
export const isLeader = (role: UserRole): boolean => {
  return role === 'Руководитель';
};
