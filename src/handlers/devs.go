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
	"strings"

	"ashokshau/tgmusic/src/core/cache"
	"ashokshau/tgmusic/src/core/db"
	"ashokshau/tgmusic/src/vc"

	td "github.com/AshokShau/gotdbot"
)

// activeVcHandler handles the /activevc command.
// It takes a telegram.NewMessage object as input.
// It returns an error if any.
func activeVcHandler(c *td.Client, m *td.Message) error {
	if !isDev(c, m) {
		return td.EndGroups
	}

	activeChats := cache.ChatCache.GetActiveChats()
	if len(activeChats) == 0 {
		_, err := m.ReplyText(c, "No active chats found.", nil)
		return err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🎵 <b>Active Voice Chats</b> (%d):\n\n", len(activeChats)))

	for _, chatID := range activeChats {
		queueLength := cache.ChatCache.GetQueueLength(chatID)
		currentSong := cache.ChatCache.GetPlayingTrack(chatID)

		var songInfo string
		if currentSong != nil {
			songInfo = fmt.Sprintf(
				"🎶 <b>Now Playing:</b> <a href='%s'>%s</a> (%ds)",
				html.EscapeString(currentSong.URL),
				html.EscapeString(currentSong.Name),
				currentSong.Duration,
			)
		} else {
			songInfo = "🔇 No song playing."
		}

		sb.WriteString(fmt.Sprintf(
			"➤ <b>Chat ID:</b> <code>%d</code>\n📌 <b>Queue Size:</b> %d\n%s\n\n",
			chatID,
			queueLength,
			songInfo,
		))
	}

	text := sb.String()
	if len(text) > 4096 {
		text = fmt.Sprintf("🎵 <b>Active Voice Chats</b> (%d)", len(activeChats))
	}

	_, err := m.ReplyText(c, text, &td.SendTextMessageOpts{ParseMode: "HTML", DisableWebPagePreview: true})
	if err != nil {
		return err
	}

	return nil
}

// Handles the /clearass command to remove all assistant assignments
func clearAssistantsHandler(c *td.Client, m *td.Message) error {
	if !isDev(c, m) {
		return td.EndGroups
	}

	done, err := db.Instance.ClearAllAssistants()
	if err != nil {
		_, _ = m.ReplyText(c, fmt.Sprintf("failed to clear assistants: %s", err.Error()), nil)
		return td.EndGroups
	}

	_, err = m.ReplyText(c, fmt.Sprintf("Removed assistant from %d chats", done), nil)
	return err
}

// Handles the /leaveall command to leave all chats
func leaveAllHandler(c *td.Client, m *td.Message) error {
	if !isDev(c, m) {
		return td.EndGroups
	}

	reply, err := m.ReplyText(c, "Assistant is leaving all chats...", nil)
	if err != nil {
		return err
	}

	leftCount, err := vc.Calls.LeaveAll()
	if err != nil {
		_, _ = reply.EditText(c, fmt.Sprintf("Failed to leave all chats: %s", err.Error()), nil)
		return err
	}

	_, err = reply.EditText(c, fmt.Sprintf("Assistant's Left %d chats", leftCount), nil)
	return err
}

// asHandler handles the /as command: invites every running assistant into
// the configured logger group, and reports which ones joined vs failed.
func asHandler(c *td.Client, m *td.Message) error {
	if !isDev(c, m) {
		return td.EndGroups
	}

	if config.LoggerId == 0 {
		_, _ = m.ReplyText(c, "Please set LOGGER_ID in .env first.", nil)
		return td.EndGroups
	}

	reply, err := m.ReplyText(c, "Inviting all assistants into the logger group...", nil)
	if err != nil {
		return err
	}

	results := vc.Calls.JoinAllAssistants(c, config.LoggerId)
	if len(results) == 0 {
		_, err = reply.EditText(c, "No assistants are currently running.", nil)
		return err
	}

	var sb strings.Builder
	sb.WriteString(headingBlock(2, "🤝 Assistant Invite Results"))
	sb.WriteString("\n\n")

	sb.WriteString("<table bordered striped>")
	sb.WriteString("<tr><th>Client</th><th>Assistant</th><th>Status</th></tr>")

	var joined, failed int
	var failLines []string
	for _, r := range results {
		name := fmt.Sprintf("%d", r.UserID)
		if r.Username != "" {
			name = "@" + r.Username
		}

		status := "✅ OK"
		if !r.Success() {
			failed++
			status = "❌ FAILED"
			failLines = append(failLines, fmt.Sprintf(
				"client%d (%s): %s", r.Index, html.EscapeString(name), html.EscapeString(truncate(r.Err.Error(), 150)),
			))
		} else {
			joined++
		}

		sb.WriteString(fmt.Sprintf(
			"<tr><td align=\"left\">client%d</td><td align=\"left\">%s</td><td align=\"left\">%s</td></tr>",
			r.Index, html.EscapeString(name), status,
		))
	}

	sb.WriteString(fmt.Sprintf(
		"<tr><td align=\"left\"><b>Total</b></td><td></td><td align=\"left\"><b>%d/%d</b></td></tr>",
		joined, joined+failed,
	))
	sb.WriteString("</table>\n")

	if len(failLines) > 0 {
		sb.WriteString("\n<blockquote expandable>\n")
		sb.WriteString(strings.Join(failLines, "\n"))
		sb.WriteString("\n</blockquote>")
	}

	_, err = editRich(c, reply, sb.String(), nil)
	return err
}

// Handles the /logger command to toggle logger status
func loggerHandler(c *td.Client, m *td.Message) error {
	if !isDev(c, m) {
		return td.EndGroups
	}

	if config.LoggerId == 0 {
		_, _ = m.ReplyText(c, "Please set LOGGER_ID in .env first.", nil)
		return td.EndGroups
	}

	loggerStatus := db.Instance.GetLoggerStatus()
	args := strings.ToLower(Args(m))
	if len(args) == 0 {
		_, _ = m.ReplyText(c, fmt.Sprintf("Usage: /logger [enable|disable|on|off]\nCurrent status: %t", loggerStatus), nil)
		return td.EndGroups
	}

	switch args {
	case "enable", "on":
		_ = db.Instance.SetLoggerStatus(true)
		_, _ = m.ReplyText(c, "Logger Enabled", nil)
	case "disable", "off":
		_ = db.Instance.SetLoggerStatus(false)
		_, _ = m.ReplyText(c, "Logger disabled", nil)
	default:
		_, _ = m.ReplyText(c, "Invalid argument. Use 'enable', 'disable', 'on', or 'off'.", nil)
	}

	return td.EndGroups
}
