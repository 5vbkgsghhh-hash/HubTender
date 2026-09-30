package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/su10/hubtender/backend/internal/notify/telegram"
	"github.com/su10/hubtender/backend/internal/repository"
)

// Входящие бота «Перечня тендеров». Бот работает только в чате команды
// (TENDER_BOT_CHAT_ID): личные сообщения он не читает, в чужих чатах на
// команду отвечает лишь id чата для настройки — данных перечня там нет.
// В группе с режимом приватности (по умолчанию) бот видит только команды и
// ответы на свои сообщения, поэтому поиск — командой /t, а запись — ответом
// на запрос бота.

const tenderBotRetryLater = "Не получилось, попробуйте позже."

// RunPoller — long polling входящих; блокирует до отмены ctx. Как у
// TelegramBot.RunPoller: у токена один получатель getUpdates, второй получит 409.
func (s *TenderBotService) RunPoller(ctx context.Context) {
	s.logger.Info().Int64("chat_id", s.cfg.ChatID).Msg("tender bot poller started")
	backoff := time.Second
	for ctx.Err() == nil {
		offset, err := s.store.GetOffset(ctx)
		if err != nil {
			s.logger.Warn().Err(err).Msg("tender bot: offset не прочитан")
			if !sleepCtx(ctx, 30*time.Second) {
				return
			}
			continue
		}
		updates, err := s.client.GetUpdates(ctx, offset, pollTimeoutSeconds)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var apiErr *telegram.APIError
			if errors.As(err, &apiErr) && apiErr.Code == 409 {
				s.logger.Warn().Msg("tender bot: getUpdates 409 — бота опрашивает другой экземпляр или задан webhook")
				backoff = 30 * time.Second
			} else {
				s.logger.Warn().Err(err).Msg("tender bot: getUpdates")
			}
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, time.Minute)
			continue
		}
		backoff = time.Second
		for _, u := range updates {
			s.handleUpdate(ctx, u)
			if err := s.store.SetOffset(ctx, u.UpdateID+1); err != nil {
				s.logger.Warn().Err(err).Msg("tender bot: offset не сохранён")
			}
		}
	}
}

func (s *TenderBotService) handleUpdate(ctx context.Context, u telegram.Update) {
	switch {
	case u.CallbackQuery != nil:
		s.handleCallback(ctx, u.CallbackQuery)
	case u.Message != nil && u.Message.Chat.Type != "private": // в личных сообщениях бот не отвечает
		s.handleMessage(ctx, u.Message)
	}
}

// teamChat — сообщение из чата команды.
func (s *TenderBotService) teamChat(chatID int64) bool {
	return s.cfg.ChatID != 0 && chatID == s.cfg.ChatID
}

func (s *TenderBotService) replyTo(ctx context.Context, m *telegram.Message, html string, keyboard [][]telegram.InlineButton) {
	if _, err := s.client.SendReply(ctx, m.Chat.ID, m.MessageID, html, keyboard); err != nil && ctx.Err() == nil {
		s.logger.Warn().Err(err).Msg("tender bot: ответ не отправлен")
	}
}

// command — команда этому боту: «/t ЖК Ода» → ("/t", "ЖК Ода"). Обычный текст и
// команды другим ботам (/t@other_bot) → "".
func (s *TenderBotService) command(text string) (cmd, arg string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	head := text
	if i := strings.IndexFunc(text, unicode.IsSpace); i >= 0 {
		head, arg = text[:i], strings.TrimSpace(text[i:])
	}
	name, bot, addressed := strings.Cut(head, "@")
	if addressed && !strings.EqualFold(bot, s.cfg.BotUsername) {
		return "", ""
	}
	return strings.ToLower(name), arg
}

func (s *TenderBotService) handleMessage(ctx context.Context, m *telegram.Message) {
	cmd, arg := s.command(m.Text)
	if !s.teamChat(m.Chat.ID) {
		if cmd != "" {
			s.replyTo(ctx, m, telegram.RenderChatIDHint(m.Chat.ID), nil)
		}
		return
	}
	switch cmd {
	case "/t", "/tender":
		if arg == "" {
			s.replyTo(ctx, m, telegram.TenderSearchUsage, nil)
			return
		}
		s.search(ctx, m, arg)
	case "/start", "/help":
		s.replyTo(ctx, m, telegram.TenderBotHelp, nil)
	case "/chatid":
		s.replyTo(ctx, m, fmt.Sprintf("ID этого чата: <code>%d</code>", m.Chat.ID), nil)
	case "":
		if m.ReplyToMessage != nil {
			s.handleReply(ctx, m)
		}
	}
}

