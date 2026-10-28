package music

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"
)

const VoiceWorkerURL = "http://127.0.0.1:8081"

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

type WorkerPlayRequest struct {
	GuildID   string `json:"guild_id"`
	ChannelID string `json:"channel_id"`
	URL       string `json:"url"`
	Title     string `json:"title"`
}

type WorkerPlayResponse struct {
	Success     bool   `json:"success"`
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	DurationSec int    `json:"duration_sec"`
	Duration    string `json:"duration"`
	Cover       string `json:"cover"`
	URL         string `json:"url"`
	Error       string `json:"error,omitempty"`
}

type WorkerVolumeRequest struct {
	Volume int `json:"volume"`
}

type WorkerChannelRequest struct {
	GuildID   string `json:"guild_id"`
	ChannelID string `json:"channel_id"`
}

// EnsureVoiceWorkerRunning checks if voice worker is responding, and starts it if not
func EnsureVoiceWorkerRunning() {
	if isWorkerHealthy() {
		log.Println("✅ [Voice Worker] Worker is already active on http://127.0.0.1:8081")
		return
	}

	log.Println("🚀 [Voice Worker] Starting voice worker background process...")
	workerDir := "voice-worker"
	if _, err := os.Stat(workerDir); os.IsNotExist(err) {
		workerDir = "/Users/rittinun/Desktop/discord-bot-backend/voice-worker"
	}

	cmd := exec.Command("node", "--env-file=../.env", "server.js")
	cmd.Dir = workerDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Printf("⚠️ [Voice Worker] Failed to start voice worker: %v", err)
		return
	}

	go func() {
		_ = cmd.Wait()
	}()

	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		if isWorkerHealthy() {
			log.Println("🟢 [Voice Worker] Successfully connected to voice worker on http://127.0.0.1:8081")
			return
		}
	}
	log.Println("⚠️ [Voice Worker] Timed out waiting for voice worker health check")
}

func isWorkerHealthy() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", VoiceWorkerURL+"/status", nil)
	if err != nil {
		return false
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// PlayInDiscord instructs voice-worker to connect and stream real audio into Discord
func PlayInDiscord(guildID, channelID, query, title string) (*WorkerPlayResponse, error) {
	payload := WorkerPlayRequest{
		GuildID:   guildID,
		ChannelID: channelID,
		URL:       query,
		Title:     title,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Post(VoiceWorkerURL+"/play", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("⚠️ [Voice Worker] Call /play error: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	var workerResp WorkerPlayResponse
	if err := json.NewDecoder(resp.Body).Decode(&workerResp); err != nil {
		return nil, err
	}

	return &workerResp, nil
}

// PauseInDiscord pauses playback in Discord
func PauseInDiscord() error {
	resp, err := httpClient.Post(VoiceWorkerURL+"/pause", "application/json", nil)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// ResumeInDiscord resumes playback in Discord
func ResumeInDiscord() error {
	resp, err := httpClient.Post(VoiceWorkerURL+"/resume", "application/json", nil)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// StopInDiscord stops playback and disconnects bot from voice in Discord
func StopInDiscord() error {
	resp, err := httpClient.Post(VoiceWorkerURL+"/stop", "application/json", nil)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// DisconnectInDiscord completely leaves voice channel and stops player
func DisconnectInDiscord() error {
	resp, err := httpClient.Post(VoiceWorkerURL+"/disconnect", "application/json", nil)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// SetVolumeInDiscord updates volume in Discord voice stream
func SetVolumeInDiscord(vol int) error {
	payload := WorkerVolumeRequest{Volume: vol}
	body, _ := json.Marshal(payload)
	resp, err := httpClient.Post(VoiceWorkerURL+"/volume", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// SetChannelInDiscord moves voice bot to target channel
func SetChannelInDiscord(guildID, channelID string) error {
	payload := WorkerChannelRequest{GuildID: guildID, ChannelID: channelID}
	body, _ := json.Marshal(payload)
	resp, err := httpClient.Post(VoiceWorkerURL+"/set_channel", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}
