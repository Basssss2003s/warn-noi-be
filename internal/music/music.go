package music

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"discord-bot-dashboard/backend/internal/models"
)

// Track represents a single audio track
type Track struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Duration    string `json:"duration"`
	DurationSec int    `json:"duration_sec"`
	Cover       string `json:"cover"`
	URL         string `json:"url"`
	AddedBy     string `json:"added_by"`
}

// State represents the current playback and queue state
type State struct {
	IsPlaying      bool    `json:"is_playing"`
	Volume         int     `json:"volume"`
	PositionSec    int     `json:"position_sec"`
	CurrentTime    string  `json:"current_time"`
	CurrentTrack   *Track  `json:"current_track"`
	Queue          []Track `json:"queue"`
	VoiceChannel   string  `json:"voice_channel"`
	VoiceChannelID string  `json:"voice_channel_id"`
}

// Manager manages guild music playback
type Manager struct {
	mu             sync.RWMutex
	guildID        string
	isPlaying      bool
	volume         int
	positionSec    int
	currentTrack   *Track
	queue          []Track
	voiceChannel   string
	voiceChannelID string
}

// Hooks for voice connection & state persistence
type VoiceJoiner func(guildID, channelID string) error
type VoiceLeaver func(guildID string) error
type StateSaver func(guildID string, state *models.MusicState)
type StateLoader func(guildID string) *models.MusicState

var (
	onJoinVoice  VoiceJoiner
	onLeaveVoice VoiceLeaver
	onSaveState  StateSaver
	onLoadState  StateLoader

	managers = make(map[string]*Manager)
	mgrLock  sync.Mutex
)

// SetHooks configures bot callbacks for voice joining and store persistence
func SetHooks(joiner VoiceJoiner, leaver VoiceLeaver, saver StateSaver, loader StateLoader) {
	onJoinVoice = joiner
	onLeaveVoice = leaver
	onSaveState = saver
	onLoadState = loader
}

// GetManager retrieves or creates a music manager for a guild
func GetManager(guildID string) *Manager {
	mgrLock.Lock()
	defer mgrLock.Unlock()

	if guildID == "" || guildID == "undefined" {
		guildID = "1564174536029507635"
	}

	if m, ok := managers[guildID]; ok {
		return m
	}

	m := &Manager{
		guildID:        guildID,
		volume:         80,
		voiceChannel:   "สนามหญ้า",
		voiceChannelID: "1564174536591671350",
		queue:          make([]Track, 0),
	}

	// Restore previous music state from persistent store if available
	if onLoadState != nil {
		if saved := onLoadState(guildID); saved != nil {
			m.isPlaying = saved.IsPlaying
			m.volume = saved.Volume
			if m.volume <= 0 {
				m.volume = 80
			}
			m.positionSec = saved.PositionSec
			if saved.VoiceChannel != "" {
				m.voiceChannel = saved.VoiceChannel
			}
			if saved.VoiceChannelID != "" {
				m.voiceChannelID = saved.VoiceChannelID
			}
			if saved.CurrentTrack != nil {
				t := Track{
					ID:          saved.CurrentTrack.ID,
					Title:       saved.CurrentTrack.Title,
					Artist:      saved.CurrentTrack.Artist,
					Duration:    saved.CurrentTrack.Duration,
					DurationSec: saved.CurrentTrack.DurationSec,
					Cover:       saved.CurrentTrack.Cover,
					URL:         saved.CurrentTrack.URL,
					AddedBy:     saved.CurrentTrack.AddedBy,
				}
				m.currentTrack = &t
			}
			for _, q := range saved.Queue {
				m.queue = append(m.queue, Track{
					ID:          q.ID,
					Title:       q.Title,
					Artist:      q.Artist,
					Duration:    q.Duration,
					DurationSec: q.DurationSec,
					Cover:       q.Cover,
					URL:         q.URL,
					AddedBy:     q.AddedBy,
				})
			}
			log.Printf("📥 [Music] Restored saved state for guild %s: %s (Queue: %d, Playing: %v)",
				guildID, func() string {
					if m.currentTrack != nil {
						return m.currentTrack.Title
					}
					return "none"
				}(), len(m.queue), m.isPlaying)
		}
	}

	managers[guildID] = m
	go m.tickerLoop()
	return m
}

