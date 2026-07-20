package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

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

// State structures for pinned message coordination
type DeviceState struct {
	DeviceName string    `json:"device_name"`
	Group      string    `json:"group"`
	Version    string    `json:"version"`
	LastSeen   time.Time `json:"last_seen"`
	Status     string    `json:"status"` // "online"
}

type CommandState struct {
	Command      string    `json:"command"`
	TargetDevice string    `json:"target_device,omitempty"`
	Args         []string  `json:"args"`
	Timestamp    time.Time `json:"timestamp"`
	FileID       string    `json:"file_id,omitempty"`
	FileName     string    `json:"file_name,omitempty"`
}

type PinnedState struct {
	ActivePoller     string                 `json:"active_poller"`
	SelectedDevice   string                 `json:"selected_device"`
	Devices          map[string]DeviceState `json:"devices"`
	PendingCommand   *CommandState          `json:"pending_command,omitempty"`
	BroadcastCommand *CommandState          `json:"broadcast_command,omitempty"`
}

type LocalState struct {
	ChatID          int64 `json:"chat_id"`
	PinnedMessageID int64 `json:"pinned_message_id"`
}

type LastMsgState struct {
	ID      int64
	Text    string
	IsMedia bool
}

type BotCoordinator struct {
	cfg           *config.Config
	client        *http.Client
	statePath     string
	localState    *LocalState
	mu            sync.Mutex
	stopChan      chan struct{}
	lastErrTime   time.Time
	isPoller      bool
	lastBroadcast time.Time
	lastMsgs      map[int64]*LastMsgState
	lastMsgsMu    sync.Mutex
}

func NewBotCoordinator(cfg *config.Config, stopChan chan struct{}) *BotCoordinator {
	var statePath string
	if service.IsRunningAsService() {
		exePath, _ := os.Executable()
		statePath = filepath.Join(filepath.Dir(exePath), "state.json")
	} else {
		statePath = "state.json"
	}

	return &BotCoordinator{
		cfg:       cfg,
		client:    &http.Client{Timeout: 30 * time.Second},
		statePath: statePath,
		stopChan:  stopChan,
		lastMsgs:  make(map[int64]*LastMsgState),
	}
}

// HTTP requests helper
func (b *BotCoordinator) request(method string, payload interface{}) ([]byte, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", b.cfg.BotToken, method)
	var body io.Reader

	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewBuffer(data)
	}

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// Local State management
func (b *BotCoordinator) getLocalState() *LocalState {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.localState == nil {
		return nil
	}
	return &LocalState{
		ChatID:          b.localState.ChatID,
		PinnedMessageID: b.localState.PinnedMessageID,
	}
}

func (b *BotCoordinator) loadLocalState() error {
	data, err := os.ReadFile(b.statePath)
	if err != nil {
		return err
	}
	var ls LocalState
	if err := json.Unmarshal(data, &ls); err != nil {
		return err
	}
	b.mu.Lock()
	b.localState = &ls
	b.mu.Unlock()
	return nil
}

func (b *BotCoordinator) saveLocalState(chatID, msgID int64) error {
	ls := LocalState{ChatID: chatID, PinnedMessageID: msgID}
	data, err := json.MarshalIndent(ls, "", "  ")
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.localState = &ls
	b.mu.Unlock()
	return os.WriteFile(b.statePath, data, 0644)
}

var (
	ErrNoPinnedState      = errors.New("no pinned message found in chat")
	ErrInvalidPinnedState = errors.New("no json state block found or invalid format in pinned message")
)

// Telegram Pinned State management
func (b *BotCoordinator) getPinnedState() (*PinnedState, error) {
	ls := b.getLocalState()
	if ls == nil {
		return nil, fmt.Errorf("local state not loaded")
	}

	type GetChatReq struct {
		ChatID int64 `json:"chat_id"`
	}
	respBytes, err := b.request("getChat", GetChatReq{ChatID: ls.ChatID})
	if err != nil {
		return nil, err
	}

	var wrapper struct {
		Ok     bool `json:"ok"`
		Result struct {
			PinnedMessage struct {
				MessageID int64  `json:"message_id"`
				Text      string `json:"text"`
			} `json:"pinned_message"`
		} `json:"result"`
	}

	if err := json.Unmarshal(respBytes, &wrapper); err != nil {
		return nil, err
	}

	if wrapper.Result.PinnedMessage.MessageID == 0 {
		return nil, ErrNoPinnedState
	}

	text := wrapper.Result.PinnedMessage.Text
	// Find JSON block within message
	startIdx := strings.Index(text, "{")
	if startIdx == -1 {
		return nil, ErrInvalidPinnedState
	}
	endIdx := strings.LastIndex(text, "}")
	if endIdx == -1 || endIdx <= startIdx {
		return nil, ErrInvalidPinnedState
	}

	var state PinnedState
	if err := json.Unmarshal([]byte(text[startIdx:endIdx+1]), &state); err != nil {
		return nil, ErrInvalidPinnedState
	}

	// Only update local state if the pinned message ID changed AND it is a valid WinMon state
	if wrapper.Result.PinnedMessage.MessageID != ls.PinnedMessageID {
		_ = b.saveLocalState(ls.ChatID, wrapper.Result.PinnedMessage.MessageID)
	}

	return &state, nil
}

func (b *BotCoordinator) updatePinnedState(state *PinnedState) error {
	ls := b.getLocalState()
	if ls == nil {
		return fmt.Errorf("local state not loaded")
	}

	stateJSON, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	text := fmt.Sprintf("WinMon Coordination Mesh Status:\n```json\n%s\n```\nDo not unpin or modify.", string(stateJSON))

	type EditMsgReq struct {
		ChatID    int64  `json:"chat_id"`
		MessageID int64  `json:"message_id"`
		Text      string `json:"text"`
		ParseMode string `json:"parse_mode"`
	}

	_, err = b.request("editMessageText", EditMsgReq{
		ChatID:    ls.ChatID,
		MessageID: ls.PinnedMessageID,
		Text:      text,
		ParseMode: "Markdown",
	})
	return err
}

func (b *BotCoordinator) createPinnedState(chatID int64) error {
	state := PinnedState{
		ActivePoller:   b.cfg.DeviceID,
		SelectedDevice: b.cfg.DeviceID,
		Devices:        make(map[string]DeviceState),
	}
	state.Devices[b.cfg.DeviceID] = DeviceState{
		DeviceName: b.cfg.DeviceName,
		Group:      b.cfg.Group,
		Version:    b.cfg.Version,
		LastSeen:   time.Now(),
		Status:     "online",
	}

	stateJSON, _ := json.MarshalIndent(state, "", "  ")
	text := fmt.Sprintf("WinMon Coordination Mesh Status:\n```json\n%s\n```\nDo not unpin or modify.", string(stateJSON))

	type SendMsgReq struct {
		ChatID    int64  `json:"chat_id"`
		Text      string `json:"text"`
		ParseMode string `json:"parse_mode"`
	}

	respBytes, err := b.request("sendMessage", SendMsgReq{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "Markdown",
	})
	if err != nil {
		return err
	}

	var wrapper struct {
		Ok     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBytes, &wrapper); err != nil {
		return err
	}

	// Pin it
	type PinReq struct {
		ChatID    int64 `json:"chat_id"`
		MessageID int64 `json:"message_id"`
	}
	_, err = b.request("pinChatMessage", PinReq{
		ChatID:    chatID,
		MessageID: wrapper.Result.MessageID,
	})
	if err != nil {
		return err
	}

	return b.saveLocalState(chatID, wrapper.Result.MessageID)
}

