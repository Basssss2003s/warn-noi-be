package bot

import (
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"

	"discord-bot-dashboard/backend/internal/config"
	"discord-bot-dashboard/backend/internal/models"
	"discord-bot-dashboard/backend/internal/music"
	"discord-bot-dashboard/backend/internal/store"
)

type BotService struct {
	cfg               *config.Config
	store             *store.Store
	session           *discordgo.Session
	startTime         time.Time
	messagesProcessed int64
	isReady           bool
	verifyMu          sync.Mutex
}

func New(cfg *config.Config, st *store.Store) *BotService {
	return &BotService{
		cfg:       cfg,
		store:     st,
		startTime: time.Now(),
	}
}

func (b *BotService) Start() error {
	if b.cfg.IsDemoMode {
		log.Println("🚀 Bot running in DEMO / SIMULATION mode (Mock Discord Gateway ready)")
		b.isReady = true
		return nil
	}

	dg, err := discordgo.New("Bot " + b.cfg.DiscordBotToken)
	if err != nil {
		return fmt.Errorf("error creating Discord session: %w", err)
	}

	b.session = dg

	// Handlers
	dg.AddHandler(b.handleReady)
	dg.AddHandler(b.handleMessageCreate)
	dg.AddHandler(b.handleGuildMemberAdd)

	// Set intents (including voice states for music playback & joining voice channels)
	dg.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildVoiceStates |
		discordgo.IntentsGuildMembers |
		discordgo.IntentsMessageContent

	if err := dg.Open(); err != nil {
		if strings.Contains(err.Error(), "4014") || strings.Contains(err.Error(), "Disallowed intent") {
			log.Println("⚠️ Privileged Intents are not enabled yet in Developer Portal. Retrying with standard intents...")
			dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsGuildVoiceStates
			if errBasic := dg.Open(); errBasic != nil {
				log.Printf("⚠️ Failed to connect even with basic intents: %v. Falling back to Demo mode.", errBasic)
				b.cfg.IsDemoMode = true
				b.isReady = true
				return nil
			}
			log.Println("🟢 Connected to live Discord Gateway with standard intents & voice states!")
			log.Println("👉 Tip: Enable 'Server Members Intent' and 'Message Content Intent' in Discord Developer Portal for full Anti-Scam protection.")
			return nil
		}

		log.Printf("⚠️ Failed to connect to Discord Live Gateway: %v. Falling back to Demo mode.", err)
		b.cfg.IsDemoMode = true
		b.isReady = true
		return nil
	}

	log.Println("🟢 Connected to live Discord Gateway successfully with full intents!")
	return nil
}

func (b *BotService) Stop() {
	if b.session != nil {
		_ = b.session.Close()
	}
}

// EnsureReportChannel finds or creates a locked channel named 'รายงาน' visible ONLY to administrators & bot
func (b *BotService) EnsureReportChannel(guildID string) (string, error) {
	if b.session == nil {
		return "", fmt.Errorf("bot session is not active")
	}
	if guildID == "" {
		guildID = "1564174536029507635"
	}

	channels, err := b.session.GuildChannels(guildID)
	if err != nil {
		return "", fmt.Errorf("failed to fetch channels: %w", err)
	}

	reportChannelName := "รายงาน"
	var reportChannel *discordgo.Channel
	for _, ch := range channels {
		if ch.Type == discordgo.ChannelTypeGuildText && (ch.Name == reportChannelName || ch.Name == "admin-reports") {
			reportChannel = ch
			break
		}
	}

	everyoneRoleID := guildID

	if reportChannel == nil {
		created, err := b.session.GuildChannelCreate(guildID, reportChannelName, discordgo.ChannelTypeGuildText)
		if err != nil {
			log.Printf("⚠️ [Report Channel] Failed to create channel #%s: %v", reportChannelName, err)
			return "", err
		}
		reportChannel = created
		log.Printf("🔒 [Report Channel] Created new locked admin report channel #%s (%s)", reportChannel.Name, reportChannel.ID)
	}

	// Permission Overwrite for #รายงาน:
	// 1. @everyone: CANNOT VIEW (Deny PermissionViewChannel)
	_ = b.session.ChannelPermissionSet(
		reportChannel.ID,
		everyoneRoleID,
		discordgo.PermissionOverwriteTypeRole,
		0,
		discordgo.PermissionViewChannel,
	)

	// 2. Bot itself: CAN VIEW, SEND, EMBED, READ HISTORY
	if b.session.State != nil && b.session.State.User != nil {
		_ = b.session.ChannelPermissionSet(
			reportChannel.ID,
			b.session.State.User.ID,
			discordgo.PermissionOverwriteTypeMember,
			discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionEmbedLinks|discordgo.PermissionReadMessageHistory,
			0,
		)
	}

	// Update store settings so LogChannelID points to reportChannel.ID
	st := b.store.GetSettings(guildID)
	if st != nil && st.LogChannelID != reportChannel.ID {
		st.LogChannelID = reportChannel.ID
		b.store.UpdateSettings(guildID, st, "Bot System")
	}

	return reportChannel.ID, nil
}

// SendAdminReport sends a rich embed log exclusively to the locked #รายงาน channel
func (b *BotService) SendAdminReport(guildID, title, description string, fields []*discordgo.MessageEmbedField, color int) {
	if b.session == nil {
		return
	}
	reportChannelID, err := b.EnsureReportChannel(guildID)
	if err != nil || reportChannelID == "" {
		log.Printf("⚠️ [Report] Could not ensure report channel: %v", err)
		return
	}

	if color == 0 {
		color = 0x09090b
	}

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Color:       color,
		Fields:      fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("ห้องรายงานแอดมิน • %s", time.Now().Format("15:04:05")),
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	_, err = b.session.ChannelMessageSendEmbed(reportChannelID, embed)
	if err != nil {
		log.Printf("⚠️ [Report] Failed to send report embed to #รายงาน (%s): %v", reportChannelID, err)
	}
}

