import { lazy } from 'react';
import { Routes, Route, Navigate } from 'react-router-dom';
import { ConfigProvider, theme, App as AntApp } from 'antd';
import { ThemeProvider, useTheme } from './contexts/ThemeContext';
import { AuthProvider } from './contexts/AuthContext';
import { WorkspaceTabsProvider } from './contexts/WorkspaceTabsContext';
import ProtectedRoute from './components/Auth/ProtectedRoute';
import MainLayout from './components/Layout/MainLayout';
import ErrorFallback from './components/ErrorFallback';
import UpdateBanner from './components/UpdateBanner';
import { Sentry } from './lib/sentry';
// Страницы аутентификации — статически: нужны на первом рендере неавторизованному пользователю.
import Login from './pages/Auth/Login';
import Register from './pages/Auth/Register';
import ForgotPassword from './pages/Auth/ForgotPassword';
import ResetPassword from './pages/Auth/ResetPassword';
import AgentConnect from './pages/AgentConnect/AgentConnect';
// Keep-alive страницы «рабочего стола» — статически: их уже статически импортирует
// MainLayout (workspacePages/WorkspaceKeepAlive), dynamic import их из бандла не вынесет.
import ClientPositions from './pages/ClientPositions/ClientPositions';
import PositionItemsRoute from './pages/PositionItems/PositionItemsRoute';
import Commerce from './pages/Commerce';
import ConstructionCostNew from './pages/Admin/ConstructionCostNew';
import './App.css';

// Остальные страницы — lazy-чанки: клик по меню перестаёт синхронно монтировать
// страницу из монолитного бандла (INP), чанк догружается в startTransition-навигации
// react-router v7 (старый экран висит до готовности, Suspense-фолбэк — в MainLayout).
const Dashboard = lazy(() => import('./pages/Dashboard/Dashboard'));
const DataQuality = lazy(() => import('./pages/DataQuality/DataQuality'));
const Tasks = lazy(() => import('./pages/Tasks'));
const Nomenclatures = lazy(() => import('./pages/Admin/Nomenclatures/Nomenclatures'));
const ConstructionCost = lazy(() => import('./pages/Admin/ConstructionCost/ConstructionCost'));
const AdminTenders = lazy(() => import('./pages/Admin/Tenders/Tenders'));
const Tenders = lazy(() => import('./pages/Tenders/Tenders'));
const MarkupConstructor = lazy(() => import('./pages/Admin/MarkupConstructor/MarkupConstructor'));
const MarkupPercentages = lazy(() => import('./pages/Admin/MarkupPercentages/MarkupPercentages'));
const Library = lazy(() => import('./pages/Library'));
const Templates = lazy(() => import('./pages/Library/Templates'));
const ImportLog = lazy(() => import('./pages/Admin/ImportLog/ImportLog'));
const Insurance = lazy(() => import('./pages/Admin/Insurance/Insurance'));
const CostRedistribution = lazy(() => import('./pages/CostRedistribution'));
const Bsm = lazy(() => import('./pages/Bsm/Bsm'));
const ObjectComparison = lazy(() => import('./pages/Analytics/ObjectComparison'));
const FinancialIndicators = lazy(() => import('./pages/FinancialIndicators/FinancialIndicators'));
const Users = lazy(() => import('./pages/Users/Users'));
const Projects = lazy(() => import('./pages/Projects'));
const ProjectDetail = lazy(() => import('./pages/Projects/ProjectDetail'));
const TenderTimeline = lazy(() => import('./pages/TenderTimeline'));
// Этапы 2.1–2.6 (релиз): аналитика тендера и AI-администрирование.
const TenderQuality = lazy(() => import('./pages/TenderQuality/TenderQuality'));
const PriceBenchmark = lazy(() => import('./pages/PriceBenchmark/PriceBenchmark'));
const PriceSourceQuality = lazy(() => import('./pages/PriceSourceQuality/PriceSourceQuality'));
const ActionPlan = lazy(() => import('./pages/ActionPlan/ActionPlan'));
const ChangeImpact = lazy(() => import('./pages/ChangeImpact/ChangeImpact'));
const ReviewPack = lazy(() => import('./pages/ReviewPack/ReviewPack'));
const AdminAiSettings = lazy(() => import('./pages/AdminAiSettings/AdminAiSettings'));
const AdminApiAccess = lazy(() => import('./pages/AdminApiAccess/AdminApiAccess'));
const AgentConnections = lazy(() => import('./pages/AgentConnections/AgentConnections'));

