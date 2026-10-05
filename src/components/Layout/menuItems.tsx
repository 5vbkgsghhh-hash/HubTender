import type { MenuProps } from 'antd';
import {
  DashboardOutlined,
  ShoppingCartOutlined,
  BookOutlined,
  BankOutlined,
  DollarOutlined,
  SettingOutlined,
  UserOutlined,
  ProfileOutlined,
  FileTextOutlined,
  PercentageOutlined,
  CheckSquareOutlined,
  ClockCircleOutlined,
  BarChartOutlined,
  FundOutlined,
  LinkOutlined,
  OrderedListOutlined,
  DiffOutlined,
  FileExcelOutlined,
  LineChartOutlined,
  SwapOutlined,
  BuildOutlined,
  ImportOutlined,
  SafetyCertificateOutlined,
  SafetyOutlined,
  RobotOutlined,
  ApiOutlined,
} from '@ant-design/icons';

/** Статическое дерево бокового меню. Фильтрация по правам/устройству — в MainLayout. */
export const menuItems: NonNullable<MenuProps['items']> = [
  {
    key: '/dashboard',
    icon: <DashboardOutlined />,
    label: 'Дашборд',
  },
  {
    key: '/positions',
    icon: <ShoppingCartOutlined />,
    label: 'Позиции заказчика',
  },
  {
    key: 'tender-data-group',
    icon: <FileTextOutlined />,
    label: 'Данные по тендерам',
    children: [
      {
        key: '/tenders',
        icon: <FileTextOutlined />,
        label: 'Перечень тендеров',
      },
      {
        key: '/tender-timeline',
        icon: <ClockCircleOutlined />,
        label: 'Хронология расчёта',
      },
    ],
  },
  {
    key: '/tasks',
    icon: <CheckSquareOutlined />,
    label: 'Список задач',
  },
  {
    key: 'commerce-group',
    icon: <DollarOutlined />,
    label: 'Коммерция',
    children: [
      {
        key: '/commerce/proposal',
        icon: <FileTextOutlined />,
        label: 'Форма КП',
      },
      {
        key: '/commerce/redistribution',
        icon: <SwapOutlined />,
        label: 'Перераспределение',
      },
    ],
  },
  {
    key: 'library',
    icon: <BookOutlined />,
    label: 'Библиотеки',
    children: [
      {
        key: '/library',
        icon: <BookOutlined />,
        label: 'Материалы и работы',
      },
      {
        key: '/library/templates',
        icon: <ProfileOutlined />,
        label: 'Шаблоны',
      },
      {
        key: '/admin/nomenclatures',
        icon: <ProfileOutlined />,
        label: 'Номенклатуры',
      },
      {
        key: '/admin/construction_cost',
        icon: <BankOutlined />,
        label: 'Справочник затрат',
      },
    ],
  },
  {
    key: '/financial-indicators',
    icon: <BarChartOutlined />,
    label: 'Финансовые показатели',
  },
  {
    key: '/data-quality',
    icon: <SafetyCertificateOutlined />,
    label: 'Проверка данных',
  },
  {
    key: 'analytics',
    icon: <FundOutlined />,
    label: 'Аналитика',
    children: [
      {
        key: '/analytics/quality',
        icon: <SafetyCertificateOutlined />,
        label: 'Качество расчёта',
      },
      {
        key: '/analytics/price-benchmark',
        icon: <FundOutlined />,
        label: 'Ценовые отклонения',
      },
      {
        key: '/analytics/price-sources',
        icon: <LinkOutlined />,
        label: 'Источники цен',
      },
      {
        key: '/analytics/action-plan',
        icon: <OrderedListOutlined />,
        label: 'План действий',
      },
      {
        key: '/analytics/change-impact',
        icon: <DiffOutlined />,
        label: 'Изменения расчёта',
      },
      {
        key: '/analytics/review-pack',
        icon: <FileExcelOutlined />,
        label: 'Отчёт для проверки',
      },
      {
        key: '/costs',
        icon: <DollarOutlined />,
        label: 'Затраты на строительство',
      },
      {
        key: '/analytics/comparison',
        icon: <LineChartOutlined />,
        label: 'Сравнение объектов',
      },
      {
        key: '/bsm',
        icon: <FileTextOutlined />,
        label: 'Базовая стоимость',
      },
      {
        key: '/projects',
        icon: <BuildOutlined />,
        label: 'Текущие объекты',
      },
    ],
  },
  {
    key: 'admin',
    icon: <SafetyOutlined />,
    label: 'Администрирование',
    children: [
      {
        key: '/admin/tenders',
        icon: <FileTextOutlined />,
        label: 'Тендеры',
      },
      {
        key: '/admin/markup',
        icon: <PercentageOutlined />,
        label: 'Проценты наценок',
      },
      {
        type: 'divider',
      },
      {
        key: '/admin/markup_constructor',
        icon: <PercentageOutlined />,
        label: 'Конструктор наценок',
      },
      {
        type: 'divider',
      },
      {
        key: '/admin/ai-settings',
        icon: <RobotOutlined />,
        label: 'AI и нейросети',
      },
    ],
  },
  {
    key: '/users',
    icon: <UserOutlined />,
    label: 'Пользователи',
  },
  {
    key: 'settings-group',
    icon: <SettingOutlined />,
    label: 'Настройки',
    children: [
      {
        key: '/settings/agents',
        icon: <RobotOutlined />,
        label: 'Подключённые агенты',
      },
      {
        key: '/admin/import-log',
        icon: <ImportOutlined />,
        label: 'Журнал импортов строк',
      },
      {
        key: '/admin/insurance',
        icon: <SafetyCertificateOutlined />,
        label: 'Страхование от судимостей',
      },
      {
        key: '/admin/api-access',
        icon: <ApiOutlined />,
        label: 'Доступ к API',
      },
    ],
  },
];

/**
 * Ключи листовых страниц, скрываемых из меню на телефонах (Android/iPhone).
 * Скрываем по конечным путям (не по ключам групп), чтобы фильтр не зависел
 * от схлопывания групп в filterMenuByAccess.
 */
export const MOBILE_HIDDEN_KEYS = new Set<string>([
  // Администрирование
  '/admin/tenders',
  '/admin/markup',
  '/admin/markup_constructor',
  // Библиотеки
  '/library',
  '/library/templates',
  '/admin/nomenclatures',
  '/admin/construction_cost',
  // Список задач
  '/tasks',
  // Настройки
  '/admin/import-log',
  '/admin/insurance',
  '/admin/api-access',
]);