// JoinVoice connects/moves the bot into a voice channel in a guild
func (b *BotService) JoinVoice(guildID, channelID string) error {
	if b.session == nil {
		return fmt.Errorf("bot session is not active")
	}

	if guildID == "" {
		guildID = "1564174536029507635"
	}

	if channelID == "" {
		channelID = "1564174536591671350" // 'สนามหญ้า'
	}

	log.Printf("🎙️ [Voice] Joining voice channel: %s in guild: %s", channelID, guildID)
	err := b.session.ChannelVoiceJoinManual(guildID, channelID, false, false)
	if err != nil {
		log.Printf("⚠️ [Voice] Failed to send ChannelVoiceJoinManual: %v", err)
		return err
	}

	return nil
}

// LeaveVoice leaves the voice channel in a guild
func (b *BotService) LeaveVoice(guildID string) error {
	if b.session == nil {
		return nil
	}
	if guildID == "" {
		guildID = "1564174536029507635"
	}
	log.Printf("👋 [Voice] Leaving voice in guild %s", guildID)
	err := b.session.ChannelVoiceJoinManual(guildID, "", false, false)
	return err
}

// GetChannel returns channel info by ID
func (b *BotService) GetChannel(channelID string) (*discordgo.Channel, error) {
	if b.session == nil {
		return nil, fmt.Errorf("bot session is not active")
	}
	return b.session.Channel(channelID)
}

// PurgeMessages deletes the last `count` messages from a channel (1-100)
func (b *BotService) PurgeMessages(guildID, channelID string, count int, filter string) (int, error) {
	if b.session == nil {
		return 0, fmt.Errorf("bot session is not active")
	}

	if count <= 0 {
		count = 1
	}
	if count > 100 {
		count = 100
	}

	msgs, err := b.session.ChannelMessages(channelID, count, "", "", "")
	if err != nil {
		return 0, fmt.Errorf("failed to fetch messages: %w", err)
	}

	if len(msgs) == 0 {
		return 0, nil
	}

	now := time.Now()
	twoWeeksAgo := now.Add(-14 * 24 * time.Hour)

	var bulkIDs []string
	var oldIDs []string

	for _, m := range msgs {
		if filter == "bot" && !m.Author.Bot {
			continue
		}
		if filter == "links" && (!strings.Contains(m.Content, "http://") && !strings.Contains(m.Content, "https://")) {
			continue
		}

		if m.Timestamp.Before(twoWeeksAgo) {
			oldIDs = append(oldIDs, m.ID)
		} else {
			bulkIDs = append(bulkIDs, m.ID)
		}
	}

	deletedCount := 0

	// 1. Bulk delete messages newer than 14 days
	if len(bulkIDs) > 1 {
		errBulk := b.session.ChannelMessagesBulkDelete(channelID, bulkIDs)
		if errBulk == nil {
			deletedCount += len(bulkIDs)
		} else {
			for _, id := range bulkIDs {
				if errDel := b.session.ChannelMessageDelete(channelID, id); errDel == nil {
					deletedCount++
				}
			}
		}
	} else if len(bulkIDs) == 1 {
		if errDel := b.session.ChannelMessageDelete(channelID, bulkIDs[0]); errDel == nil {
			deletedCount++
		}
	}

	// 2. Single delete for messages older than 14 days
	for _, id := range oldIDs {
		if errDel := b.session.ChannelMessageDelete(channelID, id); errDel == nil {
			deletedCount++
		}
	}

	chName := channelID
	if ch, errCh := b.session.Channel(channelID); errCh == nil && ch != nil {
		chName = ch.Name
	}
	filterText := "ทั้งหมด"
	if filter == "bot" {
		filterText = "เฉพาะบอท"
	} else if filter == "links" {
		filterText = "เฉพาะข้อความที่มีลิงก์"
	}

	// Send moderation report to #รายงาน
	b.SendAdminReport(guildID, "🧹 [จัดการแชท] ลบข้อความในห้องสำเร็จ",
		fmt.Sprintf("มีการลบข้อความในห้อง **#%s** (<#%s>) สำเร็จจำนวน **%d** ข้อความ\nประเภทการกรอง: `%s`",
			chName, channelID, deletedCount, filterText),
		[]*discordgo.MessageEmbedField{
			{Name: "ห้องแชท", Value: fmt.Sprintf("<#%s>", channelID), Inline: true},
			{Name: "จำนวนที่ลบ", Value: fmt.Sprintf("%d ข้อความ", deletedCount), Inline: true},
			{Name: "ตัวกรอง", Value: filterText, Inline: true},
		},
		0x71717A,
	)

	// Record in audit log store
	if b.store != nil {
		b.store.AddAuditLog(guildID, "PURGE_CHAT", "Dashboard / Slash Command",
			fmt.Sprintf("ลบข้อความในห้อง #%s จำนวน %d ข้อความ (ตัวกรอง: %s)", chName, deletedCount, filterText))
	}

	return deletedCount, nil
}

