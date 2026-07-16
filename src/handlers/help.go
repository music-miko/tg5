/*
 * TgMusicBot - Telegram Music Bot
 *  Copyright (c) 2025-2026 Ashok Shau
 *
 *  Licensed under GNU GPL v3
 *  See https://github.com/AshokShau/TgMusicBot
 */

package handlers

import (
	"fmt"
	"html"
	"strings"

	"ashokshau/tgmusic/src/core"

	td "github.com/AshokShau/gotdbot"
)

// detailsBlock renders a collapsed-by-default <details>/<summary> section.
func detailsBlock(summary, body string) string {
	return fmt.Sprintf("<details><summary>%s</summary>\n%s</details>", summary, body)
}

// cmdTable renders a Command | Description table from pairs of
// {command, description}. Descriptions are already trusted, static text.
func cmdTable(rows ...[2]string) string {
	var sb strings.Builder
	sb.WriteString("<table bordered striped>")
	sb.WriteString("<tr><th>Command</th><th>Description</th></tr>")
	for _, r := range rows {
		sb.WriteString(fmt.Sprintf("<tr><td align=\"left\"><code>%s</code></td><td align=\"left\">%s</td></tr>", r[0], r[1]))
	}
	sb.WriteString("</table>")
	return sb.String()
}

func getHelpCategories() map[string]struct {
	Title   string
	Content string
	Markup  *td.ReplyMarkupInlineKeyboard
} {
	return map[string]struct {
		Title   string
		Content string
		Markup  *td.ReplyMarkupInlineKeyboard
	}{
		"help_user": {
			Title: "User Commands",
			Content: detailsBlock("Playback", cmdTable(
				[2]string{"/play [song]", "Searches and plays a track in the group's voice chat. Accepts a search query or a direct link (YouTube, Spotify, Apple Music, SoundCloud, Deezer, JioSaavn)."},
				[2]string{"/vplay [song]", "Same as /play, but streams video instead of audio."},
			)) + "\n" +
				"<i>Admins also have /fplay and /fvplay to jump the queue, plus /autoplay — see Admin Commands.</i>\n" +
				detailsBlock("Utilities", cmdTable(
					[2]string{"/start", "Shows the welcome message and setup options."},
					[2]string{"/privacy", "Shows the bot's privacy policy."},
					[2]string{"/queue", "Shows what's currently playing and what's coming up next."},
				)),
			Markup: core.BackHelpMenuKeyboard(),
		},
		"help_admin": {
			Title: "Admin Commands",
			Content: "<i>Unless Admin Mode is turned off in /settings, these require being a group admin or an authorized user.</i>\n\n" +
				detailsBlock("Playback Controls", cmdTable(
					[2]string{"/skip", "Skips the current track and plays the next one in queue."},
					[2]string{"/pause", "Pauses playback without clearing the queue."},
					[2]string{"/resume", "Resumes playback after a pause."},
					[2]string{"/seek [seconds]", "Jumps to a specific position in the current track."},
					[2]string{"/fplay [song]", "Same as /play, but cuts straight to the front of the queue."},
					[2]string{"/fvplay [song]", "Same as /vplay, but cuts straight to the front of the queue."},
					[2]string{"/autoplay", "Toggles autoplay — keeps a related track playing once the queue is empty."},
				)) + "\n" +
				detailsBlock("Queue Management", cmdTable(
					[2]string{"/remove [position]", "Removes a specific track from the queue by its position number."},
					[2]string{"/loop [0-10]", "Repeats the current track 0-10 times; 0 turns looping off."},
				)) + "\n" +
				detailsBlock("Access Control", cmdTable(
					[2]string{"/auth [reply]", "Authorizes a user to use admin commands even if they aren't a group admin. Reply to their message."},
					[2]string{"/unauth [reply]", "Removes a previously authorized user's access."},
					[2]string{"/authlist", "Lists everyone currently authorized in this chat."},
				)),
			Markup: core.BackHelpMenuKeyboard(),
		},
		"help_devs": {
			Title: "Developer Commands",
			Content: "<i>These are available only to the bot's developers and won't do anything if you're not one.</i>\n\n" +
				detailsBlock("Diagnostics", cmdTable(
					[2]string{"/stats", "Shows runtime statistics: CPU, memory, storage, uptime, and database counts."},
				)),
			Markup: core.BackHelpMenuKeyboard(),
		},
		"help_owner": {
			Title: "Owner Commands",
			Content: detailsBlock("Settings", cmdTable(
				[2]string{"/settings", "Opens this chat's settings: admin mode, play mode, command auto-delete, and language."},
			)),
			Markup: core.BackHelpMenuKeyboard(),
		},
		"help_playlist": {
			Title: "Playlist Commands",
			Content: detailsBlock("Management", cmdTable(
				[2]string{"/createplaylist [name]", "Creates a new personal playlist you can add tracks to."},
				[2]string{"/deleteplaylist [id]", "Permanently deletes one of your playlists."},
				[2]string{"/addtoplaylist [id] [url]", "Adds a track to an existing playlist by its ID."},
				[2]string{"/removefromplaylist [id] [url]", "Removes a track from a playlist by its ID."},
				[2]string{"/playlistinfo [id]", "Shows a playlist's name, owner, and full track list."},
				[2]string{"/myplaylists", "Lists all playlists you own."},
			)),
			Markup: core.BackHelpMenuKeyboard(),
		},
	}
}

// helpCallbackHandler handles the /help menu's buttons. It's only ever
// reached from the private-chat /start screen (groups don't wire up these
// buttons).
//
// /start, the category menu, each category page, and "Home" are all Rich
// Messages — the welcome image lives in-message via <img> rather than as a
// separate photo caption — so every transition here is a plain in-place
// edit. Nothing is ever deleted and resent.
func helpCallbackHandler(c *td.Client, cb *td.UpdateNewCallbackQuery) error {
	data := cb.DataString()

	user, err := c.GetUser(cb.SenderUserId)
	if err != nil {
		user = &td.User{FirstName: "User", Id: cb.SenderUserId}
	}

	helpCategories := getHelpCategories()

	if strings.Contains(data, "help_all") {
		_ = cb.Answer(c, 0, false, "Opening help menu...", "")
		response := fmt.Sprintf(
			"%s\nHello %s, pick a category below to see what I can do.\n\n<b>Supported platforms:</b> YouTube, Spotify, Apple Music, SoundCloud, Deezer, JioSaavn and more.",
			headingBlock(3, fmt.Sprintf("📖 %s — Help Menu", html.EscapeString(c.Me.FirstName))),
			html.EscapeString(user.FirstName),
		)
		_, err := editRichByID(c, cb.ChatId, cb.MessageId, response, core.HelpMenuKeyboard())
		return err
	}

	if strings.Contains(data, "help_back") {
		_ = cb.Answer(c, 0, false, "Returning to main menu...", "")
		response := privateWelcomeText(user.FirstName, c.Me.FirstName)
		markup := core.PrivateStartMarkup(c.Me.Usernames.EditableUsername)
		_, err := editRichByID(c, cb.ChatId, cb.MessageId, response, markup)
		return err
	}

	if category, ok := helpCategories[data]; ok {
		_ = cb.Answer(c, 0, false, category.Title, "")
		response := fmt.Sprintf("%s\n\n%s\n\n<i>Use the buttons below to go back.</i>", headingBlock(2, category.Title), category.Content)
		_, err := editRichByID(c, cb.ChatId, cb.MessageId, response, category.Markup)
		return err
	}

	_ = cb.Answer(c, 0, true, "Unknown help category.", "")
	return nil
}
