# WinMon

WinMon is a secure Windows PC Remote Management tool that runs as a Telegram Bot. It can be run either as a standard console application or installed as a Windows Service (running in Session 0 with helper coordination for user session actions).

## Features

- **Remote Shell Execution:** Run permitted commands securely via Telegram commands.
- **Screen & Display:** Capture screenshots, control display.
- **System Control:** Monitor process lists, network status, system info.
- **Service Integration:** Easily install, uninstall, start, or stop WinMon as a native Windows service.
- **Security:** Limits access to designated Telegram User IDs and specifies allowed commands in configuration.

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
   Copy `config.example.json` to `config.json` and customize it:
   ```bash
   copy config.example.json config.json
   ```
   Edit `config.json` to include:
   - `bot_token`: Your Telegram Bot Token.
   - `allowed_users`: An array containing your Telegram User ID(s) (only these users will be allowed to control the bot).
   - Customize `allowed_cmds` to restrict or permit specific console commands.

3. **Build the Binary:**
   To build the executable:
   ```bash
   go build -o winmon.exe ./cmd/winmon
   ```

## Running WinMon

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
- `config.json`: Local configuration file (ignored by git).
- `state.json`: Dynamic state tracking file (ignored by git).

## License

This project is licensed under the MIT License.
