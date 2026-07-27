package core

import (
	"bufio"
	"context"
	"fmt"
	"html"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

type systemSnapshot struct {
	Hostname       string
	OS             string
	Arch           string
	Kernel         string
	HostUptime     time.Duration
	SystemCPU      string
	ProcessCPU     string
	Memory         string
	Swap           string
	LoadAverage    string
	Disk           string
	NetworkName    string
	NetworkTraffic string
	Processes      string
	Packages       string
	InitSystem     string
	Locale         string
	ScanTime       time.Duration
}

func (p *Plugin) status(ctx context.Context, request command.Request) error {
	snapshot := p.collectSystemSnapshot(ctx)
	statuses := p.registry.List()
	enabled := 0
	for _, status := range statuses {
		if status.Enabled {
			enabled++
		}
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	text := fmt.Sprintf(
		"<b>📊 TeleBox-Go 运行状态</b>\n\n"+
			"<b>🏠 主机信息</b>\n"+
			"• 主机名：<code>%s</code>\n"+
			"• 系统：<code>%s</code>\n"+
			"• 内核：<code>%s</code>\n"+
			"• CPU 核心：<code>%d</code>\n\n"+
			"<b>📦 版本信息</b>\n"+
			"• Go：<code>%s</code>\n"+
			"• TeleBox-Go：<code>%s</code>\n\n"+
			"<b>📈 运行资源</b>\n"+
			"• CPU：<code>%s</code>（系统）/ <code>%s</code>（进程）\n"+
			"• 系统内存：<code>%s</code>\n"+
			"• 进程内存：<code>%s</code>\n"+
			"• SWAP：<code>%s</code>\n"+
			"• 磁盘：<code>%s</code>\n"+
			"• Goroutine：<code>%d</code>\n"+
			"• 插件：<code>%d/%d</code> 已启用\n\n"+
			"<b>⏱️ 运行状态</b>\n"+
			"• 程序运行：<code>%s</code>\n"+
			"• 主机运行：<code>%s</code>\n"+
			"• 扫描耗时：<code>%dms</code>",
		html.EscapeString(snapshot.Hostname),
		html.EscapeString(snapshot.OS+" "+snapshot.Arch),
		html.EscapeString(snapshot.Kernel),
		runtime.NumCPU(),
		html.EscapeString(runtime.Version()),
		html.EscapeString(p.Metadata().Version),
		html.EscapeString(snapshot.SystemCPU),
		html.EscapeString(snapshot.ProcessCPU),
		html.EscapeString(snapshot.Memory),
		html.EscapeString(formatBytes(memory.Alloc)),
		html.EscapeString(snapshot.Swap),
		html.EscapeString(snapshot.Disk),
		runtime.NumGoroutine(),
		enabled,
		len(statuses),
		html.EscapeString(formatDuration(time.Since(p.started))),
		html.EscapeString(formatDuration(snapshot.HostUptime)),
		snapshot.ScanTime.Milliseconds(),
	)
	return p.respondHTML(ctx, request, text)
}

func (p *Plugin) sysinfo(ctx context.Context, request command.Request) error {
	snapshot := p.collectSystemSnapshot(ctx)
	text := fmt.Sprintf(
		"<code>root@%s\n"+
			"--------------\n"+
			"OS: %s %s\n"+
			"Kernel: %s\n"+
			"Uptime: %s\n"+
			"Loadavg: %s\n"+
			"Packages: %s\n"+
			"Init System: %s\n"+
			"Shell: Go %s\n"+
			"Locale: %s\n"+
			"Processes: %s\n"+
			"CPU: %s (system) / %s (process)\n"+
			"Memory: %s\n"+
			"Swap: %s\n"+
			"Disk: %s\n"+
			"Network (%s): %s\n"+
			"Scan Time: %dms</code>",
		html.EscapeString(snapshot.Hostname),
		html.EscapeString(snapshot.OS),
		html.EscapeString(snapshot.Arch),
		html.EscapeString(snapshot.Kernel),
		html.EscapeString(formatDuration(snapshot.HostUptime)),
		html.EscapeString(snapshot.LoadAverage),
		html.EscapeString(snapshot.Packages),
		html.EscapeString(snapshot.InitSystem),
		html.EscapeString(runtime.Version()),
		html.EscapeString(snapshot.Locale),
		html.EscapeString(snapshot.Processes),
		html.EscapeString(snapshot.SystemCPU),
		html.EscapeString(snapshot.ProcessCPU),
		html.EscapeString(snapshot.Memory),
		html.EscapeString(snapshot.Swap),
		html.EscapeString(snapshot.Disk),
		html.EscapeString(snapshot.NetworkName),
		html.EscapeString(snapshot.NetworkTraffic),
		snapshot.ScanTime.Milliseconds(),
	)
	return p.respondHTML(ctx, request, text)
}

func (p *Plugin) collectSystemSnapshot(ctx context.Context) systemSnapshot {
	started := time.Now()
	hostname, _ := os.Hostname()
	if strings.TrimSpace(hostname) == "" {
		hostname = "N/A"
	}
	result := systemSnapshot{
		Hostname:       hostname,
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		Kernel:         "N/A",
		HostUptime:     time.Since(p.started),
		SystemCPU:      "N/A",
		ProcessCPU:     "N/A",
		Memory:         "N/A",
		Swap:           "Disabled",
		LoadAverage:    "N/A",
		Disk:           "N/A",
		NetworkName:    "N/A",
		NetworkTraffic: "N/A",
		Processes:      "N/A",
		Packages:       "N/A",
		InitSystem:     "N/A",
		Locale:         firstNonEmpty(os.Getenv("LC_ALL"), os.Getenv("LANG"), "N/A"),
	}
	if runtime.GOOS == "linux" {
		p.collectLinuxSnapshot(ctx, &result)
	} else {
		p.collectPortableSnapshot(ctx, &result)
	}
	result.ScanTime = time.Since(started)
	return result
}

func (p *Plugin) collectLinuxSnapshot(
	ctx context.Context,
	result *systemSnapshot,
) {
	if fields := readKeyValueFile("/etc/os-release", "="); len(fields) > 0 {
		result.OS = strings.Trim(fields["PRETTY_NAME"], `"`)
	}
	result.Kernel = p.runShortTool(ctx, "uname", "-r")
	if result.Kernel == "" {
		result.Kernel = "Linux"
	}
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			if seconds, parseErr := strconv.ParseFloat(fields[0], 64); parseErr == nil {
				result.HostUptime = time.Duration(seconds * float64(time.Second))
			}
		}
	}
	memory := readMemInfo("/proc/meminfo")
	total := memory["MemTotal"]
	available := memory["MemAvailable"]
	if total > 0 {
		result.Memory = formatUsage(total-available, total)
	}
	swapTotal := memory["SwapTotal"]
	swapFree := memory["SwapFree"]
	if swapTotal > 0 {
		result.Swap = formatUsage(swapTotal-swapFree, swapTotal)
	}
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 3 {
			result.LoadAverage = strings.Join(fields[:3], ", ")
		}
	}
	result.SystemCPU, result.ProcessCPU = sampleLinuxCPU(ctx)
	result.Disk = p.linuxDisk(ctx)
	result.NetworkName, result.NetworkTraffic = linuxNetwork()
	result.Processes = strconv.Itoa(countLinuxProcesses())
	result.Packages = p.linuxPackages(ctx)
	switch {
	case pathExists("/run/systemd/system"):
		result.InitSystem = "systemd"
	case pathExists("/sbin/openrc"):
		result.InitSystem = "OpenRC"
	default:
		if name := p.runShortTool(ctx, "ps", "-p", "1", "-o", "comm="); name != "" {
			result.InitSystem = name
		}
	}
}

