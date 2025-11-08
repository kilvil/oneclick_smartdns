package src

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	reServerLine      = regexp.MustCompile(`^server\s+([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+)$`)
	reServerGroupLine = regexp.MustCompile(`^server\s+([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+)\s+IP\s+-group\s+(\S+)`)
)

func deleteGroupFromConfig(name string) error {
	if !fileExists(SMART_CONFIG_FILE) {
		return fmt.Errorf("配置文件不存在: %s", SMART_CONFIG_FILE)
	}
	lines, err := readLines(SMART_CONFIG_FILE)
	if err != nil {
		return err
	}
	var out []string
	removed := false
	for _, l := range lines {
		if m := reServerGroupLine.FindStringSubmatch(l); len(m) == 3 {
			if strings.EqualFold(m[2], name) {
				removed = true
				continue
			}
		}
		out = append(out, l)
	}
	if !removed {
		return fmt.Errorf("未找到分组 %s", name)
	}
	return writeLines(SMART_CONFIG_FILE, out)
}

func insertServerIntoConfig(serverLine, configFile string) error {
	if !fileExists(configFile) {
		return fmt.Errorf("配置文件不存在: %s", configFile)
	}
	lines, err := readLines(configFile)
	if err != nil {
		return err
	}
	last := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "server ") {
			last = i
		}
	}
	if last >= 0 {
		newLines := append([]string{}, lines[:last+1]...)
		newLines = append(newLines, serverLine)
		newLines = append(newLines, lines[last+1:]...)
		return writeLines(configFile, newLines)
	}
	newLines := append([]string{serverLine}, lines...)
	if err := writeLines(configFile, newLines); err != nil {
		return err
	}
	logYellow("未找到 server 条目，新条目已插入到文件开头: " + serverLine)
	return nil
}

func viewUpstreamDNS() {
	fmt.Println(CYAN + "当前配置的上游 DNS 列表：" + RESET)
	lines, err := readLines(SMART_CONFIG_FILE)
	if err != nil {
		logYellow("暂无配置的上游 DNS 或无法读取配置文件。")
		return
	}
	found := false
	for _, l := range lines {
		if reServerLine.MatchString(l) {
			fmt.Println(l)
			found = true
		}
	}
	if !found {
		logYellow("暂无配置的上游 DNS。")
	}
}

func viewUpstreamDNSGroups() {
	fmt.Println(CYAN + "当前配置的上游 DNS 组：" + RESET)
	lines, err := readLines(SMART_CONFIG_FILE)
	if err != nil {
		logYellow("暂无配置的上游 DNS 组或无法读取配置。")
		return
	}
	found := false
	for _, l := range lines {
		if m := reServerGroupLine.FindStringSubmatch(l); len(m) == 3 {
			fmt.Printf("%s %s\n", m[1], m[2])
			found = true
		}
	}
	if !found {
		logYellow("暂无配置的上游 DNS 组。")
	}
}

func ensureSmartDNSDir() error { return ensureDir(filepath.Dir(SMART_CONFIG_FILE)) }

// parseDefaultServers returns ordered plain 'server <ip>' entries from config.
func parseDefaultServers() []string {
	var out []string
	lines, err := readLines(SMART_CONFIG_FILE)
	if err != nil {
		return out
	}
	for _, l := range lines {
		if m := reServerLine.FindStringSubmatch(strings.TrimSpace(l)); len(m) == 2 {
			out = append(out, m[1])
		}
	}
	return out
}

// setDefaultServers replaces all plain 'server <ip>' lines with the provided ordered list.
func setDefaultServers(ips []string) error {
	lines, err := readLines(SMART_CONFIG_FILE)
	if err != nil {
		return err
	}
	// filter out existing plain server lines
	var rest []string
	for _, l := range lines {
		if reServerLine.MatchString(strings.TrimSpace(l)) {
			// skip
			continue
		}
		rest = append(rest, l)
	}
	// de-duplicate while preserving order
	seen := map[string]bool{}
	var newLines []string
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		if seen[ip] {
			continue
		}
		seen[ip] = true
		newLines = append(newLines, "server "+ip)
	}
	// prepend default servers at the beginning for determinism
	newLines = append(newLines, rest...)
	return writeLines(SMART_CONFIG_FILE, newLines)
}

