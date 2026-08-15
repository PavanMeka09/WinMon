# WinMon

WinMon is a Windows PC remote-management agent controlled through a **Telegram Bot**. It can run as a console app or as a Windows Service (Session 0) with a persistent Named Pipe IPC user agent in the interactive desktop session (Session 1).

---

## Features

- **Telegram command bot:** Text commands for screenshot, webcam, screen GIF, mic voice note, shell, clipboard, volume, brightness, wallpaper, notify, upload/download, and self-update.
- **Session 0 service + Session 1 agent:** Interactive desktop APIs (screenshot, webcam, clipboard, toasts) are handled in the user session over `\\.\pipe\WinMonIPC`.
- **Private working directory:** Runtime media and helper scripts use `%ProgramData%\WinMon\temp` with a restrictive ACL (not world-readable `%SystemRoot%\Temp`).
- **Access control:** Only numeric Telegram user IDs in `allowed_users` are authorized (default deny if empty).
- **No hosting required:** Uses Telegram Bot API polling.

---

## Prerequisites

1. **Go** (`1.26.5` or later recommended)
2. **Telegram Bot Token** from [@BotFather](https://t.me/BotFather)
3. **Your numeric Telegram User ID** from [@userinfobot](https://t.me/userinfobot) (usernames are not accepted)

---

## Installation & Configuration

1. **Clone the repository:**
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
       "123456789"
     ],
     "group": "home",
     "version": "1.0.0",
     "command_timeout_seconds": 20
   }
   ```
   Use **numeric user IDs only**. Username entries are ignored.

3. **Build the binary:**

   #### Standard build (recommended)
   ```cmd
   go build -ldflags="-s -w" -o winmon.exe ./cmd/winmon
   ```

   #### Obfuscated build (optional)
   Obfuscation can reduce casual inspection of strings/symbols, but it often increases antivirus false positives and does not replace proper access control.
   ```cmd
   go install mvdan.cc/garble@latest
   garble -literals build -ldflags="-s -w -H=windowsgui" -o winmon.exe ./cmd/winmon
   ```

---

## Running WinMon

> [!NOTE]
> Configuration is embedded into the binary at build time when `internal/config/config.json` is present. The compiled `winmon.exe` can be self-contained.

### Console mode
```bash
./winmon.exe -console
```

### Windows Service mode
Run elevated:

- **Install:** `winmon.exe -service install`
- **Start:** `winmon.exe -service start`
- **Stop:** `winmon.exe -service stop`
- **Uninstall:** `winmon.exe -service uninstall`

Double-clicking without `-console` attempts service install/start (UAC elevation). Prefer explicit `-service` flags when possible.

### Common Telegram commands

| Command | Description |
|---------|-------------|
| `/help` | List commands |
| `/screenshot` | Capture primary display |
| `/webcam` | Capture webcam frame |
| `/screenrecord [sec]` | Record screen GIF |
| `/listen [sec]` | Record mic voice note |
| `/cmd <command>` | Run shell command |
| `/download <path>` | Send a file from the PC |
| `/upload` | Caption on an attachment to save it |
| `/update` | Caption on a new `winmon.exe` document to self-update |
| `/restartservice` | Restart the Windows service |
| `/implode confirm` | Uninstall service and delete WinMon files |

---

## Security notes

- Keep `bot_token` private. Prefer compile-time ldflags injection for distribution builds.
- Allowlist **numeric Telegram user IDs** only.
- The Windows service typically runs as **SYSTEM**; `/cmd` and `/download` inherit that privilege.
- Named pipe ACL allows SYSTEM, Administrators, and the session-agent user SID (not all Interactive Users).
- Expect antivirus heuristics around persistence, webcam/mic, shell execution, and especially obfuscated (`garble`) builds. Prefer the standard build for personal use on machines you own.

---

## Antivirus / distribution posture

WinMon is a legitimate self-hosted RMM-style tool for machines you control. Security products may still flag:

- Automatic Windows service installation
- Session injection / named-pipe IPC
- Screenshot, webcam, and microphone capture
- Remote shell execution
- Optional `garble` obfuscation and `-H=windowsgui`

**Guidance:** ship the standard non-obfuscated build, keep allowlists tight, and do not distribute signed “stealth” builds to evade AV. If you need quieter operation, whitelist the installed path (`C:\Program Files\WinMon\winmon.exe`) in your AV rather than obfuscating.

---

## License

This project is licensed under the MIT License.
