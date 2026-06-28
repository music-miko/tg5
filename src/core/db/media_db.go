/*
 * TgMusicBot - Telegram Music Bot
 *  Copyright (c) 2025-2026 Team Arc
 *
 *  Licensed under GNU GPL v3
 *  See https://github.com/AshokShau/TgMusicBot
 */

package db

// media_db.go — YouTube media file cache helpers.
//
// Mirrors tosu4/AnonXMusic/platforms/Youtube.py:
//   _is_media(track_id, is_video)        → bool
//   _get_media_msg_id(track_id, is_video) → int|None
//
// Documents in arcapi.medias have the shape:
//   { track_id: string, isVideo: bool, message_id: int64 }
//
// The collection is populated by the Telegram media-channel uploader in tosu4.
// TgMusicBot reads from it (read-only) to skip re-downloading already cached tracks.

import (
	"context"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type mediaDoc struct {
	TrackID   string `bson:"track_id"`
	IsVideo   bool   `bson:"isVideo"`
	MessageID int64  `bson:"message_id"`
}

func (db *Database) mediaCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// IsMedia returns true if a cached media entry exists for trackID in the arcapi.medias collection.
func (db *Database) IsMedia(trackID string, isVideo bool) bool {
	if db.mediaDB == nil || trackID == "" {
		return false
	}
	ctx, cancel := db.mediaCtx()
	defer cancel()

	filter := bson.M{"track_id": trackID, "isVideo": isVideo}
	res := db.mediaDB.FindOne(ctx, filter, nil)
	if res.Err() != nil {
		return false
	}
	return true
}

// GetMediaMsgID returns the Telegram message_id for a cached track, or 0 if not found.
func (db *Database) GetMediaMsgID(trackID string, isVideo bool) int64 {
	if db.mediaDB == nil || trackID == "" {
		return 0
	}
	ctx, cancel := db.mediaCtx()
	defer cancel()

	filter := bson.M{"track_id": trackID, "isVideo": isVideo}
	var doc mediaDoc
	if err := db.mediaDB.FindOne(ctx, filter, nil).Decode(&doc); err != nil {
		slog.Debug("[MediaDB] GetMediaMsgID not found", "track_id", trackID, "error", err)
		return 0
	}
	return doc.MessageID
}
