package device

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
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

type rawStatus struct {
	CPUPercent    interface{} `json:"CPUPercent"`
	RAMTotal      int64       `json:"RAMTotal"`
	RAMFree       int64       `json:"RAMFree"`
	DiskTotal     int64       `json:"DiskTotal"`
	DiskFree      int64       `json:"DiskFree"`
	BatteryCharge interface{} `json:"BatteryCharge"`
	BatteryStatus interface{} `json:"BatteryStatus"`
}

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetTickCount64 = kernel32.NewProc("GetTickCount64")
)

func runPowerShell(cmd string) (string, error) {
	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", cmd)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var out bytes.Buffer
	c.Stdout = &out
	err := c.Run()
	return strings.TrimSpace(out.String()), err
}

func GetUptime() (time.Duration, error) {
	ret, _, _ := procGetTickCount64.Call()
	return time.Duration(ret) * time.Millisecond, nil
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

	psCmd := `$cpu = (Get-CimInstance Win32_Processor | Measure-Object -Property LoadPercentage -Average).Average; ` +
		`$ram = Get-CimInstance Win32_OperatingSystem | Select-Object TotalVisibleMemorySize, FreePhysicalMemory; ` +
		`$disk = Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='C:'" | Select-Object Size, FreeSpace; ` +
		`$bat = Get-CimInstance Win32_Battery -ErrorAction SilentlyContinue | Select-Object EstimatedChargeRemaining, BatteryStatus; ` +
		`[PSCustomObject]@{ ` +
		`CPUPercent = $cpu; ` +
		`RAMTotal = $ram.TotalVisibleMemorySize; ` +
		`RAMFree = $ram.FreePhysicalMemory; ` +
		`DiskTotal = $disk.Size; ` +
		`DiskFree = $disk.FreeSpace; ` +
		`BatteryCharge = if ($bat) { $bat.EstimatedChargeRemaining } else { $null }; ` +
		`BatteryStatus = if ($bat) { $bat.BatteryStatus } else { $null }; ` +
		`} | ConvertTo-Json`

	outStr, err := runPowerShell(psCmd)
	if err != nil {
		return nil, err
	}

	var raw rawStatus
	if err := json.Unmarshal([]byte(outStr), &raw); err != nil {
		return nil, err
	}

	// CPU Usage
	if raw.CPUPercent != nil {
		switch v := raw.CPUPercent.(type) {
		case float64:
			info.CPUPercent = v
		case int:
			info.CPUPercent = float64(v)
		}
	}

	// RAM Usage (in KB)
	if raw.RAMTotal > 0 {
		totalGB := float64(raw.RAMTotal) / (1024 * 1024)
		freeGB := float64(raw.RAMFree) / (1024 * 1024)
		info.RAMTotalGB = totalGB
		info.RAMFreeGB = freeGB
		info.RAMPercent = ((totalGB - freeGB) / totalGB) * 100
	}

	// Disk Usage (C: Drive)
	if raw.DiskTotal > 0 {
		totalGB := float64(raw.DiskTotal) / (1024 * 1024 * 1024)
		freeGB := float64(raw.DiskFree) / (1024 * 1024 * 1024)
		info.DiskTotalGB = totalGB
		info.DiskFreeGB = freeGB
		info.DiskPercent = ((totalGB - freeGB) / totalGB) * 100
	}

	// Battery Status
	if raw.BatteryCharge != nil {
		if val, ok := raw.BatteryCharge.(float64); ok {
			info.BatteryCharge = int(val)
		}
		if statusVal, ok := raw.BatteryStatus.(float64); ok {
			statusInt := int(statusVal)
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
	fnvHash := func(s string) string {
		var sum uint32 = 2166136261
		for i := 0; i < len(s); i++ {
			sum ^= uint32(s[i])
			sum *= 16777619
		}
		return fmt.Sprintf("fallback-%08x", sum)
	}
	return fnvHash(name)
}