// ExecuteCommand processes terminal commands like /play, /que, /list
func (m *Manager) ExecuteCommand(cmdStr string) (string, State) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return "⚠️ กรุณาพิมพ์คำสั่ง เช่น /play, /que, /list หรือ /help", m.getStateUnsafe()
	}

	parts := strings.SplitN(cmdStr, " ", 2)
	cmd := strings.ToLower(parts[0])
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}

	switch cmd {
	case "/play", "play":
		if arg == "" {
			if m.currentTrack != nil {
				m.isPlaying = true
				m.persist()
				go ResumeInDiscord()
				if onJoinVoice != nil {
					go onJoinVoice(m.guildID, m.voiceChannelID)
				}
				return fmt.Sprintf("▶️ เล่นเพลงต่อ: %s — %s", m.currentTrack.Title, m.currentTrack.Artist), m.getStateUnsafe()
			}
			return "⚠️ วิธีใช้: /play <ชื่อเพลง หรือ ลิงก์ YouTube>", m.getStateUnsafe()
		}

		track := resolveTrack(arg)
		m.currentTrack = &track
		m.isPlaying = true
		m.positionSec = 0
		m.persist()
		m.streamTrack(track)
		if onJoinVoice != nil {
			go onJoinVoice(m.guildID, m.voiceChannelID)
		}
		return fmt.Sprintf("🎵 [Warn-noi Music] กำลังเล่น: %s — %s (%s)\n🔊 ห้องเสียง: %s | ระดับเสียง: %d%%",
			track.Title, track.Artist, track.Duration, m.voiceChannel, m.volume), m.getStateUnsafe()

	case "/que", "/queue", "que", "queue":
		if arg == "" {
			return "⚠️ วิธีใช้: /que <ชื่อเพลง หรือ ลิงก์ YouTube เพื่อต่อคิว>", m.getStateUnsafe()
		}

		track := resolveTrack(arg)
		// If nothing is playing, play immediately
		if m.currentTrack == nil || !m.isPlaying {
			m.currentTrack = &track
			m.isPlaying = true
			m.positionSec = 0
			m.persist()
			m.streamTrack(track)
			if onJoinVoice != nil {
				go onJoinVoice(m.guildID, m.voiceChannelID)
			}
			return fmt.Sprintf("🎵 [Warn-noi Music] คิวว่าง เริ่มเล่นทันที: %s — %s (%s)",
				track.Title, track.Artist, track.Duration), m.getStateUnsafe()
		}

		m.queue = append(m.queue, track)
		m.persist()
		return fmt.Sprintf("➕ [Warn-noi Music] เพิ่มเพลงเข้าคิวลำดับที่ #%d: %s — %s (%s)",
			len(m.queue), track.Title, track.Artist, track.Duration), m.getStateUnsafe()

	case "/list", "list":
		if m.currentTrack == nil && len(m.queue) == 0 {
			return "📭 [Warn-noi Music] ขณะนี้ไม่มีเพลงที่กำลังเล่นหรืออยู่ในคิว\n💡 ใช้คำสั่ง /play <ชื่อเพลง> หรือ /que <ชื่อเพลง> เพื่อเริ่มฟังเพลง", m.getStateUnsafe()
		}

		var sb strings.Builder
		sb.WriteString("📜 [Warn-noi Music] รายการสถานะคิวเพลง:\n")
		if m.currentTrack != nil {
			statusIcon := "▶️"
			if !m.isPlaying {
				statusIcon = "⏸️"
			}
			sb.WriteString(fmt.Sprintf("%s กำลังเล่น: %s — %s [%s / %s]\n",
				statusIcon, m.currentTrack.Title, m.currentTrack.Artist, formatSeconds(m.positionSec), m.currentTrack.Duration))
		}

		if len(m.queue) == 0 {
			sb.WriteString("--- ไม่มีเพลงรอในคิวถัดไป ---")
		} else {
			sb.WriteString(fmt.Sprintf("--- คิวรอถัดไป (%d เพลง) ---\n", len(m.queue)))
			for i, t := range m.queue {
				sb.WriteString(fmt.Sprintf("%d. %s — %s (%s)\n", i+1, t.Title, t.Artist, t.Duration))
			}
		}
		return sb.String(), m.getStateUnsafe()

	case "/skip", "skip", "/next", "next":
		if len(m.queue) > 0 {
			nextTrack := m.queue[0]
			m.queue = m.queue[1:]
			m.currentTrack = &nextTrack
			m.isPlaying = true
			m.positionSec = 0
			m.persist()
			m.streamTrack(nextTrack)
			return fmt.Sprintf("⏭️ [Warn-noi Music] ข้ามเพลงแล้ว! กำลังเล่นเพลงถัดไป: %s — %s",
				nextTrack.Title, nextTrack.Artist), m.getStateUnsafe()
		}
		m.currentTrack = nil
		m.isPlaying = false
		m.positionSec = 0
		m.persist()
		go StopInDiscord()
		return "⏭️ [Warn-noi Music] ข้ามเพลงเรียบร้อย (หมดคิวแล้ว หยุดเล่น)", m.getStateUnsafe()

	case "/pause", "pause":
		m.isPlaying = false
		m.persist()
		go PauseInDiscord()
		return "⏸️ [Warn-noi Music] พักการเล่นเพลงชั่วคราว", m.getStateUnsafe()

	case "/resume", "resume":
		m.isPlaying = true
		m.persist()
		go ResumeInDiscord()
		if onJoinVoice != nil {
			go onJoinVoice(m.guildID, m.voiceChannelID)
		}
		return "▶️ [Warn-noi Music] เล่นเพลงต่อแล้ว", m.getStateUnsafe()

	case "/stop", "stop":
		state := m.Stop()
		return "⏹️ [Warn-noi Music] หยุดการเล่น ล้างคิวเพลง และบอทออกจากห้องเสียงเรียบร้อย", state

	case "/vol", "/volume", "vol", "volume":
		if arg != "" {
			if v, err := strconv.Atoi(arg); err == nil && v >= 0 && v <= 100 {
				m.volume = v
				m.persist()
				go SetVolumeInDiscord(v)
				return fmt.Sprintf("🔊 [Warn-noi Music] ปรับระดับเสียงเป็น %d%%", v), m.getStateUnsafe()
			}
		}
		return fmt.Sprintf("🔊 ระดับเสียงปัจจุบัน: %d%% (ใช้คำสั่ง /vol 0-100 เพื่อปรับ)", m.volume), m.getStateUnsafe()

	case "/help", "help":
		return `💡 [Warn-noi Music Terminal] คำสั่งที่รองรับ:
- /play <ชื่อเพลง/ลิงก์ YouTube> : เล่นเพลงทันที
- /que <ชื่อเพลง/ลิงก์ YouTube>  : ต่อคิวเพลงถัดไป
- /list                          : แสดงรายการคิวเพลง
- /skip                          : ข้ามไปเพลงถัดไป
- /pause                         : พักการเล่นเพลง
- /resume                        : เล่นต่อ
- /stop                          : หยุดเล่นและล้างคิว
- /vol <0-100>                   : ปรับระดับเสียง`, m.getStateUnsafe()

	default:
		return fmt.Sprintf("❓ ไม่พบคำสั่ง '%s' พิมพ์ /help เพื่อดูรายการคำสั่งทั้งหมด", cmd), m.getStateUnsafe()
	}
}

