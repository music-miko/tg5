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

	td "github.com/AshokShau/gotdbot"
)

// This file wires up Telegram Bot API 10.1 "Rich Messages"
// (https://core.telegram.org/bots/api#rich-messages), which is a proper
// superset of the old parse_mode=HTML formatting: on top of <b>/<i>/<code>
// etc. it adds real block-level elements — <h1>-<h6> headings, <table>,
// <details>/<summary>, <blockquote expandable>, <tg-time>, dividers, and
// more — that plain parse_mode HTML silently can't render.
//
// Several handlers in this package (stats, yt, as, gs, queue, help, the
// setup guide) already wrote their output using that markup, but sent it
// through the legacy SendTextMessageOpts{ParseMode: "HTML"} /
// EditTextMessageOpts{ParseMode: "HTML"} path, where tags like <table> and
// <details> are not understood and are shown to the user as literal text.
// The helpers below route the exact same markup through
// InputRichMessage + sendRichMessage/editMessageText instead, so it
// actually renders.
//
// One hard constraint: rich blocks (tables, details, headings, ...) can
// only live in a message's *text*, never in a media caption — Telegram
// has no "rich caption". Anywhere a rich block needs to appear in what is
// currently a photo message (e.g. the private /start message), the photo
// message has to be deleted and replaced with a real text message; see
// promoteToRich below.

// richHTML wraps Telegram Rich HTML markup into a sendable InputRichMessage.
// DetectAutomaticBlocks lets Telegram auto-linkify plain URLs, @mentions,
// #hashtags, and similar, the same way it does for ordinary messages.
func richHTML(htmlText string) *td.InputRichMessage {
	return &td.InputRichMessage{
		DetectAutomaticBlocks: true,
		Source:                td.RichMessageSourceHtml{Text: htmlText},
	}
}

// sendRich sends a brand-new rich message to chatId.
func sendRich(c *td.Client, chatId int64, htmlText string, markup td.ReplyMarkup) (*td.Message, error) {
	return c.SendRichMessage(chatId, richHTML(htmlText), &td.SendTextMessageOpts{
		DisableWebPagePreview: true,
		ReplyMarkup:           markup,
	})
}

// replyRich replies to m with a rich message.
func replyRich(c *td.Client, m *td.Message, htmlText string, markup td.ReplyMarkup) (*td.Message, error) {
	return m.ReplyRichMessage(c, richHTML(htmlText), &td.SendTextMessageOpts{
		DisableWebPagePreview: true,
		ReplyMarkup:           markup,
	})
}

// editRich replaces msg's own content with rich content in place. Only
// valid when msg is already a text/rich message — a media caption can't be
// turned into rich content this way, use promoteToRich for that.
func editRich(c *td.Client, msg *td.Message, htmlText string, markup td.ReplyMarkup) (*td.Message, error) {
	return msg.EditContent(c, &td.InputMessageRichMessage{Message: richHTML(htmlText)}, markup)
}

// editRichByID does the same as editRich but addresses the message by
// chat/message ID, for use from callback-query handlers that haven't
// already fetched the *td.Message.
func editRichByID(c *td.Client, chatId, messageId int64, htmlText string, markup td.ReplyMarkup) (*td.Message, error) {
	content := &td.InputMessageRichMessage{Message: richHTML(htmlText)}
	return c.EditMessageText(chatId, content, messageId, &td.EditMessageTextOpts{ReplyMarkup: markup})
}

// promoteToRich deletes the message at chatId/messageId and sends a fresh
// rich message with htmlText in its place. Use this when the existing
// message is a photo (or other media) whose caption needs to show rich
// blocks like tables or collapsible details, since captions can't carry
// them — the message has to become a real text message instead.
func promoteToRich(c *td.Client, chatId, messageId int64, htmlText string, markup td.ReplyMarkup) (*td.Message, error) {
	_ = c.DeleteMessages(chatId, []int64{messageId}, &td.DeleteMessagesOpts{Revoke: true})
	return sendRich(c, chatId, htmlText, markup)
}

// headingBlock renders a Rich HTML heading, clamped to the supported h1-h6 range.
func headingBlock(level int, text string) string {
	if level < 1 {
		level = 1
	} else if level > 6 {
		level = 6
	}
	return fmt.Sprintf("<h%d>%s</h%d>", level, text, level)
}

// dividerBlock renders a horizontal divider between sections.
func dividerBlock() string {
	return "<hr>"
}
