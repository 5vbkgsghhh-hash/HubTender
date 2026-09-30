package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNoPrompt — ответили не на запрос записи: его нет, он истёк или уже выполнен.
var ErrNoPrompt = errors.New("telegram: запрос записи не найден")

// InsertPrompt запоминает запрос текста записи — сообщение бота, на которое
// нужно ответить. В чате команды запросов может быть несколько сразу.
func (r *TenderBotRepo) InsertPrompt(ctx context.Context, chatID, promptMessageID int64, registryID, entryType string,
	ttl time.Duration) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.telegram_tender_prompts (chat_id, prompt_message_id, registry_id, entry_type, expires_at)
		VALUES ($1, $2, $3::uuid, $4, now() + make_interval(secs => $5))
		ON CONFLICT (chat_id, prompt_message_id) DO NOTHING`,
		chatID, promptMessageID, registryID, entryType, ttl.Seconds())
	if err != nil {
		return fmt.Errorf("tenderBotRepo.InsertPrompt: %w", err)
	}
	return nil
}

// ConsumedPrompt — запись, сделанная по запросу.
type ConsumedPrompt struct {
	Title     string
	EntryType string
}

// ConsumePrompt записывает text в хронологию тендера по запросу promptMessageID
// и закрывает запрос — одной транзакцией. Второй ответ на тот же запрос (или
// повторно доставленный апдейт) получит ErrNoPrompt и записи не задвоит.
func (r *TenderBotRepo) ConsumePrompt(ctx context.Context, chatID, promptMessageID int64, text, date string) (*ConsumedPrompt, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("tenderBotRepo.ConsumePrompt: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var registryID string
	res := &ConsumedPrompt{}
	err = tx.QueryRow(ctx, `
		DELETE FROM public.telegram_tender_prompts
		WHERE chat_id = $1 AND prompt_message_id = $2 AND expires_at > now()
		RETURNING registry_id::text, entry_type`, chatID, promptMessageID).Scan(&registryID, &res.EntryType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoPrompt
	}
	if err != nil {
		return nil, fmt.Errorf("tenderBotRepo.ConsumePrompt: %w", err)
	}
	res.Title, err = appendChronology(ctx, tx, registryID, ChronologyEntry{Date: date, Text: text, Type: res.EntryType})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tenderBotRepo.ConsumePrompt: commit: %w", err)
	}
	return res, nil
}

// GetOffset — offset getUpdates бота перечня; 0 — поллер ещё не запускался.
func (r *TenderBotRepo) GetOffset(ctx context.Context) (int64, error) {
	var off int64
	err := r.pool.QueryRow(ctx, `SELECT update_offset FROM public.telegram_tender_bot_state WHERE id = 1`).Scan(&off)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("tenderBotRepo.GetOffset: %w", err)
	}
	return off, nil
}

// SetOffset сохраняет offset: обработанные обновления не придут повторно после рестарта.
func (r *TenderBotRepo) SetOffset(ctx context.Context, offset int64) error {
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO public.telegram_tender_bot_state (id, update_offset, updated_at) VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE SET update_offset = EXCLUDED.update_offset, updated_at = now()`,
		offset); err != nil {
		return fmt.Errorf("tenderBotRepo.SetOffset: %w", err)
	}
	return nil
}