// GetState returns the thread-safe copy of State
func (m *Manager) GetState() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getStateUnsafe()
}

func (m *Manager) getStateUnsafe() State {
	queueCopy := make([]Track, len(m.queue))
	copy(queueCopy, m.queue)

	curTime := formatSeconds(m.positionSec)
	var curTrackCopy *Track
	if m.currentTrack != nil {
		t := *m.currentTrack
		curTrackCopy = &t
	}

	return State{
		IsPlaying:      m.isPlaying,
		Volume:         m.volume,
		PositionSec:    m.positionSec,
		CurrentTime:    curTime,
		CurrentTrack:   curTrackCopy,
		Queue:          queueCopy,
		VoiceChannel:   m.voiceChannel,
		VoiceChannelID: m.voiceChannelID,
	}
}

func (m *Manager) persist() {
	if onSaveState == nil {
		return
	}
	st := m.getStateUnsafe()
	var cur *models.MusicTrack
	if st.CurrentTrack != nil {
		cur = &models.MusicTrack{
			ID:          st.CurrentTrack.ID,
			Title:       st.CurrentTrack.Title,
			Artist:      st.CurrentTrack.Artist,
			Duration:    st.CurrentTrack.Duration,
			DurationSec: st.CurrentTrack.DurationSec,
			Cover:       st.CurrentTrack.Cover,
			URL:         st.CurrentTrack.URL,
			AddedBy:     st.CurrentTrack.AddedBy,
		}
	}
	var qList []models.MusicTrack
	for _, q := range st.Queue {
		qList = append(qList, models.MusicTrack{
			ID:          q.ID,
			Title:       q.Title,
			Artist:      q.Artist,
			Duration:    q.Duration,
			DurationSec: q.DurationSec,
			Cover:       q.Cover,
			URL:         q.URL,
			AddedBy:     q.AddedBy,
		})
	}
	onSaveState(m.guildID, &models.MusicState{
		IsPlaying:      st.IsPlaying,
		Volume:         st.Volume,
		PositionSec:    st.PositionSec,
		CurrentTime:    st.CurrentTime,
		CurrentTrack:   cur,
		Queue:          qList,
		VoiceChannel:   st.VoiceChannel,
		VoiceChannelID: st.VoiceChannelID,
	})
}

