package vc

import (
	"ashokshau/tgmusic/src/core/cache"
	"ashokshau/tgmusic/src/core/db"
	"ashokshau/tgmusic/src/vc/ntgcalls"
	"context"
	"errors"
	"fmt"
	"strings"

	td "github.com/AshokShau/gotdbot"
)

// errorKind classifies a Telegram group call error for retry strategy.
type errorKind int

const joinFloodWaitMarker = "FLOOD_WAIT_X"
const (
	errFatal     errorKind = iota // return immediately with a user-facing message
	errRetryOnce                  // retry the same assistant once (e.g. participants race)
	errRotate                     // try a different assistant (flood/frozen/channels)
	errUnknown                    // log and return as-is
)

func classifyError(err error) errorKind {
	// A wedged/unresponsive native engine (see Assistant.markUnhealthy) means
	// this chat is pinned to a dead assistant - retrying the same one is
	// pointless and just costs the user another nativeCallTimeout wait.
	// Rotate to a different assistant immediately instead, the same as we do
	// for CHANNELS_TOO_MUCH/FROZEN_METHOD_INVALID.
	if errors.Is(err, ErrAssistantUnhealthy) || errors.Is(err, ntgcalls.ErrNativeTimeout) {
		return errRotate
	}

	msg := err.Error()
	switch {
	case strings.Contains(msg, "is closed"),
		strings.Contains(msg, "GROUPCALL_FORBIDDEN"):
		return errFatal
	case strings.Contains(msg, "GROUPCALL_INVALID"):
		return errFatal
	case strings.Contains(msg, "GROUPCALL_ADD_PARTICIPANTS_FAILED"):
		return errRetryOnce
	case strings.Contains(msg, "CHANNELS_TOO_MUCH"),
		strings.Contains(msg, "FROZEN_METHOD_INVALID"),
		strings.Contains(msg, "FLOOD_WAIT_X"):
		return errRotate
	default:
		return errUnknown
	}
}

func fatalMessage(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "is closed") || strings.Contains(msg, "GROUPCALL_FORBIDDEN") {
		return errors.New("<b>No active video chat found.</b>\n\nPlease start one and <b>try again</b>")
	}

	if strings.Contains(msg, "GROUPCALL_INVALID") {
		return errors.New("<b>GROUPCALL_INVALID:</b> start a video chat and try again.\n\nIf the problem persists, please report it to the developer.")
	}
	return err
}

// PlayMedia plays media in a voice chat with automatic assistant rotation on certain errors.
func (c *TelegramCalls) PlayMedia(bot *td.Client, chatID int64, filePath string, video bool, ffmpegParameters string) error {
	call, index, err := c.GetGroupAssistant(chatID)
	if err != nil {
		// GetGroupAssistant itself now refuses to hand back an assistant it
		// already knows is unhealthy (see calls.go). That still needs to go
		// through the same rotate path as any other errRotate - otherwise a
		// chat pinned to a dead assistant would get this error back
		// forever, on every single /play, never actually recovering on its
		// own even though other assistants are perfectly healthy.
		if classifyError(err) == errRotate && index >= 0 {
			c.evictAssistant(chatID, index, err)
			return c.rotateAndPlay(bot, chatID, filePath, video, ffmpegParameters, map[int]bool{index: true}, err)
		}
		return err
	}

	err = c.playMedia(bot, chatID, filePath, video, ffmpegParameters, call, index)
	if err == nil {
		_ = db.Instance.SetAssistant(chatID, index)
		return nil
	}

	switch classifyError(err) {
	case errFatal:
		return fatalMessage(err)

	case errUnknown:
		logger.Error("Failed to play the media", "error", err, "index", index, "chatID", chatID)
		return fmt.Errorf("playback failed: %w", err)

	case errRetryOnce:
		err = c.playMedia(bot, chatID, filePath, video, ffmpegParameters, call, index)
		if err == nil {
			_ = db.Instance.SetAssistant(chatID, index)
			return nil
		}
		if classifyError(err) != errRotate {
			return fmt.Errorf("playback failed: %w", err)
		}
		fallthrough // GROUPCALL_ADD_PARTICIPANTS_FAILED can escalate to rotation

	case errRotate:
		c.evictAssistant(chatID, index, err)
	}

	return c.rotateAndPlay(bot, chatID, filePath, video, ffmpegParameters, map[int]bool{index: true}, err)
}

