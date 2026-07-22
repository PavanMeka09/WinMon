# WinMon

WinMon is a secure Windows PC Remote Management tool that runs as a **Discord Bot**. It can be run either as a standard console application or installed as a persistent Windows Service (running in Session 0 with a persistent Named Pipe IPC User Agent in Session 1).

---

## ⚡ Features

- **Discord Native Slash Commands:** Auto-completing `/screenshot`, `/webcam`, `/screenrecord`, `/listen`, `/cmd`, `/processes`, `/kill`, `/download`, `/upload`, `/tts`, `/playsound`, `/setwallpaper`, `/notify`, `/mouse`, `/type`, `/hotkey`, `/keypress`, `/clipboard`, `/brightness`, `/setvol`, `/mute`, `/unmute`.
- **Interactive Control Buttons:** Instant response buttons for Screenshot, Processes, Device Info, Clipboard, and Audio Mute.
- **Persistent User Agent & Named Pipe IPC:** Seamless Session 0 (Service) <-> Session 1 (User Session) IPC communication via Windows Named Pipes (`\\.\pipe\WinMonIPC`) with sub-10ms response times.
- **Zero Hosting Required:** Operates 100% on Discord's free cloud infrastructure.
- **Security:** Restricts bot control to designated Discord User Snowflake IDs.

---

## 📋 Prerequisites

1. **Go** (`1.26.5` or later recommended)
2. **Discord Bot Token**:
   - Go to [Discord Developer Portal](https://discord.com/developers/applications) -> Create an Application -> **Bot**.
   - Enable **Message Content Intent** under Bot settings.
   - Copy the **Bot Token**.
3. **Bot Invite Link**:
   - Go to **OAuth2** -> **URL Generator**.
   - Select scopes: `bot` and `applications.commands`.
   - Grant permissions (Send Messages, Attach Files, Embed Links, Read Message History).
   - Use the generated URL to invite the bot to your private Discord Server.
4. **Your Discord User ID**:
   - Enable Developer Mode in Discord Settings (`Advanced -> Developer Mode`).
   - Right-click your username -> **Copy User ID**.

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
     "bot_token": "YOUR_DISCORD_BOT_TOKEN",
     "guild_id": "YOUR_DISCORD_SERVER_ID_OPTIONAL",
     "allowed_users": [
       "YOUR_DISCORD_USER_ID"
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