// SetVoiceChannel switches target voice channel and connects bot
func (m *Manager) SetVoiceChannel(channelID, channelName string) State {
	m.mu.Lock()
	defer m.mu.Unlock()

	if channelID != "" {
		m.voiceChannelID = channelID
	}
	if channelName != "" {
		m.voiceChannel = channelName
	}
	m.persist()
	go SetChannelInDiscord(m.guildID, m.voiceChannelID)
	if onJoinVoice != nil {
		go onJoinVoice(m.guildID, m.voiceChannelID)
	}
	return m.getStateUnsafe()
}

// RemoveFromQueue removes a song at index
func (m *Manager) RemoveFromQueue(index int) State {
	m.mu.Lock()
	defer m.mu.Unlock()

	if index >= 0 && index < len(m.queue) {
		m.queue = append(m.queue[:index], m.queue[index+1:]...)
		m.persist()
	}
	return m.getStateUnsafe()
}

// TogglePlay pauses or resumes
func (m *Manager) TogglePlay() State {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.isPlaying = !m.isPlaying
	m.persist()
	if m.isPlaying {
		if m.currentTrack != nil {
			go ResumeInDiscord()
		}
		if onJoinVoice != nil {
			go onJoinVoice(m.guildID, m.voiceChannelID)
		}
	} else {
		go PauseInDiscord()
	}
	return m.getStateUnsafe()
}