// evictAssistant cleans up state for an assistant that can no longer serve a chat.
func (c *TelegramCalls) evictAssistant(chatID int64, index int, err error) {
	_ = db.Instance.RemoveAssistant(chatID)
	if strings.Contains(err.Error(), "CHANNELS_TOO_MUCH") {
		// Protect the very chat that triggered this eviction: it's usually
		// either brand new (assistant just joined it for this play attempt)
		// or otherwise not yet marked active because the queue was already
		// cleared above. Without this, the LeaveAllForClient sweep below
		// would immediately kick the assistant back out of it, producing a
		// visible "join then leave instantly" pattern for that group.
		c.mu.RLock()
		call, ok := c.assistants[index]
		c.mu.RUnlock()
		if ok {
			c.markRecentlyJoined(chatID, call.App.Me().ID)
		}
		go func() { _, _ = c.LeaveAllForClient(index) }()
	}
}

func (c *TelegramCalls) playMedia(bot *td.Client, chatID int64, filePath string, video bool, ffmpegParameters string, call *Assistant, index int) error {
	if chatID > 0 {
		return errors.New("private calls are not supported for media playback")
	}

	if err := c.joinAssistant(bot, chatID, call, index); err != nil {
		cache.ChatCache.ClearChat(chatID)
		return err
	}

	logger.Debug("Playing media in chat", "id", chatID, "path", filePath, "index", index)

	mediaDesc := getMediaDescription(filePath, video, ffmpegParameters)
	if err := call.Play(context.Background(), chatID, mediaDesc); err != nil {
		cache.ChatCache.ClearChat(chatID)
		return err
	}

	if db.Instance.GetLoggerStatus() {
		go sendLogger(bot, chatID, cache.ChatCache.GetPlayingTrack(chatID))
	}

	return nil
}

// rotateAndPlay iterates over all remaining assistants until one succeeds or all are exhausted.
func (c *TelegramCalls) rotateAndPlay(bot *td.Client, chatID int64, filePath string, video bool, ffmpegParameters string, tried map[int]bool, lastErr error) error {
	for {
		call, nextIndex, err := c.nextUntried(tried)
		if err != nil {
			logger.Error("Playback failed after full rotation", "error", lastErr, "chatID", chatID)
			return fmt.Errorf("playback failed after trying all assistants: %w", lastErr)
		}
		tried[nextIndex] = true

		err = c.playMedia(bot, chatID, filePath, video, ffmpegParameters, call, nextIndex)
		if err == nil {
			_ = db.Instance.SetAssistant(chatID, nextIndex)
			return nil
		}
		lastErr = err

		switch classifyError(err) {
		case errRetryOnce:
			err = c.playMedia(bot, chatID, filePath, video, ffmpegParameters, call, nextIndex)
			if err == nil {
				_ = db.Instance.SetAssistant(chatID, nextIndex)
				return nil
			}
			lastErr = err
			if classifyError(err) == errRotate {
				c.evictAssistant(chatID, nextIndex, err)
				continue
			}
			return fmt.Errorf("playback failed: %w", lastErr)

		case errRotate:
			c.evictAssistant(chatID, nextIndex, err)
			continue

		default:
			// errFatal or errUnknown — stop rotating.
			return fmt.Errorf("playback failed: %w", lastErr)
		}
	}
}

// nextUntried finds the next assistant index not yet tried in this rotation round.
func (c *TelegramCalls) nextUntried(tried map[int]bool) (*Assistant, int, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i, call := range c.assistants {
		if !tried[i] {
			return call, i, nil
		}
	}
	return nil, -1, errors.New("no untried assistants remain")
}
