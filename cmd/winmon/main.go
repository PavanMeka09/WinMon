package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"winmon/internal/bot"
	"winmon/internal/config"
	"winmon/internal/service"
)

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
	helperCmd := flag.String("cmd", "", "Command to execute (for session helper)")
	helperArgs := flag.String("args", "", "Arguments for the command (for session helper)")
	consoleMode := flag.Bool("console", false, "Force running WinMon in console mode (skip service checks/installation)")
	flag.Parse()

	// 1. Session Helper Routing
	if *sessionHelper {
		if *helperCmd == "" {
			log.Fatal("Missing -cmd argument for session helper")
		}
		err := bot.RunSessionHelper(*helperCmd, *helperArgs)
		if err != nil {
			log.Fatalf("Session helper error: %v", err)
		}
		os.Exit(0)
	}

	// 2. Service administrative actions
	if *serviceAction != "" {
		err := handleServiceAction(*serviceAction)
		if err != nil {
			log.Fatalf("Service action failed: %v", err)
		}
		os.Exit(0)
	}

	// Auto-registration and startup (if not running as a service and not forced console)
	if !service.IsRunningAsService() && !*consoleMode {
		const svcName = "WinMon"
		installed, err := service.IsServiceInstalled(svcName)
		if err != nil {
			log.Printf("Warning: Failed to check service installation status: %v", err)
		}

		if !installed {
			log.Println("WinMon service is not installed. Requesting administrator privileges to install and start the service...")
			err := service.ElevateProcess("-service install")
			if err != nil {
				log.Printf("Failed to request elevation: %v. Falling back to console mode.", err)
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
				os.Exit(0)
			} else {
				log.Println("WinMon service is installed but not running. Attempting to start the service...")
				err = service.StartService(svcName)
				if err != nil {
					log.Printf("Failed to start service: %v. Requesting administrator privileges to start the service...", err)
					errSvc := service.ElevateProcess("-service start")
					if errSvc != nil {
						log.Printf("Failed to request elevation: %v. Falling back to console mode.", errSvc)
					} else {
						log.Println("Elevation request sent. Exiting parent process...")
						os.Exit(0)
					}
				} else {
					log.Println("WinMon service started successfully. Exiting...")
					os.Exit(0)
				}
			}
		}
	}

	// 3. Normal execution (Service mode or Console mode)
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	stopChan := make(chan struct{})

	if service.IsRunningAsService() {
		go func() {
			coordinator := bot.NewBotCoordinator(cfg, stopChan)
			coordinator.Start()
		}()

		err = service.RunService("WinMon", stopChan)
		if err != nil {
			log.Fatalf("Service execution failed: %v", err)
		}
	} else {
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