// handleReply — ответ на сообщение бота: текст записи по запросу «Звонок»/«Событие».
func (s *TenderBotService) handleReply(ctx context.Context, m *telegram.Message) {
	to := m.ReplyToMessage.From
	if to == nil || !to.IsBot || !strings.EqualFold(to.Username, s.cfg.BotUsername) {
		return
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		s.replyTo(ctx, m, "Нужен текст: ответьте на запрос бота обычным сообщением.", nil)
		return
	}
	if utf8.RuneCountInString(text) > maxChronologyTextRunes {
		s.replyTo(ctx, m, "Слишком длинный текст: в хронологию — не больше 2000 символов.", nil)
		return
	}
	saved, err := s.store.ConsumePrompt(ctx, m.Chat.ID, m.ReplyToMessage.MessageID, text, chronologyDate(s.now()))
	switch {
	case err == nil:
		s.replyTo(ctx, m, telegram.RenderEntrySaved(saved.Title, saved.EntryType == "call_follow_up", text), nil)
	case errors.Is(err, repository.ErrNoPrompt):
		s.replyTo(ctx, m, telegram.TenderReplyHint, nil)
	default:
		s.logger.Warn().Err(err).Msg("tender bot: запись в хронологию")
		s.replyTo(ctx, m, "Не удалось сохранить запись, попробуйте позже.", nil)
	}
}

// handleCallback — кнопки: ti — карточка, tc/te — запрос текста записи.
func (s *TenderBotService) handleCallback(ctx context.Context, q *telegram.CallbackQuery) {
	answer := func(text string) {
		if err := s.client.AnswerCallback(ctx, q.ID, text); err != nil && ctx.Err() == nil {
			s.logger.Warn().Err(err).Msg("tender bot: answerCallbackQuery")
		}
	}
	action, registryID, ok := telegram.ParseTenderCallback(q.Data)
	if !ok || q.Message == nil {
		answer("Кнопка устарела")
		return
	}
	chat := q.Message.Chat.ID
	if !s.teamChat(chat) {
		answer("Бот работает только в чате команды")
		return
	}
	t, err := s.store.GetTender(ctx, registryID)
	if errors.Is(err, repository.ErrTenderRegistryNotFound) {
		answer("Тендер удалён из перечня")
		return
	}
	if err != nil {
		s.logger.Warn().Err(err).Msg("tender bot: тендер не прочитан")
		answer(tenderBotRetryLater)
		return
	}
	if action == telegram.ActionTenderInfo {
		s.sendCard(ctx, chat, 0, *t)
		answer("")
		return
	}
	call := action == telegram.ActionTenderCall
	msgID, err := s.client.SendForceReply(ctx, chat, telegram.RenderPrompt(t.Title, call, telegram.Mention(q.From)), "Текст записи")
	if err != nil {
		s.logger.Warn().Err(err).Msg("tender bot: запрос текста не отправлен")
		answer(tenderBotRetryLater)
		return
	}
	entryType := "default"
	if call {
		entryType = "call_follow_up"
	}
	if err := s.store.InsertPrompt(ctx, chat, msgID, t.ID, entryType, s.cfg.PromptTTL); err != nil {
		s.logger.Warn().Err(err).Msg("tender bot: запрос записи не сохранён")
		answer(tenderBotRetryLater)
		return
	}
	answer("Ответьте на сообщение бота текстом записи")
}

// search — карточка, если тендер один; список кнопками, если несколько.
func (s *TenderBotService) search(ctx context.Context, m *telegram.Message, query string) {
	tenders, err := s.store.ListTenders(ctx)
	if err != nil {
		s.logger.Warn().Err(err).Msg("tender bot: перечень не прочитан")
		s.replyTo(ctx, m, tenderBotRetryLater, nil)
		return
	}
	found := matchTenders(tenders, query)
	switch len(found) {
	case 0:
		s.replyTo(ctx, m, telegram.RenderNotFound(query), nil)
		return
	case 1:
		s.sendCard(ctx, m.Chat.ID, m.MessageID, found[0])
		return
	}
	views := make([]telegram.TenderView, 0, telegram.MaxSearchResults)
	for _, t := range found[:min(len(found), telegram.MaxSearchResults)] {
		views = append(views, telegram.TenderView{ID: t.ID, Number: strOrEmpty(t.TenderNumber), Title: t.Title, Client: t.ClientName})
	}
	text, kb := telegram.RenderSearchResults(query, views, len(found))
	s.replyTo(ctx, m, text, kb)
}

// sendCard — карточка тендера; replyTo > 0 — ответом на сообщение с командой.
func (s *TenderBotService) sendCard(ctx context.Context, chat, replyTo int64, t repository.BotTender) {
	text, kb := telegram.RenderTenderCard(s.tenderView(t, parseChronology(t.Chronology, s.cfg.Location), s.now(), 3))
	var err error
	if replyTo > 0 {
		_, err = s.client.SendReply(ctx, chat, replyTo, text, kb)
	} else {
		_, err = s.client.SendMessage(ctx, chat, text, kb)
	}
	if err != nil && ctx.Err() == nil {
		s.logger.Warn().Err(err).Msg("tender bot: карточка не отправлена")
	}
}
