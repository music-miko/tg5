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
	"html"
	"runtime"
	"strings"
	"time"

	"ashokshau/tgmusic/src/core"
	"ashokshau/tgmusic/src/core/db"

	td "github.com/AshokShau/gotdbot"
)

// pingHandler handles the /ping command.
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
			"<b>Go Routines:</b> <code>%d</code>\n"+
			"<b>Version:</b> <code>%s</code>\n",
		latency, uptime, runtime.NumGoroutine(), config.Version,
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
			html.EscapeString(firstName(c, m)),
			html.EscapeString(c.Me.FirstName),
		)

		_, err := m.ReplyPhoto(c, td.InputFileRemote{Id: config.StartImg}, &td.SendPhotoOpts{
			ParseMode:   "HTML",
			Caption:     response,
			ReplyMarkup: core.PrivateStartMarkup(c.Me.Usernames.EditableUsername),
		})

		return err
	}

	go func(chatID int64) {
		_ = db.Instance.AddChat(chatID)
	}(chatID)

	uptime := getFormattedDuration(time.Since(startTime))
	response := fmt.Sprintf(
		"👋<b>%s is ready</b>\n\n<b>Uptime:</b> <code>%s</code>\n\nA music player with support for YouTube, Spotify, Apple Music, SoundCloud, Deezer, JioSaavn and more.",
		html.EscapeString(c.Me.FirstName),
		uptime,
	)

	_, err := m.ReplyText(c, response, &td.SendTextMessageOpts{
		ParseMode:             "HTML",
		DisableWebPagePreview: true,
		ReplyMarkup:           core.GroupWelcomeMarkup(),
	})

	return err
}

// setupGuideText returns the step-by-step setup guide shown via the Setup Guide button.
//
// This uses the full Rich Message toolkit rather than a flat wall of text:
// a compact "stepper" table gives the whole flow at a glance, the
// admin-rights fine print is tucked into a collapsed <details> block so it
// doesn't dominate the screen, a divider separates setup from usage, and a
// second table doubles as a quick command reference. Each stepper row is
// kept short on purpose — a 2-column table whose second column holds a
// full sentence forces Telegram to squeeze or wrap it unpredictably on
// narrow/mobile clients, which is also why the admin-rights list below
// stays a bullet list instead of a table.
func setupGuideText(botName string) string {
	escBotName := html.EscapeString(botName)

	stepper := "<table bordered striped>" +
		"<tr><th align=\"center\">Step</th><th>Action</th></tr>" +
		"<tr><td align=\"center\">1️⃣</td><td>Tap <b>➕ Add to Group</b> below and pick your group</td></tr>" +
		"<tr><td align=\"center\">2️⃣</td><td>Promote the bot to admin (tap 🔐 below for the exact rights)</td></tr>" +
		"<tr><td align=\"center\">3️⃣</td><td>Start a voice/video chat in that group</td></tr>" +
		"<tr><td align=\"center\">4️⃣</td><td>Run <code>/play song name</code> and enjoy 🎶</td></tr>" +
		"</table>"

	adminRights := detailsBlock("🔐 Required admin rights", ""+
		"• <b>Invite Users via Link</b> — lets the bot's assistant account join your group's voice chat\n"+
		"• <b>Delete Messages</b> — lets the bot clean up its own command and status messages\n"+
		"• <b>Ban Users</b> — lets the bot auto-recover its assistant if it's ever muted or banned by mistake",
	)

	commandTable := "<table bordered striped>" +
		"<tr><th>Command</th><th>What it does</th></tr>" +
		"<tr><td align=\"left\"><code>/play [song]</code></td><td align=\"left\">Streams audio in the voice chat</td></tr>" +
		"<tr><td align=\"left\"><code>/vplay [song]</code></td><td align=\"left\">Streams video instead of audio</td></tr>" +
		"</table>"

	return fmt.Sprintf(
		"%s\n"+
			"<i>Get %s streaming in your group in under a minute.</i>\n\n"+
			"%s\n\n"+
			"%s\n\n"+
			"%s\n"+
			"%s\n\n"+
			"<b>🎉 That's it — you're all set. Enjoy the music!</b>",
		headingBlock(2, "🅰️ Setup Guide"),
		escBotName,
		stepper,
		adminRights,
		dividerBlock(),
		commandTable,
	)
}

// setupCallbackHandler handles the Setup Guide button and its Back navigation.
//
// The setup guide's admin-rights table only renders as a real table inside
// a Rich Message, and rich blocks can't live in a media caption. In private
// chats /start is a photo message, so opening the guide there has to delete
// that photo and send a fresh rich text message; "Back" then has to delete
// that rich message and recreate the original photo. In groups /start is
// already a plain text message, so both directions can just edit it in
// place — no delete/recreate needed.
func setupCallbackHandler(c *td.Client, cb *td.UpdateNewCallbackQuery) error {
	data := cb.DataString()

	switch {
	case strings.Contains(data, "setup_guide"):
		_ = cb.Answer(c, 0, false, "Opening setup guide...", "")
		text := setupGuideText(c.Me.FirstName)
		markup := core.GuideBackMarkup(c.Me.Usernames.EditableUsername)

		if cb.IsPrivate() {
			_, err := promoteToRich(c, cb.ChatId, cb.MessageId, text, markup)
			return err
		}

		_, err := editRichByID(c, cb.ChatId, cb.MessageId, text, markup)
		return err

	case strings.Contains(data, "setup_back"):
		_ = cb.Answer(c, 0, false, "Returning...", "")

		if cb.IsPrivate() {
			user, err := c.GetUser(cb.SenderUserId)
			name := "there"
			if err == nil && user != nil {
				name = user.FirstName
			}
			text := fmt.Sprintf(
				"👋 Hello, %s.\n\n%s is a music bot for Telegram — stream from YouTube, Spotify, Apple Music, SoundCloud, Deezer, JioSaavn and more, right inside any group voice chat.\n\nUse /help to explore all commands.",
				html.EscapeString(name), html.EscapeString(c.Me.FirstName),
			)

			_ = c.DeleteMessages(cb.ChatId, []int64{cb.MessageId}, &td.DeleteMessagesOpts{Revoke: true})
			_, err = c.SendPhoto(cb.ChatId, td.InputFileRemote{Id: config.StartImg}, &td.SendPhotoOpts{
				ParseMode:   "HTML",
				Caption:     text,
				ReplyMarkup: core.PrivateStartMarkup(c.Me.Usernames.EditableUsername),
			})
			return err
		}

		uptime := getFormattedDuration(time.Since(startTime))
		text := fmt.Sprintf(
			"👋<b>%s is ready</b>\n\n<b>Uptime:</b> <code>%s</code>\n\nA music player with support for YouTube, Spotify, Apple Music, SoundCloud, Deezer, JioSaavn and more.",
			html.EscapeString(c.Me.FirstName), uptime,
		)
		_, err := editRichByID(c, cb.ChatId, cb.MessageId, text, core.GroupWelcomeMarkup())
		return err
	}

	return nil
}