func (p *Plugin) collectPortableSnapshot(
	ctx context.Context,
	result *systemSnapshot,
) {
	if runtime.GOOS == "windows" {
		result.Kernel = p.runShortTool(ctx, "cmd.exe", "/c", "ver")
		result.InitSystem = "Windows Service"
		result.Disk = p.runShortTool(
			ctx,
			"powershell.exe",
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			"$d=Get-PSDrive -Name ($pwd.Path.Substring(0,1)); "+
				"'{0:N1} GiB / {1:N1} GiB' -f "+
				"(($d.Used)/1GB),(($d.Used+$d.Free)/1GB)",
		)
	}
	result.NetworkName, result.NetworkTraffic = portableNetwork()
}

func (p *Plugin) runShortTool(
	ctx context.Context,
	name string,
	args ...string,
) string {
	if p.services.Tools == nil {
		return ""
	}
	result, err := p.services.Tools.Run(ctx, toolrunner.Command{
		Name:      name,
		Args:      args,
		Timeout:   5 * time.Second,
		MaxOutput: 1 << 20,
	})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

func (p *Plugin) linuxDisk(ctx context.Context) string {
	output := p.runShortTool(ctx, "df", "-h", "/")
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return "N/A"
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 5 {
		return "N/A"
	}
	return fields[2] + " / " + fields[1] + " (" + fields[4] + ")"
}

func (p *Plugin) linuxPackages(ctx context.Context) string {
	for _, candidate := range []struct {
		name string
		args []string
		tag  string
	}{
		{name: "dpkg-query", args: []string{"-W", "-f=${binary:Package}\n"}, tag: "dpkg"},
		{name: "rpm", args: []string{"-qa"}, tag: "rpm"},
		{name: "apk", args: []string{"info"}, tag: "apk"},
	} {
		if p.services.Tools == nil {
			break
		}
		result, err := p.services.Tools.Run(ctx, toolrunner.Command{
			Name:      candidate.name,
			Args:      candidate.args,
			Timeout:   8 * time.Second,
			MaxOutput: 4 << 20,
		})
		if err != nil {
			continue
		}
		count := len(strings.Fields(strings.TrimSpace(result.Stdout)))
		return fmt.Sprintf("%d (%s)", count, candidate.tag)
	}
	return "N/A"
}

func readMemInfo(path string) map[string]uint64 {
	result := make(map[string]uint64)
	file, err := os.Open(path)
	if err != nil {
		return result
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err == nil {
			result[strings.TrimSuffix(fields[0], ":")] = value * 1024
		}
	}
	return result
}

func readKeyValueFile(path, separator string) map[string]string {
	result := make(map[string]string)
	file, err := os.Open(path)
	if err != nil {
		return result
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), separator)
		if ok {
			result[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return result
}

func sampleLinuxCPU(ctx context.Context) (string, string) {
	totalBefore, idleBefore := readSystemCPU()
	processBefore := readProcessCPU()
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "N/A", "N/A"
	case <-timer.C:
	}
	totalAfter, idleAfter := readSystemCPU()
	processAfter := readProcessCPU()
	system := "N/A"
	if totalAfter > totalBefore {
		busy := (totalAfter - totalBefore) - (idleAfter - idleBefore)
		system = fmt.Sprintf("%.2f%%", float64(busy)*100/float64(totalAfter-totalBefore))
	}
	process := "N/A"
	if processAfter >= processBefore {
		// Linux commonly exposes USER_HZ=100. This is an estimate but is
		// sufficient for the short status sample and is explicitly labelled.
		value := float64(processAfter-processBefore) / 100 / 0.1 * 100
		if runtime.NumCPU() > 0 {
			value /= float64(runtime.NumCPU())
		}
		process = fmt.Sprintf("%.2f%%", value)
	}
	return system, process
}

func readSystemCPU() (uint64, uint64) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0
	}
	var total uint64
	values := make([]uint64, 0, len(fields)-1)
	for _, field := range fields[1:] {
		value, _ := strconv.ParseUint(field, 10, 64)
		total += value
		values = append(values, value)
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	return total, idle
}

