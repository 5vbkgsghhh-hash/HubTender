package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/rs/zerolog"

	"github.com/su10/hubtender/backend/internal/notify/telegram"
	"github.com/su10/hubtender/backend/internal/repository"
)

// TenderBotConfig — Telegram-бот «Перечня тендеров» для чата команды.
type TenderBotConfig struct {
	ChatID       int64 // чат команды (TENDER_BOT_CHAT_ID); 0 — бот только подсказывает id чата
	BotUsername  string
	Location     *time.Location
	SendFrom     time.Duration // начало окна рассылки от полуночи
	SendUntil    time.Duration // конец окна: ночью не пишем
	ScanInterval time.Duration
	PromptTTL    time.Duration // сколько ждём ответ на запрос записи
	KeepFor      time.Duration // сколько хранить закрытые напоминания
	ChatGap      time.Duration // пауза между сообщениями: в группу — не больше 20 в минуту
	AppBaseURL   string
}

func DefaultTenderBotConfig() TenderBotConfig {
	return TenderBotConfig{
		Location:     moscowLocation(),
		SendFrom:     9 * time.Hour,
		SendUntil:    21 * time.Hour,
		ScanInterval: 10 * time.Minute,
		PromptTTL:    24 * time.Hour,
		KeepFor:      90 * 24 * time.Hour,
		ChatGap:      3 * time.Second,
	}
}

// moscowLocation — Europe/Moscow; без базы часовых поясов — фиксированный UTC+3.
func moscowLocation() *time.Location {
	if loc, err := time.LoadLocation("Europe/Moscow"); err == nil {
		return loc
	}
	return time.FixedZone("MSK", 3*3600)
}

// TenderBotConfigFromEnv — TENDER_BOT_CHAT_ID (чат команды), TENDER_BOT_TZ
// (часовой пояс) и TENDER_BOT_SEND_AT («09:00» — начало рассылки); неверные
// значения логируются, берутся умолчания.
func TenderBotConfigFromEnv(tg telegram.Config, logger zerolog.Logger) TenderBotConfig {
	cfg := DefaultTenderBotConfig()
	cfg.AppBaseURL, cfg.BotUsername = tg.AppBaseURL, tg.BotUsername
	if v := strings.TrimSpace(os.Getenv("TENDER_BOT_CHAT_ID")); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id != 0 {
			cfg.ChatID = id
		} else {
			logger.Error().Str("chat_id", v).Msg("tender bot: TENDER_BOT_CHAT_ID неверен — напоминания не отправляются")
		}
	}
	if tz := strings.TrimSpace(os.Getenv("TENDER_BOT_TZ")); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			cfg.Location = loc
		} else {
			logger.Error().Err(err).Str("tz", tz).Msg("tender bot: TENDER_BOT_TZ неверен, оставлен Europe/Moscow")
		}
	}
	if at := strings.TrimSpace(os.Getenv("TENDER_BOT_SEND_AT")); at != "" {
		t, err := time.Parse("15:04", at)
		from := time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute
		if err == nil && from < cfg.SendUntil {
			cfg.SendFrom = from
		} else {
			logger.Error().Str("send_at", at).Msg("tender bot: TENDER_BOT_SEND_AT неверен (ЧЧ:ММ раньше 21:00), оставлено 09:00")
		}
	}
	return cfg
}

type tenderBotStore interface {
	ListTenders(ctx context.Context) ([]repository.BotTender, error)
	GetTender(ctx context.Context, id string) (*repository.BotTender, error)
	EnqueueReminders(ctx context.Context, keys []repository.ReminderKey) (int64, error)
	ClaimReminders(ctx context.Context, limit int, lease time.Duration) ([]repository.ClaimedReminder, error)
	FinishReminder(ctx context.Context, k repository.ReminderKey, status string, messageID int64, reason string) error
	DeferReminder(ctx context.Context, k repository.ReminderKey, reason string, delay time.Duration) error
	PruneReminders(ctx context.Context, keep time.Duration) (int64, error)
	InsertPrompt(ctx context.Context, chatID, promptMessageID int64, registryID, entryType string, ttl time.Duration) error
	ConsumePrompt(ctx context.Context, chatID, promptMessageID int64, text, date string) (*repository.ConsumedPrompt, error)
	GetOffset(ctx context.Context) (int64, error)
	SetOffset(ctx context.Context, offset int64) error
}