// Telegram messaging helpers
func (b *BotCoordinator) sendMessage(chatID int64, text string, replyTo int64) int64 {
	type SendMsgReq struct {
		ChatID           int64  `json:"chat_id"`
		Text             string `json:"text"`
		ParseMode        string `json:"parse_mode"`
		ReplyToMessageID int64  `json:"reply_to_message_id,omitempty"`
	}
	req := SendMsgReq{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "Markdown",
	}
	if replyTo > 0 {
		req.ReplyToMessageID = replyTo
	}
	respBytes, err := b.request("sendMessage", req)
	if err != nil {
		return 0
	}
	var wrapper struct {
		Ok     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBytes, &wrapper); err != nil {
		return 0
	}

	b.lastMsgsMu.Lock()
	b.lastMsgs[replyTo] = &LastMsgState{
		ID:      wrapper.Result.MessageID,
		Text:    text,
		IsMedia: false,
	}
	b.lastMsgsMu.Unlock()

	return wrapper.Result.MessageID
}

func (b *BotCoordinator) sendChatAction(chatID int64, action string) {
	type ActionReq struct {
		ChatID int64  `json:"chat_id"`
		Action string `json:"action"`
	}
	b.request("sendChatAction", ActionReq{ChatID: chatID, Action: action})
}

func (b *BotCoordinator) sendFile(chatID int64, method string, paramName string, filePath string, replyTo int64) (int64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if replyTo > 0 {
		_ = writer.WriteField("reply_to_message_id", strconv.FormatInt(replyTo, 10))
	}

	part, err := writer.CreateFormFile(paramName, filepath.Base(filePath))
	if err != nil {
		return 0, err
	}
	_, err = io.Copy(part, file)
	if err != nil {
		return 0, err
	}

	err = writer.Close()
	if err != nil {
		return 0, err
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", b.cfg.BotToken, method)
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := b.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("telegram returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var wrapper struct {
		Ok     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &wrapper); err != nil {
		return 0, err
	}

	b.lastMsgsMu.Lock()
	b.lastMsgs[replyTo] = &LastMsgState{
		ID:      wrapper.Result.MessageID,
		Text:    "",
		IsMedia: true,
	}
	b.lastMsgsMu.Unlock()

	return wrapper.Result.MessageID, nil
}

// Run loop
func (b *BotCoordinator) Start() {
	err := b.loadLocalState()
	if err != nil {
		// Bootstrap Mode: Poll getUpdates directly until we receive an authorized message
		b.runBootstrapLoop()
	} else {
		b.runMeshLoop()
	}
}

func (b *BotCoordinator) runBootstrapLoop() {
	offset := int64(0)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-b.stopChan:
			if offset > 0 {
				_, _, _ = b.getUpdates(offset, 0)
			}
			return
		default:
			updates, newOffset, err := b.getUpdates(offset, 15)
			if err != nil {
				log.Printf("Error getting updates in bootstrap: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}
			offset = newOffset

			for _, u := range updates {
				if u.Message == nil {
					continue
				}
				if !b.isUserAuthorized(u.Message.From.ID) {
					continue
				}

				// Received authorized message! Create the mesh state
				err := b.createPinnedState(u.Message.Chat.ID)
				if err == nil {
					b.sendMessage(u.Message.Chat.ID, "WinMon successfully bootstrapped on this PC. Added to coordination mesh.", 0)
					go b.runMeshLoop()
					return
				} else {
					b.sendMessage(u.Message.Chat.ID, fmt.Sprintf("Failed to initialize pinned mesh status: %v", err), 0)
				}
			}
			time.Sleep(1 * time.Second)
		}
	}
}

func (b *BotCoordinator) runMeshLoop() {
	pollerTicker := time.NewTicker(5 * time.Second)
	workerTicker := time.NewTicker(2 * time.Second)
	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer pollerTicker.Stop()
	defer workerTicker.Stop()
	defer heartbeatTicker.Stop()

	// Initial registration
	b.sendHeartbeat()

	offset := int64(0)

	for {
		select {
		case <-b.stopChan:
			if offset > 0 {
				_, _, _ = b.getUpdates(offset, 0)
			}
			return
		case <-heartbeatTicker.C:
			b.sendHeartbeat()
		default:
			// 1. Determine role
			role, err := b.checkRole()
			if err != nil {
				log.Printf("Error checking role: %v", err)
				ls := b.getLocalState()
				if ls != nil && (errors.Is(err, ErrNoPinnedState) || errors.Is(err, ErrInvalidPinnedState)) {
					log.Println("Pinned state is missing or invalid. Re-initializing pinned state...")
					if initErr := b.createPinnedState(ls.ChatID); initErr != nil {
						log.Printf("Failed to re-initialize pinned state: %v", initErr)
					} else {
						log.Println("Pinned state successfully re-initialized.")
					}
				}
				time.Sleep(3 * time.Second)
				continue
			}

			if role == "poller" {
				b.isPoller = true
				updates, newOffset, err := b.getUpdates(offset, 15)
				if err != nil {
					log.Printf("Error getting updates: %v", err)
					time.Sleep(3 * time.Second)
					continue
				}
				offset = newOffset

				for _, u := range updates {
					if u.Message == nil {
						continue
					}
					if !b.isUserAuthorized(u.Message.From.ID) {
						continue
					}
					b.handleMessage(u.Message)
				}
			} else {
				b.isPoller = false
				// Worker mode: poll pinned state
				b.pollPinnedState()
				time.Sleep(2 * time.Second)
			}
		}
	}
}

func (b *BotCoordinator) isUserAuthorized(userID int64) bool {
	for _, id := range b.cfg.AllowedUsers {
		if id == userID {
			return true
		}
	}
	return false
}

func (b *BotCoordinator) sendHeartbeat() {
	state, err := b.getPinnedState()
	if err != nil {
		return
	}

	ds, ok := state.Devices[b.cfg.DeviceID]
	if !ok {
		ds = DeviceState{}
	}
	ds.DeviceName = b.cfg.DeviceName
	ds.Group = b.cfg.Group
	ds.Version = b.cfg.Version
	ds.LastSeen = time.Now()
	ds.Status = "online"

	state.Devices[b.cfg.DeviceID] = ds

	// Prune devices unseen for > 24 hours to stay within Telegram's 4096 character limit
	now := time.Now()
	for id, dev := range state.Devices {
		if id != b.cfg.DeviceID && now.Sub(dev.LastSeen) > 24*time.Hour {
			delete(state.Devices, id)
		}
	}

	// If there's no active poller, or the active poller is offline (> 45s), claim it!
	if state.ActivePoller == "" {
		state.ActivePoller = b.cfg.DeviceID
	} else if state.ActivePoller != b.cfg.DeviceID {
		ap, ok := state.Devices[state.ActivePoller]
		if !ok || time.Since(ap.LastSeen) > 45*time.Second {
			state.ActivePoller = b.cfg.DeviceID
		}
	}

	b.updatePinnedState(state)
}

func (b *BotCoordinator) checkRole() (string, error) {
	state, err := b.getPinnedState()
	if err != nil {
		return "worker", err
	}
	if state.ActivePoller == b.cfg.DeviceID {
		return "poller", nil
	}
	return "worker", nil
}

func (b *BotCoordinator) pollPinnedState() {
	state, err := b.getPinnedState()
	if err != nil {
		return
	}

	ls := b.getLocalState()
	if ls == nil {
		return
	}
	chatID := ls.ChatID

	// 1. Check for pending commands targeting us
	if state.PendingCommand != nil && state.PendingCommand.TargetDevice == b.cfg.DeviceID {
		cmd := state.PendingCommand.Command
		args := state.PendingCommand.Args
		fileID := state.PendingCommand.FileID
		fileName := state.PendingCommand.FileName

		// Clear pending command from pinned status so we don't execute it repeatedly
		state.PendingCommand = nil
		b.updatePinnedState(state)

		runBg := func(f func()) {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						errText := fmt.Sprintf("🔴 Execution Panicked: %v", r)
						b.sendMessage(chatID, errText, 0)
					}
				}()
				f()
			}()
		}

		if cmd == "/upload" && fileID != "" {
			doc := &TelegramDocument{FileID: fileID, FileName: fileName}
			dest := strings.Join(args, " ")
			runBg(func() { b.handleDocumentUpload(doc, chatID, 0, dest) })
		} else if cmd == "/updateservice" && fileID != "" {
			doc := &TelegramDocument{FileID: fileID, FileName: fileName}
			runBg(func() { b.handleServiceUpdate(doc, chatID, 0) })
		} else if cmd == "/playsound" && fileID != "" {
			doc := &TelegramDocument{FileID: fileID, FileName: fileName}
			runBg(func() { b.handlePlaySound(doc, chatID, 0) })
		} else if cmd == "/setwallpaper" && fileID != "" {
			doc := &TelegramDocument{FileID: fileID, FileName: fileName}
			runBg(func() { b.handleSetWallpaper(doc, chatID, 0) })
		} else {
			runBg(func() { b.executeCommandLocally(cmd, args, chatID, 0) })
		}
	}

	// 2. Check for broadcast commands we haven't executed yet
	if state.BroadcastCommand != nil && state.BroadcastCommand.Timestamp.After(b.lastBroadcast) {
		b.lastBroadcast = state.BroadcastCommand.Timestamp
		// Run command in background
		cmd := state.BroadcastCommand.Command
		args := state.BroadcastCommand.Args
		go func() {
			defer func() {
				if r := recover(); r != nil {
					errText := fmt.Sprintf("🔴 Broadcast Execution Panicked: %v", r)
					b.sendMessage(chatID, errText, 0)
				}
			}()
			b.executeCommandLocally(cmd, args, chatID, 0)
		}()
	}
}

