package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"discord-bot-dashboard/backend/internal/models"
)

type Store struct {
	mu            sync.RWMutex
	filePath      string
	settings      map[string]*models.GuildSettings
	auditLogs     map[string][]models.AuditLogEntry
	musicStates   map[string]*models.MusicState
	verifiedUsers map[string]bool
	demoGuilds    []models.Guild
}

type persistData struct {
	Settings      map[string]*models.GuildSettings  `json:"settings"`
	AuditLogs     map[string][]models.AuditLogEntry `json:"audit_logs"`
	MusicStates   map[string]*models.MusicState     `json:"music_states,omitempty"`
	VerifiedUsers map[string]bool                   `json:"verified_users,omitempty"`
}

func New(filePath string) *Store {
	s := &Store{
		filePath:      filePath,
		settings:      make(map[string]*models.GuildSettings),
		auditLogs:     make(map[string][]models.AuditLogEntry),
		musicStates:   make(map[string]*models.MusicState),
		verifiedUsers: make(map[string]bool),
		demoGuilds:    createDemoGuilds(),
	}

	s.load()

	// Initialize default settings for demo guilds if not exists
	for _, g := range s.demoGuilds {
		if _, exists := s.settings[g.ID]; !exists {
			s.settings[g.ID] = defaultSettings(g.ID)
		}
	}

	return s
}

func defaultSettings(guildID string) *models.GuildSettings {
	return &models.GuildSettings{
		GuildID:          guildID,
		Prefix:           "!",
		WelcomeEnabled:   true,
		WelcomeChannelID: "ch-welcome",
		WelcomeMessage:   "🎉 ยินดีต้อนรับคุณ {user} สู่ {server}! อย่าลืมอ่านกฎในห้องประกาศด้วยนะ",
		AutoModEnabled:   true,
		AntiSpam:         true,
		AntiInvite:       true,
		BadWords:         []string{"gambling", "crypto-scam", "free-nitro", "hack"},
		LogChannelID:     "ch-mod-logs",
		MuteRoleID:       "role-muted",
		VerifyEnabled:    true,
		VerifyChannelID:  "1564174536591671349",
		VerifyRoleID:     "1564500987882897409",
		VerifyMessage:    "ยินดีต้อนรับสู่ **หวานน้อย | Warn noi**! กรุณากดปุ่ม **\"ยืนยันตัวตน\"** ด้านล่างเพื่อปลดล็อคห้องพูดคุยทั้งหมดครับ ✨",
		HoneypotEnabled:  true,
		HoneypotChannelID: "1564175826860118026",
		UpdatedAt:        time.Now(),
	}
}

func (s *Store) GetSettings(guildID string) *models.GuildSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if st, ok := s.settings[guildID]; ok {
		return st
	}
	return defaultSettings(guildID)
}

func (s *Store) UpdateSettings(guildID string, updated *models.GuildSettings, modName string) *models.GuildSettings {
	s.mu.Lock()
	defer s.mu.Unlock()

	updated.GuildID = guildID
	updated.UpdatedAt = time.Now()
	s.settings[guildID] = updated

	// Add audit log
	logEntry := models.AuditLogEntry{
		ID:        fmt.Sprintf("log-%d", time.Now().UnixNano()),
		GuildID:   guildID,
		Action:    "UPDATE_SETTINGS",
		Moderator: modName,
		Details:   fmt.Sprintf("Prefix: %s | AutoMod: %t | Welcome: %t", updated.Prefix, updated.AutoModEnabled, updated.WelcomeEnabled),
		CreatedAt: time.Now(),
	}
	s.auditLogs[guildID] = append([]models.AuditLogEntry{logEntry}, s.auditLogs[guildID]...)
	if len(s.auditLogs[guildID]) > 50 {
		s.auditLogs[guildID] = s.auditLogs[guildID][:50]
	}

	go s.save()
	return updated
}

func (s *Store) GetAuditLogs(guildID string) []models.AuditLogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	logs, ok := s.auditLogs[guildID]
	if !ok {
		return []models.AuditLogEntry{}
	}
	return logs
}

func (s *Store) AddAuditLog(guildID, action, mod, details string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	logEntry := models.AuditLogEntry{
		ID:        fmt.Sprintf("log-%d", time.Now().UnixNano()),
		GuildID:   guildID,
		Action:    action,
		Moderator: mod,
		Details:   details,
		CreatedAt: time.Now(),
	}
	s.auditLogs[guildID] = append([]models.AuditLogEntry{logEntry}, s.auditLogs[guildID]...)
	if len(s.auditLogs[guildID]) > 50 {
		s.auditLogs[guildID] = s.auditLogs[guildID][:50]
	}
	go s.save()
}

func (s *Store) GetDemoGuilds() []models.Guild {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.demoGuilds
}

func (s *Store) GetDemoGuild(id string) *models.Guild {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, g := range s.demoGuilds {
		if g.ID == id {
			return &g
		}
	}
	return nil
}

