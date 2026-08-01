package bot

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

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
	"winmon/internal/updater"
)

type BotCoordinator struct {
	cfg      *config.Config
	bot      *tgbotapi.BotAPI
	stopChan chan struct{}
}

func NewBotCoordinator(cfg *config.Config, stopChan chan struct{}) *BotCoordinator {
	return &BotCoordinator{
		cfg:      cfg,
		stopChan: stopChan,
	}
}

func (b *BotCoordinator) Start() {
	if len(b.cfg.AllowedUsers) == 0 {
		log.Println("WARNING: allowed_users is empty in configuration! ALL incoming Telegram requests will be DENIED by default for security.")
	}

	bot, err := tgbotapi.NewBotAPI(b.cfg.BotToken)
	if err != nil {
		log.Fatalf("Failed to create Telegram bot session: %v", err)
	}
	b.bot = bot

	log.Printf("WinMon Telegram Bot connected successfully as @%s (Device: %s)",
		bot.Self.UserName, b.cfg.DeviceName)

	// Clear Bot Commands Menu from Telegram
	if _, err := b.bot.Request(tgbotapi.NewDeleteMyCommands()); err != nil {
		log.Printf("Warning: Failed to clear Telegram bot commands menu: %v", err)
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := bot.GetUpdatesChan(u)

	for {
		select {
		case <-b.stopChan:
			log.Println("Stopping WinMon Telegram Bot...")
			bot.StopReceivingUpdates()
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			go b.handleUpdate(update)
		}
	}
}

func (b *BotCoordinator) isAuthorized(userID int64, username string) bool {
	if len(b.cfg.AllowedUsers) == 0 {
		return false
	}
	idStr := strconv.FormatInt(userID, 10)
	for _, allowed := range b.cfg.AllowedUsers {
		allowedClean := strings.TrimSpace(allowed)
		if allowedClean == idStr || (username != "" && strings.EqualFold(allowedClean, username)) {
			return true
		}
	}
	return false
}

func (b *BotCoordinator) handleUpdate(update tgbotapi.Update) {
	if update.Message == nil {
		return
	}

	msg := update.Message
	userID := msg.From.ID
	username := msg.From.UserName
	chatID := msg.Chat.ID

	if !b.isAuthorized(userID, username) {
		log.Printf("Unauthorized Telegram access attempt from UserID: %d (@%s)", userID, username)
		b.sendText(chatID, fmt.Sprintf("[Error] Access Denied: User ID `%d` is not authorized to control device `%s`.", userID, b.cfg.DeviceName))
		return
	}

	// Handle attachments (Uploads / Wallpapers)
	if msg.Photo != nil || msg.Document != nil {
		caption := strings.TrimSpace(msg.Caption)
		if strings.HasPrefix(caption, "/setwallpaper") {
			b.handleSetWallpaperAttachment(msg)
			return
		}
		if strings.HasPrefix(caption, "/upload") {
			dest := strings.TrimSpace(strings.TrimPrefix(caption, "/upload"))
			b.handleAttachmentUpload(msg, dest)
			return
		}
		if msg.Document != nil {
			b.handleAttachmentUpload(msg, "")
			return
		}
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}

	fields := strings.Fields(text)
	cmd := strings.ToLower(fields[0])
	if idx := strings.Index(cmd, "@"); idx != -1 {
		cmd = cmd[:idx]
	}
	args := fields[1:]

	b.processCommand(cmd, args, chatID)
}

func (b *BotCoordinator) processCommand(cmd string, args []string, chatID int64) {
	switch cmd {
	case "/start":
		info := fmt.Sprintf("💻 **WinMon Remote Management**\n"+
			"**Device:** `%s`\n"+
			"**Group:** `%s`\n"+
			"**Version:** `%s`\n\n"+
			"Type /help to see all available commands.",
			b.cfg.DeviceName, b.cfg.Group, b.cfg.Version)
		b.sendText(chatID, info)

	case "/help":
		helpMsg := "⚡ **WinMon Available Commands**\n\n" +
			"💻 /start - Show basic device status\n" +
			"❓ /help - Show available commands list\n" +
			"📸 /screenshot - Capture primary display screenshot\n" +
			"📹 /webcam - Capture photo from active webcam\n" +
			"🎥 /screenrecord [sec] - Record screen activity as GIF (e.g. /screenrecord 5)\n" +
			"🎙 /listen [sec] - Record microphone audio voice note (e.g. /listen 5)\n" +
			"🔊 /tts <text> - Convert text to speech and play on PC\n" +
			"💻 /cmd <command> - Execute shell command (e.g. /cmd ipconfig)\n" +
			"📊 /sysinfo - Display hardware metrics & system info\n" +
			"⚙️ /processes - List running processes\n" +
			"💀 /kill <PID|Name> - Kill process by PID or name\n" +
			"📥 /download <path> - Download file from PC\n" +
			"📤 /upload [path] - Upload file attachment to PC\n" +
			"📋 /clipboard [text] - Read or set text on PC clipboard\n" +
			"🔆 /brightness <0-100> - Set display brightness\n" +
			"🔊 /volume <0-100|mute|unmute> - Set or toggle master audio\n" +
			"🔒 /lock - Lock Windows workstation\n" +
			"🔔 /notify <Title | Message> - Show toast notification on PC\n" +
			"🖼 /setwallpaper - Set wallpaper from photo attachment\n" +
			"🖼 /wallpaper - Retrieve current desktop wallpaper photo\n" +
			"🔄 /restartservice - Restart WinMon service\n" +
			"🛑 /shutdownservice - Stop WinMon service/process\n" +
			"💥 /implode - Self-destruct WinMon from this PC"
		b.sendText(chatID, helpMsg)

	case "/screenshot", "/webcam", "/screenrecord", "/listen", "/clipboard", "/tts", "/wallpaper":
		b.executeCommandLocallyOrIPC(cmd, args, chatID)

	case "/sysinfo", "/deviceinfo":
		b.executeNativeTelegram("/sysinfo", args, chatID, time.Now())

	case "/processes", "/kill", "/cmd", "/download", "/volume", "/brightness", "/lock", "/notify", "/setwallpaper":
		b.executeNativeTelegram(cmd, args, chatID, time.Now())

	case "/restartservice":
		b.sendText(chatID, "🔄 Restarting WinMon service...")
		if service.IsRunningAsService() {
			if err := service.StartService("WinMon"); err != nil {
				log.Printf("Failed to restart WinMon service: %v", err)
			}
		}

	case "/shutdownservice":
		b.sendText(chatID, "🛑 Stopping WinMon service/process...")
		if service.IsRunningAsService() {
			if err := service.StopService("WinMon"); err != nil {
				log.Printf("Failed to stop WinMon service: %v", err)
			}
		} else {
			go func() {
				time.Sleep(1 * time.Second)
				os.Exit(0)
			}()
		}

	case "/implode":
		if len(args) > 0 && strings.ToLower(args[0]) == "confirm" {
			b.sendText(chatID, "💥 Uninstalling WinMon service and self-destructing...")
			err := updater.ImplodeService(b.cfg.BotToken, chatID)
			if err != nil {
				b.sendText(chatID, fmt.Sprintf("❌ [Error] Implode failed: %v", err))
			}
			return
		}
		confirmMsg := "⚠️ **Self-Destruct Confirmation Required**\n\n" +
			"Are you sure you want to completely uninstall WinMon and delete all files from this PC?\n\n" +
			"Type /confirm_implode to execute self-destruction."
		b.sendText(chatID, confirmMsg)

	case "/confirm_implode":
		b.sendText(chatID, "💥 Uninstalling WinMon service and self-destructing...")
		err := updater.ImplodeService(b.cfg.BotToken, chatID)
		if err != nil {
			b.sendText(chatID, fmt.Sprintf("❌ [Error] Implode failed: %v", err))
		}

	default:
		b.sendText(chatID, fmt.Sprintf("Unknown command: `%s`. Type /help for available commands.", cmd))
	}
}

func (b *BotCoordinator) executeCommandLocallyOrIPC(cmd string, args []string, chatID int64) {
	start := time.Now()
	flatArgs := strings.Join(args, " ")

	var customTempPath string
	ts := time.Now().UnixNano()
	switch cmd {
	case "/screenshot":
		customTempPath = filepath.Join(service.GetSharedTempDir(), fmt.Sprintf("helper_screenshot_%d.jpg", ts))
	case "/webcam":
		customTempPath = filepath.Join(service.GetSharedTempDir(), fmt.Sprintf("helper_webcam_%d.jpg", ts))
	case "/screenrecord":
		customTempPath = filepath.Join(service.GetSharedTempDir(), fmt.Sprintf("helper_record_%d.gif", ts))
	case "/listen":
		customTempPath = filepath.Join(service.GetSharedTempDir(), fmt.Sprintf("helper_audio_%d.wav", ts))
	case "/clipboard":
		customTempPath = filepath.Join(service.GetSharedTempDir(), fmt.Sprintf("helper_clipboard_%d.txt", ts))
	case "/wallpaper":
		// Extension is applied by the session helper based on the real wallpaper format.
		customTempPath = filepath.Join(service.GetSharedTempDir(), fmt.Sprintf("helper_wallpaper_%d", ts))
	}

	if service.IsRunningAsService() {
		// Route via Session 1 IPC Agent
		resp, err := service.SendIPCCommand(service.IPCRequest{
			Cmd:        cmd,
			Args:       args,
			FlatArgs:   flatArgs,
			OutputFile: customTempPath,
		}, 60*time.Second)
		if err != nil {
			if customTempPath != "" {
				_ = os.Remove(customTempPath)
			}
			b.sendText(chatID, fmt.Sprintf("[Error] Session Agent IPC Error: %v", err))
			return
		}
		if !resp.Success {
			if customTempPath != "" {
				_ = os.Remove(customTempPath)
			}
			b.sendText(chatID, fmt.Sprintf("[Error] IPC Command Error: %s", resp.Error))
			return
		}
		b.handleHelperOutputTelegram(cmd, chatID, start, customTempPath)
		return
	}

	// Console mode / Local service execution
	err := RunSessionHelper(cmd, flatArgs, customTempPath)
	if err != nil {
		if customTempPath != "" {
			_ = os.Remove(customTempPath)
		}
		b.sendText(chatID, fmt.Sprintf("[Error] Command Error: %v", err))
		return
	}
	b.handleHelperOutputTelegram(cmd, chatID, start, customTempPath)
}

func (b *BotCoordinator) executeNativeTelegram(cmd string, args []string, chatID int64, start time.Time) {
	dur := fmt.Sprintf("(%d ms)", time.Since(start).Milliseconds())

	switch cmd {
	case "/sysinfo", "/deviceinfo":
		info, err := device.GetDeviceInfo(b.cfg.DeviceName, b.cfg.DeviceID, b.cfg.Version)
		if err != nil {
			b.sendText(chatID, fmt.Sprintf("[Error] Error getting sysinfo: %v", err))
			return
		}
		msg := fmt.Sprintf("**WinMon System Metrics** %s\n```\n%s\n```", dur, info)
		b.sendText(chatID, msg)

	case "/processes":
		procs, err := shell.ExecuteCommand("tasklist", 10*time.Second)
		if err != nil {
			b.sendText(chatID, fmt.Sprintf("[Error] Error fetching processes: %v", err))
			return
		}
		if len(procs) > 3800 {
			procs = procs[:3800] + "\n... [truncated]"
		}
		b.sendText(chatID, fmt.Sprintf("**Running Processes:** %s\n```\n%s\n```", dur, procs))

	case "/kill":
		if len(args) < 1 {
			b.sendText(chatID, "Usage: /kill <PID or ProcessName>")
			return
		}
		killCmd := fmt.Sprintf("taskkill /F /PID %s || taskkill /F /IM %s", args[0], args[0])
		out, err := shell.ExecuteCommand(killCmd, 10*time.Second)
		if err != nil {
			b.sendText(chatID, fmt.Sprintf("[Error] Process kill output:\n```\n%s\nError: %v\n```", out, err))
		} else {
			b.sendText(chatID, fmt.Sprintf("Process `%s` terminated successfully:\n```\n%s\n```", args[0], out))
		}

	case "/cmd":
		if len(args) < 1 {
			b.sendText(chatID, "Usage: /cmd <command>")
			return
		}
		execStr := strings.Join(args, " ")
		out, err := shell.ExecuteCommand(execStr, 25*time.Second)
		if len(out) > 3800 {
			out = out[:3800] + "\n... [truncated]"
		}
		if err != nil {
			b.sendText(chatID, fmt.Sprintf("[Error] **Command Execution Error:**\n```\n%s\nError: %v\n```", out, err))
		} else {
			b.sendText(chatID, fmt.Sprintf("**Command Output:** %s\n```\n%s\n```", dur, out))
		}

	case "/download":
		if len(args) < 1 {
			b.sendText(chatID, "Usage: /download <filepath>")
			return
		}
		filePath := strings.Join(args, " ")
		b.sendFile(chatID, filePath, fmt.Sprintf("Downloaded from `%s`:", b.cfg.DeviceName))

	case "/volume":
		if len(args) < 1 {
			b.sendText(chatID, "Usage: /volume <0-100 | mute | unmute>")
			return
		}
		arg := strings.ToLower(args[0])
		if arg == "mute" {
			_ = audio.SetMute(true)
			b.sendText(chatID, "Audio Muted.")
		} else if arg == "unmute" {
			_ = audio.SetMute(false)
			b.sendText(chatID, "Audio Unmuted.")
		} else {
			vol, err := strconv.Atoi(arg)
			if err != nil {
				b.sendText(chatID, "Volume must be an integer between 0 and 100.")
				return
			}
			err = audio.SetVolume(vol)
			if err != nil {
				b.sendText(chatID, fmt.Sprintf("[Error] Failed to set volume: %v", err))
			} else {
				b.sendText(chatID, fmt.Sprintf("Volume set to **%d%%**.", vol))
			}
		}

	case "/brightness":
		if len(args) < 1 {
			b.sendText(chatID, "Usage: /brightness <0-100>")
			return
		}
		bri, err := strconv.Atoi(args[0])
		if err != nil {
			b.sendText(chatID, "Brightness must be an integer (0-100).")
			return
		}
		if service.IsRunningAsService() {
			b.executeCommandLocallyOrIPC("/brightness", []string{args[0]}, chatID)
		} else {
			err = display.SetBrightness(bri)
			if err != nil {
				b.sendText(chatID, fmt.Sprintf("[Error] Brightness error: %v", err))
			} else {
				b.sendText(chatID, fmt.Sprintf("Brightness set to **%d%%**.", bri))
			}
		}

	case "/lock":
		_ = input.TriggerHotkey("win+l")
		b.sendText(chatID, "Workstation Locked.")

	case "/notify":
		if len(args) < 1 {
			b.sendText(chatID, "Usage: /notify <title> | <message>")
			return
		}
		fullText := strings.Join(args, " ")
		if service.IsRunningAsService() {
			b.executeCommandLocallyOrIPC("/notify", []string{fullText}, chatID)
		} else {
			parts := strings.Split(fullText, "|")
			title := "WinMon Notification"
			msg := fullText
			if len(parts) > 1 {
				title = strings.TrimSpace(parts[0])
				msg = strings.TrimSpace(parts[1])
			}
			err := notifications.ShowToastLocal(title, msg)
			if err != nil {
				b.sendText(chatID, fmt.Sprintf("[Error] Notification error: %v", err))
			} else {
				b.sendText(chatID, "Notification displayed on PC screen.")
			}
		}
	}
}

func (b *BotCoordinator) handleHelperOutputTelegram(cmd string, chatID int64, start time.Time, tempPath string) {
	dur := fmt.Sprintf("(%d ms)", time.Since(start).Milliseconds())

	switch cmd {
	case "/screenshot":
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_screenshot.jpg")
		}
		if _, err := os.Stat(tempPath); err == nil {
			b.sendPhoto(chatID, tempPath, "📸 **Desktop Screenshot** "+dur)
			os.Remove(tempPath)
		} else {
			b.sendText(chatID, "❌ [Error] Failed to retrieve screenshot from session agent.")
		}
	case "/webcam":
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_webcam.jpg")
		}
		if _, err := os.Stat(tempPath); err == nil {
			b.sendPhoto(chatID, tempPath, "📹 **Webcam Photo** "+dur)
			os.Remove(tempPath)
		} else {
			b.sendText(chatID, "❌ [Error] Failed to retrieve webcam photo from session agent.")
		}
	case "/screenrecord":
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_record.gif")
		}
		if _, err := os.Stat(tempPath); err == nil {
			b.sendAnimation(chatID, tempPath, "🎥 **Screen Recording GIF** "+dur)
			os.Remove(tempPath)
		} else {
			b.sendText(chatID, "❌ [Error] Failed to retrieve screen recording from session agent.")
		}
	case "/listen":
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_audio.wav")
		}
		if _, err := os.Stat(tempPath); err == nil {
			b.sendVoice(chatID, tempPath, "🎙️ **Microphone Audio Voice Note** "+dur)
			os.Remove(tempPath)
		} else {
			b.sendText(chatID, "❌ [Error] Failed to retrieve audio recording from session agent.")
		}
	case "/clipboard":
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_clipboard.txt")
		}
		if data, err := os.ReadFile(tempPath); err == nil {
			b.sendText(chatID, fmt.Sprintf("📋 **Clipboard Content:**\n```\n%s\n```", string(data)))
			os.Remove(tempPath)
		} else {
			b.sendText(chatID, "❌ [Error] Failed to read clipboard from session agent.")
		}
	case "/tts":
		b.sendText(chatID, "🔊 Spoke text on PC speakers.")
	case "/notify":
		b.sendText(chatID, "🔔 Notification displayed on PC screen.")
	case "/wallpaper":
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_wallpaper")
		}
		wallFile := findWallpaperOutput(tempPath)
		if wallFile != "" {
			caption := "🖼 **Desktop Wallpaper** " + dur
			ext := strings.ToLower(filepath.Ext(wallFile))
			switch ext {
			case ".jpg", ".jpeg", ".png", ".gif", ".webp":
				b.sendPhoto(chatID, wallFile, caption)
			default:
				b.sendFile(chatID, wallFile, caption)
			}
			os.Remove(wallFile)
		} else {
			b.sendText(chatID, "❌ [Error] Failed to retrieve desktop wallpaper.")
		}
	default:
		b.sendText(chatID, fmt.Sprintf("✅ Command `%s` executed successfully %s.", cmd, dur))
	}
}