// Telegram Updates retrieval
type TelegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *TelegramMessage `json:"message"`
}

type TelegramMessage struct {
	MessageID int64             `json:"message_id"`
	From      TelegramUser      `json:"from"`
	Chat      TelegramChat      `json:"chat"`
	Text      string            `json:"text"`
	Caption   string            `json:"caption,omitempty"`
	Document  *TelegramDocument `json:"document"`
}

type TelegramUser struct {
	ID int64 `json:"id"`
}

type TelegramChat struct {
	ID int64 `json:"id"`
}

type TelegramDocument struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
}

func (b *BotCoordinator) getUpdates(offset int64, timeout int) ([]TelegramUpdate, int64, error) {
	type GetUpdatesReq struct {
		Offset  int64 `json:"offset"`
		Timeout int   `json:"timeout"`
	}
	respBytes, err := b.request("getUpdates", GetUpdatesReq{Offset: offset, Timeout: timeout})
	if err != nil {
		return nil, offset, err
	}

	var wrapper struct {
		Ok     bool             `json:"ok"`
		Result []TelegramUpdate `json:"result"`
	}

	if err := json.Unmarshal(respBytes, &wrapper); err != nil {
		return nil, offset, err
	}

	newOffset := offset
	for _, u := range wrapper.Result {
		if u.UpdateID >= newOffset {
			newOffset = u.UpdateID + 1
		}
	}

	return wrapper.Result, newOffset, nil
}

// Command dispatch
func (b *BotCoordinator) handleMessage(msg *TelegramMessage) {

	defer func() {
		if r := recover(); r != nil {
			errText := fmt.Sprintf("🔴 Execution Panicked: %v", r)
			b.sendMessage(msg.Chat.ID, errText, msg.MessageID)
		}
	}()

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = strings.TrimSpace(msg.Caption)
	}
	if text == "" {
		return
	}

	// Split commands and arguments
	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	// Filter commands targeting this or selected PC
	switch cmd {
	case "/devices":
		b.handleDevices(msg.Chat.ID, msg.MessageID)
		return
	case "/select":
		if len(args) < 1 {
			b.sendMessage(msg.Chat.ID, "Usage: `/select <device_id>`", msg.MessageID)
			return
		}
		b.handleSelect(args[0], msg.Chat.ID, msg.MessageID)
		return
	case "/broadcast":
		if len(args) < 1 {
			b.sendMessage(msg.Chat.ID, "Usage: `/broadcast <command>`", msg.MessageID)
			return
		}
		b.handleBroadcast(args[0], args[1:], msg.Chat.ID, msg.MessageID)
		return
	case "/start":
		b.handleStart(msg.Chat.ID, msg.MessageID)
		return
	case "/help", "/h":
		b.handleHelp(msg.Chat.ID, msg.MessageID)
		return
	}

	// For all other commands, route to the currently selected device
	state, err := b.getPinnedState()
	if err != nil {
		b.sendMessage(msg.Chat.ID, fmt.Sprintf("Failed to read selection state: %v", err), msg.MessageID)
		return
	}

	target := state.SelectedDevice
	if target == "" {
		target = b.cfg.DeviceID
	}

	fileID := ""
	fileName := ""
	if msg.Document != nil {
		fileID = msg.Document.FileID
		fileName = msg.Document.FileName
	}

	if target == b.cfg.DeviceID {
		runBg := func(f func()) {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						errText := fmt.Sprintf("🔴 Execution Panicked: %v", r)
						b.sendMessage(msg.Chat.ID, errText, msg.MessageID)
					}
				}()
				f()
			}()
		}

		if cmd == "/upload" {
			if msg.Document == nil {
				b.sendMessage(msg.Chat.ID, "File upload must include an attached document.", msg.MessageID)
				return
			}
			if len(args) < 1 {
				b.sendMessage(msg.Chat.ID, "Usage: `/upload <destination>`", msg.MessageID)
				return
			}
			dest := strings.Join(args, " ")
			runBg(func() { b.handleDocumentUpload(msg.Document, msg.Chat.ID, msg.MessageID, dest) })
		} else if cmd == "/updateservice" {
			if msg.Document == nil {
				b.sendMessage(msg.Chat.ID, "Update service must include an attached executable.", msg.MessageID)
				return
			}
			runBg(func() { b.handleServiceUpdate(msg.Document, msg.Chat.ID, msg.MessageID) })
		} else if cmd == "/playsound" {
			if msg.Document == nil {
				b.sendMessage(msg.Chat.ID, "Playsound must include an attached audio file.", msg.MessageID)
				return
			}
			runBg(func() { b.handlePlaySound(msg.Document, msg.Chat.ID, msg.MessageID) })
		} else if cmd == "/setwallpaper" {
			if msg.Document == nil {
				b.sendMessage(msg.Chat.ID, "Setwallpaper must include an attached image file.", msg.MessageID)
				return
			}
			runBg(func() { b.handleSetWallpaper(msg.Document, msg.Chat.ID, msg.MessageID) })
		} else {
			runBg(func() { b.executeCommandLocally(cmd, args, msg.Chat.ID, msg.MessageID) })
		}
	} else {
		// Route command to target worker using pinned message
		state.PendingCommand = &CommandState{
			Command:      cmd,
			TargetDevice: target,
			Args:         args,
			Timestamp:    time.Now(),
			FileID:       fileID,
			FileName:     fileName,
		}
		b.updatePinnedState(state)
	}
}

