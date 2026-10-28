package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"discord-bot-dashboard/backend/internal/bot"
	"discord-bot-dashboard/backend/internal/config"
	"discord-bot-dashboard/backend/internal/models"
	"discord-bot-dashboard/backend/internal/music"
	"discord-bot-dashboard/backend/internal/store"
)

type Server struct {
	cfg   *config.Config
	store *store.Store
	bot   *bot.BotService
}

func NewServer(cfg *config.Config, st *store.Store, b *bot.BotService) *Server {
	return &Server{
		cfg:   cfg,
		store: st,
		bot:   b,
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Enable CORS for frontend Vite dev server & production
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173", "http://localhost:3000", "http://127.0.0.1:5173", "*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	r.Route("/api", func(r chi.Router) {
		r.Get("/status", s.handleGetStatus)

		// Auth endpoints
		r.Route("/auth", func(r chi.Router) {
			r.Get("/me", s.handleGetMe)
			r.Post("/login-demo", s.handleLoginDemo)
			r.Get("/discord/url", s.handleDiscordOAuthURL)
			r.Post("/discord/callback", s.handleDiscordOAuthCallback)
		})

		// Guild management
		r.Route("/guilds", func(r chi.Router) {
			r.Get("/", s.handleGetGuilds)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", s.handleGetGuild)
				r.Get("/members", s.handleGetGuildMembers)
				r.Get("/settings", s.handleGetGuildSettings)
				r.Put("/settings", s.handleUpdateGuildSettings)
				r.Post("/test-welcome", s.handleTestWelcome)
				r.Get("/logs", s.handleGetAuditLogs)

				// Music Station & Terminal
				r.Get("/music", s.handleGetMusicState)
				r.Post("/music/command", s.handleMusicCommand)
				r.Post("/music/action", s.handleMusicAction)

				// Verification Gate & Roles
				r.Post("/roles/create", s.handleCreateGuildRole)
				r.Post("/verify/deploy", s.handleDeployVerify)
				r.Post("/verify/setup-perms", s.handleSetupVerifyPerms)

				// Honeypot Trap
				r.Post("/honeypot/setup-perms", s.handleSetupHoneypotPerms)

				// Chat Moderation & Purge
				r.Post("/chat/purge", s.handlePurgeChat)
			})
		})
	})

	// Serve static Vue frontend if dist folder is found
	candidateDirs := []string{
		"../discord-bot-frontend/dist",
		"./dist",
		"../frontend/dist",
	}
	var distDir string
	for _, dir := range candidateDirs {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			distDir = dir
			break
		}
	}

	if distDir != "" {
		fileServer := http.FileServer(http.Dir(distDir))
		r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
			path := filepath.Join(distDir, r.URL.Path)
			if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
			http.ServeFile(w, r, filepath.Join(distDir, "index.html"))
		})
	}

	return r
}

