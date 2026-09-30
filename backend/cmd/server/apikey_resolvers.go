package main

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Резолверы тендера для гейта машинного доступа: маршруты PATCH /items/{id}
// и POST /positions/{id}/recompute-totals не несут id тендера в URL, а ключ
// может быть ограничен списком тендеров.

func (d *deps) tenderOfItem(r *http.Request) (string, error) {
	item, err := d.boqSvc.GetBoqItemByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return "", err
	}
	return item.TenderID, nil
}

func (d *deps) tenderOfPosition(r *http.Request) (string, error) {
	pos, err := d.positionSvc.GetPositionByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return "", err
	}
	return pos.TenderID, nil
}

// errRegistryNotTenderScoped — у «Перечня тендеров» нет связи с тендерами:
// принадлежность строки перечня списку разрешённых тендеров не проверить.
var errRegistryNotTenderScoped = errors.New("перечень тендеров не привязан к тендерам")

// registryDeniesRestrictedKey — резолвер маршрутов перечня: вызывается только
// для ключа, ограниченного списком тендеров, и всегда отказывает (403
// API_KEY_TENDER_DENIED). Обычный ключ проходит по области.
func registryDeniesRestrictedKey(*http.Request) (string, error) {
	return "", errRegistryNotTenderScoped
}