func (b *BotCoordinator) sendText(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	if _, err := b.bot.Send(msg); err != nil {
		// Fallback to plain text if Markdown parsing fails
		msg.ParseMode = ""
		_, _ = b.bot.Send(msg)
	}
}

func (b *BotCoordinator) sendPhoto(chatID int64, filePath string, caption string) {
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(filePath))
	photo.Caption = caption
	photo.ParseMode = tgbotapi.ModeMarkdown
	if _, err := b.bot.Send(photo); err != nil {
		log.Printf("Error sending photo to Telegram (retrying as plain text): %v", err)
		photo.ParseMode = ""
		_, _ = b.bot.Send(photo)
	}
}

func (b *BotCoordinator) sendVoice(chatID int64, filePath string, caption string) {
	voice := tgbotapi.NewVoice(chatID, tgbotapi.FilePath(filePath))
	voice.Caption = caption
	voice.ParseMode = tgbotapi.ModeMarkdown
	if _, err := b.bot.Send(voice); err != nil {
		log.Printf("Error sending voice note to Telegram (retrying as plain text): %v", err)
		voice.ParseMode = ""
		_, _ = b.bot.Send(voice)
	}
}

func (b *BotCoordinator) sendAnimation(chatID int64, filePath string, caption string) {
	anim := tgbotapi.NewAnimation(chatID, tgbotapi.FilePath(filePath))
	anim.Caption = caption
	anim.ParseMode = tgbotapi.ModeMarkdown
	if _, err := b.bot.Send(anim); err != nil {
		log.Printf("Error sending animation to Telegram (retrying as plain text): %v", err)
		anim.ParseMode = ""
		_, _ = b.bot.Send(anim)
	}
}

