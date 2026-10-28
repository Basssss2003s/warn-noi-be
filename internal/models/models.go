package models

import "time"

// BotStats represents real-time performance and status metrics
type BotStats struct {
	Status            string  `json:"status"`
	IsDemo            bool    `json:"is_demo"`
	Uptime            string  `json:"uptime"`
	UptimeSeconds     int64   `json:"uptime_seconds"`
	PingMs            int64   `json:"ping_ms"`
	GuildCount        int     `json:"guild_count"`
	UserCount         int     `json:"user_count"`
	ChannelCount      int     `json:"channel_count"`
	MessagesProcessed int64   `json:"messages_processed"`
	MemoryAllocMB     float64 `json:"memory_alloc_mb"`
	MemorySysMB       float64 `json:"memory_sys_mb"`
	Goroutines        int     `json:"goroutines"`
	GoVersion         string  `json:"go_version"`
	BotUsername       string  `json:"bot_username"`
	BotAvatar         string  `json:"bot_avatar"`
	IsOnline          bool    `json:"is_online"`
	ConnectedAt       string  `json:"connected_at,omitempty"`
	LastDisconnectAt  string  `json:"last_disconnect_at,omitempty"`
}

// Channel represents a text/voice channel in a guild
type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type int    `json:"type"` // 0 = text, 2 = voice, 4 = category
}

// Role represents a role inside a Discord guild
type Role struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ColorHex string `json:"color_hex"`
	Position int    `json:"position"`
	Managed  bool   `json:"managed"`
}

// Guild represents a Discord guild/server
type Guild struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Icon        string    `json:"icon"`
	MemberCount int       `json:"member_count"`
	IsOwner     bool      `json:"is_owner"`
	BotInGuild  bool      `json:"bot_in_guild"`
	Channels    []Channel `json:"channels,omitempty"`
	Roles       []Role    `json:"roles,omitempty"`
}

// GuildSettings contains configurable bot options for a server
type GuildSettings struct {
	GuildID          string    `json:"guild_id"`
	Prefix           string    `json:"prefix"`
	WelcomeEnabled   bool      `json:"welcome_enabled"`
	WelcomeChannelID string    `json:"welcome_channel_id"`
	WelcomeMessage   string    `json:"welcome_message"`
	AutoModEnabled   bool      `json:"automod_enabled"`
	AntiSpam         bool      `json:"anti_spam"`
	AntiInvite       bool      `json:"anti_invite"`
	BadWords         []string  `json:"bad_words"`
	LogChannelID     string    `json:"log_channel_id"`
	MuteRoleID       string    `json:"mute_role_id"`
	LevelingEnabled  bool      `json:"leveling_enabled"`
	VerifyEnabled    bool      `json:"verify_enabled"`
	VerifyChannelID  string    `json:"verify_channel_id"`
	VerifyRoleID     string    `json:"verify_role_id"`
	VerifyMessage    string    `json:"verify_message"`
	HoneypotEnabled  bool      `json:"honeypot_enabled"`
	HoneypotChannelID string   `json:"honeypot_channel_id"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// AuditLogEntry tracks changes or automated mod actions
type AuditLogEntry struct {
	ID        string    `json:"id"`
	GuildID   string    `json:"guild_id"`
	Action    string    `json:"action"`
	Moderator string    `json:"moderator"`
	Details   string    `json:"details"`
	CreatedAt time.Time `json:"created_at"`
}

// User represents the currently authenticated dashboard user
type User struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Discriminator string `json:"discriminator"`
	GlobalName    string `json:"global_name"`
	Avatar        string `json:"avatar"`
	IsDemo        bool   `json:"is_demo"`
}

// GuildMember represents a member in a Discord guild
type GuildMember struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Avatar      string   `json:"avatar"`
	Roles       []string `json:"roles"`
	RoleName    string   `json:"role_name"`
	RoleColor   string   `json:"role_color"`
	VoiceRoom   string   `json:"voice_room,omitempty"`
	IsBot       bool     `json:"is_bot"`
	IsMuted     bool     `json:"is_muted"`
	IsDeafened  bool     `json:"is_deafened"`
	Game        string   `json:"game,omitempty"`
	GameDetail  string   `json:"game_detail,omitempty"`
	Status      string   `json:"status"`
}

// MusicTrack represents a music track
type MusicTrack struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Duration    string `json:"duration"`
	DurationSec int    `json:"duration_sec"`
	Cover       string `json:"cover"`
	URL         string `json:"url"`
	AddedBy     string `json:"added_by,omitempty"`
}

// MusicState represents music playback state
type MusicState struct {
	IsPlaying      bool         `json:"is_playing"`
	Volume         int          `json:"volume"`
	PositionSec    int          `json:"position_sec"`
	CurrentTime    string       `json:"current_time"`
	CurrentTrack   *MusicTrack  `json:"current_track"`
	Queue          []MusicTrack `json:"queue"`
	VoiceChannel   string       `json:"voice_channel"`
	VoiceChannelID string       `json:"voice_channel_id,omitempty"`
}
