import { Space } from 'antd';
import { LinkOutlined, PhoneOutlined } from '@ant-design/icons';
import type { TenderRegistryWithRelations } from '../../../lib/types';
import {
  formatArea,
  formatDate,
  formatMoney,
  formatRubPerSquare,
  formatTime,
  getControlDate,
  getDashboardStatus,
  getDaysSinceControl,
  getDaysToSubmission,
  getPackageItems,
  getPackageLinkHref,
  getStatusBadgeStyle,
  getTenderPackageBadgeStyle,
  getTenderStatusDisplayLabel,
} from '../utils/tenderMonitor';
import type { TenderMonitorPalette } from '../utils/tenderMonitorTheme';
import { MapPopover, ChronologyPopover } from './TenderMonitorPopovers';

type TableColumn = {
  key: string;
  label: string;
  template: string;
  align?: 'left' | 'center' | 'right';
};

const GRID_COLUMNS: TableColumn[] = [
  { key: 'index', label: '№', template: '42px', align: 'center' },
  { key: 'title', label: 'ЖК / объект', template: 'minmax(84px, 0.82fr)' },
  { key: 'client', label: 'Заказчик', template: 'minmax(64px, 0.5fr)' },
  { key: 'area', label: 'Площадь', template: '96px', align: 'center' },
  { key: 'cost', label: 'Стоимость КП', template: '116px', align: 'center' },
  { key: 'rate', label: '₽/м²', template: '86px', align: 'center' },
  { key: 'submission', label: 'Дата подачи', template: '204px', align: 'center' },
  { key: 'package', label: 'Тендерный пакет', template: 'minmax(180px, 0.7fr)', align: 'left' },
  { key: 'status', label: 'Статус / время', template: '132px', align: 'center' },
  { key: 'timeline', label: 'Хронология', template: '84px', align: 'center' },
  { key: 'invite', label: 'Приглашение', template: '82px', align: 'center' },
];

const GRID_TEMPLATE = GRID_COLUMNS.map((column) => column.template).join(' ');

export function DesktopTableHeader({ palette }: { palette: TenderMonitorPalette }) {
  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: GRID_TEMPLATE,
        columnGap: 8,
        padding: '0 14px',
        alignItems: 'stretch',
        borderBottom: `1px solid ${palette.border}`,
      }}
    >
      {GRID_COLUMNS.map((column) => (
        <div
          key={column.key}
          style={{
            padding: '10px 0',
            textAlign: column.align || 'left',
            color: palette.muted,
            fontSize: 9,
            letterSpacing: '0.12em',
            textTransform: 'uppercase',
            lineHeight: 1.25,
            whiteSpace: 'normal',
          }}
        >
          {column.label}
        </div>
      ))}
    </div>
  );
}

export function SectionHeader({
  title,
  tenders,
  palette,
}: {
  title: string;
  tenders: TenderRegistryWithRelations[];
  palette: TenderMonitorPalette;
}) {
  const totalArea = tenders.reduce((sum, tender) => sum + (tender.area || 0), 0);
  const totalCost = tenders.reduce((sum, tender) => sum + (tender.total_cost || tender.manual_total_cost || 0), 0);

  return (
    <div
      style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        padding: '11px 14px',
        borderBottom: `1px solid ${palette.border}`,
        background: palette.sectionBg,
        gap: 12,
        flexWrap: 'wrap',
      }}
    >
      <div style={{ color: palette.text, fontSize: 14, letterSpacing: '0.08em', textTransform: 'uppercase' }}>{title}</div>
      <div style={{ color: palette.muted, fontSize: 11 }}>
        {tenders.length} тендеров · {formatArea(totalArea)} · {formatMoney(totalCost)}
      </div>
    </div>
  );
}

