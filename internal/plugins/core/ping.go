package core

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

var telegramDataCenters = []struct {
	Name     string
	Location string
	Address  string
}{
	{Name: "DC1", Location: "Miami", Address: "149.154.175.53"},
	{Name: "DC2", Location: "Amsterdam", Address: "149.154.167.51"},
	{Name: "DC3", Location: "Miami", Address: "149.154.175.100"},
	{Name: "DC4", Location: "Amsterdam", Address: "149.154.167.91"},
	{Name: "DC5", Location: "Singapore", Address: "91.108.56.130"},
}

var (
	linuxPingAverage = regexp.MustCompile(
		`(?m)(?:rtt|round-trip)[^=]*=\s*[\d.]+/([\d.]+)`,
	)
	windowsPingAverage = regexp.MustCompile(
		`(?i)Average\s*=\s*(\d+)ms`,
	)
	pingPacketLoss = regexp.MustCompile(
		`(?i)(\d+(?:\.\d+)?)%\s*(?:packet )?loss`,
	)
)

type targetProbe struct {
	Input      string
	Address    string
	Kind       string
	DNSLatency time.Duration
	ResolvedIP string
	ICMP       string
	PacketLoss string
	TCP80      time.Duration
	TCP443     time.Duration
	HTTPS      time.Duration
}

func (p *Plugin) ping(ctx context.Context, request command.Request) error {
	if len(request.Args) == 0 {
		return p.telegramPing(ctx, request)
	}
	target := strings.ToLower(strings.TrimSpace(request.Args[0]))
	switch target {
	case "help", "h":
		return p.respondHTML(ctx, request, pingHelp(request.Prefix))
	case "all", "dc":
		return p.pingAllDataCenters(ctx, request)
	}
	if len(request.Args) != 1 {
		return p.respondHTML(ctx, request, pingHelp(request.Prefix))
	}
	input, address, kind, err := parsePingTarget(target)
	if err != nil {
		return p.respond(ctx, request, "❌ 无效的 Ping 目标："+err.Error())
	}
	if err := p.respondHTML(
		ctx,
		request,
		"🔍 正在测试 <code>"+html.EscapeString(input)+"</code>…",
	); err != nil {
		return err
	}
	probe := p.probeTarget(ctx, input, address, kind)
	return p.respondHTML(ctx, request, formatTargetProbe(probe))
}

func (p *Plugin) telegramPing(
	ctx context.Context,
	request command.Request,
) error {
	apiStarted := time.Now()
	if _, err := p.services.Telegram.ResolveUser(ctx, "me"); err != nil {
		p.services.Logger.Warn("Telegram latency probe failed", "error", err)
		return p.respond(ctx, request, "❌ Telegram 连接不可用")
	}
	apiLatency := time.Since(apiStarted).Milliseconds()

	messageLatency := int64(0)
	if request.Message.Outgoing {
		messageStarted := time.Now()
		if _, err := p.services.Telegram.EditText(
			ctx,
			request.Message.ChatID,
			request.Message.ID,
			"🏓 Pong!",
		); err != nil {
			return err
		}
		messageLatency = time.Since(messageStarted).Milliseconds()
	}

	return p.respondHTML(ctx, request, fmt.Sprintf(
		"<b>🏓 Pong!</b>\n\n"+
			"📡 API 延迟：<code>%dms</code>\n"+
			"✏️ 消息延迟：<code>%dms</code>\n\n"+
			"⏰ <i>%s</i>",
		apiLatency,
		messageLatency,
		time.Now().Format("2006/01/02 15:04:05"),
	))
}

func (p *Plugin) pingAllDataCenters(
	ctx context.Context,
	request command.Request,
) error {
	if err := p.respond(ctx, request, "🔍 正在测试所有 Telegram 数据中心…"); err != nil {
		return err
	}
	results := make([]string, len(telegramDataCenters))
	var wait sync.WaitGroup
	for index, dc := range telegramDataCenters {
		index, dc := index, dc
		wait.Add(1)
		go func() {
			defer wait.Done()
			icmp, _, ok := p.icmpPing(ctx, dc.Address)
			latency := icmp
			if !ok {
				tcp := tcpLatency(ctx, dc.Address, 443)
				if tcp > 0 {
					latency = fmt.Sprintf("%dms TCP", tcp.Milliseconds())
				} else {
					latency = "超时"
				}
			}
			results[index] = fmt.Sprintf(
				"🌐 <b>%s (%s)：</b> <code>%s</code>",
				dc.Name,
				dc.Location,
				html.EscapeString(latency),
			)
		}()
	}
	wait.Wait()
	return p.respondHTML(ctx, request,
		"<b>🌐 Telegram 数据中心延迟</b>\n\n"+
			strings.Join(results, "\n")+
			"\n\n⏰ <i>"+time.Now().Format("2006/01/02 15:04:05")+"</i>",
	)
}

func (p *Plugin) probeTarget(
	ctx context.Context,
	input, address, kind string,
) targetProbe {
	result := targetProbe{
		Input:   input,
		Address: address,
		Kind:    kind,
		ICMP:    "不可用",
	}
	if net.ParseIP(address) == nil {
		started := time.Now()
		lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ips, err := net.DefaultResolver.LookupHost(lookupCtx, address)
		cancel()
		if err == nil && len(ips) > 0 {
			result.DNSLatency = time.Since(started)
			result.ResolvedIP = ips[0]
		}
	}
	if average, loss, ok := p.icmpPing(ctx, address); ok {
		result.ICMP = average
		result.PacketLoss = loss
	}
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		result.TCP80 = tcpLatency(ctx, address, 80)
	}()
	go func() {
		defer wait.Done()
		result.TCP443 = tcpLatency(ctx, address, 443)
	}()
	if kind == "域名" && p.services.HTTP != nil {
		started := time.Now()
		requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		_, err := p.services.HTTP.Do(requestCtx, httpclient.Request{
			Method: http.MethodHead,
			URL:    "https://" + address,
		})
		cancel()
		if err == nil {
			result.HTTPS = time.Since(started)
		}
	}
	wait.Wait()
	return result
}