func (b *BotService) handleReady(s *discordgo.Session, r *discordgo.Ready) {
	b.isReady = true
	log.Printf("🤖 Bot is ready as %s#%s on %d guilds", r.User.Username, r.User.Discriminator, len(r.Guilds))

	// Preload existing verified members from guilds to prevent duplicate verification reports
	go func() {
		for _, g := range r.Guilds {
			members, err := s.GuildMembers(g.ID, "", 1000)
			if err != nil {
				continue
			}
			st := b.store.GetSettings(g.ID)
			roleID := "1564500987882897409"
			if st != nil && st.VerifyRoleID != "" {
				roleID = st.VerifyRoleID
			}
			count := 0
			for _, m := range members {
				for _, rID := range m.Roles {
					if rID == roleID {
						b.store.SetUserVerified(g.ID, m.User.ID)
						count++
						break
					}
				}
			}
			if count > 0 {
				log.Printf("🔒 Preloaded %d verified members for guild %s", count, g.ID)
			}
		}
	}()

	// Register slash commands
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "ping",
			Description: "ตรวจสอบความหน่วงและสถานะของบอท",
		},
		{
			Name:        "dashboard",
			Description: "รับลิงก์สำหรับเข้าสู่หน้า Dashboard จัดการบอท",
		},
		{
			Name:        "play",
			Description: "เล่นเพลงทันทีใน Warn-noi Music Station",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "song",
					Description: "ชื่อเพลง หรือ ลิงก์เพลงที่ต้องการเล่น",
					Required:    false,
				},
			},
		},
		{
			Name:        "que",
			Description: "ต่อคิวเพลงใน Warn-noi Music Station",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "song",
					Description: "ชื่อเพลงที่ต้องการต่อคิว",
					Required:    true,
				},
			},
		},
		{
			Name:        "list",
			Description: "แสดงรายการคิวเพลงปัจจุบัน",
		},
		{
			Name:        "skip",
			Description: "ข้ามไปเพลงถัดไปในคิว",
		},
		{
			Name:        "stop",
			Description: "หยุดเล่นเพลงและล้างคิวทั้งหมด",
		},
		{
			Name:        "verify",
			Description: "ยืนยันตัวตนเพื่อรับยศและปลดล็อคห้องทั้งหมดในเซิร์ฟเวอร์",
		},
		{
			Name:        "setup-verify",
			Description: "ส่งกล่องปุ่มยืนยันตัวตน (Verify Gate) มายังห้องนี้",
		},
		{
			Name:        "clear",
			Description: "ลบข้อความในห้องแชทล่าสุดตามจำนวนที่กำหนด (1-100)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "amount",
					Description: "จำนวนข้อความที่ต้องการลบ (1-100)",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "ห้องที่ต้องการลบ (หากไม่ระบุจะลบในห้องนี้)",
					Required:    false,
					ChannelTypes: []discordgo.ChannelType{
						discordgo.ChannelTypeGuildText,
					},
				},
			},
		},
	}

	for _, cmd := range commands {
		_, err := s.ApplicationCommandCreate(s.State.User.ID, "", cmd)
		if err != nil {
			log.Printf("Could not register command %s: %v", cmd.Name, err)
		}
	}

	// Ensure Honeypot trap channel permissions and admin report channel on ready
	go func() {
		time.Sleep(2 * time.Second)
		_ = b.EnsureHoneypotPermissions("1564174536029507635", "1564175826860118026")
		for _, g := range r.Guilds {
			_, _ = b.EnsureReportChannel(g.ID)
		}
	}()

	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		// 1. Slash Commands Handler
		if i.Type == discordgo.InteractionApplicationCommand {
			data := i.ApplicationCommandData()
			switch data.Name {
			case "ping":
				_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: fmt.Sprintf("🏓 Pong! Latency: `%dms`", s.HeartbeatLatency().Milliseconds()),
					},
				})
			case "dashboard":
				_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "🌐 เข้าสู่หน้าจัดการ Web Dashboard: http://localhost:5173",
					},
				})
			case "play":
				mgr := music.GetManager(i.GuildID)
				song := ""
				if len(data.Options) > 0 {
					song = data.Options[0].StringValue()
				}
				cmd := "/play " + song
				out, _ := mgr.ExecuteCommand(cmd)
				_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: out,
					},
				})
			case "que":
				mgr := music.GetManager(i.GuildID)
				song := ""
				if len(data.Options) > 0 {
					song = data.Options[0].StringValue()
				}
				cmd := "/que " + song
				out, _ := mgr.ExecuteCommand(cmd)
				_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: out,
					},
				})
			case "list":
				mgr := music.GetManager(i.GuildID)
				out, _ := mgr.ExecuteCommand("/list")
				_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: out,
					},
				})
			case "skip":
				mgr := music.GetManager(i.GuildID)
				out, _ := mgr.ExecuteCommand("/skip")
				_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: out,
					},
				})
			case "stop":
				mgr := music.GetManager(i.GuildID)
				out, _ := mgr.ExecuteCommand("/stop")
				_ = b.LeaveVoice(i.GuildID)
				_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: out,
					},
				})
			case "clear":
				amount := 1
				targetChannelID := i.ChannelID
				for _, opt := range data.Options {
					if opt.Name == "amount" {
						amount = int(opt.IntValue())
					} else if opt.Name == "channel" {
						targetChannelID = opt.ChannelValue(s).ID
					}
				}

				if amount < 1 {
					amount = 1
				}
				if amount > 100 {
					amount = 100
				}

				_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Flags: discordgo.MessageFlagsEphemeral,
					},
				})

				deleted, err := b.PurgeMessages(i.GuildID, targetChannelID, amount, "")
				var msg string
				if err != nil {
					msg = fmt.Sprintf("⚠️ ไม่สามารถลบข้อความได้: %v", err)
				} else {
					msg = fmt.Sprintf("🧹 ลบข้อความในห้อง <#%s> เรียบร้อยแล้วจำนวน **%d** ข้อความ!", targetChannelID, deleted)
				}

				_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
					Content: &msg,
				})
			case "verify":
				b.processMemberVerification(s, i.Interaction, "slash")
			case "setup-verify":
				err := b.SendVerifyMessage(i.GuildID, i.ChannelID)
				resp := "✅ ส่งกล่องปุ่มยืนยันตัวตน (Verify Gate) มายังห้องนี้เรียบร้อยแล้ว!"
				if err != nil {
					resp = fmt.Sprintf("❌ เกิดข้อผิดพลาดในการส่ง: %v", err)
				}
				_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Flags:   discordgo.MessageFlagsEphemeral,
						Content: resp,
					},
				})
			}
		}

		// 2. Interactive Button Click Handler (Verify Gate Button)
		if i.Type == discordgo.InteractionMessageComponent {
			data := i.MessageComponentData()
			if data.CustomID == "verify_member_btn" {
				b.processMemberVerification(s, i.Interaction, "button")
			}
		}
	})
}

