// Payload элементов для POST /api/v1/imports/boq — общий для массового и
// одиночного импорта. Клиент передаёт только исходные данные (количество,
// цена, валюта, коэффициенты, доставка, связи); total_amount считает сервер.

import { isMaterial, isWork } from './importShared';

/** Поля распарсенной строки, которые уходят на сервер. */
export interface BoqItemPayloadSource {
  rowIndex: number;
  /** Существующая позиция… */
  matchedPositionId?: string;
  /** …или ДОП, создаваемая этим же импортом (temp_id строки «доп»). */
  additionalTempId?: string;
  boq_item_type: string;
  unit_code: string;
  quantity?: number;
  base_quantity?: number;
  consumption_coefficient?: number;
  conversion_coefficient?: number;
  currency_type?: string;
  delivery_price_type?: string;
  delivery_amount?: number;
  unit_rate?: number;
  detail_cost_category_id?: string;
  quote_link?: string;
  description?: string;
  work_name_id?: string;
  material_name_id?: string;
  material_type?: string;
  tempId?: string;
  parent_work_item_id?: string;
}

export const buildBoqItemsPayload = (
  data: BoqItemPayloadSource[],
): Record<string, unknown>[] => {
  return data
    .filter(item => item.matchedPositionId || item.additionalTempId)
    .map((item) => {
      const payload: Record<string, unknown> = {
        row_index: item.rowIndex,
        boq_item_type: item.boq_item_type,
        unit_code: item.unit_code,
        quantity: item.quantity,
      };

      if (item.additionalTempId) {
        payload.client_position_temp_id = item.additionalTempId;
      } else {
        payload.client_position_id = item.matchedPositionId;
      }

      if (item.base_quantity !== undefined) {
        payload.base_quantity = item.base_quantity;
      }
      if (item.consumption_coefficient !== undefined) {
        payload.consumption_coefficient = item.consumption_coefficient;
      }
      if (item.conversion_coefficient !== undefined) {
        payload.conversion_coefficient = item.conversion_coefficient;
      }
      if (item.currency_type) {
        payload.currency_type = item.currency_type;
      }
      if (item.delivery_price_type) {
        payload.delivery_price_type = item.delivery_price_type;
      }
      if (item.delivery_amount !== undefined) {
        payload.delivery_amount = item.delivery_amount;
      }
      if (item.unit_rate !== undefined) {
        payload.unit_rate = item.unit_rate;
      }
      if (item.detail_cost_category_id) {
        payload.detail_cost_category_id = item.detail_cost_category_id;
      }
      if (item.quote_link) {
        payload.quote_link = item.quote_link;
      }
      if (item.description) {
        payload.description = item.description;
      }

      if (isWork(item.boq_item_type)) {
        payload.work_name_id = item.work_name_id;
        if (item.tempId) {
          payload.temp_id = item.tempId;
        }
      }

      if (isMaterial(item.boq_item_type)) {
        payload.material_type = item.material_type;
        payload.material_name_id = item.material_name_id;
        if (item.parent_work_item_id) {
          payload.parent_work_temp_id = item.parent_work_item_id;
        }
      }

      return payload;
    });
};