func (s *Store) GetMusicState(guildID string) *models.MusicState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if st, ok := s.musicStates[guildID]; ok {
		return st
	}
	return nil
}

func (s *Store) SaveMusicState(guildID string, state *models.MusicState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.musicStates[guildID] = state
	go s.save()
}

func (s *Store) IsUserVerified(guildID, userID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := fmt.Sprintf("%s:%s", guildID, userID)
	return s.verifiedUsers[key]
}

func (s *Store) SetUserVerified(guildID, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%s:%s", guildID, userID)
	if s.verifiedUsers[key] {
		return
	}
	s.verifiedUsers[key] = true
	go s.save()
}

func (s *Store) RemoveUserVerified(guildID, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%s:%s", guildID, userID)
	delete(s.verifiedUsers, key)
	go s.save()
}

func (s *Store) save() {
	s.mu.RLock()
	data := persistData{
		Settings:      s.settings,
		AuditLogs:     s.auditLogs,
		MusicStates:   s.musicStates,
		VerifiedUsers: s.verifiedUsers,
	}
	s.mu.RUnlock()

	bytes, err := json.MarshalIndent(data, "", "  ")
	if err == nil && s.filePath != "" {
		_ = os.WriteFile(s.filePath, bytes, 0644)
	}
}

func (s *Store) load() {
	if s.filePath == "" {
		return
	}
	bytes, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}
	var data persistData
	if err := json.Unmarshal(bytes, &data); err == nil {
		if data.Settings != nil {
			s.settings = data.Settings
		}
		if data.AuditLogs != nil {
			s.auditLogs = data.AuditLogs
		}
		if data.MusicStates != nil {
			s.musicStates = data.MusicStates
		}
		if data.VerifiedUsers != nil {
			s.verifiedUsers = data.VerifiedUsers
		}
	}
}

func createDemoGuilds() []models.Guild {
	return []models.Guild{
		{
			ID:          "112233445566",
			Name:        "Cyber Gaming TH 🎮",
			Icon:        "https://images.unsplash.com/photo-1618005182384-a83a8bd57fbe?w=128&auto=format&fit=crop&q=60",
			MemberCount: 1420,
			IsOwner:     true,
			BotInGuild:  true,
			Channels: []models.Channel{
				{ID: "ch-general", Name: "💬-คุยเล่นทั่วไป", Type: 0},
				{ID: "ch-welcome", Name: "🎉-ต้อนรับสมาชิก", Type: 0},
				{ID: "ch-announcements", Name: "📢-ประกาศ", Type: 0},
				{ID: "ch-mod-logs", Name: "🛡️-บันทึกแบน-เตือน", Type: 0},
				{ID: "ch-voice-1", Name: "🔊 ห้องคุยสด 1", Type: 2},
				{ID: "ch-voice-2", Name: "🔊 ห้องคุยสด 2", Type: 2},
			},
			Roles: []models.Role{
				{ID: "role-owner", Name: "Server Owner", ColorHex: "#F59E0B", Position: 1},
				{ID: "role-mod", Name: "Moderator", ColorHex: "#3B82F6", Position: 2},
				{ID: "role-vip", Name: "VIP Member", ColorHex: "#8B5CF6", Position: 3},
				{ID: "role-member", Name: "Verified Member", ColorHex: "#10B981", Position: 4},
				{ID: "role-muted", Name: "Muted", ColorHex: "#EF4444", Position: 5},
			},
		},
		{
			ID:          "998877665544",
			Name:        "Dev Community Studio 💻",
			Icon:        "https://images.unsplash.com/photo-1607799279861-4dd421887fb3?w=128&auto=format&fit=crop&q=60",
			MemberCount: 856,
			IsOwner:     false,
			BotInGuild:  true,
			Channels: []models.Channel{
				{ID: "ch-dev-chat", Name: "💡-คุยโค้ด-ถามตอบ", Type: 0},
				{ID: "ch-welcome", Name: "👋-welcome", Type: 0},
				{ID: "ch-mod-logs", Name: "🪵-system-logs", Type: 0},
				{ID: "ch-voice-lounge", Name: "🎧 Dev Lounge", Type: 2},
			},
			Roles: []models.Role{
				{ID: "role-admin", Name: "Administrator", ColorHex: "#EC4899", Position: 1},
				{ID: "role-contributor", Name: "Core Contributor", ColorHex: "#6366F1", Position: 2},
				{ID: "role-dev", Name: "Developer", ColorHex: "#06B6D4", Position: 3},
				{ID: "role-muted", Name: "Muted", ColorHex: "#6B7280", Position: 4},
			},
		},
		{
			ID:          "554433221100",
			Name:        "Anime & Chill Vibes 🌸",
			Icon:        "https://images.unsplash.com/photo-1578632767115-351597cf2477?w=128&auto=format&fit=crop&q=60",
			MemberCount: 2310,
			IsOwner:     false,
			BotInGuild:  false, // Needs invite!
			Channels:    []models.Channel{},
			Roles:       []models.Role{},
		},
	}
}