// processMemberVerification handles member verification idempotently with race-condition locking
func (b *BotService) processMemberVerification(s *discordgo.Session, i *discordgo.Interaction, source string) {
	userID := ""
	username := "Member"
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
		username = i.Member.User.Username
	}
	if userID == "" {
		return
	}

	guildID := i.GuildID
	st := b.store.GetSettings(guildID)
	roleID := "1564500987882897409" // Default to 'สมาชิก'
	if st != nil && st.VerifyRoleID != "" {
		roleID = st.VerifyRoleID
	}

	alreadyVerifiedMsg := "✨ คุณได้รับการยืนยันตัวตนและมียศอยู่แล้วครับ! สามารถเข้าใช้งานและพูดคุยในห้องทั้งหมดได้ทันที 🎉"

	// 1. Fast check: In-memory/persistent store
	if b.store.IsUserVerified(guildID, userID) {
		_ = s.InteractionRespond(i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Flags:   discordgo.MessageFlagsEphemeral,
				Content: alreadyVerifiedMsg,
			},
		})
		return
	}

	// 2. Check cached interaction roles
	if i.Member != nil {
		for _, r := range i.Member.Roles {
			if r == roleID {
				b.store.SetUserVerified(guildID, userID)
				_ = s.InteractionRespond(i, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Flags:   discordgo.MessageFlagsEphemeral,
						Content: alreadyVerifiedMsg,
					},
				})
				return
			}
		}
	}

	// 3. Check authoritative live member from Discord API in case interaction payload is stale
	if mem, err := s.GuildMember(guildID, userID); err == nil && mem != nil {
		for _, r := range mem.Roles {
			if r == roleID {
				b.store.SetUserVerified(guildID, userID)
				_ = s.InteractionRespond(i, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Flags:   discordgo.MessageFlagsEphemeral,
						Content: alreadyVerifiedMsg,
					},
				})
				return
			}
		}
	}

	// 4. Synchronize via mutex to prevent concurrent rapid-click race condition
	b.verifyMu.Lock()
	if b.store.IsUserVerified(guildID, userID) {
		b.verifyMu.Unlock()
		_ = s.InteractionRespond(i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Flags:   discordgo.MessageFlagsEphemeral,
				Content: alreadyVerifiedMsg,
			},
		})
		return
	}
	// Mark immediately to block other concurrent interactions from this user
	b.store.SetUserVerified(guildID, userID)
	b.verifyMu.Unlock()

	// 5. Add role in Discord
	err := s.GuildMemberRoleAdd(guildID, userID, roleID)
	if err != nil {
		log.Printf("⚠️ Failed to assign verify role %s to %s: %v", roleID, userID, err)
		b.store.RemoveUserVerified(guildID, userID)
		_ = s.InteractionRespond(i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Flags:   discordgo.MessageFlagsEphemeral,
				Content: fmt.Sprintf("⚠️ ไม่สามารถเพิ่มยศยืนยันตัวตนได้: %v (กรุณาให้แอดมินตรวจสอบว่ายศของบอทอยู่สูงกว่ายศที่จะแจกใน Server Settings > Roles)", err),
			},
		})
		return
	}

	// 6. Report to admin log ONCE and ONLY ONCE
	titleSuffix := ""
	if source == "slash" {
		titleSuffix = " (Slash Command)"
	}
	b.store.AddAuditLog(guildID, "VERIFY_MEMBER", "Security Bot", fmt.Sprintf("Verified member %s and added role %s", username, roleID))
	b.SendAdminReport(guildID, "✅ สมาชิกยืนยันตัวตนสำเร็จ"+titleSuffix,
		fmt.Sprintf("สมาชิก <@%s> (%s)\n• ได้รับยศ: <@&%s>\n• ห้อง Welcome ถูกซ่อนจากผู้ใช้รายนี้อัตโนมัติแล้ว", userID, username, roleID),
		nil, 0x10B981)

	// 7. Success response to user
	_ = s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "🎉 ยืนยันตัวตนสำเร็จแล้ว! คุณได้รับยศเรียบร้อย และปลดล็อคห้องทั้งหมดแล้วครับ (ห้องยินดีต้อนรับนี้จะถูกซ่อนอัตโนมัติ) 🍧✨",
		},
	})
}

