import React, { useEffect, useMemo, useState } from 'react';
import { Card, Select, Button, Space, Typography, Tag, Collapse, Empty, Spin, Switch, Alert, Popconfirm, Tabs } from 'antd';
import { ReloadOutlined, SafetyCertificateOutlined, CheckOutlined, SendOutlined, RobotOutlined } from '@ant-design/icons';
import { useIsMobile } from '../../hooks/useIsMobile';
import { useQualityReport } from './hooks/useQualityReport';
import { FindingsTable } from './components/FindingsTable';
import { ProposalReadinessCard } from './components/ProposalReadinessCard';
import { CheckpointBar } from './components/CheckpointBar';
import { SectionsPanel } from './components/SectionsPanel';
import { CostBenchmarkPanel } from './components/CostBenchmarkPanel';
import { DispatchModal } from './components/DispatchModal';
import { fetchTelegramStatus } from '../../lib/api/telegram';
import { dispatchableIds } from '../../lib/quality/dispatchPolicy';
import { useAuth } from '../../contexts/AuthContext';
import { useAITriage } from './hooks/useAITriage';
import { AITriageBar } from './components/AITriageBar';
import { AITriageSettingsCard } from './components/AITriageSettingsCard';
import { aiOkToAccept, arrangeFindings, canEditAISettings, countLabels } from '../../lib/quality/aiTriagePolicy';

const { Title, Text, Paragraph } = Typography;

const SEVERITY_META = {
  error: { color: 'red', label: 'Ошибки' },
  warning: { color: 'orange', label: 'Предупреждения' },
  info: { color: 'blue', label: 'Информация' },
} as const;