func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	stats := s.bot.GetStats()
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	// Standard user response - if in demo, return simulated admin user
	user := models.User{
		ID:            "8888999911112222",
		Username:      "Rittinun Admin",
		Discriminator: "0001",
		GlobalName:    "Rittinun",
		Avatar:        "https://images.unsplash.com/photo-1534528741775-53994a69daeb?w=128&auto=format&fit=crop&q=80",
		IsDemo:        s.cfg.IsDemoMode,
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleLoginDemo(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"token": "demo-admin-jwt-token-xyz",
		"user": models.User{
			ID:            "8888999911112222",
			Username:      "Rittinun Admin",
			Discriminator: "0001",
			GlobalName:    "Rittinun",
			Avatar:        "https://images.unsplash.com/photo-1534528741775-53994a69daeb?w=128&auto=format&fit=crop&q=80",
			IsDemo:        true,
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDiscordOAuthURL(w http.ResponseWriter, r *http.Request) {
	if s.cfg.DiscordClientID == "" {
		writeJSON(w, http.StatusOK, map[string]string{
			"url":     "",
			"message": "Discord Client ID not configured. Please set DISCORD_CLIENT_ID in .env or continue in Demo Mode.",
		})
		return
	}

	oauthURL := fmt.Sprintf(
		"https://discord.com/oauth2/authorize?client_id=%s&response_type=code&scope=%s&redirect_uri=%s",
		s.cfg.DiscordClientID,
		url.QueryEscape("identify guilds"),
		url.QueryEscape(s.cfg.DiscordRedirectURI),
	)

	writeJSON(w, http.StatusOK, map[string]string{"url": oauthURL})
}

func (s *Server) handleDiscordOAuthCallback(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Code == "" {
		http.Error(w, `{"error":"invalid code"}`, http.StatusBadRequest)
		return
	}

	// For demonstration / development:
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"token":   "live-oauth2-session-token",
		"user": models.User{
			ID:         "1234567890123456",
			Username:   "Discord User",
			GlobalName: "Discord Admin",
			Avatar:     "https://cdn.discordapp.com/embed/avatars/1.png",
			IsDemo:     false,
		},
	})
}

func (s *Server) handleGetGuilds(w http.ResponseWriter, r *http.Request) {
	guilds := s.bot.GetGuilds()
	writeJSON(w, http.StatusOK, guilds)
}

func (s *Server) handleGetGuild(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	guild := s.bot.GetGuild(id)
	if guild == nil {
		http.Error(w, `{"error":"guild not found"}`, http.StatusNotFound)
		return
	}

	settings := s.store.GetSettings(id)

	response := map[string]interface{}{
		"guild":    guild,
		"settings": settings,
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGetGuildMembers(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	members, err := s.bot.GetGuildMembers(id)
	if err != nil {
		writeJSON(w, http.StatusOK, []models.GuildMember{})
		return
	}
	writeJSON(w, http.StatusOK, members)
}

func (s *Server) handleGetGuildSettings(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	st := s.store.GetSettings(id)
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleUpdateGuildSettings(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var newSettings models.GuildSettings
	if err := json.NewDecoder(r.Body).Decode(&newSettings); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}

	modName := "Admin"
	updated := s.store.UpdateSettings(id, &newSettings, modName)

	if updated.HoneypotEnabled && updated.HoneypotChannelID != "" {
		go func() {
			_ = s.bot.EnsureHoneypotPermissions(id, updated.HoneypotChannelID)
		}()
	}

	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleTestWelcome(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	err := s.bot.SendTestWelcome(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "ส่งข้อความทดสอบต้อนรับเรียบร้อยแล้ว!",
	})
}

func (s *Server) handleGetAuditLogs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	logs := s.store.GetAuditLogs(id)
	writeJSON(w, http.StatusOK, logs)
}

func (s *Server) handleGetMusicState(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	mgr := music.GetManager(id)
	writeJSON(w, http.StatusOK, mgr.GetState())
}

type musicCmdRequest struct {
	Command string `json:"command"`
}

func (s *Server) handleMusicCommand(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req musicCmdRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}

	mgr := music.GetManager(id)
	output, state := mgr.ExecuteCommand(req.Command)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"output": output,
		"state":  state,
	})
}