func addDefaultServer(ip string) error {
	ip = strings.TrimSpace(ip)
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("无效IP: %s", ip)
	}
	current := parseDefaultServers()
	for _, v := range current {
		if strings.EqualFold(v, ip) {
			return nil
		}
	}
	current = append(current, ip)
	return setDefaultServers(current)
}

func removeDefaultServerAt(idx int) error {
	current := parseDefaultServers()
	if idx < 0 || idx >= len(current) {
		return fmt.Errorf("索引越界")
	}
	next := append([]string{}, current[:idx]...)
	next = append(next, current[idx+1:]...)
	return setDefaultServers(next)
}

// ensureSmartDNSBaseDirectives appends a set of recommended SmartDNS directives
// to SMART_CONFIG_FILE if the exact desired lines are not already present.
// When appending, these lines are placed at the end of file so they take
// precedence over earlier conflicting values (SmartDNS uses last-one-wins).
func ensureSmartDNSBaseDirectives() error {
    if err := ensureSmartDNSDir(); err != nil { return err }
    req := []string{
        "dualstack-ip-selection no",
        "speed-check-mode none",
        "serve-expired-prefetch-time 21600",
        "prefetch-domain yes",
        "cache-size 32768",
        "cache-persist yes",
        "cache-file /etc/smartdns/cache",
        "serve-expired yes",
        "serve-expired-ttl 259200",
        "serve-expired-reply-ttl 3",
        "cache-checkpoint-time 86400",
    }
    if !fileExists(SMART_CONFIG_FILE) {
        // create with default template which already contains these
        return os.WriteFile(SMART_CONFIG_FILE, []byte(defaultSmartDNSConfig), 0o644)
    }
    lines, err := readLines(SMART_CONFIG_FILE)
    if err != nil { return err }
    have := map[string]bool{}
    for _, l := range lines { have[strings.TrimSpace(l)] = true }
    toAdd := []string{}
    for _, want := range req {
        if !have[want] { toAdd = append(toAdd, want) }
    }
    if len(toAdd) == 0 { return nil }
    // append a blank line then desired directives
    out := append(lines, "")
    out = append(out, toAdd...)
    return writeLines(SMART_CONFIG_FILE, out)
}

func configureSmartDNS(r *bufio.Reader) {
	logBlue("正在配置 SmartDNS...")
	if err := ensureSmartDNSDir(); err != nil {
		logRed("创建 SmartDNS 目录失败: " + err.Error())
		return
	}
	if err := os.WriteFile(SMART_CONFIG_FILE, []byte(defaultSmartDNSConfig), 0o644); err != nil {
		logRed("写入默认配置失败: " + err.Error())
		return
	}
	logGreen("默认配置文件已生成：" + SMART_CONFIG_FILE)
	addUpstreamDNSGroup(r)
	logGreen("SmartDNS 配置完成！")
}

func addUpstreamDNSGroup(r *bufio.Reader) {
	for {
		if !confirm(r, BLUE+"是否需要添加自定义上游组 DNS？(y/N): "+RESET) {
			return
		}
		fmt.Print(BLUE + "请输入上游 DNS 的 IP 地址（格式：11.22.33.44）：" + RESET)
		ip, _ := readLine(r)
		if net.ParseIP(ip) == nil {
			logRed("无效的 IP 地址，请重新输入！")
			continue
		}
		fmt.Print(BLUE + "请输入该组的名称（例如：us）：" + RESET)
		group, _ := readLine(r)
		if strings.TrimSpace(group) == "" {
			logRed("组名称不能为空，请重新输入！")
			continue
		}
		line := fmt.Sprintf("server %s IP -group %s -exclude-default-group", ip, group)
		if err := insertServerIntoConfig(line, SMART_CONFIG_FILE); err != nil {
			logRed("写入失败: " + err.Error())
			return
		}
		logGreen("已成功添加上游 DNS：" + line)
	}
}