func (b *BotService) handleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || s.State == nil || s.State.User == nil || m.Author.ID == s.State.User.ID {
		return
	}
	if m.GuildID == "" {
		return
	}

	st := b.store.GetSettings(m.GuildID)

	// 🚨 HONEYPOT SPAM TRAP CHECK
	// If message is posted in the honeypot bait channel (e.g. ᴍʀʙᴇᴀꜱᴛ-ᴀᴅꜱ-ʜᴇʀᴇ), ban immediately!
	isHoneypot := false
	if st != nil && st.HoneypotEnabled && st.HoneypotChannelID != "" && m.ChannelID == st.HoneypotChannelID {
		isHoneypot = true
	} else if m.ChannelID == "1564175826860118026" { // Default Honeypot room ᴍʀʙᴇᴀꜱᴛ-ᴀᴅꜱ-ʜᴇʀᴇ
		isHoneypot = true
	}

	if isHoneypot {
		if b.isExemptFromHoneypot(s, m.GuildID, m.Author.ID) {
			log.Printf("🛡️ [Honeypot] Exempt user/admin %s (%s) typed in honeypot room. Skipping auto-ban.", m.Author.Username, m.Author.ID)
			return
		}

		log.Printf("🚨 [Honeypot] Triggered by %s (%s) in honeypot room! Banning spammer immediately...", m.Author.Username, m.Author.ID)

		// 1. Delete the spam message
		_ = s.ChannelMessageDelete(m.ChannelID, m.ID)

		// 2. Ban user with reason (delete 1 day of messages across server)
		banReason := "Auto-banned: Honeypot Spam Trap (ส่งข้อความในห้องดักสแปม ᴍʀʙᴇᴀꜱᴛ-ᴀᴅꜱ-ʜᴇʀᴇ)"
		err := s.GuildBanCreateWithReason(m.GuildID, m.Author.ID, banReason, 1)
		if err != nil {
			log.Printf("❌ [Honeypot] Failed to ban user %s: %v", m.Author.ID, err)
		} else {
			log.Printf("🔨 [Honeypot] Successfully auto-banned spammer %s (%s)", m.Author.Username, m.Author.ID)
		}

		// 3. Add to Audit Log
		logSnippet := truncateString(m.Content, 100)
		b.store.AddAuditLog(m.GuildID, "HONEYPOT_BAN", "Honeypot Shield", fmt.Sprintf("แบนผู้ใช้ %s (%s) ข้อความ: \"%s\" (ห้องดักสแปม)", m.Author.Username, m.Author.ID, logSnippet))

		// 4. Send alert embed to locked #รายงาน channel ONLY
		b.SendAdminReport(m.GuildID, "🚨 ดักจับและแบนสแปมบอทสำเร็จ! (Honeypot Trap)",
			fmt.Sprintf("ตรวจพบการส่งข้อความในห้องดักสแปม <#%s> ระบบทำการแบนและลบข้อความทันที", m.ChannelID),
			[]*discordgo.MessageEmbedField{
				{
					Name:   "👤 ผู้ส่งข้อความ (Spammer)",
					Value:  fmt.Sprintf("%s (%s)\nID: `%s`", m.Author.Mention(), m.Author.Username, m.Author.ID),
					Inline: true,
				},
				{
					Name:   "⚡ การดำเนินการ",
					Value:  "**แบนถาวร (Banned)** + ลบข้อความ 24 ชม.",
					Inline: true,
				},
				{
					Name:   "💬 ข้อความที่ส่ง (Message Snippet)",
					Value:  fmt.Sprintf("```\n%s\n```", truncateString(m.Content, 300)),
					Inline: false,
				},
			},
			0xEF4444,
		)

		return
	}

	if m.Author.Bot {
		return
	}
	atomic.AddInt64(&b.messagesProcessed, 1)

	// Fetch guild settings for automod
	if st != nil && st.AutoModEnabled {
		contentLower := strings.ToLower(m.Content)

		// Anti-Invite check
		if st.AntiInvite && (strings.Contains(contentLower, "discord.gg/") || strings.Contains(contentLower, "discord.com/invite/")) {
			_ = s.ChannelMessageDelete(m.ChannelID, m.ID)
			b.store.AddAuditLog(m.GuildID, "AUTOMOD_DELETE", "AutoMod Bot", fmt.Sprintf("Deleted invite link from %s", m.Author.Username))
			// Send report to locked #รายงาน ONLY (no spam in public chat)
			b.SendAdminReport(m.GuildID, "🛡️ [AutoMod] ลบลิงก์เชิญ (Anti-Invite)",
				fmt.Sprintf("ตรวจพบผู้ใช้ส่งลิงก์ Discord Invite ในห้อง <#%s> ระบบทำการลบข้อความทันที", m.ChannelID),
				[]*discordgo.MessageEmbedField{
					{
						Name:   "👤 ผู้ใช้",
						Value:  fmt.Sprintf("%s (%s)", m.Author.Mention(), m.Author.Username),
						Inline: true,
					},
					{
						Name:   "💬 ข้อความที่ถูกลบ",
						Value:  fmt.Sprintf("```\n%s\n```", truncateString(m.Content, 200)),
						Inline: false,
					},
				},
				0xF59E0B,
			)
			return
		}

		// Bad words check
		for _, badWord := range st.BadWords {
			if strings.TrimSpace(badWord) != "" && strings.Contains(contentLower, strings.ToLower(badWord)) {
				_ = s.ChannelMessageDelete(m.ChannelID, m.ID)
				b.store.AddAuditLog(m.GuildID, "AUTOMOD_BADWORD", "AutoMod Bot", fmt.Sprintf("Filtered word '%s' from %s", badWord, m.Author.Username))
				// Send report to locked #รายงาน ONLY
				b.SendAdminReport(m.GuildID, "🛡️ [AutoMod] กรองคำหยาบ (Bad Words Filter)",
					fmt.Sprintf("ตรวจพบข้อความมีคำที่ไม่อนุญาตในห้อง <#%s> ระบบทำการลบข้อความทันที", m.ChannelID),
					[]*discordgo.MessageEmbedField{
						{
							Name:   "👤 ผู้ใช้",
							Value:  fmt.Sprintf("%s (%s)", m.Author.Mention(), m.Author.Username),
							Inline: true,
						},
						{
							Name:   "🚫 คำที่ตรวจพบ",
							Value:  fmt.Sprintf("`%s`", badWord),
							Inline: true,
						},
						{
							Name:   "💬 ข้อความที่ถูกลบ",
							Value:  fmt.Sprintf("```\n%s\n```", truncateString(m.Content, 200)),
							Inline: false,
						},
					},
					0xF59E0B,
				)
				return
			}
		}
	}
}

func (b *BotService) handleGuildMemberAdd(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
	b.SendAdminReport(m.GuildID, "👋 สมาชิกใหม่เข้าร่วมเซิร์ฟเวอร์",
		fmt.Sprintf("ผู้ใช้ %s (<@%s>) เข้าร่วมเซิร์ฟเวอร์เรียบร้อย\nสถานะ: กำลังรอยืนยันตัวตนที่ห้อง Welcome (ไม่มียศ)", m.User.Username, m.User.ID),
		nil, 0x3B82F6)
}

// SendVerifyMessage sends the interactive verification embed with button to target channel
func (b *BotService) SendVerifyMessage(guildID, channelID string) error {
	if b.session == nil {
		return fmt.Errorf("bot session is not active")
	}

	if channelID == "" {
		channelID = "1564174536591671349" // default to ᴡᴇʟᴄᴏᴍᴇ
	}

	st := b.store.GetSettings(guildID)
	msgContent := "ยินดีต้อนรับสู่ **หวานน้อย | Warn noi**!\n\nเพื่อความปลอดภัยและป้องกันสแปมบอท กรุณากดปุ่ม **\"ยืนยันตัวตน (Verify)\"** ด้านล่างนี้เพื่อรับยศและปลดล็อคห้องแชทและห้องพูดคุยทั้งหมดในเซิร์ฟเวอร์ครับ ✨"
	if st != nil && st.VerifyMessage != "" {
		msgContent = st.VerifyMessage
	}

	msg := &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{
			{
				Title:       "🍧 ยืนยันตัวตนเข้าสู่เซิร์ฟเวอร์ | Warn-noi Verification",
				Description: msgContent,
				Color:       0x09090b,
				Fields: []*discordgo.MessageEmbedField{
					{
						Name:   "📜 กฎระเบียบและข้อแนะนำ",
						Value:  "• ห้ามส่งลิงก์หลอกลวง, Phishing หรือ Scam\n• สุภาพและให้เกียรติเพื่อนๆ ในเซิร์ฟเวอร์",
						Inline: false,
					},
					{
						Name:   "✨ สิ่งที่จะได้รับหลังยืนยัน",
						Value:  "• ยศสมาชิกสำหรับเข้าถึงห้องทั้งหมด\n• ปลดล็อคห้องข้อความและห้องเสียง",
						Inline: false,
					},
				},
				Footer: &discordgo.MessageEmbedFooter{
					Text: "หวานน้อย | Warn-noi Verification Gate",
				},
				Timestamp: time.Now().Format(time.RFC3339),
			},
		},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "ยืนยันตัวตน (Verify)",
						Style:    discordgo.SuccessButton,
						CustomID: "verify_member_btn",
						Emoji: &discordgo.ComponentEmoji{
							Name: "✅",
						},
					},
				},
			},
		},
	}

	_, err := b.session.ChannelMessageSendComplex(channelID, msg)
	if err != nil {
		return fmt.Errorf("failed to send verify message: %w", err)
	}
	log.Printf("✅ Sent verification embed with button to channel %s in guild %s", channelID, guildID)
	return nil
}