// Commands Implementation
func (b *BotCoordinator) handleStart(chatID int64, msgID int64) {
	pcName, _ := os.Hostname()
	ip := device.GetIPAddresses()
	osName, _ := device.GetOSVersion()
	curTime := time.Now().Format("2006-01-02 15:04:05")

	svcStatus := "Running (Console Mode)"
	if service.IsRunningAsService() {
		svcStatus = "Running (Windows Service)"
	}

	text := fmt.Sprintf("💻 *WinMon Client Status*\n\n"+
		"*PC Name:* %s\n"+
		"*Local IP:* %s\n"+
		"*OS:* %s\n"+
		"*Time:* %s\n"+
		"*Service Status:* %s\n\n"+
		"Use `/help` or `/h` to list available commands.",
		pcName, ip, osName, curTime, svcStatus)

	b.sendMessage(chatID, text, msgID)
}

func (b *BotCoordinator) handleHelp(chatID int64, msgID int64) {
	helpText := `📖 *WinMon Help Guide*

⚙️ *Mesh Control*
• /devices - List registered devices
• /select <device> - Set active device
• /deviceinfo - Show selected PC info
• /broadcast <cmd> - Run command on all

🖥️ *General & System*
• /start - Diagnostic system overview
• /status - Uptime, CPU, RAM, Disk, Battery
• /version - System, Bot, and Go versions
• /processes [filter] - List top running processes
• /kill <process> - Kill running process
• /cmd <cmd> - Execute shell command

📂 *File Management*
• /download <path> - Download file or ZIP folder
• /upload <dest> - Upload attached Telegram file

📸 *Media & Capture*
• /screenshot - Grab screen capture
• /webcam - Grab webcam capture
• /screenrecord <duration> - Record GIF screen (e.g. 5s)
• /listen <duration> - Record WAV audio (e.g. 10s)

🔊 *Audio Controls*
• /tts <text> - Speak message aloud
• /playsound - Play attached WAV/MP3 sound
• /setvol <0-100> - Set speaker volume
• /maxvol | /minvol - Max/Min volume
• /mute | /unmute - Mute/Unmute audio

⌨️ *Input Automation*
• /type <text> - Type string simulation
• /keypress <key> - Press key (e.g. enter, space)
• /hotkey <keys> - Trigger combo (e.g. ctrl+c, win+r)
• /mouse move <x> <y> - Move cursor
• /mouse click | rightclick | doubleclick - Mouse clicks
• /mouse scroll <amount> - Scroll mouse wheel

🖥️ *Display*
• /brightness <0-100> - Adjust monitor brightness
• /monitoroff - Turn off display monitor
• /wallpaper - Fetch current desktop wallpaper
• /setwallpaper - Set desktop wallpaper (attach image)

🛠️ *Service Controls*
• /shutdownservice - Shutdown WinMon service
• /restartservice - Restart WinMon service
• /updateservice - Update bot binary (attach exe)
• /implode - Completely remove WinMon service and files`

	b.sendMessage(chatID, helpText, msgID)
}

func (b *BotCoordinator) handleDevices(chatID int64, msgID int64) {
	state, err := b.getPinnedState()
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to load device mesh: %v", err), msgID)
		return
	}

	var sb strings.Builder
	sb.WriteString("🖥️ *WinMon Mesh Devices*\n\n")

	for id, dev := range state.Devices {
		statusStr := "🟢"
		// If last seen > 45s, mark offline
		if time.Since(dev.LastSeen) > 45*time.Second {
			statusStr = "🔴"
		}

		selectedIndicator := ""
		if state.SelectedDevice == id {
			selectedIndicator = " ⭐"
		}

		sb.WriteString(fmt.Sprintf("%s *%s*%s\nID: `%s` | Group: `%s` | V: `%s`\n\n",
			statusStr, dev.DeviceName, selectedIndicator, id, dev.Group, dev.Version))
	}

	b.sendMessage(chatID, sb.String(), msgID)
}

func (b *BotCoordinator) handleSelect(deviceID string, chatID int64, msgID int64) {
	state, err := b.getPinnedState()
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Error reading mesh: %v", err), msgID)
		return
	}

	// Verify device exists
	_, exists := state.Devices[deviceID]
	if !exists {
		b.sendMessage(chatID, fmt.Sprintf("Device ID `%s` not found in mesh.", deviceID), msgID)
		return
	}

	state.SelectedDevice = deviceID

	// If the selected device is active online, we can make it the active poller
	dev, ok := state.Devices[deviceID]
	if ok && time.Since(dev.LastSeen) <= 45*time.Second {
		state.ActivePoller = deviceID
	}

	err = b.updatePinnedState(state)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to update selection: %v", err), msgID)
		return
	}

	b.sendMessage(chatID, fmt.Sprintf("⭐ Selected Device: *%s* (`%s`)", dev.DeviceName, deviceID), msgID)
}

func (b *BotCoordinator) handleBroadcast(command string, args []string, chatID int64, msgID int64) {
	state, err := b.getPinnedState()
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Error writing broadcast state: %v", err), msgID)
		return
	}

	state.BroadcastCommand = &CommandState{
		Command:   command,
		Args:      args,
		Timestamp: time.Now(),
	}

	b.updatePinnedState(state)
	b.sendMessage(chatID, fmt.Sprintf("📢 Broadcasted command `/ %s` to all online mesh clients.", command), msgID)
}

func (b *BotCoordinator) executeCommandLocally(cmd string, args []string, chatID int64, msgID int64) {
	start := time.Now()

	// Continuous typing indicator loop
	action := "typing"
	switch cmd {
	case "/download", "/screenrecord", "/listen":
		action = "upload_document"
	case "/screenshot", "/webcam":
		action = "upload_photo"
	}
	cancelTyping := b.startTypingIndicator(chatID, action)
	defer cancelTyping()

	// Check if this command is interactive (must run in user session helper if running as service)
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
	}

	if _, ok := interactiveCmds[cmd]; ok {
		isInteractive = true
	}

	if isInteractive && service.IsRunningAsService() {
		// Launch helper inside user session
		// Flatten args
		flatArgs := strings.Join(args, " ")
		helperArgs := fmt.Sprintf("-session-helper -cmd %s -args \"%s\"", cmd, strings.ReplaceAll(flatArgs, "\"", "\\\""))

		err := service.RunInUserSession(helperArgs, 60*time.Second)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("🔴 Session Helper Error: %v", err), msgID)
			return
		}

		// Read output if any (session helper writes output to temp files)
		b.handleHelperOutput(cmd, chatID, msgID, start)
		return
	}

	// Execute natively (either console mode, or command is non-interactive)
	b.executeNative(cmd, args, chatID, msgID, start)
}

