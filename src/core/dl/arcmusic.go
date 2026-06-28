/*
 * TgMusicBot - Telegram Music Bot
 *  Copyright (c) 2025-2026 Team Arc
 *
 *  Licensed under GNU GPL v3
 *  See https://github.com/AshokShau/TgMusicBot
 */

package dl

// arcmusic.go — YouTube download via ArcMusic API with direct DB cache.
//
// Download priority (mirrors tosu4/AnonXMusic/platforms/Youtube.py _optimized_download):
//   1. Local file cache  — if the file already exists on disk, return it immediately.
//   2. Telegram DB cache — look up arcapi.medias (MongoDB) for a pre-uploaded file;
//                          download it from the media channel via the bot client.
//   3. ArcMusic API      — create job → poll → save (api.arcmusic.fun).
//   4. yt-dlp fallback   — direct download via yt-dlp when all else fails.

import (
	"ashokshau/tgmusic/config"
	"ashokshau/tgmusic/src/core/db"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	td "github.com/AshokShau/gotdbot"
)

const (
	arcCreateRetries = 3
	arcPollRetries   = 12
	arcPollInterval  = 3 * time.Second
	arcJobTimeout    = 90 * time.Second
)

// ── API response shapes ───────────────────────────────────────────────────────

type arcJobResponse struct {
	Status string `json:"status"`
	JobID  string `json:"job_id"`
}

type arcStatusResponse struct {
	Status string `json:"status"`
	Job    struct {
		Status string `json:"status"`
		Result struct {
			PublicURL string `json:"public_url"`
		} `json:"result"`
	} `json:"job"`
}

// ── Step 1: create a download job ────────────────────────────────────────────

func arcCreateJob(apiURL, apiKey, videoID string, isVideo bool) (string, error) {
	endpoint := strings.TrimRight(apiURL, "/") + "/youtube/v2/download"
	params := url.Values{
		"api_key": {apiKey},
		"query":   {videoID},
		"isVideo": {fmt.Sprintf("%v", isVideo)},
	}
	fullURL := endpoint + "?" + params.Encode()

	for attempt := 0; attempt < arcCreateRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			cancel()
			continue
		}

		resp, err := client.Do(req)
		cancel()
		if err != nil {
			slog.Warn("[ArcMusic] create_job request failed", "attempt", attempt+1, "error", err)
			time.Sleep(time.Second)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			slog.Warn("[ArcMusic] create_job bad status", "status", resp.StatusCode, "attempt", attempt+1)
			time.Sleep(time.Second)
			continue
		}

		var data arcJobResponse
		if err = json.Unmarshal(body, &data); err != nil || data.JobID == "" {
			slog.Warn("[ArcMusic] create_job invalid response", "attempt", attempt+1)
			time.Sleep(time.Second)
			continue
		}

		return data.JobID, nil
	}
	return "", fmt.Errorf("[ArcMusic] create_job failed after %d attempts for %s", arcCreateRetries, videoID)
}

// ── Step 2: poll for the download URL ────────────────────────────────────────

func arcPollJob(apiURL, jobID string) (string, error) {
	endpoint := strings.TrimRight(apiURL, "/") + "/youtube/jobStatus"
	fullURL := endpoint + "?" + url.Values{"job_id": {jobID}}.Encode()

	for attempt := 1; attempt <= arcPollRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			cancel()
			time.Sleep(arcPollInterval)
			continue
		}

		resp, err := client.Do(req)
		cancel()
		if err != nil {
			time.Sleep(arcPollInterval)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			time.Sleep(arcPollInterval)
			continue
		}

		var data arcStatusResponse
		if err = json.Unmarshal(body, &data); err != nil {
			time.Sleep(arcPollInterval)
			continue
		}

		if data.Status != "success" || data.Job.Status != "done" {
			time.Sleep(arcPollInterval)
			continue
		}

		publicURL := data.Job.Result.PublicURL
		if publicURL == "" {
			slog.Warn("[ArcMusic] poll_job: no public_url", "job_id", jobID)
			break
		}
		if strings.HasPrefix(publicURL, "/") {
			publicURL = strings.TrimRight(apiURL, "/") + publicURL
		}

		slog.Info("[ArcMusic] Download URL ready", "attempt", attempt, "job_id", jobID)
		return publicURL, nil
	}

	return "", fmt.Errorf("[ArcMusic] poll_job exhausted %d retries for job %s", arcPollRetries, jobID)
}