// SetupVerifyPermissions automatically configures channel permission overwrites:
// - @everyone can ONLY view the verify room and cannot view any other rooms or categories
// - Verified Role can view and access all normal rooms
func (b *BotService) SetupVerifyPermissions(guildID, verifyChannelID, verifiedRoleID string) error {
	if b.session == nil {
		return fmt.Errorf("bot session is not active")
	}

	if verifyChannelID == "" {
		verifyChannelID = "1564174536591671349"
	}
	if verifiedRoleID == "" {
		verifiedRoleID = "1564500987882897409" // Default to 'สมาชิก'
	}

	// Ensure admin report channel exists
	_, _ = b.EnsureReportChannel(guildID)

	channels, err := b.session.GuildChannels(guildID)
	if err != nil {
		return fmt.Errorf("failed to fetch channels: %w", err)
	}

	everyoneRoleID := guildID // @everyone ID is identical to guildID

	// Honeypot channel check
	st := b.store.GetSettings(guildID)
	honeypotChannelID := ""
	if st != nil && st.HoneypotChannelID != "" {
		honeypotChannelID = st.HoneypotChannelID
	} else {
		honeypotChannelID = "1564175826860118026"
	}

	// Find categories containing verify channel and honeypot channel
	var verifyParentID string
	var honeypotParentID string
	for _, ch := range channels {
		if ch.ID == verifyChannelID {
			verifyParentID = ch.ParentID
		}
		if ch.ID == honeypotChannelID {
			honeypotParentID = ch.ParentID
		}
	}

	for _, ch := range channels {
		if ch.Type == discordgo.ChannelTypeGuildCategory {
			// Categories:
			// If this category contains the verify channel or honeypot channel, @everyone can view it.
			// Otherwise, hide the category from @everyone and allow verified role.
			if (ch.ID == verifyParentID && verifyParentID != "") || (ch.ID == honeypotParentID && honeypotParentID != "") {
				_ = b.session.ChannelPermissionSet(
					ch.ID,
					everyoneRoleID,
					discordgo.PermissionOverwriteTypeRole,
					discordgo.PermissionViewChannel,
					0,
				)
			} else {
				_ = b.session.ChannelPermissionSet(
					ch.ID,
					everyoneRoleID,
					discordgo.PermissionOverwriteTypeRole,
					0,
					discordgo.PermissionViewChannel,
				)
				_ = b.session.ChannelPermissionSet(
					ch.ID,
					verifiedRoleID,
					discordgo.PermissionOverwriteTypeRole,
					discordgo.PermissionViewChannel,
					0,
				)
			}
			continue
		}

		if ch.ID == verifyChannelID {
			// On Welcome / Verify Room:
			// 1. @everyone (incoming members who have NO roles) CAN VIEW and READ HISTORY, CANNOT SEND
			_ = b.session.ChannelPermissionSet(
				ch.ID,
				everyoneRoleID,
				discordgo.PermissionOverwriteTypeRole,
				discordgo.PermissionViewChannel|discordgo.PermissionReadMessageHistory,
				discordgo.PermissionSendMessages|discordgo.PermissionAddReactions,
			)
			// 2. Verified Role: HIDE COMPLETELY (deny ViewChannel)
			// As requested: "1. ยืนยันตัวตนสำเร็จ ก็ซ่อนห้อง welcome ไปเลยครับ ห้อง welcome ให้เห็นแค่คนที่เข้ามาใหม่ ไม่มียศ"
			_ = b.session.ChannelPermissionSet(
				ch.ID,
				verifiedRoleID,
				discordgo.PermissionOverwriteTypeRole,
				0,
				discordgo.PermissionViewChannel,
			)
		} else if ch.Name == "รายงาน" || ch.Name == "admin-reports" {
			// Admin-only Locked Report Room:
			// 1. @everyone CANNOT VIEW
			_ = b.session.ChannelPermissionSet(
				ch.ID,
				everyoneRoleID,
				discordgo.PermissionOverwriteTypeRole,
				0,
				discordgo.PermissionViewChannel,
			)
			// 2. Verified Role CANNOT VIEW (only Admins and Bot can view)
			_ = b.session.ChannelPermissionSet(
				ch.ID,
				verifiedRoleID,
				discordgo.PermissionOverwriteTypeRole,
				0,
				discordgo.PermissionViewChannel,
			)
			// 3. Bot CAN VIEW, SEND, EMBED
			if b.session.State != nil && b.session.State.User != nil {
				_ = b.session.ChannelPermissionSet(
					ch.ID,
					b.session.State.User.ID,
					discordgo.PermissionOverwriteTypeMember,
					discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionEmbedLinks|discordgo.PermissionReadMessageHistory,
					0,
				)
			}
		} else if ch.ID == honeypotChannelID {
			// Honeypot Channel (Trap for Spam Bots):
			// @everyone MUST be able to view and send messages so bots fall into the trap!
			_ = b.session.ChannelPermissionSet(
				ch.ID,
				everyoneRoleID,
				discordgo.PermissionOverwriteTypeRole,
				discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionReadMessageHistory,
				0,
			)
			// Verified Role CAN VIEW, SEND, READ
			_ = b.session.ChannelPermissionSet(
				ch.ID,
				verifiedRoleID,
				discordgo.PermissionOverwriteTypeRole,
				discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionReadMessageHistory,
				0,
			)
		} else if ch.Type == discordgo.ChannelTypeGuildVoice {
			// Voice Channels:
			// 1. @everyone CANNOT VIEW, CONNECT, OR SPEAK
			_ = b.session.ChannelPermissionSet(
				ch.ID,
				everyoneRoleID,
				discordgo.PermissionOverwriteTypeRole,
				0,
				discordgo.PermissionViewChannel|discordgo.PermissionVoiceConnect|discordgo.PermissionVoiceSpeak,
			)
			// 2. Verified Role CAN VIEW, CONNECT, AND SPEAK
			_ = b.session.ChannelPermissionSet(
				ch.ID,
				verifiedRoleID,
				discordgo.PermissionOverwriteTypeRole,
				discordgo.PermissionViewChannel|discordgo.PermissionVoiceConnect|discordgo.PermissionVoiceSpeak,
				0,
			)
		} else {
			// Normal Text Channels:
			// 1. @everyone CANNOT VIEW
			_ = b.session.ChannelPermissionSet(
				ch.ID,
				everyoneRoleID,
				discordgo.PermissionOverwriteTypeRole,
				0,
				discordgo.PermissionViewChannel,
			)

			// 2. Verified Role (e.g. สมาชิก) CAN VIEW, SEND MESSAGES, READ HISTORY
			_ = b.session.ChannelPermissionSet(
				ch.ID,
				verifiedRoleID,
				discordgo.PermissionOverwriteTypeRole,
				discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionReadMessageHistory,
				0,
			)
		}
	}

	b.store.AddAuditLog(guildID, "SETUP_VERIFY_PERMS", "Admin Dashboard", fmt.Sprintf("Auto-configured channel permissions for verify room %s, honeypot %s, and role %s", verifyChannelID, honeypotChannelID, verifiedRoleID))
	b.SendAdminReport(guildID, "🔒 ปรับปรุงสิทธิ์ความปลอดภัยเซิร์ฟเวอร์",
		fmt.Sprintf("• ห้อง Welcome (<#%s>): กำหนดให้เห็นเฉพาะผู้ใช้ใหม่ที่ยังไม่มียศ และ**ซ่อนอัตโนมัติ**เมื่อยืนยันสำเร็จ\n• ห้องดักสแปม (<#%s>): เปิดเหยื่อล่อให้สแปมบอทพิมพ์เพื่อแบนทันที\n• ห้องรายงาน: ล็อกให้เห็นเฉพาะแอดมินและบอทเท่านั้น", verifyChannelID, honeypotChannelID),
		nil, 0x10B981)
	log.Printf("🔒 Successfully configured verify & honeypot channel permissions in guild %s", guildID)
	return nil
}