export function TenderRow({
  tender,
  onOpenTender,
  onOpenTimeline,
  onQuickCall,
  palette,
  readOnly,
}: {
  tender: TenderRegistryWithRelations;
  onOpenTender: (tender: TenderRegistryWithRelations) => void;
  onOpenTimeline: (tender: TenderRegistryWithRelations) => void;
  onQuickCall: (tender: TenderRegistryWithRelations) => Promise<void> | void;
  palette: TenderMonitorPalette;
  readOnly?: boolean;
}) {
  const dashboardStatus = getDashboardStatus(tender);
  const badgeStyle = getStatusBadgeStyle(dashboardStatus);
  const packageItems = getPackageItems(tender).filter((item) => item.text?.trim());
  const daysToSubmission = getDaysToSubmission(tender);
  const daysSinceControl = getDaysSinceControl(tender);
  const canQuickCall = dashboardStatus === 'sent' && (daysSinceControl ?? 0) > 7;
  const controlDate = getControlDate(tender);
  const canSubmissionCall = dashboardStatus === 'calc' && controlDate != null && (daysSinceControl ?? 0) >= 7;

  return (
    <div
      onClick={() => onOpenTender(tender)}
      style={{
        display: 'grid',
        gridTemplateColumns: GRID_TEMPLATE,
        columnGap: 8,
        padding: '0 14px',
        alignItems: 'center',
        borderBottom: `1px solid ${palette.borderSoft}`,
        cursor: 'pointer',
        background: palette.cardBgAlt,
      }}
    >
      <div style={{ padding: '12px 0', color: palette.subtleText, fontSize: 11, textAlign: 'center' }}>{tender.sort_order}</div>

      <div style={{ padding: '12px 0', minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
          <div
            style={{
              color: palette.text,
              fontSize: 14,
              fontWeight: 600,
              lineHeight: 1.25,
              minWidth: 0,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
          >
            {tender.title}
          </div>
          <MapPopover tender={tender} palette={palette} />
        </div>
        {tender.tender_number ? <div style={{ color: palette.muted, fontSize: 11, marginTop: 3 }}>{tender.tender_number}</div> : null}
      </div>

      <div
        style={{
          padding: '12px 0',
          color: palette.muted,
          fontSize: 13,
          minWidth: 0,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {tender.client_name || '—'}
      </div>

      <div style={{ padding: '12px 0', color: palette.text, textAlign: 'center', fontSize: 13, fontWeight: 600 }}>{formatArea(tender.area)}</div>
      <div style={{ padding: '12px 0', color: palette.text, textAlign: 'center', fontSize: 13, fontWeight: 600 }}>
        {formatMoney(tender.total_cost || tender.manual_total_cost)}
      </div>
      <div style={{ padding: '12px 0', color: palette.muted, textAlign: 'center', fontSize: 12 }}>
        {formatRubPerSquare(tender.total_cost || tender.manual_total_cost, tender.area)}
      </div>

      <div style={{ padding: '12px 0', textAlign: 'center' }}>
        <div style={{ display: 'inline-flex', alignItems: 'baseline', gap: 4, justifyContent: 'center' }}>
          <span style={{ color: palette.sent, fontSize: 16.8, fontWeight: 700 }}>
            {formatDate(tender.submission_date)}
          </span>
          {formatTime(tender.submission_date) ? (
            <span style={{ color: palette.success, fontSize: 16.8, fontWeight: 600 }}>{formatTime(tender.submission_date)}</span>
          ) : null}
        </div>
        {dashboardStatus === 'calc' && daysToSubmission != null ? (
          canSubmissionCall && !readOnly ? (
            <div style={{ marginTop: 5 }}>
              <button
                type="button"
                className="tender-monitor-call-button"
                onClick={(event) => {
                  event.stopPropagation();
                  void onQuickCall(tender);
                }}
                style={{
                  border: `1px solid ${palette.dangerBorder}`,
                  background: palette.dangerBg,
                  color: palette.danger,
                  borderRadius: 6,
                  padding: '2px 7px',
                  fontSize: 11,
                  cursor: 'pointer',
                }}
              >
                <PhoneOutlined style={{ marginRight: 6 }} />
                Позвонить
              </button>
            </div>
          ) : (
            <div style={{ color: daysToSubmission < 0 && controlDate ? palette.success : '#ff9f43', fontSize: 11, marginTop: 3 }}>
              {daysToSubmission < 0 && controlDate ? `${daysSinceControl ?? 0}/7 дн` : `${daysToSubmission} дн`}
            </div>
          )
        ) : null}
      </div>

      <div style={{ padding: '12px 0', minWidth: 0 }}>
        {packageItems.length > 0 ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
            {packageItems.map((item, index) => {
              const href = getPackageLinkHref(item.link);

              return href ? (
                <a
                  key={`${item.text}-${item.date || 'empty'}-${index}`}
                  href={href}
                  target="_blank"
                  rel="noreferrer"
                  onClick={(event) => event.stopPropagation()}
                  style={{
                    display: 'inline-flex',
                    alignSelf: 'flex-start',
                    padding: '3px 8px',
                    borderRadius: 6,
                    fontSize: 11,
                    fontWeight: 600,
                    textDecoration: 'none',
                    wordBreak: 'break-word',
                    ...getTenderPackageBadgeStyle(item.text),
                  }}
                >
                  {item.text}
                </a>
              ) : (
                <div
                  key={`${item.text}-${item.date || 'empty'}-${index}`}
                  style={{
                    display: 'inline-flex',
                    alignSelf: 'flex-start',
                    padding: '3px 8px',
                    borderRadius: 6,
                    fontSize: 11,
                    fontWeight: 600,
                    wordBreak: 'break-word',
                    ...getTenderPackageBadgeStyle(item.text),
                  }}
                >
                  {item.text}
                </div>
              );
            })}
          </div>
        ) : (
          <div style={{ color: palette.muted, fontSize: 12 }}>—</div>
        )}
      </div>

      <div style={{ padding: '12px 0', textAlign: 'center' }}>
        <div
          style={{
            display: 'inline-flex',
            padding: '3px 8px',
            borderRadius: 6,
            fontSize: 11,
            fontWeight: 600,
            ...badgeStyle,
          }}
        >
          {getTenderStatusDisplayLabel(tender)}
        </div>
        {dashboardStatus === 'sent' && daysSinceControl != null ? (
          <div style={{ marginTop: 6 }}>
            <span style={{ color: palette.danger, fontSize: 12, marginRight: 6 }}>{daysSinceControl}д</span>
            {canQuickCall && !readOnly ? (
              <button
                type="button"
                className="tender-monitor-call-button"
                onClick={(event) => {
                  event.stopPropagation();
                  void onQuickCall(tender);
                }}
                style={{
                  border: `1px solid ${palette.dangerBorder}`,
                  background: palette.dangerBg,
                  color: palette.danger,
                  borderRadius: 6,
                  padding: '2px 7px',
                  fontSize: 11,
                  cursor: 'pointer',
                }}
              >
                <PhoneOutlined style={{ marginRight: 6 }} />
                Позвонить
              </button>
            ) : null}
          </div>
        ) : null}
      </div>

      <div
        style={{ padding: '12px 0', textAlign: 'center' }}
        onClick={(event) => event.stopPropagation()}
      >
        <Space size={8}>
          <button
            type="button"
            disabled={!tender.site_visit_photo_url}
            onClick={(event) => {
              event.stopPropagation();
              if (tender.site_visit_photo_url) {
                window.open(tender.site_visit_photo_url, '_blank', 'noopener,noreferrer');
              }
            }}
            style={{
              width: 24,
              height: 24,
              borderRadius: 6,
              border: `1px solid ${tender.site_visit_photo_url ? `${palette.info}55` : palette.border}`,
              background: tender.site_visit_photo_url ? `${palette.info}1f` : palette.disabledBg,
              color: tender.site_visit_photo_url ? palette.info : palette.disabledText,
              cursor: tender.site_visit_photo_url ? 'pointer' : 'not-allowed',
            }}
            title="Посещение площадки"
          >
            <LinkOutlined />
          </button>
          <ChronologyPopover tender={tender} palette={palette} onOpenTimeline={onOpenTimeline} />
        </Space>
      </div>

      <div style={{ padding: '12px 0', color: palette.muted, textAlign: 'center', fontSize: 12 }}>
        {formatDate(tender.invitation_date)}
      </div>
    </div>
  );
}