func readProcessCPU() uint64 {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	end := strings.LastIndex(string(data), ")")
	if end < 0 {
		return 0
	}
	fields := strings.Fields(string(data)[end+1:])
	if len(fields) <= 12 {
		return 0
	}
	user, _ := strconv.ParseUint(fields[11], 10, 64)
	system, _ := strconv.ParseUint(fields[12], 10, 64)
	return user + system
}

func linuxNetwork() (string, string) {
	name, traffic := portableNetwork()
	if name == "N/A" {
		return name, traffic
	}
	readCounter := func(counter string) uint64 {
		data, err := os.ReadFile(filepath.Join(
			"/sys/class/net",
			name,
			"statistics",
			counter,
		))
		if err != nil {
			return 0
		}
		value, _ := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		return value
	}
	return name, formatBytes(readCounter("rx_bytes")) + " ↓ / " +
		formatBytes(readCounter("tx_bytes")) + " ↑"
}

func portableNetwork() (string, string) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "N/A", "N/A"
	}
	for _, item := range interfaces {
		if item.Flags&net.FlagUp != 0 && item.Flags&net.FlagLoopback == 0 {
			return item.Name, "N/A"
		}
	}
	return "N/A", "N/A"
}

func countLinuxProcesses() int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err == nil {
			count++
		}
	}
	return count
}

func formatUsage(used, total uint64) string {
	if total == 0 {
		return "N/A"
	}
	return fmt.Sprintf(
		"%s / %s (%.1f%%)",
		formatBytes(used),
		formatBytes(total),
		float64(used)*100/float64(total),
	)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