type tenderBotClient interface {
	GetUpdates(ctx context.Context, offset int64, timeout int) ([]telegram.Update, error)
	SendMessage(ctx context.Context, chatID int64, html string, keyboard [][]telegram.InlineButton) (int64, error)
	SendReply(ctx context.Context, chatID, replyTo int64, html string, keyboard [][]telegram.InlineButton) (int64, error)
	SendForceReply(ctx context.Context, chatID int64, html, placeholder string) (int64, error)
	AnswerCallback(ctx context.Context, callbackID, text string) error
}

// TenderBotService — отдельный Telegram-бот «Перечня тендеров» для одного чата
// команды: напоминания по «Направлено» и «Ожидание ПД», карточка тендера по
// команде /t и запись звонка/события в хронологию (входящие — tender_bot_chat.go).
// В личных сообщениях и в чужих чатах данных перечня не выдаёт.
type TenderBotService struct {
	store  tenderBotStore
	client tenderBotClient
	cfg    TenderBotConfig
	logger zerolog.Logger
	now    func() time.Time
}

func NewTenderBotService(store tenderBotStore, client tenderBotClient, cfg TenderBotConfig, logger zerolog.Logger) *TenderBotService {
	if cfg.Location == nil {
		cfg.Location = moscowLocation()
	}
	return &TenderBotService{store: store, client: client, cfg: cfg, logger: logger, now: time.Now}
}

const (
	tenderReminderBatch       = 50
	tenderReminderLease       = 5 * time.Minute
	tenderReminderMaxAttempts = 5
)

// Run блокирует до отмены ctx: сразу и затем каждые ScanInterval ставит в
// очередь положенные напоминания и отправляет их в чат команды (в окне рассылки).
func (s *TenderBotService) Run(ctx context.Context) {
	if s.cfg.ChatID == 0 {
		s.logger.Warn().Msg("tender bot: TENDER_BOT_CHAT_ID не задан — напоминания не отправляются; " +
			"добавьте бота в чат команды и отправьте там /chatid")
		return
	}
	if s.cfg.ScanInterval <= 0 {
		s.logger.Info().Msg("tender bot reminders disabled")
		return
	}
	s.logger.Info().Int64("chat_id", s.cfg.ChatID).Str("tz", s.cfg.Location.String()).Msg("tender bot reminders started")
	s.RunOnce(ctx)
	ticker := time.NewTicker(s.cfg.ScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RunOnce(ctx)
		}
	}
}

// RunOnce — один проход: очистка, постановка в очередь, отправка.
func (s *TenderBotService) RunOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			sentry.CurrentHub().Recover(r)
			s.logger.Error().Interface("panic", r).Msg("tender bot: паника в рассылке")
		}
	}()
	logErr := func(err error, msg string) {
		if err != nil && ctx.Err() == nil {
			s.logger.Warn().Err(err).Msg(msg)
		}
	}
	_, err := s.store.PruneReminders(ctx, s.cfg.KeepFor)
	logErr(err, "tender bot: очистка напоминаний")

	now := s.now()
	if s.cfg.ChatID == 0 || !s.inWindow(now) {
		return
	}
	tenders, err := s.store.ListTenders(ctx)
	if err != nil {
		logErr(err, "tender bot: перечень не прочитан")
		return
	}
	n, err := s.store.EnqueueReminders(ctx, s.dueKeys(tenders, now))
	logErr(err, "tender bot: напоминания не поставлены в очередь")
	if n > 0 {
		s.logger.Info().Int64("queued", n).Msg("tender bot: напоминания поставлены в очередь")
	}
	s.sendPending(ctx, tenders, now)
}

