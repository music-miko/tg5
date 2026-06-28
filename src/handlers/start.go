/*
 * TgMusicBot - Telegram Music Bot
 *  Copyright (c) 2025-2026 Ashok Shau
 *
 *  Licensed under GNU GPL v3
 *  See https://github.com/AshokShau/TgMusicBot
 */

package handlers

import (
	"ashokshau/tgmusic/config"
	"fmt"
	"runtime"
	"time"

	"ashokshau/tgmusic/src/core"
	"ashokshau/tgmusic/src/core/db"

	td "github.com/AshokShau/gotdbot"
)

// startBackCallback handles the start_back callback — returns to the main start screen.
func startBackCallback(c *td.Client, cb *td.UpdateNewCallbackQuery) error {
	_ = cb.Answer(c, 0, false, "", "")

	user, err := c.GetUser(cb.SenderUserId)
	if err != nil {
		user = &td.User{FirstName: "there", Id: cb.SenderUserId}
	}

	response := fmt.Sprintf(
		"👋 Hello, %s.\n\n%s is a music bot for Telegram — stream from YouTube, Spotify, Apple Music, SoundCloud, Deezer, JioSaavn and more, right inside any group voice chat.\n\nUse /help to explore all commands.",
		user.FirstName,
		c.Me.FirstName,
	)
	_, _ = cb.EditMessageCaption(c, response, &td.EditCaptionOpts{
		ReplyMarkup: core.AddMeMarkup(c.Me.Usernames.EditableUsername),
		ParseMode:   "HTML",
	})
	return nil
}

// setupGuideCallback handles the setup_guide callback query.
func setupGuideCallback(c *td.Client, cb *td.UpdateNewCallbackQuery) error {
	_ = cb.Answer(c, 0, false, "Setup Guide", "")

	botName := c.Me.FirstName
	response := fmt.Sprintf(
		"📖 <b>Setup Guide</b>\n\n"+
			"Get %s up and running in your group in under a minute:\n\n"+
			"<b>Step 1 — Add the bot</b>\n"+
			"Tap <b>Add to Group</b> and select your group.\n\n"+
			"<b>Step 2 — Promote the bot</b>\n"+
			"Make %s an admin and grant the <b>Invite Users via Link</b> (also shown as <b>Add Users</b>) permission. This is required for voice chats.\n\n"+
			"<b>Step 3 — Start a voice chat</b>\n"+
			"Open your group and start a video/voice chat.\n\n"+
			"<b>Step 4 — Play music</b>\n"+
			"Use <code>/play song name</code> or <code>/vplay song name</code> for video.\n\n"+
			"Example: <code>/play shape of you</code>\n\n"+
			"That's it — enjoy the music! 🎶",
		botName, botName,
	)

	backBtn := core.BackToStartMarkup(c.Me.Usernames.EditableUsername)
	_, _ = cb.EditMessageCaption(c, response, &td.EditCaptionOpts{ReplyMarkup: backBtn, ParseMode: "HTML"})
	return nil
}

func pingHandler(c *td.Client, m *td.Message) error {

	start := time.Now()

	msg, err := m.ReplyText(c, "Pinging… please wait…", nil)
	if err != nil {
		return err
	}

	latency := time.Since(start).Milliseconds()
	uptime := getFormattedDuration(time.Since(startTime))

	response := fmt.Sprintf(
		"<b>📊 System Performance Metrics</b>\n\n"+
			"<b>Bot Latency:</b> <code>%d ms</code>\n"+
			"<b>Uptime:</b> <code>%s</code>\n"+
			"<b>Go Routines:</b> <code>%d</code>\n",
		latency, uptime, runtime.NumGoroutine(),
	)

	_, err = msg.EditText(c, response, &td.EditTextMessageOpts{ParseMode: "HTML"})
	return err
}

// startHandler handles the /start command.
func startHandler(c *td.Client, m *td.Message) error {
	chatID := m.ChatId

	if m.IsPrivate() {
		go func(chatID int64) {
			_ = db.Instance.AddUser(chatID)
		}(chatID)

		response := fmt.Sprintf(
			"👋 Hello, %s.\n\n%s is a music bot for Telegram — stream from YouTube, Spotify, Apple Music, SoundCloud, Deezer, JioSaavn and more, right inside any group voice chat.\n\nUse /help to explore all commands.",
			firstName(c, m),
			c.Me.FirstName,
		)

		_, err := m.ReplyPhoto(c, td.InputFileRemote{Id: config.StartImg}, &td.SendPhotoOpts{
			ParseMode:   "HTML",
			Caption:     response,
			ReplyMarkup: core.AddMeMarkup(c.Me.Usernames.EditableUsername),
		})

		return err
	}

	go func(chatID int64) {
		_ = db.Instance.AddChat(chatID)
	}(chatID)

	uptime := getFormattedDuration(time.Since(startTime))
	response := fmt.Sprintf(
		"👋 <b>%s is ready</b>\n\n"+
			"<b>Uptime:</b> <code>%s</code>\n\n"+
			"A music player with support for YouTube, Spotify, Apple Music, SoundCloud, Deezer, JioSaavn and more.",
		c.Me.FirstName,
		uptime,
	)

	_, err := m.ReplyText(c, response, &td.SendTextMessageOpts{
		ParseMode:             "HTML",
		DisableWebPagePreview: true,
		ReplyMarkup:           core.SupportBtn(),
	})

	return err
}