type musicActionRequest struct {
	Action      string `json:"action"`
	Index       int    `json:"index"`
	Volume      int    `json:"volume"`
	Seek        int    `json:"seek"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
}

func (s *Server) handleMusicAction(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req musicActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}

	mgr := music.GetManager(id)
	var state music.State
	switch req.Action {
	case "toggle_play":
		state = mgr.TogglePlay()
	case "skip":
		state = mgr.SkipTrack()
	case "remove_queue":
		state = mgr.RemoveFromQueue(req.Index)
	case "set_volume":
		state = mgr.SetVolume(req.Volume)
	case "seek":
		state = mgr.Seek(req.Seek)
	case "set_channel", "join_channel":
		if req.ChannelID != "" {
			state = mgr.SetVoiceChannel(req.ChannelID, req.ChannelName)
			_ = s.bot.JoinVoice(id, req.ChannelID)
		} else {
			state = mgr.GetState()
		}
	case "stop":
		state = mgr.Stop()
		_ = s.bot.LeaveVoice(id)
	case "clear_queue":
		state = mgr.ClearQueue()
	case "leave_channel":
		_ = s.bot.LeaveVoice(id)
		state = mgr.GetState()
	default:
		state = mgr.GetState()
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"state": state,
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

type verifyActionReq struct {
	ChannelID string `json:"channel_id"`
	RoleID    string `json:"role_id"`
}

func (s *Server) handleDeployVerify(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req verifyActionReq
	_ = json.NewDecoder(r.Body).Decode(&req)

	st := s.store.GetSettings(id)
	chID := req.ChannelID
	if chID == "" && st != nil {
		chID = st.VerifyChannelID
	}
	if chID == "" {
		chID = "1564174536591671349" // default 'ᴡᴇʟᴄᴏᴍᴇ'
	}

	err := s.bot.SendVerifyMessage(id, chID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "ส่งกล่องปุ่มยืนยันตัวตน (Verify Gate) ไปยังห้อง Discord สำเร็จแล้ว!",
	})
}

func (s *Server) handleSetupVerifyPerms(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req verifyActionReq
	_ = json.NewDecoder(r.Body).Decode(&req)

	st := s.store.GetSettings(id)
	chID := req.ChannelID
	roleID := req.RoleID
	if chID == "" && st != nil {
		chID = st.VerifyChannelID
	}
	if roleID == "" && st != nil {
		roleID = st.VerifyRoleID
	}
	if chID == "" {
		chID = "1564174536591671349"
	}
	if roleID == "" {
		roleID = "1564500987882897409" // Default to 'สมาชิก'
	}

	err := s.bot.SetupVerifyPermissions(id, chID, roleID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "ตั้งค่าสิทธิ์ซ่อนห้องทั้งหมด และเปิดให้เห็นเฉพาะห้องยืนยันตัวตนสำเร็จแล้ว!",
	})
}

func (s *Server) handleCreateGuildRole(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var payload struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)

	role, err := s.bot.CreateMemberRole(id, payload.Name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	st := s.store.GetSettings(id)
	chID := "1564174536591671349"
	if st != nil && st.VerifyChannelID != "" {
		chID = st.VerifyChannelID
	}
	_ = s.bot.SetupVerifyPermissions(id, chID, role.ID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"role":    role,
		"message": fmt.Sprintf("สร้างยศ '%s' สำเร็จ และเชื่อมต่อกับระบบยืนยันตัวตนเรียบร้อยแล้ว!", role.Name),
	})
}

func (s *Server) handleSetupHoneypotPerms(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	st := s.store.GetSettings(id)
	chID := "1564175826860118026"
	if st != nil && st.HoneypotChannelID != "" {
		chID = st.HoneypotChannelID
	}
	err := s.bot.EnsureHoneypotPermissions(id, chID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "ตั้งค่าสิทธิ์ห้องดักสแปมบอทเรียบร้อยแล้ว (@everyone สามารถส่งข้อความเพื่อดักแบนได้)",
	})
}

type purgeChatRequest struct {
	ChannelID string `json:"channel_id"`
	Count     int    `json:"count"`
	Filter    string `json:"filter"` // "all", "bot", "links"
}

func (s *Server) handlePurgeChat(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req purgeChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "invalid request body",
		})
		return
	}

	if req.ChannelID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "channel_id is required",
		})
		return
	}

	if req.Count <= 0 {
		req.Count = 1
	}
	if req.Count > 100 {
		req.Count = 100
	}

	deleted, err := s.bot.PurgeMessages(id, req.ChannelID, req.Count, req.Filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	chName := req.ChannelID
	if ch, errCh := s.bot.GetChannel(req.ChannelID); errCh == nil && ch != nil {
		chName = ch.Name
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":       true,
		"deleted_count": deleted,
		"channel_id":    req.ChannelID,
		"channel_name":  chName,
		"message":       fmt.Sprintf("ลบข้อความในห้อง #%s สำเร็จจำนวน %d ข้อความ", chName, deleted),
	})
}