function AppContent() {
  const { theme: currentTheme } = useTheme();

  return (
    <ConfigProvider
      theme={{
        algorithm: currentTheme === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm,
        token: {
          colorPrimary: '#10b981',
          colorSuccess: '#159957',
          colorInfo: '#0891b2',
        },
      }}
    >
      <AntApp>
        {/* Вне Routes: обновление фронтенда не связано с сессией и должно
            предлагаться в том числе на /login */}
        <UpdateBanner />
        <Routes>
          {/* Публичные маршруты */}
          <Route path="/login" element={<Login />} />
          <Route path="/register" element={<Register />} />
          <Route path="/forgot-password" element={<ForgotPassword />} />
          <Route path="/reset-password" element={<ResetPassword />} />
          <Route path="/agent-connect" element={<AgentConnect />} />

          {/* Защищенные маршруты */}
          <Route
            path="/"
            element={
              <ProtectedRoute>
                <WorkspaceTabsProvider>
                  <MainLayout />
                </WorkspaceTabsProvider>
              </ProtectedRoute>
            }
          >
            <Route index element={<Dashboard />} />
            <Route path="dashboard" element={<Dashboard />} />
            <Route path="tenders" element={<Tenders />} />
            <Route path="tender-timeline" element={<TenderTimeline />} />
            <Route path="tasks" element={<Tasks />} />
            <Route path="positions" element={<ClientPositions />} />
            <Route path="positions/:positionId/items" element={<PositionItemsRoute />} />
            <Route path="pricing-drafts" element={<Navigate to="/positions" replace />} />
            <Route path="commerce/proposal" element={<Commerce />} />
            <Route path="commerce/redistribution" element={<CostRedistribution />} />
            <Route path="commerce" element={<Navigate to="/commerce/proposal" replace />} />
            <Route path="library" element={<Library />} />
            <Route path="library/templates" element={<Templates />} />
            <Route path="bsm" element={<Bsm />} />
            <Route path="analytics">
              <Route path="comparison" element={<ObjectComparison />} />
              <Route path="quality" element={<TenderQuality />} />
              <Route path="price-benchmark" element={<PriceBenchmark />} />
              <Route path="price-sources" element={<PriceSourceQuality />} />
              <Route path="action-plan" element={<ActionPlan />} />
              <Route path="change-impact" element={<ChangeImpact />} />
              <Route path="review-pack" element={<ReviewPack />} />
            </Route>
            <Route path="admin">
              <Route index element={<Navigate to="/admin/nomenclatures" replace />} />
              <Route path="nomenclatures" element={<Nomenclatures />} />
              <Route path="construction_cost" element={<ConstructionCost />} />
              <Route path="tenders" element={<AdminTenders />} />
              <Route path="markup_constructor" element={<MarkupConstructor />} />
              <Route path="markup" element={<MarkupPercentages />} />
              <Route path="import-log" element={<ImportLog />} />
              <Route path="insurance" element={<Insurance />} />
              <Route path="ai-settings" element={<AdminAiSettings />} />
              <Route path="api-access" element={<AdminApiAccess />} />
            </Route>
            <Route path="costs" element={<ConstructionCostNew />} />
            <Route path="financial-indicators" element={<FinancialIndicators />} />
            <Route path="data-quality" element={<DataQuality />} />
            <Route path="projects" element={<Projects />} />
            <Route path="projects/:projectId" element={<ProjectDetail />} />
            <Route path="users" element={<Users />} />
            <Route path="settings" element={<Navigate to="/settings/agents" replace />} />
            <Route path="settings/agents" element={<AgentConnections />} />
          </Route>

          {/* Перенаправление на главную для неизвестных маршрутов */}
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </AntApp>
    </ConfigProvider>
  );
}

function App() {
  return (
    <Sentry.ErrorBoundary
      fallback={({ error, resetError }) => <ErrorFallback error={error} resetError={resetError} />}
    >
      <ThemeProvider>
        <AuthProvider>
          <AppContent />
        </AuthProvider>
      </ThemeProvider>
    </Sentry.ErrorBoundary>
  );
}

export default App
