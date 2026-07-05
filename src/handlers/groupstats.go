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
	"strings"
	"time"

	"ashokshau/tgmusic/src/core/db"

	td "github.com/AshokShau/gotdbot"
)

// gsRow builds one row of the group-stats table.
func gsRow(label string, count int64) []string {
	return []string{label, fmt.Sprintf("%d", count)}
}

// groupStatsHandler handles the /gs command: a developer-facing breakdown
// of how many groups the bot has been added to today, this week, this
// month, this year, and in total, rendered as a monospaced table (there is
// no real <table> tag in Telegram's HTML style, see
// https://core.telegram.org/bots/api#formatting-options).
func groupStatsHandler(c *td.Client, m *td.Message) error {
	if !isDev(c, m) {
		return td.EndGroups
	}

	stats, err := db.Instance.GetChatJoinStats()
	if err != nil {
		_, _ = m.ReplyText(c, fmt.Sprintf("Failed to fetch group stats: %v", err), nil)
		return td.EndGroups
	}

	var sb strings.Builder
	sb.WriteString("<b>📊 Group Join Stats</b>\n\n")

	sb.WriteString(renderTable(
		[]string{"Period", "Groups"},
		[]tableAlign{AlignLeft, AlignRight},
		[][]string{
			gsRow("Today", stats.Today),
			gsRow("Last 7 days", stats.Last7Days),
			gsRow("Last 30 days", stats.Last30Days),
			gsRow("Last year", stats.LastYear),
			gsRow("Total", stats.Total),
		},
	))

	if stats.GroupsBeforeTracking > 0 {
		sb.WriteString(fmt.Sprintf(
			"\n\n<blockquote>ℹ️ <code>%d</code> of the total were added before join-date tracking was enabled, so they aren't broken down by period above.</blockquote>",
			stats.GroupsBeforeTracking,
		))
	}

	sb.WriteString(fmt.Sprintf("\n\n<i>Generated %s</i>", tgTime(time.Now())))

	_, err = m.ReplyText(c, sb.String(), replyOpts)
	return err
}
