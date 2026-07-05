/*
 * TgMusicBot - Telegram Music Bot
 *  Copyright (c) 2025-2026 Ashok Shau
 *
 *  Licensed under GNU GPL v3
 *  See https://github.com/AshokShau/TgMusicBot
 */

package vc

import (
	"ashokshau/tgmusic/config"
	"ashokshau/tgmusic/src/core/cache"

	"context"
	"fmt"
	"html"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	td "github.com/AshokShau/gotdbot"
	"github.com/amarnathcjd/gogram/telegram"
)

// tgTime renders a human-readable, HTML-escaped timestamp for t. There is
// no <tg-time> tag in Telegram's Bot API HTML style (that was a fictional
// tag - see https://core.telegram.org/bots/api#formatting-options for the
// real, supported list), so this just formats plain text. Returns "never"
// for a zero time.
func tgTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return html.EscapeString(t.Format("Jan 2, 15:04 MST"))
}

func (c *TelegramCalls) LeaveAll() (int, error) {
	var totalLeft atomic.Int64
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex

	c.mu.RLock()
	var ubContexts []*Assistant
	for _, call := range c.assistants {
		ubContexts = append(ubContexts, call)
	}
	c.mu.RUnlock()

	for _, call := range ubContexts {
		wg.Add(1)
		go func(ctx *Assistant) {
			defer wg.Done()
			count, err := c.leaveAssistantDialogs(ctx)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				return
			}
			totalLeft.Add(int64(count))
		}(call)
	}

	wg.Wait()
	return int(totalLeft.Load()), firstErr
}

func (c *TelegramCalls) LeaveAllForClient(index int) (int, error) {
	c.mu.RLock()
	call, ok := c.assistants[index]
	c.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("no ntgcalls instance was found for client index %d", index)
	}
	return c.leaveAssistantDialogs(call)
}

// recentJoinGracePeriod is how long a chat is protected from being auto-left
// after an assistant joins it. This prevents the "join then leave instantly"
// pattern that used to happen when: (a) a user just added the bot/assistant
// to a fresh group and hasn't started playback yet when a periodic AutoLeave
// sweep runs, or (b) a play attempt fails with CHANNELS_TOO_MUCH right after
// joining, which used to trigger an immediate LeaveAllForClient sweep that
// caught the chat it had just joined. Both cases also produced bursts of
// LeaveChannel calls in a short window, which is what was tripping FLOOD_WAIT.
const recentJoinGracePeriod = 30 * time.Minute

// leaveDelay is the pause between consecutive LeaveChannel calls for the same
// assistant. Kept deliberately conservative to stay well under Telegram's
// leave-rate limits and avoid FLOOD_WAIT during large sweeps.
const leaveDelay = 3 * time.Second

func (c *TelegramCalls) leaveAssistantDialogs(ctx *Assistant) (int, error) {
	userBot := ctx.App
	userID := userBot.Me().ID
	var totalLeft int
	dialogs, err := userBot.GetDialogs(&telegram.DialogOptions{
		Limit:            -1,
		SleepThresholdMs: 20,
	})
	if err != nil {
		return 0, fmt.Errorf("account %s: failed to get dialogs: %w",
			userBot.Me().FirstName, err)
	}

	logger.Info("found dialogs",
		"user", userBot.Me().FirstName,
		"count", len(dialogs),
	)

	for _, d := range dialogs {
		var chatID int64
		switch p := d.Peer.(type) {
		case *telegram.PeerChannel:
			chatID = p.ChannelID
		case *telegram.PeerChat:
			chatID = p.ChatID
		default:
			continue
		}

		if chatID == 0 {
			continue
		}

		if config.LoggerId != 0 && chatID == config.LoggerId {
			logger.Debug("skipping logger group", "user", userBot.Me().FirstName, "chat_id", chatID)
			continue
		}

		if cache.ChatCache.IsActive(chatID) {
			continue
		}

		if _, recentlyJoined := c.recentJoinCache.Get(fmt.Sprintf("%d:%d", chatID, userID)); recentlyJoined {
			logger.Debug("skipping recently joined chat",
				"user", userBot.Me().FirstName,
				"chat_id", chatID,
			)
			continue
		}

		for {
			if cache.ChatCache.IsActive(chatID) {
				break
			}

			err = userBot.LeaveChannel(chatID)
			if err == nil {
				totalLeft++
				break
			}
			if strings.Contains(err.Error(), "USER_NOT_PARTICIPANT") ||
				strings.Contains(err.Error(), "CHANNEL_PRIVATE") {
				break
			}
			wait := telegram.GetFloodWait(err)
			if wait > 0 {
				logger.Warn("flood wait",
					"user", userBot.Me().FirstName,
					"chat_id", chatID,
					"seconds", wait,
				)
				time.Sleep(time.Duration(wait+20) * time.Second)
				continue
			}
			logger.Warn("leave failed",
				"user", userBot.Me().FirstName,
				"chat_id", chatID,
				"error", err,
			)
			break
		}

		time.Sleep(leaveDelay)
	}
	return totalLeft, nil
}

const autoLeaveInterval = 18 * time.Hour

func (c *TelegramCalls) startAutoLeave(ctx context.Context, bot *td.Client) {
	if !config.AutoLeave {
		return
	}
	go func() {
		logger.Info("AutoLeave enabled, starting background task",
			"interval", autoLeaveInterval)
		ticker := time.NewTicker(autoLeaveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				logger.Info("AutoLeave: background task stopped")
				return
			case <-ticker.C:
				c.runAutoLeave(bot)
			}
		}
	}()
}

func (c *TelegramCalls) runAutoLeave(bot *td.Client) {
	logger.Info("AutoLeave: leaving inactive chats")
	leftCount, err := c.LeaveAll()
	if err != nil {
		logger.Error("AutoLeave: failed to leave chats", "error", err)
		return
	}
	logger.Info("AutoLeave: completed", "leftCount", leftCount)
	if leftCount > 0 && config.LoggerId != 0 {
		msg := fmt.Sprintf(
			"<b>AutoLeave</b>\nAssistant left <code>%d</code> inactive chats\n<i>%s</i>",
			leftCount, tgTime(time.Now()),
		)
		if _, err = bot.SendTextMessage(config.LoggerId, msg, &td.SendTextMessageOpts{ParseMode: "HTML"}); err != nil {
			logger.Error("AutoLeave: failed to send log message", "error", err)
		}
	}
}
