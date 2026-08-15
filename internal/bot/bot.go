package bot

import (
	"context"
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

	"winmon/internal/config"
	"winmon/internal/executor"
	"winmon/internal/files"
	"winmon/internal/service"
	"winmon/internal/updater"
)

type BotCoordinator struct {
	cfg        *config.Config
	bot        *tgbotapi.BotAPI
	executor   executor.ActionExecutor
	stopChan   chan struct{}
	handlerSem chan struct{}
}

const maxConcurrentHandlers = 4

// NewBotCoordinator constructs a BotCoordinator with the default context-aware AutoExecutor.
func NewBotCoordinator(cfg *config.Config, stopChan chan struct{}) *BotCoordinator {
	return NewBotCoordinatorWithExecutor(cfg, executor.NewAutoExecutor(), stopChan)
}

// NewBotCoordinatorWithExecutor constructs a BotCoordinator with a custom ActionExecutor (useful for testing).
func NewBotCoordinatorWithExecutor(cfg *config.Config, exec executor.ActionExecutor, stopChan chan struct{}) *BotCoordinator {
	return &BotCoordinator{
		cfg:        cfg,
		executor:   exec,
		stopChan:   stopChan,
		handlerSem: make(chan struct{}, maxConcurrentHandlers),
	}
}

func (b *BotCoordinator) Start() {
	if len(b.cfg.AllowedUsers) == 0 {
		log.Println("WARNING: allowed_users is empty in configuration! ALL incoming Telegram requests will be DENIED by default for security.")
	}
	if skipped := nonNumericAllowedUsers(b.cfg.AllowedUsers); len(skipped) > 0 {
		log.Printf("WARNING: non-numeric allowed_users entries are ignored (use numeric Telegram user IDs only): %v", skipped)
	}

	var bot *tgbotapi.BotAPI
	backoff := 2 * time.Second

	for {
		select {
		case <-b.stopChan:
			log.Println("WinMon stopping before Telegram bot session established.")
			return
		default:
		}

		var err error
		bot, err = tgbotapi.NewBotAPI(b.cfg.BotToken)
		if err == nil {
			break
		}

		log.Printf("Waiting for network/Telegram connection: %v (retrying in %v)...", err, backoff)
		select {
		case <-b.stopChan:
			return
		case <-time.After(backoff):
			if backoff < 30*time.Second {
				backoff += 2 * time.Second
			}
		}
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

	case "/download":
		if len(args) < 1 {
			b.sendText(chatID, "Usage: /download <filepath>")
			return
		}
		filePath := strings.Join(args, " ")
		b.sendFile(chatID, filePath, fmt.Sprintf("Downloaded from `%s`:", b.cfg.DeviceName))

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

	default:
		// Map command string to host action type
		actionType := executor.ActionType(strings.TrimPrefix(cmd, "/"))
		if actionType == "deviceinfo" {
			actionType = executor.ActionSysInfo
		}

		flatArgs := strings.Join(args, " ")
		res, err := b.executor.Execute(context.Background(), executor.Action{
			Type:     actionType,
			Args:     args,
			FlatArgs: flatArgs,
			Timeout:  25 * time.Second,
		})
		if err != nil {
			b.sendText(chatID, fmt.Sprintf("❌ [Error] %v", err))
			return
		}

		if res == nil {
			return
		}
		if res.Cleanup != nil {
			defer res.Cleanup()
		}

		b.dispatchResult(chatID, res)
	}
}

func (b *BotCoordinator) dispatchResult(chatID int64, res *executor.Result) {
	switch res.Kind {
	case executor.ResultKindText:
		b.sendText(chatID, res.Text)
	case executor.ResultKindPhoto:
		b.sendPhoto(chatID, res.FilePath, res.Caption)
	case executor.ResultKindAnimation:
		b.sendAnimation(chatID, res.FilePath, res.Caption)
	case executor.ResultKindAudio:
		b.sendVoice(chatID, res.FilePath, res.Caption)
	case executor.ResultKindFile:
		b.sendFile(chatID, res.FilePath, res.Caption)
	}
}

