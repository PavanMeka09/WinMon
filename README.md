# WinMon

WinMon is a secure Windows PC Remote Management tool that runs as a **Telegram Bot**. It can be run either as a standard console application or installed as a persistent Windows Service (running in Session 0 with a persistent Named Pipe IPC User Agent in Session 1).

---

## ⚡ Features

- **Interactive Inline Keyboard Dashboard:** Touch-friendly buttons for instant Screenshot, Webcam, Voice Note recording, System Metrics, Processes, Clipboard, PC Lock, and Volume controls.
- **Telegram Auto-Completing Commands:** Typing `/` automatically lists commands like `/screenshot`, `/webcam`, `/screenrecord`, `/listen`, `/cmd`, `/sysinfo`, `/processes`, `/kill`, `/download`, `/upload`, `/clipboard`, `/brightness`, `/volume`, `/lock`, `/notify`, `/setwallpaper`.
- **Native Voice Notes:** High-quality microphone audio capture delivered directly as Telegram Voice Messages (`.ogg`/`.wav`).
- **Persistent User Agent & Named Pipe IPC:** Seamless Session 0 (Service) <-> Session 1 (User Session) IPC communication via Windows Named Pipes (`\\.\pipe\WinMonIPC`) with sub-10ms response times.
- **Zero Hosting Required:** Operates 100% on Telegram's free cloud infrastructure.
- **Security:** Restricts bot control to designated Telegram User IDs or Usernames.

---

## 📋 Prerequisites

1. **Go** (`1.26.5` or later recommended)
2. **Telegram Bot Token**:
   - Open Telegram and message [@BotFather](https://t.me/BotFather).
   - Send `/newbot`, follow the prompts, and copy the **Bot Token**.
3. **Your Telegram User ID**:
   - Message [@userinfobot](https://t.me/userinfobot) on Telegram to get your numeric **User ID** (or use your `@username`).

---

## 🛠️ Installation & Configuration

1. **Clone the Repository:**
   ```bash
   git clone https://github.com/PavanMeka09/WinMon.git
   cd WinMon
   ```

2. **Configure WinMon:**
   Copy `config.example.json` to `internal/config/config.json`:
   ```cmd
   copy config.example.json internal\config\config.json
   ```
   Edit `internal/config/config.json`:
   ```json
   {
     "bot_token": "YOUR_TELEGRAM_BOT_TOKEN",
     "allowed_users": [
       "YOUR_TELEGRAM_USER_ID_OR_USERNAME"
     ],
     "group": "home",
     "version": "1.0.0",
     "command_timeout_seconds": 20
   }
   ```

3. **Build the Binary:**

   #### Standard Build
   ```cmd
   go build -ldflags="-s -w" -o winmon.exe ./cmd/winmon
   ```

   #### Obfuscated Build (Recommended for Security)
   To obfuscate package names, function names, and string literals:
   ```cmd
   go install mvdan.cc/garble@latest
   garble -literals build -ldflags="-s -w -H=windowsgui" -o winmon.exe ./cmd/winmon
   ```

---

## 🚀 Running WinMon

> [!NOTE]
> Configuration is embedded into the binary at build time. The compiled `winmon.exe` is completely self-contained.

### Console Mode
Run directly from terminal:
```bash
./winmon.exe -console
```

### Windows Service Mode
Run with Administrator privileges to manage the Windows Service:

- **Install Service:** `winmon.exe -service install`
- **Start Service:** `winmon.exe -service start`
- **Stop Service:** `winmon.exe -service stop`
- **Uninstall Service:** `winmon.exe -service uninstall`

---

## 📜 License

This project is licensed under the MIT License.

