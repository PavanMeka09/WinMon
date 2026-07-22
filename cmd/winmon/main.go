package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"

	"winmon/internal/bot"
	"winmon/internal/config"
	"winmon/internal/service"
)

func showMsgBox(title, msg string, isError bool) {
	titlePtr, errTitle := windows.UTF16PtrFromString(title)
	msgPtr, errMsg := windows.UTF16PtrFromString(msg)
	if errTitle != nil || errMsg != nil {
		log.Printf("showMsgBox error: titleErr=%v, msgErr=%v", errTitle, errMsg)
		return
	}
	var style uint32 = windows.MB_OK | windows.MB_SETFOREGROUND
	if isError {
		style |= windows.MB_ICONERROR
	} else {
		style |= windows.MB_ICONINFORMATION
	}
	windows.MessageBox(0, msgPtr, titlePtr, style)
}

func main() {
	// Set shared temp directory for Session 0 service <-> Session 1 helper coordination
	sharedTemp := "C:\\Windows\\Temp"
	if envRoot := os.Getenv("SystemRoot"); envRoot != "" {
		sharedTemp = filepath.Join(envRoot, "Temp")
	}
	os.Setenv("TEMP", sharedTemp)
	os.Setenv("TMP", sharedTemp)

	serviceAction := flag.String("service", "", "Service action: install, uninstall, start, stop")
	sessionHelper := flag.Bool("session-helper", false, "Run as user session helper")
	sessionAgent := flag.Bool("session-agent", false, "Run as persistent user session agent")
	helperCmd := flag.String("cmd", "", "Command to execute (for session helper)")
	helperArgs := flag.String("args", "", "Arguments for the command (for session helper)")
	consoleMode := flag.Bool("console", false, "Force running WinMon in console mode (skip service checks/installation)")
	flag.Parse()

	// 1. Persistent Session Agent Routing
	if *sessionAgent {
		err := bot.RunSessionAgentLoop()
		if err != nil {
			log.Fatalf("Session agent error: %v", err)
		}
		os.Exit(0)
	}

	// 2. Session Helper Routing
	if *sessionHelper {
		if *helperCmd == "" {
			log.Fatal("Missing -cmd argument for session helper")
		}
		err := bot.RunSessionHelper(*helperCmd, *helperArgs, "")
		if err != nil {
			log.Fatalf("Session helper error: %v", err)
		}
		os.Exit(0)
	}

	// 3. Service administrative actions
	if *serviceAction != "" {
		err := handleServiceAction(*serviceAction)
		if err != nil {
			log.Printf("Service action failed: %v", err)
			showMsgBox("WinMon Service Error", fmt.Sprintf("Service action '%s' failed:\n%v", *serviceAction, err), true)
			os.Exit(1)
		}
		if *serviceAction == "install" {
			showMsgBox("WinMon Setup Complete", "WinMon Service installed and started successfully!\n\nIt is now running in the background as a Windows Service at C:\\Program Files\\WinMon\\winmon.exe.", false)
		}
		os.Exit(0)
	}

	// Auto-registration and startup (if double clicked in GUI and not forced console)
	if !service.IsRunningAsService() && !*consoleMode {
		const svcName = "WinMon"
		targetExePath := `C:\Program Files\WinMon\winmon.exe`
		targetExists := false
		if _, err := os.Stat(targetExePath); err == nil {
			targetExists = true
		}

		installed, err := service.IsServiceInstalled(svcName)
		if err != nil {
			log.Printf("Warning: Failed to check service installation status: %v", err)
		}

		if !installed || !targetExists {
			log.Println("WinMon service is not installed or binary is missing. Requesting administrator privileges to install...")
			err := service.ElevateProcess("-service install")
			if err != nil {
				log.Printf("Failed to request elevation: %v. Falling back to console mode.", err)
				showMsgBox("WinMon Setup Error", fmt.Sprintf("Failed to request Administrator privileges:\n%v\n\nFalling back to console mode.", err), true)
			} else {
				log.Println("Elevation request sent. Exiting parent process...")
				os.Exit(0)
			}
		} else {
			running, err := service.IsServiceRunning(svcName)
			if err != nil {
				log.Printf("Warning: Failed to check if service is running: %v", err)
			}

			if running {
				log.Println("WinMon service is already running in the background. Exiting...")
				showMsgBox("WinMon Service", "WinMon is already installed and running active in the background.", false)
				os.Exit(0)
			} else {
				log.Println("WinMon service is installed but not running. Attempting to start the service...")
				err = service.StartService(svcName)
				if err != nil {
					log.Printf("Failed to start service: %v. Requesting administrator privileges to re-install...", err)
					errSvc := service.ElevateProcess("-service install")
					if errSvc != nil {
						showMsgBox("WinMon Error", fmt.Sprintf("Failed to install and start service:\n%v", errSvc), true)
					} else {
						os.Exit(0)
					}
				} else {
					showMsgBox("WinMon Service", "WinMon Service started successfully in the background.", false)
					os.Exit(0)
				}
			}
		}
	}

	// 4. Normal execution (Service mode vs Console mode)
	if service.IsRunningAsService() {
		stopChan := make(chan struct{})

		go func() {
			cfg, err := config.LoadConfig()
			if err != nil {
				service.LogServiceError("Service config error: %v", err)
				service.SafeClose(stopChan)
				return
			}
			coordinator := bot.NewBotCoordinator(cfg, stopChan)
			coordinator.Start()
		}()

		err := service.RunService("WinMon", stopChan)
		if err != nil {
			service.LogServiceError("Service run error: %v", err)
		}
		os.Exit(0)
	}

	// Console mode execution
	cfg, err := config.LoadConfig()
	if err != nil {
		if !*consoleMode {
			showMsgBox("WinMon Configuration Error", fmt.Sprintf("Failed to load config:\n%v\n\nPlease ensure configuration is embedded or config.json is present.", err), true)
		}
		log.Fatalf("Failed to load configuration: %v", err)
	}

	stopChan := make(chan struct{})
	log.Println("Starting WinMon in console mode (Press Ctrl+C to stop)...")

	coordinator := bot.NewBotCoordinator(cfg, stopChan)
	go coordinator.Start()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigChan:
		log.Println("Received shutdown signal. Stopping...")
		close(stopChan)
	case <-stopChan:
		log.Println("Bot coordinator requested shutdown. Stopping...")
	}
}

func handleServiceAction(action string) error {
	const svcName = "WinMon"
	const svcDisplayName = "WinMon PC Remote Management Service"
	const svcDescription = "Enables secure remote management of this Windows PC via Telegram."

	switch action {
	case "install":
		err := service.InstallService(svcName, svcDisplayName, svcDescription)
		if err == nil {
			fmt.Println("Service installed successfully.")
			err = service.StartService(svcName)
			if err == nil {
				fmt.Println("Service started successfully.")
			} else {
				fmt.Printf("Failed to start service: %v\n", err)
			}
		}
		return err
	case "uninstall":
		err := service.UninstallService(svcName)
		if err == nil {
			fmt.Println("Service uninstalled successfully.")
		}
		return err
	case "start":
		err := service.StartService(svcName)
		if err == nil {
			fmt.Println("Service started successfully.")
		}
		return err
	case "stop":
		err := service.StopService(svcName)
		if err == nil {
			fmt.Println("Service stopped successfully.")
		}
		return err
	default:
		return fmt.Errorf("unknown service action: %s", action)
	}
}