func (b *BotCoordinator) sendFile(chatID int64, filePath string, caption string) {
	doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(filePath))
	doc.Caption = caption
	doc.ParseMode = tgbotapi.ModeMarkdown
	if _, err := b.bot.Send(doc); err != nil {
		log.Printf("Error sending document to Telegram (retrying as plain text): %v", err)
		doc.ParseMode = ""
		if _, err2 := b.bot.Send(doc); err2 != nil {
			b.sendText(chatID, fmt.Sprintf("[Error] Error sending file %s: %v", filePath, err2))
		}
	}
}

func (b *BotCoordinator) handleAttachmentUpload(msg *tgbotapi.Message, destination string) {
	chatID := msg.Chat.ID
	var fileID string
	var fileName string

	if msg.Document != nil {
		fileID = msg.Document.FileID
		fileName = msg.Document.FileName
	} else if msg.Photo != nil && len(msg.Photo) > 0 {
		bestPhoto := msg.Photo[len(msg.Photo)-1]
		fileID = bestPhoto.FileID
		fileName = fmt.Sprintf("photo_%d.jpg", time.Now().Unix())
	}

	if fileID == "" {
		b.sendText(chatID, "[Error] No valid attachment found to download.")
		return
	}

	fileURL, err := b.bot.GetFileDirectURL(fileID)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to resolve Telegram file URL: %v", err))
		return
	}

	finalPath, err := files.PrepareUploadPath(destination, fileName)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Invalid upload destination: %v", err))
		return
	}

	resp, err := http.Get(fileURL)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to download file stream: %v", err))
		return
	}
	defer resp.Body.Close()

	out, err := os.Create(finalPath)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to create target file: %v", err))
		return
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to save file contents: %v", err))
		return
	}

	b.sendText(chatID, fmt.Sprintf("File uploaded successfully to `%s`", finalPath))
}