// SkipTrack skips to the next track
func (m *Manager) SkipTrack() State {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.queue) > 0 {
		nextTrack := m.queue[0]
		m.queue = m.queue[1:]
		m.currentTrack = &nextTrack
		m.isPlaying = true
		m.positionSec = 0
		m.streamTrack(nextTrack)
	} else {
		m.currentTrack = nil
		m.isPlaying = false
		m.positionSec = 0
		go StopInDiscord()
	}
	m.persist()
	return m.getStateUnsafe()
}

// Stop stops playback, clears current track and queue, and disconnects bot from voice channel
func (m *Manager) Stop() State {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.isPlaying = false
	m.currentTrack = nil
	m.queue = make([]Track, 0)
	m.positionSec = 0
	m.persist()
	go StopInDiscord()
	go DisconnectInDiscord()
	if onLeaveVoice != nil {
		go onLeaveVoice(m.guildID)
	}
	return m.getStateUnsafe()
}

// ClearQueue clears queued songs without stopping current track
func (m *Manager) ClearQueue() State {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.queue = make([]Track, 0)
	m.persist()
	return m.getStateUnsafe()
}

// SetVolume sets the volume level
func (m *Manager) SetVolume(vol int) State {
	m.mu.Lock()
	defer m.mu.Unlock()

	if vol < 0 {
		vol = 0
	}
	if vol > 100 {
		vol = 100
	}
	m.volume = vol
	m.persist()
	go SetVolumeInDiscord(m.volume)
	return m.getStateUnsafe()
}

// Seek sets the playback position
func (m *Manager) Seek(sec int) State {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.positionSec = sec
	m.persist()
	return m.getStateUnsafe()
}

func (m *Manager) streamTrack(track Track) {
	go func(gID, cID string, t Track) {
		resp, err := PlayInDiscord(gID, cID, t.URL, t.Title)
		if err == nil && resp != nil && resp.Success {
			m.mu.Lock()
			if m.currentTrack != nil && m.isPlaying {
				if resp.Title != "" {
					m.currentTrack.Title = resp.Title
				}
				if resp.Artist != "" {
					m.currentTrack.Artist = resp.Artist
				}
				if resp.DurationSec > 0 {
					m.currentTrack.DurationSec = resp.DurationSec
					m.currentTrack.Duration = resp.Duration
				}
				if resp.Cover != "" {
					m.currentTrack.Cover = resp.Cover
				}
				if resp.URL != "" {
					m.currentTrack.URL = resp.URL
				}
				m.persist()
			}
			m.mu.Unlock()
		}
	}(m.guildID, m.voiceChannelID, track)
}

func (m *Manager) tickerLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		m.mu.Lock()
		if m.isPlaying && m.currentTrack != nil {
			m.positionSec++
			if m.currentTrack.DurationSec > 0 && m.positionSec >= m.currentTrack.DurationSec {
				// Song finished, auto-advance to next track in queue
				if len(m.queue) > 0 {
					next := m.queue[0]
					m.queue = m.queue[1:]
					m.currentTrack = &next
					m.positionSec = 0
					m.streamTrack(next)
				} else {
					m.currentTrack = nil
					m.isPlaying = false
					m.positionSec = 0
					go StopInDiscord()
				}
				m.persist()
			}
		}
		m.mu.Unlock()
	}
}

type ytOEmbedResponse struct {
	Title        string `json:"title"`
	AuthorName   string `json:"author_name"`
	ThumbnailURL string `json:"thumbnail_url"`
}

func resolveYouTubeInfo(rawURL string) (string, string, string, bool) {
	rawURL = strings.TrimSpace(rawURL)
	lower := strings.ToLower(rawURL)
	if !strings.Contains(lower, "youtube.com") && !strings.Contains(lower, "youtu.be") {
		return "", "", "", false
	}

	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}

	apiURL := "https://www.youtube.com/oembed?url=" + url.QueryEscape(rawURL) + "&format=json"
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		return "", "", "", false
	}
	defer resp.Body.Close()

	var data ytOEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", "", "", false
	}

	title := strings.TrimSpace(data.Title)
	artist := strings.TrimSpace(data.AuthorName)
	thumb := strings.TrimSpace(data.ThumbnailURL)

	if thumb == "" {
		thumb = "https://images.unsplash.com/photo-1511671782779-c97d3d27a1d4?w=200&auto=format&fit=crop&q=80"
	}
	if artist == "" {
		artist = "YouTube"
	}

	return title, artist, thumb, true
}