func (s *TenderBotService) inWindow(now time.Time) bool {
	l := now.In(s.cfg.Location)
	sinceMidnight := time.Duration(l.Hour())*time.Hour + time.Duration(l.Minute())*time.Minute
	return sinceMidnight >= s.cfg.SendFrom && sinceMidnight < s.cfg.SendUntil
}

// dueReminder — положенное тендеру напоминание: вид и дата срока (YYYY-MM-DD).
// «Направлено» — на каждый день, пока звонок просрочен; «Ожидание ПД» — на
// «месячную годовщину» последней записи.
func (s *TenderBotService) dueReminder(t repository.BotTender, entries []chronoEntry, now time.Time) (kind, dueOn string, ok bool) {
	loc := s.cfg.Location
	switch ResolveDashboardStatus(t.StatusName, t.DashboardStatus, t.IsArchived) {
	case dashSent:
		if evalCall(t, entries, now, loc).Due {
			return repository.ReminderKindCall, localDate(now, loc), true
		}
	case dashWaitingPD:
		if p := evalPD(t, entries, now, loc); p.Due {
			return repository.ReminderKindWaitingPD, p.DueOn.Format("2006-01-02"), true
		}
	}
	return "", "", false
}

func (s *TenderBotService) dueKeys(tenders []repository.BotTender, now time.Time) []repository.ReminderKey {
	var keys []repository.ReminderKey
	for _, t := range tenders {
		if kind, dueOn, ok := s.dueReminder(t, parseChronology(t.Chronology, s.cfg.Location), now); ok {
			keys = append(keys, repository.ReminderKey{ChatID: s.cfg.ChatID, RegistryID: t.ID, Kind: kind, DueOn: dueOn})
		}
	}
	return keys
}

func (s *TenderBotService) sendPending(ctx context.Context, tenders []repository.BotTender, now time.Time) {
	claimed, err := s.store.ClaimReminders(ctx, tenderReminderBatch, tenderReminderLease)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Warn().Err(err).Msg("tender bot: очередь не прочитана")
		}
		return
	}
	byID := make(map[string]repository.BotTender, len(tenders))
	for _, t := range tenders {
		byID[t.ID] = t
	}
	var lastSent time.Time
	for _, c := range claimed {
		if ctx.Err() != nil || !s.sendReminder(ctx, c, byID, now, &lastSent) {
			return
		}
	}
}

// sendReminder отправляет одно напоминание, перепроверив, что оно ещё
// актуально. false — Telegram просит подождать (429): остальное — в следующий проход.
func (s *TenderBotService) sendReminder(ctx context.Context, c repository.ClaimedReminder,
	tenders map[string]repository.BotTender, now time.Time, lastSent *time.Time) bool {
	finish := func(status, reason string, msgID int64) {
		if err := s.store.FinishReminder(ctx, c.ReminderKey, status, msgID, reason); err != nil && ctx.Err() == nil {
			s.logger.Warn().Err(err).Str("registry_id", c.RegistryID).Msg("tender bot: статус напоминания не сохранён")
		}
	}
	if c.ChatID != s.cfg.ChatID {
		finish("skipped", "чат бота сменился", 0)
		return true
	}
	t, ok := tenders[c.RegistryID]
	if !ok {
		finish("skipped", "тендер удалён из перечня", 0)
		return true
	}
	text, keyboard, ok := s.renderReminder(t, c.ReminderKey, now)
	if !ok {
		finish("skipped", "напоминание больше не актуально", 0)
		return true
	}
	if !lastSent.IsZero() && s.cfg.ChatGap > 0 {
		if wait := s.cfg.ChatGap - time.Since(*lastSent); wait > 0 && !sleepCtx(ctx, wait) {
			return false
		}
	}
	msgID, err := s.client.SendMessage(ctx, c.ChatID, text, keyboard)
	*lastSent = time.Now()
	if err == nil {
		finish("sent", "", msgID)
		return true
	}
	if ctx.Err() != nil {
		return false
	}
	var apiErr *telegram.APIError
	switch {
	case errors.As(err, &apiErr) && apiErr.Code == 429:
		delay := time.Duration(max(apiErr.RetryAfter, 1)) * time.Second
		if derr := s.store.DeferReminder(ctx, c.ReminderKey, err.Error(), delay); derr != nil {
			s.logger.Warn().Err(derr).Msg("tender bot: повтор не запланирован")
		}
		return false
	case errors.As(err, &apiErr) && apiErr.MigrateToChatID != 0:
		s.logger.Error().Int64("new_chat_id", apiErr.MigrateToChatID).
			Msg("tender bot: чат команды стал супергруппой — укажите новый TENDER_BOT_CHAT_ID")
		finish("failed", err.Error(), 0)
	case errors.As(err, &apiErr) && apiErr.Permanent():
		finish("failed", err.Error(), 0)
	case c.Attempts >= tenderReminderMaxAttempts:
		finish("failed", err.Error(), 0)
	default:
		if derr := s.store.DeferReminder(ctx, c.ReminderKey, err.Error(), tenderReminderLease); derr != nil {
			s.logger.Warn().Err(derr).Msg("tender bot: повтор не запланирован")
		}
	}
	return true
}

