/*
 * ArcMusicBot - Telegram Music Bot
 *  Copyright (c) 2025-2026 Team Arc
 *
 *  Licensed under GNU GPL v3
 *  See https://t.me/ArcUpdates
 */

package dl

import (
	"ashokshau/tgmusic/config"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ---------------------------------------------------------------------------
// Arc API response types
// ---------------------------------------------------------------------------

type arcJobResponse struct {
	Status string `json:"status"`
	JobID  string `json:"job_id"`
}

type arcJobStatusResponse struct {
	Status string `json:"status"`
	Job    struct {
		Status string `json:"status"`
		Result struct {
			PublicURL string `json:"public_url"`
		} `json:"result"`
	} `json:"job"`
}

// arcMediaDoc mirrors the Python Cache document structure in _api.py.
type arcMediaDoc struct {
	TrackID   string `bson:"track_id"`
	MessageID int64  `bson:"message_id"`
	IsVideo   bool   `bson:"is_video"`
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	arcDownloadEndpoint  = "/youtube/v2/download"
	arcJobStatusEndpoint = "/youtube/jobStatus"
	arcChunkSize         = 1024 * 1024
	arcMaxRetries        = 10
	arcJobRetries        = 3
)

// ---------------------------------------------------------------------------
// arcCache — owns its own MongoDB connection via DB_URI (like _api.py Cache)
// ---------------------------------------------------------------------------

type arcCache struct {
	client *mongo.Client
	col    *mongo.Collection // "medias" collection
}

// newArcCache opens a separate MongoDB connection using DB_URI.
// This mirrors the Python Cache class which connects via config.CACHE_DB.
func newArcCache() (*arcCache, error) {
	uri := config.DbURI
	if uri == "" {
		return nil, errors.New("arc cache: DB_URI / MONGO_URI not configured")
	}

	opts := options.Client().ApplyURI(uri).
		SetServerSelectionTimeout(12500 * time.Millisecond).
		SetConnectTimeout(15 * time.Second)

	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("arc cache: connect failed: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12500*time.Millisecond)
	defer cancel()
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("arc cache: ping failed: %w", err)
	}

	col := client.Database("arcapi").Collection("medias")

	// Ensure unique compound index on (track_id, is_video)
	idxCtx, idxCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer idxCancel()
	_, _ = col.Indexes().CreateOne(idxCtx, mongo.IndexModel{
		Keys:    bson.D{{Key: "track_id", Value: 1}, {Key: "is_video", Value: 1}},
		Options: options.Index().SetUnique(true),
	})

	slog.Info("[ArcCache] Connected to media cache DB")
	return &arcCache{client: client, col: col}, nil
}

// fetchID returns the Telegram message_id for a given videoID (mirrors _api.py fetch_id).
func (c *arcCache) fetchID(videoID string, video bool) (int64, error) {
	fname := videoID + ".mp3"
	if video {
		fname = videoID + ".mp4"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var doc arcMediaDoc
	err := c.col.FindOne(ctx, bson.M{"track_id": fname, "is_video": video}).Decode(&doc)
	if err != nil {
		return 0, err // includes mongo.ErrNoDocuments
	}
	return doc.MessageID, nil
}

// saveID upserts the message_id for a given videoID into the cache collection.
func (c *arcCache) saveID(videoID string, video bool, msgID int64) error {
	fname := videoID + ".mp3"
	if video {
		fname = videoID + ".mp4"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.col.UpdateOne(ctx,
		bson.M{"track_id": fname, "is_video": video},
		bson.M{"$set": bson.M{"track_id": fname, "is_video": video, "message_id": msgID}},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

// getTrack fetches the cached track from the Telegram media channel and returns
// the local file path (mirrors _api.py get_track).
func (c *arcCache) getTrack(videoID string, video bool) (string, error) {
	if config.MediaChannelID == 0 {
		return "", errors.New("arc cache: MEDIA_CHANNEL_ID not set")
	}

	msgID, err := c.fetchID(videoID, video)
	if err != nil {
		return "", err // cache miss
	}

	// Use DlBot if available, otherwise the primary bot
	bot := DlBot
	if bot == nil {
		return "", errors.New("arc cache: no bot client available to download from channel")
	}

	msg, err := bot.GetMessage(config.MediaChannelID, msgID)
	if err != nil {
		return "", fmt.Errorf("arc cache: GetMessage failed: %w", err)
	}

	file, err := msg.Download(bot, 1, 0, 0, true)
	if err != nil {
		return "", fmt.Errorf("arc cache: Download failed: %w", err)
	}
	if file == nil || file.Local == nil || file.Local.Path == "" {
		return "", errors.New("arc cache: downloaded file has no local path")
	}

	return file.Local.Path, nil
}

func (c *arcCache) close() {
	if c.client != nil {
		_ = c.client.Disconnect(context.Background())
	}
}

// ---------------------------------------------------------------------------
// arcAPIClient — Arc API job queue + DB channel cache (YouTube only)
// ---------------------------------------------------------------------------

type arcAPIClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	cache      *arcCache // nil if DB_URI not configured
}

func newArcAPIClient() *arcAPIClient {
	client := &arcAPIClient{
		baseURL: strings.TrimRight(config.ArcApiUrl, "/"),
		apiKey:  config.ArcApiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}

	// Connect cache DB — non-fatal if it fails
	if cache, err := newArcCache(); err != nil {
		slog.Warn("[ArcAPI] Media cache DB unavailable", "error", err)
	} else {
		client.cache = cache
	}

	return client
}

func (a *arcAPIClient) isAvailable() bool {
	return a.baseURL != "" && a.apiKey != ""
}

// ---------------------------------------------------------------------------
// createJob — POST to /youtube/v2/download (mirrors _api.py create_job)
// ---------------------------------------------------------------------------

func (a *arcAPIClient) createJob(videoID string, video bool) (string, error) {
	params := url.Values{
		"api_key": {a.apiKey},
		"query":   {videoID},
		"isVideo": {fmt.Sprintf("%v", video)},
	}
	reqURL := a.baseURL + arcDownloadEndpoint + "?" + params.Encode()

	for attempt := 0; attempt < arcJobRetries; attempt++ {
		resp, err := a.httpClient.Get(reqURL)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			time.Sleep(time.Second)
			continue
		}
		var data arcJobResponse
		if err := json.Unmarshal(body, &data); err != nil || data.Status != "queued" || data.JobID == "" {
			time.Sleep(time.Second)
			continue
		}
		return data.JobID, nil
	}
	return "", fmt.Errorf("arc api: failed to create job for %s after %d attempts", videoID, arcJobRetries)
}

// ---------------------------------------------------------------------------
// pollJobURL — GET /youtube/jobStatus (mirrors _api.py get_url)
// ---------------------------------------------------------------------------

func (a *arcAPIClient) pollJobURL(jobID string) (string, error) {
	pollClient := &http.Client{Timeout: 10 * time.Second}
	reqURL := a.baseURL + arcJobStatusEndpoint + "?job_id=" + url.QueryEscape(jobID)

	for attempt := 1; attempt <= arcMaxRetries; attempt++ {
		resp, err := pollClient.Get(reqURL)
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			time.Sleep(3 * time.Second)
			continue
		}
		var data arcJobStatusResponse
		if err := json.Unmarshal(body, &data); err != nil {
			time.Sleep(3 * time.Second)
			continue
		}
		if data.Status != "success" || data.Job.Status != "done" {
			time.Sleep(3 * time.Second)
			continue
		}
		publicURL := data.Job.Result.PublicURL
		if publicURL == "" {
			return "", fmt.Errorf("arc api: job %s done but no public_url", jobID)
		}
		fullURL := a.baseURL + publicURL
		slog.Info("ArcApi: received download URL", "attempt", attempt, "url", fullURL)
		return fullURL, nil
	}
	return "", fmt.Errorf("arc api: job %s did not complete after %d attempts", jobID, arcMaxRetries)
}

// ---------------------------------------------------------------------------
// saveFile — streams download to disk (mirrors _api.py save_file)
// ---------------------------------------------------------------------------

func (a *arcAPIClient) saveFile(dlURL string) (string, error) {
	segments := strings.Split(dlURL, "/")
	filename := segments[len(segments)-1]
	if filename == "" {
		filename = "arc_download"
	}
	fpath := filepath.Join(config.DownloadsDir, filename)

	noTimeoutClient := &http.Client{}
	resp, err := noTimeoutClient.Get(dlURL)
	if err != nil {
		return "", fmt.Errorf("arc api: fetch file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("arc api: unexpected status %d fetching file", resp.StatusCode)
	}

	f, err := os.Create(fpath)
	if err != nil {
		return "", fmt.Errorf("arc api: create file %s: %w", fpath, err)
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, arcChunkSize)
	if _, err := io.CopyBuffer(f, resp.Body, buf); err != nil {
		return "", fmt.Errorf("arc api: write file: %w", err)
	}
	return fpath, nil
}

// ---------------------------------------------------------------------------
// DownloadYouTube — public entry point (mirrors _api.py download)
//
// Priority:
//  1. DB channel cache (DB_URI + MEDIA_CHANNEL_ID)
//  2. Arc API job queue  (API_URL + API_KEY)
//  3. caller falls back to yt-dlp
// ---------------------------------------------------------------------------

func (a *arcAPIClient) DownloadYouTube(videoID string, video bool) (string, error) {
	if !a.isAvailable() {
		return "", errors.New("arc api: not configured (missing ARC_API_URL / ARC_API_KEY)")
	}

	// 1. DB channel cache check (mirrors _api.py cache.get_track)
	if a.cache != nil {
		if fpath, err := a.cache.getTrack(videoID, video); err == nil && fpath != "" {
			slog.Info("ArcApi: retrieved from DB channel cache", "videoID", videoID)
			return fpath, nil
		}
	}

	// 2. Arc API job queue (2 attempts, matching _api.py download loop)
	for attempt := 0; attempt < 2; attempt++ {
		jobID, err := a.createJob(videoID, video)
		if err != nil {
			slog.Warn("ArcApi: create job failed", "attempt", attempt, "error", err)
			if attempt == 0 {
				time.Sleep(2 * time.Second)
			}
			continue
		}

		dlURL, err := a.pollJobURL(jobID)
		if err != nil {
			slog.Warn("ArcApi: poll job failed", "attempt", attempt, "error", err)
			if attempt == 0 {
				time.Sleep(2 * time.Second)
			}
			continue
		}

		fpath, err := a.saveFile(dlURL)
		if err != nil {
			slog.Warn("ArcApi: save file failed", "attempt", attempt, "error", err)
			if attempt == 0 {
				time.Sleep(2 * time.Second)
			}
			continue
		}

		return fpath, nil
	}

	return "", fmt.Errorf("arc api: all download attempts failed for videoID %s", videoID)
}

// SaveToDBCache saves a newly uploaded media message ID back to the cache DB.
// Call this after uploading a downloaded file to MEDIA_CHANNEL_ID.
func (a *arcAPIClient) SaveToDBCache(videoID string, video bool, msgID int64) {
	if a.cache == nil {
		return
	}
	if err := a.cache.saveID(videoID, video, msgID); err != nil {
		slog.Warn("ArcApi: failed to save to DB cache", "videoID", videoID, "error", err)
	} else {
		slog.Info("ArcApi: saved to DB cache", "videoID", videoID, "msgID", msgID)
	}
}

// ArcAPI is the package-level singleton Arc API client.
var ArcAPI = newArcAPIClient()
