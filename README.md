# WinMon

WinMon is a secure Windows PC Remote Management tool that runs as a Telegram Bot. It can be run either as a standard console application or installed as a Windows Service (running in Session 0 with helper coordination for user session actions).

## Features

- **Remote Shell Execution:** Run shell commands directly via Telegram commands.
- **Screen & Display:** Capture screenshots, control display.
- **System Control:** Monitor process lists, network status, system info.
- **Service Integration:** Easily install, uninstall, start, or stop WinMon as a native Windows service.
- **Security:** Limits access to designated Telegram User IDs.

## Prerequisites

- Go (1.26.5 or later recommended)
- A Telegram Bot Token (created via [@BotFather](https://t.me/BotFather))
- Your Telegram User ID (which can be obtained from bots like `@userinfobot`)

## Installation & Setup

1. **Clone the Repository:**
   ```bash
   git clone https://github.com/PavanMeka09/WinMon.git
   cd WinMon
   ```

2. **Configure WinMon:**
   Copy `config.example.json` to `internal/config/config.json` and customize it:
   ```cmd
   copy config.example.json internal\config\config.json
   ```
   Edit `internal/config/config.json` to include:
   - `bot_token`: Your Telegram Bot Token.
   - `allowed_users`: An array containing your Telegram User ID(s) (only these users will be allowed to control the bot).

   *(Note: `device_id` and `device_name` are dynamically determined using the computer's hostname and hardware UUID, meaning the same executable can be shared across multiple computers without conflicts).*

3. **Build the Binary:**
   To build the standalone executable with your configuration embedded:
   ```cmd
   go build -ldflags "-H windowsgui" -o winmon.exe cmd\winmon\main.go
   ```

   #### Obfuscated Build (Highly Recommended for Security)
   To prevent anyone from extracting your Telegram Bot Token from the compiled executable, use [garble](https://github.com/burrowers/garble) to obfuscate and scramble package names, function names, and string literals:
   ```cmd
   go install mvdan.cc/garble@latest
   garble -literals build -ldflags "-H windowsgui" -o winmon.exe cmd\winmon\main.go
   ```

## Running WinMon

> [!NOTE]
> Since the configuration is embedded at build time, the compiled `winmon.exe` is completely self-contained. You do **not** need to place or keep `config.json` next to the executable.

### Console Mode

Run the compiled executable directly from your terminal:
```bash
./winmon.exe
```

### Windows Service Mode

To run WinMon as a background service that starts automatically with Windows, run the executable with Administrator privileges:

- **Install Service:**
  ```cmd
  winmon.exe -service install
  ```
- **Start Service:**
  ```cmd
  winmon.exe -service start
  ```
- **Stop Service:**
  ```cmd
  winmon.exe -service stop
  ```
- **Uninstall Service:**
  ```cmd
  winmon.exe -service uninstall
  ```

## Project Structure

- `cmd/winmon/`: Main entry point for the service and console runner.
- `internal/`: Core components of WinMon (bot coordinator, service handlers, OS integrations).
  - `internal/config/config.json`: Embedded configuration file containing your private credentials (ignored by git).
  - `internal/config/config.json.template`: Default template to ensure compilation succeeds when no configuration is embedded (tracked by git).
- `state.json`: Dynamic state tracking file (ignored by git).

## License

This project is licensed under the MIT License.
