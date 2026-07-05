/*
 * TgMusicBot - Telegram Music Bot
 *  Copyright (c) 2025-2026 Ashok Shau
 *
 *  Licensed under GNU GPL v3
 *  See https://github.com/AshokShau/TgMusicBot
 */

package vc

import (
	"log/slog"
	"regexp"
	"sync"
	"time"

	"ashokshau/tgmusic/src/core/cache"

	td "github.com/AshokShau/gotdbot"
	tg "github.com/amarnathcjd/gogram/telegram"
)

var logger = slog.Default()
var urlRegex = regexp.MustCompile(`^https?://`)

// TelegramCalls manages the state and operations for voice calls, including userbots and the main bot client.
type TelegramCalls struct {
	mu          sync.RWMutex
	assistants  map[int]*Assistant
	clients     map[int]*tg.Client
	statusCache *cache.Cache[td.ChatMemberStatus]
	inviteCache *cache.Cache[string]

	// recentJoinCache remembers, per "chatID:assistantUserID", chats that an
	// assistant has just joined. leaveAssistantDialogs consults this to avoid
	// immediately leaving a chat it was only just added to (e.g. right before
	// starting playback, or when CHANNELS_TOO_MUCH triggers an eviction leave
	// sweep for the same assistant). Without this grace window, the assistant
	// would join a group and then get swept out again on the very next leave
	// pass, which also caused bursts of LeaveChannel calls -> FLOOD_WAIT.
	recentJoinCache *cache.Cache[bool]

	// prefetchTimers holds, per chatID, the pending timer that will trigger a
	// background download of the upcoming track shortly before the current
	// one ends. See prefetch.go.
	prefetchMu     sync.Mutex
	prefetchTimers map[int64]*time.Timer
}

var (
	instance *TelegramCalls
	once     sync.Once
)

// getCalls returns the singleton instance of the TelegramCalls manager, ensuring that only one instance is created.
func getCalls() *TelegramCalls {
	once.Do(func() {
		instance = &TelegramCalls{
			assistants:      make(map[int]*Assistant),
			clients:         make(map[int]*tg.Client),
			statusCache:     cache.NewCache[td.ChatMemberStatus](2 * time.Hour),
			inviteCache:     cache.NewCache[string](2 * time.Hour),
			recentJoinCache: cache.NewCache[bool](recentJoinGracePeriod),
			prefetchTimers:  make(map[int64]*time.Timer),
		}
	})
	return instance
}

// Calls is the singleton instance of TelegramCalls, initialized lazily.
var Calls = getCalls()
