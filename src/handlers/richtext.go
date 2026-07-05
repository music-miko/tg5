/*
 * TgMusicBot - Telegram Music Bot
 *  Copyright (c) 2025-2026 Ashok Shau
 *
 *  Licensed under GNU GPL v3
 *  See https://github.com/AshokShau/TgMusicBot
 */

package handlers

import (
	"html"
	"strings"
	"unicode/utf8"
)

// Telegram's Bot API "HTML style" (parse_mode HTML) supports only a fixed,
// small set of tags:
//
//	<b>/<strong>, <i>/<em>, <u>/<ins>, <s>/<strike>/<del>,
//	<span class="tg-spoiler">/<tg-spoiler>, <a href="...">,
//	<tg-emoji emoji-id="...">, <code>, <pre>, <pre><code class="language-...">,
//	<blockquote>, <blockquote expandable>
//
// See https://core.telegram.org/bots/api#formatting-options
//
// There is no <table>, <tr>, <td>, <th>, <details>, <summary>, or <tg-time>
// tag. TDLib rejects any message containing them with:
//
//	Can't parse entities: Unsupported start tag "table"
//
// which is exactly the crash this file fixes. The helpers below render
// tabular / collapsible content using only tags Telegram actually
// understands, so callers never need to hand-roll HTML for these shapes
// again.

// tableAlign controls how a column's cells are padded when rendered.
type tableAlign int

const (
	AlignLeft tableAlign = iota
	AlignRight
)

// renderTable renders headers/rows as a monospaced <pre> block, which is
// the closest thing to a real table Telegram's HTML style supports. It's
// best suited to short, uniform cell content (numbers, ids, short labels);
// for long free-text columns prefer a bullet list instead, since a fixed
// monospace grid doesn't wrap nicely on mobile.
//
// Every cell is HTML-escaped internally: pass raw/plain text, never
// pre-escaped text.
func renderTable(headers []string, aligns []tableAlign, rows [][]string) string {
	cols := len(headers)
	if cols == 0 {
		return ""
	}

	widths := make([]int, cols)
	for i, h := range headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		for i := 0; i < cols && i < len(row); i++ {
			if w := utf8.RuneCountInString(row[i]); w > widths[i] {
				widths[i] = w
			}
		}
	}

	alignFor := func(i int) tableAlign {
		if i < len(aligns) {
			return aligns[i]
		}
		return AlignLeft
	}

	pad := func(s string, width int, align tableAlign) string {
		gap := width - utf8.RuneCountInString(s)
		if gap <= 0 {
			return s
		}
		spaces := strings.Repeat(" ", gap)
		if align == AlignRight {
			return spaces + s
		}
		return s + spaces
	}

	writeRow := func(sb *strings.Builder, cells []string) {
		for i := 0; i < cols; i++ {
			if i > 0 {
				sb.WriteString("  ")
			}
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			sb.WriteString(html.EscapeString(pad(cell, widths[i], alignFor(i))))
		}
	}

	var sb strings.Builder
	sb.WriteString("<pre>")
	writeRow(&sb, headers)

	sb.WriteString("\n")
	for i, w := range widths {
		if i > 0 {
			sb.WriteString("  ")
		}
		sb.WriteString(strings.Repeat("─", w))
	}

	for _, row := range rows {
		sb.WriteString("\n")
		writeRow(&sb, row)
	}
	sb.WriteString("</pre>")
	return sb.String()
}

// renderKV renders label/value pairs as a monospaced <pre> block with no
// header row - labels left-aligned, values right-aligned. Used for simple
// stat dashboards (e.g. "CPU usage   12.3%").
func renderKV(rows [][2]string) string {
	if len(rows) == 0 {
		return ""
	}

	labelWidth := 0
	for _, r := range rows {
		if w := utf8.RuneCountInString(r[0]); w > labelWidth {
			labelWidth = w
		}
	}

	var sb strings.Builder
	sb.WriteString("<pre>")
	for i, r := range rows {
		if i > 0 {
			sb.WriteString("\n")
		}
		gap := labelWidth - utf8.RuneCountInString(r[0])
		if gap < 0 {
			gap = 0
		}
		sb.WriteString(html.EscapeString(r[0] + strings.Repeat(" ", gap)))
		sb.WriteString("  ")
		sb.WriteString(html.EscapeString(r[1]))
	}
	sb.WriteString("</pre>")
	return sb.String()
}

// renderExpandable renders a collapsed-by-default section using
// <blockquote expandable>, Telegram's real equivalent of a <details>
// disclosure widget. The title is shown bold on the first line; body is
// expected to already be valid Telegram HTML (e.g. built with renderTable
// or plain <b>/<code> markup) and is not escaped again here.
func renderExpandable(title, body string) string {
	var sb strings.Builder
	sb.WriteString("<blockquote expandable><b>")
	sb.WriteString(html.EscapeString(title))
	sb.WriteString("</b>\n")
	sb.WriteString(body)
	sb.WriteString("</blockquote>")
	return sb.String()
}