// ── Step 3: stream the file to disk ──────────────────────────────────────────

func arcSaveFile(dlURL, outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), arcJobTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	tmp := outPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	if _, err = io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to write file: %w", err)
	}
	_ = f.Close()

	if err = os.Rename(tmp, outPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	info, err := os.Stat(outPath)
	if err != nil || info.Size() == 0 {
		_ = os.Remove(outPath)
		return fmt.Errorf("downloaded file is empty or missing at %s", outPath)
	}
	return nil
}

// ── Arc API full cycle ────────────────────────────────────────────────────────

func arcAPIDownload(videoID string, isVideo bool, outPath string) error {
	apiURL := config.ApiUrl
	apiKey := config.ApiKey
	if apiURL == "" || apiKey == "" {
		return fmt.Errorf("[ArcMusic] API_URL or API_KEY not configured")
	}

	for cycle := 1; cycle <= 2; cycle++ {
		jobID, err := arcCreateJob(apiURL, apiKey, videoID, isVideo)
		if err != nil {
			slog.Error("[ArcMusic] create_job failed", "cycle", cycle, "video_id", videoID, "error", err)
			if cycle == 1 {
				time.Sleep(2 * time.Second)
			}
			continue
		}

		dlURL, err := arcPollJob(apiURL, jobID)
		if err != nil {
			slog.Error("[ArcMusic] poll_job failed", "cycle", cycle, "job_id", jobID, "error", err)
			if cycle == 1 {
				time.Sleep(2 * time.Second)
			}
			continue
		}

		if err = arcSaveFile(dlURL, outPath); err != nil {
			slog.Error("[ArcMusic] save_file failed", "cycle", cycle, "url", dlURL, "error", err)
			if cycle == 1 {
				time.Sleep(2 * time.Second)
			}
			continue
		}

		slog.Info("[ArcMusic] API download success", "path", outPath)
		return nil
	}

	return fmt.Errorf("[ArcMusic] all API download cycles failed for %s", videoID)
}

// ── Telegram DB cache download ────────────────────────────────────────────────
//
// Looks up arcapi.medias for a pre-uploaded message in the media channel,
// then downloads the file via the bot client — exactly like tosu4's
// _download_from_media_db(track_id, is_video).