// EnsureHoneypotPermissions configures the honeypot trap channel so @everyone can view and send messages (acting as bait)
func (b *BotService) EnsureHoneypotPermissions(guildID, channelID string) error {
	if b.session == nil {
		return fmt.Errorf("bot session is not active")
	}
	if guildID == "" {
		guildID = "1564174536029507635"
	}
	if channelID == "" {
		channelID = "1564175826860118026" // Default ᴍʀʙᴇᴀꜱᴛ-ᴀᴅꜱ-ʜᴇʀᴇ
	}

	everyoneRoleID := guildID

	ch, err := b.session.Channel(channelID)
	if err != nil {
		return fmt.Errorf("failed to fetch honeypot channel: %w", err)
	}

	// If inside a category, ensure the category is viewable by @everyone
	if ch.ParentID != "" {
		_ = b.session.ChannelPermissionSet(
			ch.ParentID,
			everyoneRoleID,
			discordgo.PermissionOverwriteTypeRole,
			discordgo.PermissionViewChannel,
			0,
		)
	}

	// Set overwrite on honeypot room: @everyone CAN view, read history, and SEND messages
	err = b.session.ChannelPermissionSet(
		channelID,
		everyoneRoleID,
		discordgo.PermissionOverwriteTypeRole,
		discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionReadMessageHistory,
		0,
	)
	if err != nil {
		log.Printf("⚠️ [Honeypot] Failed to configure permissions on channel %s: %v", channelID, err)
		return err
	}

	log.Printf("🍯 [Honeypot Trap] Successfully configured trap permissions on #%s (%s)", ch.Name, channelID)
	return nil
}