func (b *BotCoordinator) handleSetWallpaperAttachment(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	tempPath := filepath.Join(service.GetSharedTempDir(), fmt.Sprintf("winmon_wall_%d.jpg", time.Now().UnixNano()))
	b.handleAttachmentUpload(msg, tempPath)
	b.executeCommandLocallyOrIPC("/setwallpaper", []string{tempPath}, chatID)
	_ = os.Remove(tempPath)
}

// Session helper commands (executed inside user desktop session)
func RunSessionHelper(cmd string, args string, outputFile string) error {
	switch cmd {
	case "/screenshot":
		tempPath := outputFile
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_screenshot.jpg")
		}
		return media.CaptureScreen(tempPath)
	case "/webcam":
		tempPath := outputFile
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_webcam.jpg")
		}
		return media.CaptureWebcam(tempPath)
	case "/screenrecord":
		dur := parseDuration(args)
		tempPath := outputFile
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_record.gif")
		}
		return media.RecordScreen(dur, tempPath)
	case "/listen":
		dur := parseDuration(args)
		tempPath := outputFile
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_audio.wav")
		}
		return media.RecordAudio(dur, tempPath)
	case "/setwallpaper":
		return display.SetWallpaperLocal(args)
	case "/wallpaper":
		tempPath := outputFile
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_wallpaper")
		}
		wallPath, err := display.GetWallpaperPath()
		if err != nil {
			return fmt.Errorf("failed to get wallpaper path: %w", err)
		}
		data, err := os.ReadFile(wallPath)
		if err != nil {
			return fmt.Errorf("failed to read wallpaper file at %s: %w", wallPath, err)
		}
		ext := strings.ToLower(filepath.Ext(wallPath))
		if ext == "" {
			ext = imageExtFromMagic(data)
		}
		if filepath.Ext(tempPath) != "" {
			tempPath = strings.TrimSuffix(tempPath, filepath.Ext(tempPath))
		}
		tempPath = tempPath + ext
		return os.WriteFile(tempPath, data, 0644)
	case "/notify":
		parts := strings.Split(args, "|")
		title := "WinMon Notification"
		msg := args
		if len(parts) > 1 {
			title = strings.TrimSpace(parts[0])
			msg = strings.TrimSpace(parts[1])
		}
		err := notifications.ShowToastLocal(title, msg)
		if err != nil {
			return notifications.ShowAlert(title, msg)
		}
		return nil
	case "/tts":
		return audio.SpeakTTS(args)
	case "/clipboard":
		tempPath := outputFile
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_clipboard.txt")
		}
		if strings.TrimSpace(args) != "" {
			err := clipboard.SetClipboardLocal(args)
			if err != nil {
				return err
			}
			return os.WriteFile(tempPath, []byte("Clipboard text updated successfully."), 0644)
		}
		txt, err := clipboard.GetClipboardLocal()
		if err != nil || strings.TrimSpace(txt) == "" {
			txt = "(Clipboard is empty or contains non-text data)"
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

// findWallpaperOutput locates the wallpaper file written by the session helper.
// The helper appends the real image extension to the base output path.
func findWallpaperOutput(basePath string) string {
	if basePath == "" {
		return ""
	}
	if info, err := os.Stat(basePath); err == nil && !info.IsDir() {
		return basePath
	}
	matches, err := filepath.Glob(basePath + ".*")
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// imageExtFromMagic sniffs common image formats when the wallpaper path has no extension.
func imageExtFromMagic(data []byte) string {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return ".jpg"
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return ".png"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return ".gif"
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return ".webp"
	case len(data) >= 2 && data[0] == 0x42 && data[1] == 0x4D:
		return ".bmp"
	default:
		return ".bin"
	}
}

// RunSessionAgentLoop runs the persistent IPC listener in Session 1
func RunSessionAgentLoop() error {
	log.Println("Starting WinMon Persistent Session Agent (Session 1 IPC Listener)...")
	return service.StartIPCAgentServer(func(req service.IPCRequest) service.IPCResponse {
		err := RunSessionHelper(req.Cmd, req.FlatArgs, req.OutputFile)
		if err != nil {
			return service.IPCResponse{
				Success: false,
				Error:   err.Error(),
			}
		}
		return service.IPCResponse{
			Success:    true,
			OutputFile: req.OutputFile,
		}
	})
}