const DataQuality: React.FC = () => {
  const { isPhone } = useIsMobile();
  const {
    tenders,
    selectedTenderId,
    setSelectedTenderId,
    report,
    rules,
    groups,
    counts,
    loading,
    showAccepted,
    setShowAccepted,
    showOnlyNew,
    setShowOnlyNew,
    checkpointing,
    markCheckpoint,
    recheck,
    submitVerdict,
    submitGroupVerdict,
  } = useQualityReport();

  // Рассылка в Telegram — только если бот настроен на сервере.
  const [telegramEnabled, setTelegramEnabled] = useState(false);
  const [dispatchIds, setDispatchIds] = useState<string[] | null>(null);
  useEffect(() => {
    fetchTelegramStatus()
      .then((s) => setTelegramEnabled(s.enabled))
      .catch(() => setTelegramEnabled(false));
  }, []);
  const allDispatchable = dispatchableIds(groups.flatMap((g) => g.findings));

  // ИИ-разбор: оценки модели — подсказка, вердикт ставит проверяющий.
  const { user } = useAuth();
  const ai = useAITriage(selectedTenderId);
  const [hideAIOk, setHideAIOk] = useState(false);
  const shownGroups = useMemo(
    () =>
      groups
        .map((g) => ({ ...g, findings: arrangeFindings(g.findings, ai.byFinding, hideAIOk) }))
        .filter((g) => g.findings.length > 0),
    [groups, ai.byFinding, hideAIOk],
  );
  const activeFindings = (report?.findings ?? []).filter((f) => f.verdict === null);
  const aiCounts = countLabels(activeFindings, ai.byFinding);

  // Вкладка «Замечания»: на время перепрогона правил — спиннер.
  const findingsTab = loading ? (
    <Card>
      <Spin tip="Выполняются правила…">
        <div style={{ minHeight: 80 }} />
      </Spin>
    </Card>
  ) : report ? (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card size="small">
        <Space size={12} wrap>
          <Tag color="red">Ошибки: {counts.error}</Tag>
          <Tag color="orange">Предупреждения: {counts.warning}</Tag>
          <Tag color="blue">Информация: {counts.info}</Tag>
          <Tag color="green">Принято: {counts.accepted}</Tag>
          {counts.money > 0 && (
            <Tag color="volcano">
              Денежный эффект: {Math.round(counts.money).toLocaleString('ru-RU')} ₽
            </Tag>
          )}
        </Space>
      </Card>

      <CheckpointBar
        report={report}
        newCount={counts.new}
        showOnlyNew={showOnlyNew}
        onShowOnlyNewChange={setShowOnlyNew}
        checkpointing={checkpointing}
        onCheckpoint={() => void markCheckpoint()}
        isPhone={isPhone}
      />

      <ProposalReadinessCard
        findings={report.findings}
        rules={rules}
        isPhone={isPhone}
      />

      <AITriageBar
        data={ai.data}
        counts={aiCounts}
        total={activeFindings.length}
        starting={ai.starting}
        hideLikelyOk={hideAIOk}
        onHideLikelyOkChange={setHideAIOk}
        onStart={() => void ai.start()}
        isPhone={isPhone}
      />

      {report.errors.length > 0 && (
        <Alert
          type="warning"
          showIcon
          message="Часть правил не отработала"
          description={report.errors.map((e) => `${e.rule_code}: ${e.message}`).join('; ')}
        />
      )}

      {shownGroups.length === 0 ? (
        <Card>
          <Empty
            description={
              showOnlyNew
                ? 'Новых находок с последней отметки проверки нет'
                : showAccepted
                  ? 'Находок нет'
                  : 'Активных находок нет — возможно, все отмечены как норма'
            }
          />
        </Card>
      ) : (
        <Collapse
          items={shownGroups.map((g) => ({
            key: g.ruleCode,
            label: (
              <Space size={8} wrap>
                <Tag color={SEVERITY_META[g.severity].color}>{g.ruleCode}</Tag>
                <Text strong>{g.ruleTitle}</Text>
                <Text type="secondary">({g.findings.length})</Text>
                {g.moneyTotal > 0 && (
                  <Tag color="volcano">
                    {Math.round(g.moneyTotal).toLocaleString('ru-RU')} ₽
                  </Tag>
                )}
                {g.acceptedCount > 0 && (
                  <Tag color="green">принято: {g.acceptedCount}</Tag>
                )}
              </Space>
            ),
            children: (
              <Space direction="vertical" size={12} style={{ width: '100%' }}>
                <Paragraph
                  type="secondary"
                  style={{ marginBottom: 0, whiteSpace: 'pre-line', fontSize: 13 }}
                >
                  {g.summary}
                </Paragraph>
                {(() => {
                  const pending = g.findings.filter((f) => f.verdict !== 'accepted').length;
                  if (pending === 0) return null;
                  return (
                    <Popconfirm
                      title="Принять всю группу как норму?"
                      description={`Будет отмечено находок: ${pending}. Каждую можно переоткрыть по отдельности.`}
                      okText="Принять"
                      cancelText="Отмена"
                      onConfirm={() => void submitGroupVerdict(g.findings, 'accepted')}
                    >
                      <Button size="small" icon={<CheckOutlined />}>
                        Принять всю группу как норму ({pending})
                      </Button>
                    </Popconfirm>
                  );
                })()}
                {telegramEnabled && dispatchableIds(g.findings).length > 0 && (
                  <Button size="small" icon={<SendOutlined />} onClick={() => setDispatchIds(dispatchableIds(g.findings))}>
                    Отправить группу исполнителям ({dispatchableIds(g.findings).length})
                  </Button>
                )}
                {(() => {
                  const okByAI = aiOkToAccept(g.findings, ai.byFinding);
                  if (okByAI.length === 0) return null;
                  return (
                    <Popconfirm
                      title="Принять как норму то, что ИИ считает нормой?"
                      description={`Будет отмечено находок: ${okByAI.length}. Перед этим просмотрите причины в таблице.`}
                      okText="Принять"
                      cancelText="Отмена"
                      onConfirm={() => void submitGroupVerdict(okByAI, 'accepted')}
                    >
                      <Button size="small" icon={<RobotOutlined />}>
                        Принять «похоже на норму» по ИИ ({okByAI.length})
                      </Button>
                    </Popconfirm>
                  );
                })()}
                <FindingsTable
                  findings={g.findings}
                  isPhone={isPhone}
                  onVerdict={submitVerdict}
                  assessments={ai.byFinding}
                />
              </Space>
            ),
          }))}
        />
      )}
    </Space>
  ) : null;

  return (
    <div style={{ padding: isPhone ? 12 : 24 }}>
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        <div>
          <Title level={isPhone ? 4 : 3} style={{ marginBottom: 4 }}>
            <SafetyCertificateOutlined /> Проверка данных
          </Title>
          <Text type="secondary">
            Находки правил по выбранному тендеру. Отмечайте, что реально ошибка, а что
            легитимный случай — это и есть замер точности правил.
          </Text>
        </div>

        <Card size="small">
          <Space direction={isPhone ? 'vertical' : 'horizontal'} size={12} style={{ width: '100%' }} wrap>
            <Select
              showSearch
              style={{ width: isPhone ? '100%' : 420 }}
              placeholder="Выберите тендер"
              value={selectedTenderId}
              onChange={setSelectedTenderId}
              optionFilterProp="label"
              options={tenders.map((t) => ({
                value: t.id,
                label: t.version ? `${t.title} — версия ${t.version}` : t.title,
              }))}
            />
            <Button
              icon={<ReloadOutlined />}
              onClick={recheck}
              disabled={!selectedTenderId || loading}
            >
              Перепроверить
            </Button>
            {telegramEnabled && (
              <Button
                icon={<SendOutlined />}
                onClick={() => setDispatchIds(allDispatchable)}
                disabled={!selectedTenderId || loading || allDispatchable.length === 0}
              >
                Отправить исполнителям ({allDispatchable.length})
              </Button>
            )}
            <Space size={8}>
              <Switch checked={showAccepted} onChange={setShowAccepted} size="small" />
              <Text>Показывать принятые</Text>
            </Space>
          </Space>
        </Card>

        {canEditAISettings(user?.role_code) && <AITriageSettingsCard tenders={tenders} />}

        {selectedTenderId ? (
          // Разделы и эталоны грузятся отдельно от находок и не пересоздаются, пока
          // идёт перепрогон правил, — иначе мигали бы на каждой правке тендера.
          // Вкладка монтируется при первом открытии. На телефоне подписи короткие,
          // чтобы три вкладки влезали в 360 px.
          <Tabs
            defaultActiveKey="sections"
            items={[
              {
                key: 'sections',
                label: isPhone ? 'Разделы' : 'Готовность по разделам ВОР',
                children: <SectionsPanel tenderId={selectedTenderId} isPhone={isPhone} />,
              },
              {
                key: 'costs',
                label: isPhone ? 'Затраты' : 'Сравнение по затратам',
                children: <CostBenchmarkPanel tenderId={selectedTenderId} isPhone={isPhone} />,
              },
              {
                key: 'findings',
                label: !loading && report
                  ? `Замечания (${counts.error + counts.warning + counts.info})`
                  : 'Замечания',
                children: findingsTab,
              },
            ]}
          />
        ) : (
          <Card>
            <Empty description="Выберите тендер, чтобы увидеть находки" />
          </Card>
        )}
      </Space>
      {selectedTenderId && dispatchIds && (
        <DispatchModal
          open
          tenderId={selectedTenderId}
          findingIds={dispatchIds}
          onClose={() => setDispatchIds(null)}
        />
      )}
    </div>
  );
};

export default DataQuality;