func (p *Plugin) icmpPing(
	ctx context.Context,
	target string,
) (string, string, bool) {
	if p.services.Tools == nil {
		return "", "", false
	}
	args := []string{"-c", "3", "-W", "5", target}
	if runtime.GOOS == "windows" {
		args = []string{"-n", "3", "-w", "5000", target}
	}
	result, runErr := p.services.Tools.Run(ctx, toolrunner.Command{
		Name:      "ping",
		Args:      args,
		Timeout:   12 * time.Second,
		MaxOutput: 128 << 10,
	})
	output := result.Stdout + "\n" + result.Stderr
	average, loss, parsed := parsePingOutput(output)
	if runErr != nil && !parsed {
		return "", "", false
	}
	return average, loss, parsed
}

func parsePingOutput(output string) (string, string, bool) {
	average := ""
	if match := linuxPingAverage.FindStringSubmatch(output); len(match) == 2 {
		if value, err := strconv.ParseFloat(match[1], 64); err == nil {
			average = fmt.Sprintf("%.0fms", value)
		}
	}
	if average == "" {
		if match := windowsPingAverage.FindStringSubmatch(output); len(match) == 2 {
			average = match[1] + "ms"
		}
	}
	loss := ""
	if match := pingPacketLoss.FindStringSubmatch(output); len(match) == 2 {
		loss = match[1] + "%"
	}
	return average, loss, average != ""
}

func tcpLatency(ctx context.Context, host string, port int) time.Duration {
	started := time.Now()
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(
		dialCtx,
		"tcp",
		net.JoinHostPort(host, strconv.Itoa(port)),
	)
	if err != nil {
		return 0
	}
	_ = connection.Close()
	return time.Since(started)
}

func parsePingTarget(input string) (string, string, string, error) {
	input = strings.TrimSpace(input)
	if input == "" || len(input) > 253 ||
		strings.ContainsAny(input, " \t\r\n/\\") {
		return "", "", "", fmt.Errorf("%q", input)
	}
	if strings.HasPrefix(input, "dc") && len(input) == 3 {
		index, err := strconv.Atoi(input[2:])
		if err == nil && index >= 1 && index <= len(telegramDataCenters) {
			return input, telegramDataCenters[index-1].Address, "数据中心", nil
		}
	}
	if net.ParseIP(input) != nil {
		return input, input, "IP 地址", nil
	}
	for _, label := range strings.Split(input, ".") {
		if label == "" || len(label) > 63 ||
			strings.HasPrefix(label, "-") ||
			strings.HasSuffix(label, "-") {
			return "", "", "", fmt.Errorf("%q", input)
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') &&
				(character < '0' || character > '9') &&
				character != '-' {
				return "", "", "", fmt.Errorf("%q", input)
			}
		}
	}
	return input, input, "域名", nil
}

func formatTargetProbe(result targetProbe) string {
	var lines []string
	if result.DNSLatency > 0 {
		lines = append(lines, fmt.Sprintf(
			"🔍 <b>DNS 解析：</b> <code>%dms</code> → <code>%s</code>",
			result.DNSLatency.Milliseconds(),
			html.EscapeString(result.ResolvedIP),
		))
	}
	lines = append(lines, "🏓 <b>ICMP Ping：</b> <code>"+
		html.EscapeString(result.ICMP)+"</code>")
	if result.PacketLoss != "" {
		lines[len(lines)-1] += "（丢包 " + html.EscapeString(result.PacketLoss) + "）"
	}
	if result.TCP80 > 0 {
		lines = append(lines, fmt.Sprintf(
			"🌐 <b>TCP 连接 (80)：</b> <code>%dms</code>",
			result.TCP80.Milliseconds(),
		))
	}
	if result.TCP443 > 0 {
		lines = append(lines, fmt.Sprintf(
			"🔒 <b>TCP 连接 (443)：</b> <code>%dms</code>",
			result.TCP443.Milliseconds(),
		))
	}
	if result.HTTPS > 0 {
		lines = append(lines, fmt.Sprintf(
			"📡 <b>HTTPS 请求：</b> <code>%dms</code>",
			result.HTTPS.Milliseconds(),
		))
	}
	display := html.EscapeString(result.Input)
	if result.Input != result.Address {
		display += " → " + html.EscapeString(result.Address)
	}
	return "🎯 <b>" + result.Kind + "延迟测试</b>\n<code>" + display +
		"</code>\n\n" + strings.Join(lines, "\n") +
		"\n\n⏰ <i>" + time.Now().Format("2006/01/02 15:04:05") + "</i>"
}

func pingHelp(prefix string) string {
	prefix = html.EscapeString(prefix)
	return "<b>🏓 Ping 工具</b>\n\n" +
		"• <code>" + prefix + "ping</code>  Telegram API 与消息延迟\n" +
		"• <code>" + prefix + "ping all</code>  所有 Telegram 数据中心\n" +
		"• <code>" + prefix + "ping dc1</code>  指定数据中心\n" +
		"• <code>" + prefix + "ping 8.8.8.8</code>  IP 延迟\n" +
		"• <code>" + prefix + "ping example.com</code>  域名延迟\n\n" +
		"目标测试包含 DNS、ICMP、TCP 80/443 和 HTTPS；不可用的项目会自动跳过。"
}