func downloadFromMediaDB(bot *td.Client, videoID string, isVideo bool, outPath string) error {
	if db.Instance == nil {
		return fmt.Errorf("[MediaDB] database not initialised")
	}
	if config.MediaChannelId == 0 {
		return fmt.Errorf("[MediaDB] MEDIA_CHANNEL_ID not configured")
	}

	// Try the three key variants tosu4 uses
	ext := "mp3"
	if isVideo {
		ext = "mp4"
	}
	suffix := "a"
	if isVideo {
		suffix = "v"
	}
	keys := []string{
		fmt.Sprintf("%s.%s", videoID, ext),
		videoID,
		fmt.Sprintf("%s_%s.%s", videoID, suffix, ext),
	}

	var msgID int64
	for _, k := range keys {
		if db.Instance.IsMedia(k, isVideo) {
			msgID = db.Instance.GetMediaMsgID(k, isVideo)
			if msgID > 0 {
				break
			}
		}
	}
	if msgID == 0 {
		return fmt.Errorf("[MediaDB] no cached entry for %s (video=%v)", videoID, isVideo)
	}

	// Build a private channel URL: https://t.me/c/CHATID/MSGID
	// Strip the -100 prefix that Telegram uses for channel IDs
	chanID := config.MediaChannelId
	if chanID < 0 {
		chanID = -chanID
	}
	// Remove leading 100 from supergroup IDs (e.g. -1001234567890 → 1234567890)
	chanIDStr := fmt.Sprintf("%d", chanID)
	if strings.HasPrefix(chanIDStr, "100") {
		chanIDStr = chanIDStr[3:]
	}
	msgURL := fmt.Sprintf("https://t.me/c/%s/%d", chanIDStr, msgID)

	dlBot := bot
	if DlBot != nil {
		dlBot = DlBot
	}

	msg, err := dlBot.GetMessageLinkInfo(msgURL)
	if err != nil || msg.Message == nil {
		return fmt.Errorf("[MediaDB] failed to get message %s: %w", msgURL, err)
	}

	file, err := msg.Message.Download(dlBot, 1, 0, 0, true)
	if err != nil || file == nil || file.Local == nil {
		return fmt.Errorf("[MediaDB] failed to download file from channel message: %w", err)
	}

	localPath := file.Local.Path
	if localPath == "" || !file.Local.IsDownloadingCompleted {
		return fmt.Errorf("[MediaDB] file download incomplete for msg %s", msgURL)
	}

	// Copy to our standard output path if TDLib wrote it elsewhere
	if localPath != outPath {
		if err = os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return fmt.Errorf("[MediaDB] mkdir failed: %w", err)
		}
		in, err := os.Open(localPath)
		if err != nil {
			return fmt.Errorf("[MediaDB] open source file: %w", err)
		}
		out, err := os.Create(outPath)
		if err != nil {
			_ = in.Close()
			return fmt.Errorf("[MediaDB] create dest file: %w", err)
		}
		_, copyErr := io.Copy(out, in)
		_ = in.Close()
		_ = out.Close()
		if copyErr != nil {
			_ = os.Remove(outPath)
			return fmt.Errorf("[MediaDB] copy failed: %w", copyErr)
		}
	}

	slog.Info("[MediaDB] Channel cache hit", "video_id", videoID, "msg_id", msgID, "path", outPath)
	return nil
}

// ── Public entry point ────────────────────────────────────────────────────────

// ArcMusicDownload downloads a YouTube track using the full priority chain:
//  1. Local disk cache
//  2. Telegram media-channel DB cache (arcapi.medias → MongoDB)
//  3. ArcMusic API (api.arcmusic.fun) job-based download
//  4. yt-dlp fallback (caller is responsible — this func returns an error)
func ArcMusicDownload(bot *td.Client, videoID string, isVideo bool) (string, error) {
	ext := "m4a"
	if isVideo {
		ext = "mp4"
	}
	outPath := filepath.Join(config.DownloadsDir, videoID+"."+ext)

	// 1. Local file cache
	if info, err := os.Stat(outPath); err == nil && info.Size() > 0 {
		slog.Info("[ArcMusic] Local disk cache hit", "path", outPath)
		return outPath, nil
	}

	// 2. Telegram DB cache
	if bot != nil && config.MediaChannelId != 0 && db.Instance != nil {
		if err := downloadFromMediaDB(bot, videoID, isVideo, outPath); err == nil {
			return outPath, nil
		} else {
			slog.Debug("[ArcMusic] DB cache miss", "video_id", videoID, "reason", err)
		}
	}

	// 3. ArcMusic API
	if err := arcAPIDownload(videoID, isVideo, outPath); err == nil {
		return outPath, nil
	} else {
		slog.Warn("[ArcMusic] API download failed, caller should fall back to yt-dlp",
			"video_id", videoID, "error", err)
	}

	return "", fmt.Errorf("[ArcMusic] all download strategies failed for %s", videoID)
}