func (b *BotCoordinator) executeNative(cmd string, args []string, chatID int64, msgID int64, start time.Time) {
	switch cmd {
	case "/deviceinfo":
		info, err := device.GetDeviceInfo(b.cfg.DeviceName, b.cfg.DeviceID, b.cfg.Version)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Error: %v", err), msgID)
			return
		}
		text := fmt.Sprintf("ℹ️ *Device Information*\n\n"+
			"*Device Name:* %s\n"+
			"*Device ID:* %s\n"+
			"*PC Name:* %s\n"+
			"*IP Address:* %s\n"+
			"*Version:* %s\n"+
			"*OS:* %s\n"+
			"*Uptime:* %s\n",
			info.DeviceName, info.DeviceID, info.PCName, info.IPAddress, info.Version, info.OS, device.FormatDuration(info.Uptime))
		b.sendMessage(chatID, text, msgID)

	case "/status":
		status, err := device.GetStatus()
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Error: %v", err), msgID)
			return
		}
		svcStatus := "Console"
		if service.IsRunningAsService() {
			svcStatus = "Service"
		}
		text := fmt.Sprintf("📊 *System Status* (%s)\n\n"+
			"*CPU Usage:* %.1f%%\n"+
			"*RAM Usage:* %.1f%% (%.1f GB / %.1f GB)\n"+
			"*Disk Usage:* %.1f%% (%.1f GB Free / %.1f GB Total)\n"+
			"*Battery:* %d%% (%s)\n"+
			"*Uptime:* %s\n",
			svcStatus, status.CPUPercent, status.RAMPercent, status.RAMTotalGB-status.RAMFreeGB, status.RAMTotalGB,
			status.DiskPercent, status.DiskFreeGB, status.DiskTotalGB, status.BatteryCharge, status.BatteryStatus,
			device.FormatDuration(status.Uptime))
		b.sendMessage(chatID, text, msgID)

	case "/version":
		pcVer, _ := device.GetOSVersion()
		text := fmt.Sprintf("🤖 *Version Diagnostics*\n\n"+
			"*TaskBot Version:* %s\n"+
			"*Go Version:* %s\n"+
			"*Windows Version:* %s\n",
			b.cfg.Version, "go1.20", pcVer)
		b.sendMessage(chatID, text, msgID)

	case "/processes":
		filter := ""
		if len(args) > 0 {
			filter = args[0]
		}
		b.handleProcesses(filter, chatID, msgID)

	case "/kill":
		if len(args) < 1 {
			b.sendMessage(chatID, "Usage: `/kill <process_name_or_pid>`", msgID)
			return
		}
		b.handleKill(args[0], chatID, msgID)

	case "/cmd":
		if len(args) < 1 {
			b.sendMessage(chatID, "Usage: `/cmd <command>`", msgID)
			return
		}
		cmdLine := strings.Join(args, " ")
		b.sendChatAction(chatID, "typing")
		out, err := shell.ExecuteCommand(cmdLine, time.Duration(b.cfg.CommandTimeoutSeconds)*time.Second)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("🔴 Execution Error: %v\n\n```\n%s\n```", err, out), msgID)
		} else {
			b.sendMessage(chatID, fmt.Sprintf("🟢 Execution Success:\n\n```\n%s\n```", out), msgID)
		}

	case "/download":
		if len(args) < 1 {
			b.sendMessage(chatID, "Usage: `/download <path>`", msgID)
			return
		}
		b.handleDownload(strings.Join(args, " "), chatID, msgID)

	case "/upload":
		b.sendMessage(chatID, "File upload must include an attached document.", msgID)

	// Audio commands (non-interactive side of mute/vol)
	case "/setvol":
		if len(args) < 1 {
			b.sendMessage(chatID, "Usage: `/setvol <0-100>`", msgID)
			return
		}
		vol, err := strconv.Atoi(args[0])
		if err != nil {
			b.sendMessage(chatID, "Volume must be an integer.", msgID)
			return
		}
		audio.SetVolume(vol)
		b.sendMessage(chatID, fmt.Sprintf("🔊 Volume set to %d%%", vol), msgID)

	case "/maxvol":
		audio.SetVolume(100)
		b.sendMessage(chatID, "🔊 Volume set to 100%", msgID)

	case "/minvol":
		audio.SetVolume(0)
		b.sendMessage(chatID, "🔇 Volume set to 0%", msgID)

	case "/mute":
		audio.SetMute(true)
		b.sendMessage(chatID, "🔇 System audio muted.", msgID)

	case "/unmute":
		audio.SetMute(false)
		b.sendMessage(chatID, "🔊 System audio unmuted.", msgID)

	case "/brightness":
		if len(args) < 1 {
			b.sendMessage(chatID, "Usage: `/brightness <0-100>`", msgID)
			return
		}
		bri, err := strconv.Atoi(args[0])
		if err != nil {
			b.sendMessage(chatID, "Brightness must be an integer.", msgID)
			return
		}
		display.SetBrightness(bri)
		b.sendMessage(chatID, fmt.Sprintf("🔆 Brightness adjusted to %d%%", bri), msgID)

	case "/monitoroff":
		display.TurnMonitorOff()
		b.sendMessage(chatID, "🖥️ Monitor turned off.", msgID)

	case "/alert":
		if len(args) < 1 {
			b.sendMessage(chatID, "Usage: `/alert <message>`", msgID)
			return
		}
		msg := strings.Join(args, " ")
		go func() {
			notifications.ShowAlert("WinMon Notice", msg)
		}()
		b.sendMessage(chatID, "🔔 Alert dispatched to target desktop.", msgID)

	case "/shutdownservice":
		b.sendMessage(chatID, "🛑 Shutting down WinMon service on this PC...", msgID)
		time.Sleep(1 * time.Second)
		close(b.stopChan)

	case "/implode":
		if len(args) == 0 || strings.ToLower(args[0]) != "confirm" {
			b.sendMessage(chatID, "⚠️ *WARNING*: This command will completely uninstall the WinMon service and delete all local config, state, and executable files from this PC.\n\nThis action is *IRREVERSIBLE*.\n\nTo proceed, please type:\n`/implode confirm`", msgID)
			return
		}
		b.sendMessage(chatID, "💥 Initiating self-destruction. WinMon service and local files will be completely removed...", msgID)
		err := updater.ImplodeService(b.cfg.BotToken, chatID)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("🔴 Implode failed: %v", err), msgID)
			return
		}
		time.Sleep(500 * time.Millisecond)
		close(b.stopChan)

	case "/restartservice":
		b.sendMessage(chatID, "🔄 Restarting WinMon service...", msgID)
		go func() {
			time.Sleep(1 * time.Second)
			exec.Command("powershell", "-Command", "Restart-Service -Name WinMon -Force").Run()
		}()

	// Fallback/interactive commands executed in console mode directly
	case "/screenshot":
		tempPath := filepath.Join(service.GetSharedTempDir(), "screenshot.jpg")
		err := media.CaptureScreen(tempPath)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Failed to capture screenshot: %v", err), msgID)
			return
		}
		defer os.Remove(tempPath)
		b.sendChatAction(chatID, "upload_photo")
		b.sendFile(chatID, "sendPhoto", "photo", tempPath, msgID)

	case "/webcam":
		tempPath := filepath.Join(service.GetSharedTempDir(), "webcam.jpg")
		err := media.CaptureWebcam(tempPath)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Failed to capture webcam: %v", err), msgID)
			return
		}
		defer os.Remove(tempPath)
		b.sendChatAction(chatID, "upload_photo")
		b.sendFile(chatID, "sendPhoto", "photo", tempPath, msgID)

	case "/screenrecord":
		dur := parseDuration(strings.Join(args, " "))
		tempPath := filepath.Join(service.GetSharedTempDir(), "record.gif")
		b.sendMessage(chatID, fmt.Sprintf("📹 Recording screen for %s...", dur), msgID)
		err := media.RecordScreen(dur, tempPath)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Recording failed: %v", err), msgID)
			return
		}
		defer os.Remove(tempPath)
		b.sendFile(chatID, "sendDocument", "document", tempPath, msgID)

	case "/listen":
		dur := parseDuration(strings.Join(args, " "))
		tempPath := filepath.Join(service.GetSharedTempDir(), "audio.wav")
		b.sendMessage(chatID, fmt.Sprintf("🎙️ Recording audio for %s...", dur), msgID)
		err := media.RecordAudio(dur, tempPath)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Audio recording failed: %v", err), msgID)
			return
		}
		defer os.Remove(tempPath)
		b.sendFile(chatID, "sendDocument", "document", tempPath, msgID)

	case "/tts":
		if len(args) < 1 {
			b.sendMessage(chatID, "Usage: `/tts <text>`", msgID)
			return
		}
		audio.SpeakTTS(strings.Join(args, " "))
		b.sendMessage(chatID, "🗣️ Speaking message through target default audio device.", msgID)

	case "/playsound":
		b.sendMessage(chatID, "Playsound must be initiated by attaching a sound file.", msgID)

	case "/wallpaper":
		path, err := display.GetWallpaperPath()
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Failed to get wallpaper: %v", err), msgID)
			return
		}
		if _, err := os.Stat(path); err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Current wallpaper path: `%s` (file not found or inaccessible)", path), msgID)
			return
		}
		b.sendChatAction(chatID, "upload_photo")
		b.sendFile(chatID, "sendPhoto", "photo", path, msgID)

	case "/clipboard":
		txt, err := clipboard.GetClipboardLocal()
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Failed to read clipboard: %v", err), msgID)
			return
		}
		b.sendMessage(chatID, fmt.Sprintf("📋 Clipboard Content:\n```\n%s\n```", txt), msgID)

	case "/setclipboard":
		if len(args) < 1 {
			b.sendMessage(chatID, "Usage: `/setclipboard <text>`", msgID)
			return
		}
		clipboard.SetClipboardLocal(strings.Join(args, " "))
		b.sendMessage(chatID, "📋 Clipboard set.", msgID)

	case "/setwallpaper":
		if len(args) < 1 {
			b.sendMessage(chatID, "Usage: `/setwallpaper <path>` (or attach an image to command)", msgID)
			return
		}
		path := strings.Join(args, " ")
		err := display.SetWallpaperLocal(path)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Failed to set wallpaper: %v", err), msgID)
			return
		}
		b.sendMessage(chatID, "🖼️ Wallpaper set successfully.", msgID)

	case "/notify":
		if len(args) < 1 {
			b.sendMessage(chatID, "Usage: `/notify <message>` or `/notify <title>|<message>`", msgID)
			return
		}
		fullArgs := strings.Join(args, " ")
		parts := strings.Split(fullArgs, "|")
		title := "WinMon Notification"
		msg := fullArgs
		if len(parts) > 1 {
			title = parts[0]
			msg = parts[1]
		}
		err := notifications.ShowToastLocal(title, msg)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Failed to show notification: %v", err), msgID)
			return
		}
		b.sendMessage(chatID, "🔔 Notification shown.", msgID)

	case "/type":
		if len(args) < 1 {
			b.sendMessage(chatID, "Usage: `/type <text>`", msgID)
			return
		}
		inputText := strings.Join(args, " ")
		inputText = strings.ReplaceAll(inputText, "\\\"", "\"")
		input.TypeText(inputText)
		b.sendMessage(chatID, "⌨️ Text typed.", msgID)

	case "/keypress":
		if len(args) < 1 {
			b.sendMessage(chatID, "Usage: `/keypress <key>`", msgID)
			return
		}
		key := strings.Join(args, " ")
		err := input.PressKey(key)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Failed to press key: %v", err), msgID)
			return
		}
		b.sendMessage(chatID, "⌨️ Key pressed.", msgID)

	case "/hotkey":
		if len(args) < 1 {
			b.sendMessage(chatID, "Usage: `/hotkey <keys>` (e.g. ctrl+alt+delete)", msgID)
			return
		}
		hotkey := strings.Join(args, " ")
		err := input.TriggerHotkey(hotkey)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Failed to trigger hotkey: %v", err), msgID)
			return
		}
		b.sendMessage(chatID, "⌨️ Hotkey triggered.", msgID)

	case "/mouse":
		if len(args) < 1 {
			b.sendMessage(chatID, "Usage: `/mouse <action>` (e.g. click, move 100 200)", msgID)
			return
		}
		fullArgs := strings.Join(args, " ")
		fields := strings.Fields(fullArgs)
		if len(fields) < 1 {
			b.sendMessage(chatID, "Invalid mouse action.", msgID)
			return
		}
		action := fields[0]
		var err error
		switch action {
		case "move":
			if len(fields) < 3 {
				b.sendMessage(chatID, "Usage: `/mouse move <x> <y>`", msgID)
				return
			}
			x, _ := strconv.Atoi(fields[1])
			y, _ := strconv.Atoi(fields[2])
			err = input.MoveMouse(x, y)
		case "click":
			input.ClickMouse()
		case "rightclick":
			input.RightClickMouse()
		case "doubleclick":
			input.DoubleClickMouse()
		case "scroll":
			if len(fields) < 2 {
				b.sendMessage(chatID, "Usage: `/mouse scroll <amount>`", msgID)
				return
			}
			amount, _ := strconv.Atoi(fields[1])
			input.ScrollMouse(amount)
		default:
			b.sendMessage(chatID, fmt.Sprintf("Unknown mouse action: %s", action), msgID)
			return
		}
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Mouse action failed: %v", err), msgID)
			return
		}
		b.sendMessage(chatID, "🖱️ Mouse action executed.", msgID)

	default:
		b.sendMessage(chatID, "❓ Unknown command. Use `/help` to see list of options.", msgID)
		return
	}

	b.sendExecutionTime(chatID, msgID, start)
}