func resolveTrack(query string) Track {
	query = strings.TrimSpace(query)

	// 1. YouTube Link Resolution via oEmbed
	if ytTitle, ytArtist, ytCover, ok := resolveYouTubeInfo(query); ok {
		return Track{
			ID:          "trk-" + strconv.FormatInt(time.Now().UnixNano(), 36),
			Title:       ytTitle,
			Artist:      ytArtist,
			Duration:    "03:45",
			DurationSec: 225,
			Cover:       ytCover,
			URL:         query,
		}
	}

	lower := strings.ToLower(query)

	// Catalog of matches for instant aesthetic preview
	if strings.Contains(lower, "apt") {
		return Track{
			ID:          "trk-" + strconv.FormatInt(time.Now().UnixNano(), 36),
			Title:       "APT.",
			Artist:      "ROSE & Bruno Mars",
			Duration:    "02:50",
			DurationSec: 170,
			Cover:       "https://images.unsplash.com/photo-1511671782779-c97d3d27a1d4?w=200&auto=format&fit=crop&q=80",
			URL:         query,
		}
	}
	if strings.Contains(lower, "die with a smile") || strings.Contains(lower, "die with") {
		return Track{
			ID:          "trk-" + strconv.FormatInt(time.Now().UnixNano(), 36),
			Title:       "Die With A Smile",
			Artist:      "Lady Gaga & Bruno Mars",
			Duration:    "04:12",
			DurationSec: 252,
			Cover:       "https://images.unsplash.com/photo-1470225620780-dba8ba36b745?w=200&auto=format&fit=crop&q=80",
			URL:         query,
		}
	}
	if strings.Contains(lower, "ทรงอย่างแบด") || strings.Contains(lower, "bad") {
		return Track{
			ID:          "trk-" + strconv.FormatInt(time.Now().UnixNano(), 36),
			Title:       "ทรงอย่างแบด (Bad Boy)",
			Artist:      "Paper Planes",
			Duration:    "03:15",
			DurationSec: 195,
			Cover:       "https://images.unsplash.com/photo-1514525253161-7a46d19cd819?w=200&auto=format&fit=crop&q=80",
			URL:         query,
		}
	}
	if strings.Contains(lower, "นะหน้าทอง") {
		return Track{
			ID:          "trk-" + strconv.FormatInt(time.Now().UnixNano(), 36),
			Title:       "นะหน้าทอง",
			Artist:      "โจอี้ ภูวศิษฐ์",
			Duration:    "04:22",
			DurationSec: 262,
			Cover:       "https://images.unsplash.com/photo-1493225457124-a3eb161ffa5f?w=200&auto=format&fit=crop&q=80",
			URL:         query,
		}
	}

	// Dynamic song from query
	title := query
	artist := "Warn-noi Audio Stream"
	if strings.Contains(query, "-") {
		parts := strings.SplitN(query, "-", 2)
		artist = strings.TrimSpace(parts[0])
		title = strings.TrimSpace(parts[1])
	}

	return Track{
		ID:          "trk-" + strconv.FormatInt(time.Now().UnixNano(), 36),
		Title:       title,
		Artist:      artist,
		Duration:    "03:45",
		DurationSec: 225,
		Cover:       "https://images.unsplash.com/photo-1511671782779-c97d3d27a1d4?w=200&auto=format&fit=crop&q=80",
		URL:         query,
	}
}

func formatSeconds(s int) string {
	mins := s / 60
	secs := s % 60
	return fmt.Sprintf("%02d:%02d", mins, secs)
}