// renderReminder собирает текст по свежим данным; ok=false — напоминание уже
// не нужно (звонок отмечен, статус сменился, появилась новая запись, срок прошёл).
func (s *TenderBotService) renderReminder(t repository.BotTender, k repository.ReminderKey, now time.Time) (string, [][]telegram.InlineButton, bool) {
	entries := parseChronology(t.Chronology, s.cfg.Location)
	kind, dueOn, ok := s.dueReminder(t, entries, now)
	if !ok || kind != k.Kind || dueOn != k.DueOn {
		return "", nil, false
	}
	view := s.tenderView(t, entries, now, 1)
	loc := s.cfg.Location
	if kind == repository.ReminderKindCall {
		ev := evalCall(t, entries, now, loc)
		text, kb := telegram.RenderCallReminder(telegram.CallReminder{
			Tender: view, Days: ev.Days, Threshold: callThresholdDays, Since: fmtDate(&ev.Since, loc), FromCall: ev.FromCall,
		})
		return text, kb, true
	}
	ev := evalPD(t, entries, now, loc)
	text, kb := telegram.RenderPDReminder(telegram.PDReminder{
		Tender: view, Months: ev.Months, Since: fmtDate(&ev.Anchor, loc), FromCreated: ev.FromCreated,
	})
	return text, kb, true
}

// tenderView — данные тендера для сообщения; lastN — сколько записей хронологии показать.
func (s *TenderBotService) tenderView(t repository.BotTender, entries []chronoEntry, now time.Time, lastN int) telegram.TenderView {
	loc := s.cfg.Location
	status := ResolveDashboardStatus(t.StatusName, t.DashboardStatus, t.IsArchived)
	v := telegram.TenderView{
		ID: t.ID, Number: strings.TrimSpace(strOrEmpty(t.TenderNumber)), Title: t.Title, Client: t.ClientName,
		Status: statusLabel(t.StatusName, status), Scope: strOrEmpty(t.ScopeName),
		Area: fmtAmount(t.Area, "м²"), Cost: fmtAmount(t.TotalCost, "₽"),
		Submission: fmtDateTime(t.SubmissionDate, loc), Invitation: fmtDate(t.InvitationDate, loc),
		SiteStart: fmtDate(t.ConstructionStart, loc), Commission: fmtDate(t.CommissionDate, loc),
		Address: strOrEmpty(t.ObjectAddress), Package: packageSummary(t.TenderPackage),
		Entries: latestEntries(entries, lastN, loc),
	}
	if status == dashSent {
		if ev := evalCall(t, entries, now, loc); ev.Known {
			from := "подачи КП"
			if ev.FromCall {
				from = "последнего звонка"
			}
			v.CallStatus = fmt.Sprintf("%s с %s (%s)", telegram.PluralRu(ev.Days, "день", "дня", "дней"), from, fmtDate(&ev.Since, loc))
			if ev.Due {
				v.CallStatus += " — пора звонить"
			}
		}
	}
	if s.cfg.AppBaseURL != "" {
		v.Link = s.cfg.AppBaseURL + "/tenders"
	}
	return v
}