func (b *BotCoordinator) handleProcesses(filter string, chatID int64, msgID int64) {
	psCmd := "Get-Process | Sort-Object CPU -Descending | Select-Object -First 15 ProcessName, Id, CPU | ConvertTo-Json"
	if filter != "" {
		psCmd = fmt.Sprintf("Get-Process -Name *%s* | Sort-Object CPU -Descending | Select-Object -First 15 ProcessName, Id, CPU | ConvertTo-Json", filter)
	}

	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := c.Output()
	if err != nil {
		b.sendMessage(chatID, "No matching processes found or failed to query.", msgID)
		return
	}

	var sb strings.Builder
	sb.WriteString("📁 *Process Registry*\n\n```\n%-20s %-8s %-6s\n" + strings.Repeat("-", 36) + "\n")

	// Parse JSON
	type ProcessInfo struct {
		ProcessName string  `json:"ProcessName"`
		ID          int     `json:"Id"`
		CPU         float64 `json:"CPU"`
	}

	// Try single object first
	var single ProcessInfo
	if err := json.Unmarshal(out, &single); err == nil {
		sb.WriteString(fmt.Sprintf("%-20s %-8d %-6.1f\n", single.ProcessName, single.ID, single.CPU))
	} else {
		var list []ProcessInfo
		if err := json.Unmarshal(out, &list); err == nil {
			for _, p := range list {
				sb.WriteString(fmt.Sprintf("%-20s %-8d %-6.1f\n", p.ProcessName, p.ID, p.CPU))
			}
		}
	}
	sb.WriteString("```")
	b.sendMessage(chatID, fmt.Sprintf(sb.String(), "Process Name", "PID", "CPU"), msgID)
}