// isExemptFromHoneypot checks if a user is the server owner or has Administrator/Ban permissions
func (b *BotService) isExemptFromHoneypot(s *discordgo.Session, guildID, userID string) bool {
	guild, err := s.State.Guild(guildID)
	if err != nil || guild == nil {
		guild, err = s.Guild(guildID)
	}
	if guild != nil {
		if guild.OwnerID == userID {
			return true
		}
		member, err := s.State.Member(guildID, userID)
		if err != nil || member == nil {
			member, err = s.GuildMember(guildID, userID)
		}
		if member != nil {
			for _, roleID := range member.Roles {
				for _, r := range guild.Roles {
					if r.ID == roleID {
						if r.Permissions&discordgo.PermissionAdministrator != 0 || r.Permissions&discordgo.PermissionBanMembers != 0 {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (b *BotService) GetStats() models.BotStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	uptimeDuration := time.Since(b.startTime)
	uptimeStr := formatDuration(uptimeDuration)

	var ping int64 = 34
	guildCount := 2
	userCount := 2276
	channelCount := 10
	status := "online"
	botName := "หวานน้อย (Warn-noi)"
	botAvatar := "https://cdn.discordapp.com/embed/avatars/0.png"

	if !b.cfg.IsDemoMode && b.session != nil && b.session.State != nil {
		ping = b.session.HeartbeatLatency().Milliseconds()
		if ping <= 0 {
			ping = 25
		}
		guildCount = len(b.session.State.Guilds)
		userCount = 0
		channelCount = 0
		for _, g := range b.session.State.Guilds {
			userCount += g.MemberCount
			channelCount += len(g.Channels)
		}
		if b.session.State.User != nil {
			botName = b.session.State.User.Username
			if b.session.State.User.Avatar != "" {
				botAvatar = b.session.State.User.AvatarURL("128")
			}
		}
	} else {
		// In demo mode, increment messages slowly for live dashboard feel
		atomic.AddInt64(&b.messagesProcessed, 1)
	}

	return models.BotStats{
		Status:            status,
		IsDemo:            b.cfg.IsDemoMode,
		Uptime:            uptimeStr,
		UptimeSeconds:     int64(uptimeDuration.Seconds()),
		PingMs:            ping,
		GuildCount:        guildCount,
		UserCount:         userCount,
		ChannelCount:      channelCount,
		MessagesProcessed: atomic.LoadInt64(&b.messagesProcessed),
		MemoryAllocMB:     float64(m.Alloc) / 1024 / 1024,
		MemorySysMB:       float64(m.Sys) / 1024 / 1024,
		Goroutines:        runtime.NumGoroutine(),
		GoVersion:         runtime.Version(),
		BotUsername:       botName,
		BotAvatar:         botAvatar,
	}
}

func (b *BotService) GetGuilds() []models.Guild {
	if b.cfg.IsDemoMode || b.session == nil || b.session.State == nil {
		return b.store.GetDemoGuilds()
	}

	var guilds []models.Guild
	for _, g := range b.session.State.Guilds {
		var channels []models.Channel
		for _, c := range g.Channels {
			channels = append(channels, models.Channel{
				ID:   c.ID,
				Name: c.Name,
				Type: int(c.Type),
			})
		}

		var roles []models.Role
		guildRoles := g.Roles
		if b.session != nil {
			if liveRoles, err := b.session.GuildRoles(g.ID); err == nil && len(liveRoles) > 0 {
				guildRoles = liveRoles
			}
		}
		for _, r := range guildRoles {
			roles = append(roles, models.Role{
				ID:       r.ID,
				Name:     r.Name,
				ColorHex: fmt.Sprintf("#%06X", r.Color),
				Position: r.Position,
				Managed:  r.Managed,
			})
		}

		iconURL := ""
		if g.Icon != "" {
			iconURL = g.IconURL("128")
		}

		guilds = append(guilds, models.Guild{
			ID:          g.ID,
			Name:        g.Name,
			Icon:        iconURL,
			MemberCount: g.MemberCount,
			IsOwner:     false,
			BotInGuild:  true,
			Channels:    channels,
			Roles:       roles,
		})
	}
	return guilds
}

func (b *BotService) GetGuild(id string) *models.Guild {
	guilds := b.GetGuilds()
	for _, g := range guilds {
		if g.ID == id {
			return &g
		}
	}
	return nil
}

func (b *BotService) GetGuildMembers(guildID string) ([]models.GuildMember, error) {
	if b.cfg.IsDemoMode || b.session == nil {
		return []models.GuildMember{}, nil
	}

	members, err := b.session.GuildMembers(guildID, "", 100)
	if err != nil {
		g, gErr := b.session.State.Guild(guildID)
		if gErr == nil && g != nil {
			members = g.Members
		} else {
			return []models.GuildMember{}, nil
		}
	}

	voiceMap := make(map[string]string)
	muteMap := make(map[string]bool)
	g, _ := b.session.State.Guild(guildID)
	if g != nil {
		chNames := make(map[string]string)
		for _, c := range g.Channels {
			chNames[c.ID] = c.Name
		}
		for _, vs := range g.VoiceStates {
			voiceMap[vs.UserID] = chNames[vs.ChannelID]
			muteMap[vs.UserID] = vs.Mute || vs.SelfMute
		}
	}

	var result []models.GuildMember
	for _, m := range members {
		if m.User == nil {
			continue
		}
		name := m.User.Username
		if m.Nick != "" {
			name = m.Nick
		}

		avatar := m.User.AvatarURL("128")
		if avatar == "" {
			avatar = "https://cdn.discordapp.com/embed/avatars/0.png"
		}

		roleName := "Member"
		roleColor := "rgba(255, 255, 255, 0.2)"
		if g != nil && len(m.Roles) > 0 {
			for _, r := range g.Roles {
				if r.ID == m.Roles[0] {
					roleName = r.Name
					if r.Color != 0 {
						roleColor = fmt.Sprintf("#%06X", r.Color)
					}
					break
				}
			}
		}

		result = append(result, models.GuildMember{
			ID:          m.User.ID,
			Username:    m.User.Username,
			DisplayName: name,
			Avatar:      avatar,
			Roles:       m.Roles,
			RoleName:    roleName,
			RoleColor:   roleColor,
			VoiceRoom:   voiceMap[m.User.ID],
			IsBot:       m.User.Bot,
			IsMuted:     muteMap[m.User.ID],
			Status:      "online",
		})
	}

	return result, nil
}

func (b *BotService) SendTestWelcome(guildID string) error {
	st := b.store.GetSettings(guildID)
	if st == nil {
		return fmt.Errorf("settings not found")
	}

	b.store.AddAuditLog(guildID, "TEST_WELCOME", "Dashboard User", fmt.Sprintf("Triggered test welcome message to channel: %s", st.WelcomeChannelID))

	if b.cfg.IsDemoMode || b.session == nil {
		return nil
	}

	if st.WelcomeChannelID != "" {
		_, err := b.session.ChannelMessageSend(st.WelcomeChannelID, "🧪 [Test Notification] "+st.WelcomeMessage)
		return err
	}
	return nil
}

// CreateMemberRole creates an assignable member role in Discord
func (b *BotService) CreateMemberRole(guildID, roleName string) (*models.Role, error) {
	if b.session == nil {
		return nil, fmt.Errorf("bot session is not active")
	}

	if roleName == "" {
		roleName = "สมาชิก"
	}

	color := 0x22c55e // emerald green
	hoist := true
	mentionable := true

	role, err := b.session.GuildRoleCreate(guildID, &discordgo.RoleParams{
		Name:        roleName,
		Color:       &color,
		Hoist:       &hoist,
		Mentionable: &mentionable,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create role: %w", err)
	}

	// Update guild settings with this new role as default verify role
	st := b.store.GetSettings(guildID)
	if st != nil {
		st.VerifyRoleID = role.ID
		st.VerifyEnabled = true
		b.store.UpdateSettings(guildID, st, "Bot Auto-Create Role")
	}

	b.store.AddAuditLog(guildID, "CREATE_ROLE", "Bot", fmt.Sprintf("Created new member role '%s' (%s)", role.Name, role.ID))

	return &models.Role{
		ID:       role.ID,
		Name:     role.Name,
		ColorHex: fmt.Sprintf("#%06X", role.Color),
		Position: role.Position,
		Managed:  role.Managed,
	}, nil
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	return fmt.Sprintf("%dm %ds", minutes, seconds)
}