func (b *BotCoordinator) sendText(chatID int64, text string) {
	if b.bot == nil {
		return
	}
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	if _, err := b.bot.Send(msg); err != nil {
		msg.ParseMode = ""
		_, _ = b.bot.Send(msg)
	}
}

func (b *BotCoordinator) sendPhoto(chatID int64, filePath string, caption string) {
	if b.bot == nil {
		return
	}
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
	if b.bot == nil {
		return
	}
	voice := tgbotapi.NewVoice(chatID, tgbotapi.FilePath(filePath))
	voice.Caption = caption
	voice.ParseMode = tgbotapi.ModeMarkdown
	if _, err := b.bot.Send(voice); err != nil {
		log.Printf("Error sending voice note to Telegram (retrying as document): %v", err)
		b.sendFile(chatID, filePath, caption)
	}
}

func (b *BotCoordinator) sendAnimation(chatID int64, filePath string, caption string) {
	if b.bot == nil {
		return
	}
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
	if b.bot == nil {
		return
	}
	// Check if filePath is a directory -> compress to ZIP before sending
	info, err := os.Stat(filePath)
	if err == nil && info.IsDir() {
		zipPath := filepath.Join(service.GetSharedTempDir(), fmt.Sprintf("%s.zip", filepath.Base(filePath)))
		if zipErr := files.ZipDirectory(filePath, zipPath); zipErr != nil {
			b.sendText(chatID, fmt.Sprintf("❌ [Error] Failed to compress folder `%s`: %v", filePath, zipErr))
			return
		}
		defer os.Remove(zipPath)
		filePath = zipPath
	}

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
	var origFilename string
	var fileSize int

	if msg.Document != nil {
		fileID = msg.Document.FileID
		origFilename = msg.Document.FileName
		fileSize = msg.Document.FileSize
	} else if len(msg.Photo) > 0 {
		photo := msg.Photo[len(msg.Photo)-1]
		fileID = photo.FileID
		origFilename = fmt.Sprintf("photo_%d.jpg", time.Now().Unix())
		fileSize = photo.FileSize
	} else {
		b.sendText(chatID, "[Error] No valid file attachment found in message.")
		return "", fmt.Errorf("no valid file attachment")
	}

	maxBytes := b.cfg.MaxUploadSizeMB * 1024 * 1024
	if maxBytes > 0 && int64(fileSize) > maxBytes {
		b.sendText(chatID, fmt.Sprintf("[Error] File size (%d MB) exceeds configured limit (%d MB).",
			fileSize/(1024*1024), b.cfg.MaxUploadSizeMB))
		return "", fmt.Errorf("file exceeds size limit")
	}

	finalPath, err := files.PrepareUploadPath(destination, origFilename)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to prepare upload path: %v", err))
		return "", err
	}

	fileURL, err := b.bot.GetFileDirectURL(fileID)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to resolve Telegram file URL: %v", err))
		return "", err
	}

	resp, err := http.Get(fileURL)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to download file from Telegram: %v", err))
		return "", err
	}
	defer resp.Body.Close()

	out, err := os.Create(finalPath)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed to create local file `%s`: %v", finalPath, err))
		return "", err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("[Error] Failed writing file to disk: %v", err))
		return "", err
	}

	b.sendText(chatID, fmt.Sprintf("✅ File uploaded and saved successfully to:\n`%s`", finalPath))
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

	res, err := b.executor.Execute(context.Background(), executor.Action{
		Type:     executor.ActionSetWallpaper,
		FlatArgs: savedPath,
	})
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("❌ [Error] Failed to set wallpaper: %v", err))
		return
	}
	if res != nil {
		b.dispatchResult(chatID, res)
	}
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

// RunSessionHelper executes a single command inside the user's interactive desktop session.
// Deprecated: Use executor.RunSessionHelper directly.
func RunSessionHelper(cmd string, args string, outputFile string) (string, error) {
	return executor.RunSessionHelper(cmd, args, outputFile)
}

// RunSessionAgentLoop runs the persistent Named Pipe IPC listener in Session 1.
// Deprecated: Use executor.RunSessionAgentLoop directly.
func RunSessionAgentLoop() error {
	return executor.RunSessionAgentLoop()
}
