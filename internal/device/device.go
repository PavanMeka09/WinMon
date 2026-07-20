package device

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type StatusInfo struct {
	CPUPercent    float64
	RAMFreeGB     float64
	RAMTotalGB    float64
	RAMPercent    float64
	DiskFreeGB    float64
	DiskTotalGB   float64
	DiskPercent   float64
	BatteryCharge int    // percent, -1 if no battery
	BatteryStatus string // "Charging", "Discharging", "Full", etc.
	Uptime        time.Duration
}

type DeviceInfo struct {
	DeviceName string
	DeviceID   string
	PCName     string
	IPAddress  string
	Version    string
	OS         string
	Uptime     time.Duration
}

func runPowerShell(cmd string) (string, error) {
	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", cmd)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var out bytes.Buffer
	c.Stdout = &out
	err := c.Run()
	return strings.TrimSpace(out.String()), err
}

func GetUptime() (time.Duration, error) {
	// Query LastBootUpTime using CIM
	out, err := runPowerShell("(Get-Date) - (Get-CimInstance Win32_OperatingSystem).LastBootUpTime | Select-Object -ExpandProperty TotalSeconds")
	if err != nil {
		return 0, err
	}
	secs, err := strconv.ParseFloat(out, 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(secs) * time.Second, nil
}

func GetOSVersion() (string, error) {
	out, err := runPowerShell("(Get-CimInstance Win32_OperatingSystem).Caption + ' (Build ' + (Get-CimInstance Win32_OperatingSystem).BuildNumber + ')'")
	if err != nil {
		return "Windows", err
	}
	return out, nil
}

func GetIPAddresses() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "Unknown"
	}
	var ips []string
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				ips = append(ips, ipnet.IP.String())
			}
		}
	}
	if len(ips) == 0 {
		return "No IP"
	}
	return strings.Join(ips, ", ")
}

func GetStatus() (*StatusInfo, error) {
	info := &StatusInfo{BatteryCharge: -1, BatteryStatus: "N/A"}

	// CPU Usage
	cpuStr, err := runPowerShell("(Get-CimInstance Win32_Processor | Measure-Object -Property LoadPercentage -Average).Average")
	if err == nil {
		if val, err := strconv.ParseFloat(cpuStr, 64); err == nil {
			info.CPUPercent = val
		}
	}

	// RAM Usage (in KB)
	ramStr, err := runPowerShell("Get-CimInstance Win32_OperatingSystem | Select-Object TotalVisibleMemorySize, FreePhysicalMemory | ConvertTo-Json")
	if err == nil {
		// Quick parse free & total
		reTotal := regexp.MustCompile(`"TotalVisibleMemorySize":\s*(\d+)`)
		reFree := regexp.MustCompile(`"FreePhysicalMemory":\s*(\d+)`)
		mTotal := reTotal.FindStringSubmatch(ramStr)
		mFree := reFree.FindStringSubmatch(ramStr)
		if len(mTotal) > 1 && len(mFree) > 1 {
			totalKB, _ := strconv.ParseFloat(mTotal[1], 64)
			freeKB, _ := strconv.ParseFloat(mFree[1], 64)
			usedKB := totalKB - freeKB

			info.RAMTotalGB = totalKB / (1024 * 1024)
			info.RAMFreeGB = freeKB / (1024 * 1024)
			info.RAMPercent = (usedKB / totalKB) * 100
		}
	}

	// Disk Usage (C: Drive)
	diskStr, err := runPowerShell("Get-CimInstance Win32_LogicalDisk -Filter \"DeviceID='C:'\" | Select-Object Size, FreeSpace | ConvertTo-Json")
	if err == nil {
		reSize := regexp.MustCompile(`"Size":\s*(\d+)`)
		reFree := regexp.MustCompile(`"FreeSpace":\s*(\d+)`)
		mSize := reSize.FindStringSubmatch(diskStr)
		mFree := reFree.FindStringSubmatch(diskStr)
		if len(mSize) > 1 && len(mFree) > 1 {
			sizeBytes, _ := strconv.ParseFloat(mSize[1], 64)
			freeBytes, _ := strconv.ParseFloat(mFree[1], 64)
			usedBytes := sizeBytes - freeBytes

			info.DiskTotalGB = sizeBytes / (1024 * 1024 * 1024)
			info.DiskFreeGB = freeBytes / (1024 * 1024 * 1024)
			info.DiskPercent = (usedBytes / sizeBytes) * 100
		}
	}

	// Battery Status
	batteryStr, err := runPowerShell("Get-CimInstance Win32_Battery | Select-Object EstimatedChargeRemaining, BatteryStatus | ConvertTo-Json")
	if err == nil && strings.Contains(batteryStr, "EstimatedChargeRemaining") {
		reCharge := regexp.MustCompile(`"EstimatedChargeRemaining":\s*(\d+)`)
		reStatus := regexp.MustCompile(`"BatteryStatus":\s*(\d+)`)
		mCharge := reCharge.FindStringSubmatch(batteryStr)
		mStatus := reStatus.FindStringSubmatch(batteryStr)
		if len(mCharge) > 1 {
			charge, _ := strconv.Atoi(mCharge[1])
			info.BatteryCharge = charge
		}
		if len(mStatus) > 1 {
			statusInt, _ := strconv.Atoi(mStatus[1])
			// BatteryStatus mapping: 1=Other, 2=Unknown, 3=Fully Charged, 4=Low, 5=Critical, 6=Charging, 7=Charging and High, 8=Charging and Low, 9=Charging and Critical, 10=Undefined, 11=Partially Charged
			switch statusInt {
			case 3:
				info.BatteryStatus = "Full"
			case 6, 7, 8, 9:
				info.BatteryStatus = "Charging"
			default:
				info.BatteryStatus = "Discharging"
			}
		}
	}

	// Uptime
	uptime, err := GetUptime()
	if err == nil {
		info.Uptime = uptime
	}

	return info, nil
}

func GetDeviceInfo(deviceName, deviceID, version string) (*DeviceInfo, error) {
	pcName, _ := os.Hostname()
	osVer, _ := GetOSVersion()
	uptime, _ := GetUptime()

	return &DeviceInfo{
		DeviceName: deviceName,
		DeviceID:   deviceID,
		PCName:     pcName,
		IPAddress:  GetIPAddresses(),
		Version:    version,
		OS:         osVer,
		Uptime:     uptime,
	}, nil
}

func FormatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 || days > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 || hours > 0 || days > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	parts = append(parts, fmt.Sprintf("%ds", seconds))

	return strings.Join(parts, " ")
}

func GetComputerName() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "Unknown-PC"
	}
	return name
}

func GetComputerUUID() string {
	uuid, err := runPowerShell("(Get-CimInstance Win32_ComputerSystemProduct).UUID")
	if err == nil && uuid != "" {
		// Clean and validate that the UUID is not all zeros or placeholder
		cleaned := strings.ToLower(strings.TrimSpace(uuid))
		if cleaned != "" && cleaned != "00000000-0000-0000-0000-000000000000" {
			return cleaned
		}
	}

	// Fallback to FNV hash of hostname to guarantee a stable, unique ID
	name := GetComputerName()
	importHash := func(s string) string {
		var sum uint32 = 2166136261
		for i := 0; i < len(s); i++ {
			sum ^= uint32(s[i])
			sum *= 16777619
		}
		return fmt.Sprintf("fallback-%08x", sum)
	}
	return importHash(name)
}