func (b *BotCoordinator) handleKill(target string, chatID int64, msgID int64) {
	var cmd *exec.Cmd
	if pid, err := strconv.Atoi(target); err == nil {
		cmd = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid))
	} else {
		// Append extension if missing
		if !strings.HasSuffix(strings.ToLower(target), ".exe") {
			target += ".exe"
		}
		cmd = exec.Command("taskkill", "/F", "/IM", target)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("🔴 Failed to terminate process `%s`.\n\nError: %v\n```\n%s\n```", target, err, strings.TrimSpace(string(out))), msgID)
	} else {
		b.sendMessage(chatID, fmt.Sprintf("🟢 Successfully terminated process `%s`.", target), msgID)
	}
}

func (b *BotCoordinator) handleDownload(path string, chatID int64, msgID int64) {
	info, err := os.Stat(path)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("File path not found: %v", err), msgID)
		return
	}

	b.sendChatAction(chatID, "upload_document")

	if info.IsDir() {
		// Zip it
		b.sendMessage(chatID, "🗜️ Target is a folder. Compressing to temporary ZIP...", msgID)
		zipPath := filepath.Join(service.GetSharedTempDir(), info.Name()+".zip")
		err := files.ZipDirectory(path, zipPath)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Compression failed: %v", err), msgID)
			return
		}
		defer os.Remove(zipPath)

		_, err = b.sendFile(chatID, "sendDocument", "document", zipPath, msgID)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Failed to send ZIP file: %v", err), msgID)
		}
	} else {
		_, err = b.sendFile(chatID, "sendDocument", "document", path, msgID)
		if err != nil {
			b.sendMessage(chatID, fmt.Sprintf("Failed to send file: %v", err), msgID)
		}
	}
}

func (b *BotCoordinator) handleHelperOutput(cmd string, chatID int64, msgID int64, start time.Time) {
	// Locate temporary helper outputs based on command type
	switch cmd {
	case "/screenshot":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_screenshot.jpg")
		if _, err := os.Stat(tempPath); err == nil {
			b.sendFile(chatID, "sendPhoto", "photo", tempPath, msgID)
			os.Remove(tempPath)
		} else {
			b.sendMessage(chatID, "Failed to retrieve screenshot from session helper.", msgID)
		}
	case "/webcam":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_webcam.jpg")
		if _, err := os.Stat(tempPath); err == nil {
			b.sendFile(chatID, "sendPhoto", "photo", tempPath, msgID)
			os.Remove(tempPath)
		} else {
			b.sendMessage(chatID, "Failed to retrieve webcam capture from session helper.", msgID)
		}
	case "/screenrecord":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_record.gif")
		if _, err := os.Stat(tempPath); err == nil {
			b.sendFile(chatID, "sendDocument", "document", tempPath, msgID)
			os.Remove(tempPath)
		} else {
			b.sendMessage(chatID, "Failed to retrieve recording from session helper.", msgID)
		}
	case "/listen":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_audio.wav")
		if _, err := os.Stat(tempPath); err == nil {
			b.sendFile(chatID, "sendDocument", "document", tempPath, msgID)
			os.Remove(tempPath)
		} else {
			b.sendMessage(chatID, "Failed to retrieve audio from session helper.", msgID)
		}
	case "/clipboard":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_clipboard.txt")
		if data, err := os.ReadFile(tempPath); err == nil {
			b.sendMessage(chatID, fmt.Sprintf("📋 Clipboard Content:\n```\n%s\n```", string(data)), msgID)
			os.Remove(tempPath)
		} else {
			b.sendMessage(chatID, "Failed to read clipboard from session helper.", msgID)
		}
	case "/wallpaper":
		tempPath := filepath.Join(service.GetSharedTempDir(), "helper_wallpaper.jpg")
		if _, err := os.Stat(tempPath); err == nil {
			b.sendFile(chatID, "sendPhoto", "photo", tempPath, msgID)
			os.Remove(tempPath)
		} else {
			// Read path directly if it is a system default
			path, err := display.GetWallpaperPath()
			if err == nil && path != "" {
				b.sendMessage(chatID, fmt.Sprintf("Wallpaper is a desktop background solid color or defaults to path: `%s`", path), msgID)
			} else {
				b.sendMessage(chatID, "Failed to capture wallpaper from session helper.", msgID)
			}
		}
	default:
		// Commands like type, setvol, mute, toast etc don't return files, they just succeed.
		b.sendMessage(chatID, "🟢 Command completed successfully in user session.", msgID)
	}

	b.sendExecutionTime(chatID, msgID, start)
}

func (b *BotCoordinator) sendExecutionTime(chatID int64, msgID int64, start time.Time) {
	duration := time.Since(start)
	durationMs := duration.Milliseconds()
	completionText := fmt.Sprintf("\n\nCompleted successfully.\n\nExecution Time:\n%d ms", durationMs)

	b.lastMsgsMu.Lock()
	state := b.lastMsgs[msgID]
	delete(b.lastMsgs, msgID)
	b.lastMsgsMu.Unlock()

	lastID := int64(0)
	lastText := ""
	lastIsMedia := false
	if state != nil {
		lastID = state.ID
		lastText = state.Text
		lastIsMedia = state.IsMedia
	}

	if lastID != 0 {
		if lastIsMedia {
			type EditMsgCaptionReq struct {
				ChatID    int64  `json:"chat_id"`
				MessageID int64  `json:"message_id"`
				Caption   string `json:"caption"`
				ParseMode string `json:"parse_mode"`
			}
			_, err := b.request("editMessageCaption", EditMsgCaptionReq{
				ChatID:    chatID,
				MessageID: lastID,
				Caption:   strings.TrimSpace(completionText),
				ParseMode: "Markdown",
			})
			if err == nil {
				return
			}
		} else {
			type EditMsgReq struct {
				ChatID    int64  `json:"chat_id"`
				MessageID int64  `json:"message_id"`
				Text      string `json:"text"`
				ParseMode string `json:"parse_mode"`
			}
			_, err := b.request("editMessageText", EditMsgReq{
				ChatID:    chatID,
				MessageID: lastID,
				Text:      lastText + completionText,
				ParseMode: "Markdown",
			})
			if err == nil {
				return
			}
		}
	}

	b.sendMessage(chatID, fmt.Sprintf("Completed successfully.\n\nExecution Time:\n%d ms", durationMs), msgID)
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
		// Copy file to tempPath
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
		inputText := args
		// Handle escaped quotes
		inputText = strings.ReplaceAll(inputText, "\\\"", "\"")
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
	}

	return fmt.Errorf("unsupported helper command: %s", cmd)
}

func (b *BotCoordinator) handleDocumentUpload(doc *TelegramDocument, chatID int64, msgID int64, destination string) {
	// Downloads attached document from Telegram and saves it to target destination path
	// File paths must be validated and directories automatically created.
	// Returns confirmation saved path
	type GetFileReq struct {
		FileID string `json:"file_id"`
	}
	respBytes, err := b.request("getFile", GetFileReq{FileID: doc.FileID})
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to query upload file: %v", err), msgID)
		return
	}

	var wrapper struct {
		Ok     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBytes, &wrapper); err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to parse file info: %v", err), msgID)
		return
	}

	// Download URL: https://api.telegram.org/file/bot<token>/<file_path>
	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", b.cfg.BotToken, wrapper.Result.FilePath)
	resp, err := b.client.Get(downloadURL)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Download failed: %v", err), msgID)
		return
	}
	defer resp.Body.Close()

	// Prepare final save path
	finalPath, err := files.PrepareUploadPath(destination, doc.FileName)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Invalid destination structure: %v", err), msgID)
		return
	}

	out, err := os.Create(finalPath)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to create file locally: %v", err), msgID)
		return
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to write file locally: %v", err), msgID)
		return
	}

	b.sendMessage(chatID, fmt.Sprintf("🟢 Document successfully saved locally:\n`%s`", finalPath), msgID)
}

