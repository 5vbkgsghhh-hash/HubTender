import { useEffect, useState } from 'react';
import type { TenderRegistryWithRelations, TenderStatus, ConstructionScope } from '../../../lib/types';
import { useRealtimeTopic } from '../../../lib/realtime/useRealtimeTopic';
import { useRealtimeAwareLoading } from '../../../lib/realtime/useRealtimeAwareLoading';
import {
  fetchTenderRegistryWithRelations,
  fetchTenderStatuses,
  fetchConstructionScopes,
  fetchTenderNumbers,
  fetchRelatedTendersByNumbers,
} from '../../../lib/api/tenderRegistry';

const normalizeTenderTitle = (title?: string | null) =>
  (title || '')
    .replace(/\s*\([^)]*\)\s*/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .toLocaleLowerCase('ru-RU');

const getTenderRegistryDedupKey = (tender: TenderRegistryWithRelations) => {
  if (tender.tender_number) {
    return `number:${tender.tender_number}`;
  }

  return `title:${normalizeTenderTitle(tender.title)}|client:${(tender.client_name || '').trim().toLocaleLowerCase('ru-RU')}`;
};

// Каждая новая версия тендера триггером плодит пустую строку tender_registry с
// тем же tender_number. Чтобы пустой дубль не «перекрывал» данные, при дедупе
// выбираем самую заполненную строку (тай-брейк — более ранняя created_at).
const scoreRegistryRow = (row: TenderRegistryWithRelations): number => {
  const filled = (v: unknown) => (v != null && v !== '' ? 1 : 0);
  const jsonbFilled = (v: unknown) => (Array.isArray(v) && v.length > 0 ? 1 : 0);

  return (
    filled(row.submission_date) +
    filled(row.construction_start_date) +
    filled(row.site_visit_date) +
    filled(row.invitation_date) +
    filled(row.commission_date) +
    filled(row.object_address) +
    filled(row.object_coordinates) +
    filled(row.chronology) +
    filled(row.has_tender_package) +
    filled(row.manual_total_cost) +
    jsonbFilled(row.chronology_items) +
    jsonbFilled(row.tender_package_items) +
    (row.is_archived ? 1 : 0)
  );
};

const dedupeTenderRegistry = (items: TenderRegistryWithRelations[]) => {
  const map = new Map<string, TenderRegistryWithRelations>();

  items.forEach((item) => {
    const key = getTenderRegistryDedupKey(item);
    const current = map.get(key);

    if (!current) {
      map.set(key, item);
      return;
    }

    const nextScore = scoreRegistryRow(item);
    const currentScore = scoreRegistryRow(current);

    if (nextScore > currentScore) {
      map.set(key, item);
      return;
    }

    if (nextScore === currentScore) {
      const nextCreated = new Date(item.created_at).getTime();
      const currentCreated = new Date(current.created_at).getTime();
      if (nextCreated < currentCreated) {
        map.set(key, item);
      }
    }
  });

  return Array.from(map.values()).sort((left, right) => (left.sort_order || 0) - (right.sort_order || 0));
};

export const useTenderData = () => {
  const [tenders, setTenders] = useState<TenderRegistryWithRelations[]>([]);
  const [statuses, setStatuses] = useState<TenderStatus[]>([]);
  const [constructionScopes, setConstructionScopes] = useState<ConstructionScope[]>([]);
  const [tenderNumbers, setTenderNumbers] = useState<string[]>([]);
  const [loading, setLoading] = useRealtimeAwareLoading(false);

  const fetchTenders = async () => {
    setLoading(true);
    try {
      const data = await fetchTenderRegistryWithRelations();
      const tendersData = dedupeTenderRegistry(data);

      setTenders(tendersData);
      setLoading(false);

      const tendersWithNumbers = tendersData.filter((t) => t.tender_number);
      const tenderIdMap = new Map<string, string>();
      const grandTotalMap = new Map<string, number>();

      if (tendersWithNumbers.length > 0) {
        const numbers = tendersWithNumbers
          .map((t) => t.tender_number)
          .filter((tn): tn is string => tn != null);
        const relatedTenders = await fetchRelatedTendersByNumbers(numbers);

        if (relatedTenders.length > 0) {
          const sorted = [...relatedTenders].sort((a, b) => (b.version || 0) - (a.version || 0));
          sorted.forEach((rt) => {
            if (rt.tender_number && !tenderIdMap.has(rt.tender_number)) {
              tenderIdMap.set(rt.tender_number, rt.id);
            }
            grandTotalMap.set(rt.id, rt.cached_grand_total || 0);
          });
        }
      }

      const updatedTenders = tendersData.map((tender) => {
        if (tender.manual_total_cost != null) {
          return { ...tender, total_cost: tender.manual_total_cost };
        }

        if (tender.tender_number && tenderIdMap.has(tender.tender_number)) {
          const tenderId = tenderIdMap.get(tender.tender_number)!;
          return { ...tender, total_cost: grandTotalMap.get(tenderId) ?? null };
        }

        return { ...tender, total_cost: null };
      });

      setTenders(updatedTenders);
    } catch {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchTenders();
    fetchTenderStatuses().then(setStatuses).catch(() => setStatuses([]));
    fetchConstructionScopes().then(setConstructionScopes).catch(() => setConstructionScopes([]));
    fetchTenderNumbers().then(setTenderNumbers).catch(() => setTenderNumbers([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Перечень меняют другие пользователи и Telegram-бот (запись звонка из чата):
  // без обновления открытая страница перезаписала бы хронологию старой копией.
  useRealtimeTopic('tenders', () => {
    void fetchTenders();
  });

  return { tenders, statuses, constructionScopes, tenderNumbers, loading, refetch: fetchTenders };
};