func (b *BotCoordinator) handleServiceUpdate(doc *TelegramDocument, chatID int64, msgID int64) {
	b.sendMessage(chatID, "📥 Downloading new update binary...", msgID)

	type GetFileReq struct {
		FileID string `json:"file_id"`
	}
	respBytes, err := b.request("getFile", GetFileReq{FileID: doc.FileID})
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to query update binary: %v", err), msgID)
		return
	}

	var wrapper struct {
		Ok     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBytes, &wrapper); err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to parse file info: %v", err), msgID)
		return
	}

	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", b.cfg.BotToken, wrapper.Result.FilePath)
	resp, err := b.client.Get(downloadURL)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Download failed: %v", err), msgID)
		return
	}
	defer resp.Body.Close()

	tempPath := filepath.Join(service.GetSharedTempDir(), "winmon_new.exe")
	out, err := os.Create(tempPath)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to create temporary update file: %v", err), msgID)
		return
	}

	_, err = io.Copy(out, resp.Body)
	out.Close() // Close immediately to release file lock on Windows
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to write update binary: %v", err), msgID)
		os.Remove(tempPath)
		return
	}

	// Validate executable
	err = updater.ValidateBinary(tempPath)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("🔴 Validation Failed: %v", err), msgID)
		os.Remove(tempPath)
		return
	}

	b.sendMessage(chatID, "🔄 Validation successful. Restarting WinMon service to apply update...", msgID)

	// Launch update script
	err = updater.UpdateService(tempPath, b.cfg.BotToken, chatID)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("🔴 Update launch failed: %v", err), msgID)
		os.Remove(tempPath)
		return
	}

	// Exit service process so that the script can copy the file
	time.Sleep(500 * time.Millisecond)
	close(b.stopChan)
}

func parseDuration(s string) time.Duration {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 5 * time.Second
	}
	// If it's a number only, default to seconds
	if val, err := strconv.Atoi(s); err == nil {
		return time.Duration(val) * time.Second
	}
	// Check for seconds
	if strings.HasSuffix(s, "seconds") || strings.HasSuffix(s, "second") || strings.HasSuffix(s, "s") {
		numStr := s
		numStr = strings.TrimSuffix(numStr, "seconds")
		numStr = strings.TrimSuffix(numStr, "second")
		numStr = strings.TrimSuffix(numStr, "s")
		numStr = strings.TrimSpace(numStr)
		if val, err := strconv.Atoi(numStr); err == nil {
			return time.Duration(val) * time.Second
		}
	}
	// Check for minutes
	if strings.HasSuffix(s, "minutes") || strings.HasSuffix(s, "minute") || strings.HasSuffix(s, "m") {
		numStr := s
		numStr = strings.TrimSuffix(numStr, "minutes")
		numStr = strings.TrimSuffix(numStr, "minute")
		numStr = strings.TrimSuffix(numStr, "m")
		numStr = strings.TrimSpace(numStr)
		if val, err := strconv.Atoi(numStr); err == nil {
			return time.Duration(val) * time.Minute
		}
	}
	// Fallback to time.ParseDuration
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return 5 * time.Second
}

func (b *BotCoordinator) startTypingIndicator(chatID int64, action string) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		b.sendChatAction(chatID, action)
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.sendChatAction(chatID, action)
			}
		}
	}()
	return cancel
}

func (b *BotCoordinator) handlePlaySound(doc *TelegramDocument, chatID int64, msgID int64) {
	start := time.Now()
	b.sendMessage(chatID, "📥 Downloading audio file...", msgID)

	type GetFileReq struct {
		FileID string `json:"file_id"`
	}
	respBytes, err := b.request("getFile", GetFileReq{FileID: doc.FileID})
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to query audio file: %v", err), msgID)
		return
	}

	var wrapper struct {
		Ok     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBytes, &wrapper); err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to parse file info: %v", err), msgID)
		return
	}

	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", b.cfg.BotToken, wrapper.Result.FilePath)
	resp, err := b.client.Get(downloadURL)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Download failed: %v", err), msgID)
		return
	}
	defer resp.Body.Close()

	tempPath := filepath.Join(service.GetSharedTempDir(), "winmon_sound"+filepath.Ext(doc.FileName))
	out, err := os.Create(tempPath)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to create temporary audio file: %v", err), msgID)
		return
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to write audio file: %v", err), msgID)
		return
	}

	b.sendMessage(chatID, "🎵 Playing sound on target PC...", msgID)

	if service.IsRunningAsService() {
		helperArgs := fmt.Sprintf("-session-helper -cmd /playsound -args \"%s\"", strings.ReplaceAll(tempPath, "\"", "\\\""))
		err = service.RunInUserSession(helperArgs, 60*time.Second)
	} else {
		err = audio.PlaySoundLocal(tempPath)
	}

	os.Remove(tempPath)

	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("🔴 Failed to play audio: %v", err), msgID)
	} else {
		b.sendMessage(chatID, "🟢 Audio played successfully.", msgID)
	}
	b.sendExecutionTime(chatID, msgID, start)
}

func (b *BotCoordinator) handleSetWallpaper(doc *TelegramDocument, chatID int64, msgID int64) {
	start := time.Now()
	b.sendMessage(chatID, "📥 Downloading wallpaper image...", msgID)

	type GetFileReq struct {
		FileID string `json:"file_id"`
	}
	respBytes, err := b.request("getFile", GetFileReq{FileID: doc.FileID})
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to query image file: %v", err), msgID)
		return
	}

	var wrapper struct {
		Ok     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBytes, &wrapper); err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to parse file info: %v", err), msgID)
		return
	}

	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", b.cfg.BotToken, wrapper.Result.FilePath)
	resp, err := b.client.Get(downloadURL)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Download failed: %v", err), msgID)
		return
	}
	defer resp.Body.Close()

	tempPath := filepath.Join(service.GetSharedTempDir(), "winmon_wallpaper"+filepath.Ext(doc.FileName))
	out, err := os.Create(tempPath)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to create temporary image file: %v", err), msgID)
		return
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("Failed to write image file: %v", err), msgID)
		return
	}

	b.sendMessage(chatID, "🖼️ Setting desktop wallpaper on target PC...", msgID)

	if service.IsRunningAsService() {
		helperArgs := fmt.Sprintf("-session-helper -cmd /setwallpaper -args \"%s\"", strings.ReplaceAll(tempPath, "\"", "\\\""))
		err = service.RunInUserSession(helperArgs, 60*time.Second)
	} else {
		err = display.SetWallpaperLocal(tempPath)
	}

	os.Remove(tempPath)

	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("🔴 Failed to set wallpaper: %v", err), msgID)
	} else {
		b.sendMessage(chatID, "🟢 Wallpaper updated successfully.", msgID)
	}
	b.sendExecutionTime(chatID, msgID, start)
}
