package main

import (
    "archive/tar"
    "bufio"
    "compress/gzip"
    "context"
    "errors"
    "fmt"
    "io"
    "io/fs"
    "net"
    "net/http"
    "os"
    "os/signal"
    "os/exec"
    "path/filepath"
    "regexp"
    "runtime"
    "strconv"
    "strings"
    "syscall"
    "time"
)

const (
	RESET  = "\033[0m"
	BLUE   = "\033[1;34m"
	GREEN  = "\033[1;32m"
	RED    = "\033[1;31m"
	YELLOW = "\033[1;33m"
	CYAN   = "\033[1;36m"
)

const (
	SCRIPT_VERSION                    = "GO_V1.0.0"
	REMOTE_SCRIPT_URL                 = "https://raw.githubusercontent.com/kilvil/oneclick_smartdns/main/smartdns_install.sh"
	REMOTE_STREAM_CONFIG_FILE_URL     = "https://raw.githubusercontent.com/kilvil/oneclick_smartdns/main/StreamConfig.yaml"
	REMOTE_SMARTDNS_URL               = "https://github.com/pymumu/smartdns/releases/download/Release46/smartdns.1.2024.06.12-2222.x86-linux-all.tar.gz"
	REMOTE_RegionRestrictionCheck_URL = "https://raw.githubusercontent.com/1-stream/RegionRestrictionCheck/main/check.sh"

	SMART_CONFIG_FILE = "/etc/smartdns/smartdns.conf"
	SNIPROXY_CONFIG   = "/etc/sniproxy.conf"
)

const defaultSmartDNSConfig = `bind [::]:53

dualstack-ip-selection no
speed-check-mode none
serve-expired-prefetch-time 21600
prefetch-domain yes
cache-size 32768
cache-persist yes
cache-file /etc/smartdns/cache
prefetch-domain yes
serve-expired yes
serve-expired-ttl 259200
serve-expired-reply-ttl 3
prefetch-domain yes
serve-expired-prefetch-time 21600
cache-checkpoint-time 86400

# 默认上游 DNS
server 8.8.8.8
server 8.8.4.4
`

func logGreen(s string)  { fmt.Printf("%s%s%s\n", GREEN, s, RESET) }
func logRed(s string)    { fmt.Printf("%s%s%s\n", RED, s, RESET) }
func logBlue(s string)   { fmt.Printf("%s%s%s\n", BLUE, s, RESET) }
func logYellow(s string) { fmt.Printf("%s%s%s\n", YELLOW, s, RESET) }
func logCyan(s string)   { fmt.Printf("%s%s%s\n", CYAN, s, RESET) }

func mustRoot() {
	if os.Geteuid() != 0 {
		logRed("[错误] 请以 root 权限运行此程序！")
		os.Exit(1)
	}
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if errors.Is(err, io.EOF) && len(line) > 0 {
		return strings.TrimSpace(line), nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func confirm(r *bufio.Reader, prompt string) bool {
	fmt.Print(prompt)
	s, _ := readLine(r)
	return strings.ToLower(s) == "y"
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runCmdInteractive(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runShellInteractive(shellLine string) error {
	sh := "/bin/bash"
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("bash"); err != nil {
			sh = "/bin/sh"
		}
	}
	return runCmdInteractive(sh, "-lc", shellLine)
}

func runCmdCapture(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func httpGetTimeout(url string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("http error: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func checkScriptUpdate(r *bufio.Reader) {
	logGreen("正在检查脚本更新...")
	body, err := httpGetTimeout(REMOTE_SCRIPT_URL, 10*time.Second)
	if err != nil {
		logYellow("无法获取到最新版本 (超时或网络问题). 可尝试急救还原DNS设置后再试。")
		return
	}
	re := regexp.MustCompile(`(?m)^SCRIPT_VERSION=\"([^\"]+)\"`)
	m := re.FindStringSubmatch(string(body))
	if len(m) < 2 {
		logYellow("远程脚本未包含版本信息。")
		return
	}
	remote := m[1]
	if remote != SCRIPT_VERSION {
		logGreen(fmt.Sprintf("发现新版本 (%s) ，当前版本 %s.", remote, SCRIPT_VERSION))
		if confirm(r, "是否打开更新页面? (y/N): ") {
			logYellow("请访问仓库手动更新 Go 版本程序或使用原 shell 脚本更新。")
		}
	} else {
		logGreen("当前脚本已为最新版本: " + SCRIPT_VERSION)
	}
}

var (
	reServerLine      = regexp.MustCompile(`^server\s+([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+)$`)
	reServerGroupLine = regexp.MustCompile(`^server\s+([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+)\s+IP\s+-group\s+(\S+)`)
)

func readLines(path string) ([]string, error) {
    b, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    s := strings.ReplaceAll(string(b), "\r\n", "\n")
    s = strings.ReplaceAll(s, "\r", "\n")
    return strings.Split(s, "\n"), nil
}

func writeLines(path string, lines []string) error {
	data := strings.Join(lines, "\n")
	if !strings.HasSuffix(data, "\n") {
		data += "\n"
	}
	return os.WriteFile(path, []byte(data), 0o644)
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

func ensureSmartDNSDir() error {
	return ensureDir(filepath.Dir(SMART_CONFIG_FILE))
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

func manageService(service, action, desc string) error {
	logCyan(fmt.Sprintf("正在%s %s 服务...", desc, service))
	if err := runCmdInteractive("systemctl", action, service); err != nil {
		logRed(fmt.Sprintf("%s %s失败，请检查系统日志。", service, desc))
		return err
	}
	logGreen(fmt.Sprintf("%s %s成功。", service, desc))
	return nil
}

func checkServiceStatus(service, serviceName string) {
	activeOut, _ := runCmdCapture("systemctl", "is-active", service)
	enabledOut, _ := runCmdCapture("systemctl", "is-enabled", service)
	isActive := strings.TrimSpace(activeOut) == "active"
	isEnabled := strings.TrimSpace(enabledOut) == "enabled"
	fmt.Printf("%s%s 服务状态：%s\n", CYAN, serviceName, RESET)
	if isActive {
		fmt.Printf("  运行状态: %s运行中%s\n", GREEN, RESET)
	} else {
		fmt.Printf("  运行状态: %s已停止%s\n", RED, RESET)
	}
	if isEnabled {
		fmt.Printf("  开机自启: %s已启用%s\n", GREEN, RESET)
	} else {
		fmt.Printf("  开机自启: %s未启用%s\n", RED, RESET)
	}
}

func restoreService(service string) {
	_ = manageService(service, "start", "启动")
	_ = manageService(service, "enable", "设置为开机启动")
}

func stopService(service string) {
	_ = manageService(service, "stop", "停止")
	_ = manageService(service, "disable", "关闭开机自启")
}

func checkSmartDNSStatus()  { checkServiceStatus("smartdns", "SmartDNS") }
func checkSystemDNSStatus() { checkServiceStatus("systemd-resolved", "系统 DNS") }
func checkSniproxyStatus()  { checkServiceStatus("sniproxy", "sniproxy") }

func restoreSystemDNS() {
	stopService("smartdns")
	restoreService("systemd-resolved")
	logGreen("系统 DNS 服务已启动并设置为开机启动。")
}

func restoreSniproxy() {
	restoreService("sniproxy")
	logGreen("sniproxy 服务已启动并设置为开机启动。")
}

func startSmartDNS() {
	stopService("systemd-resolved")
	restoreService("smartdns")
	modifyResolv("127.0.0.1")
	logGreen("SmartDNS 服务已启动并设置为开机启动！")
}

func stopSystemDNS() {
	stopService("systemd-resolved")
	logGreen("系统 DNS 服务已停止并关闭开机自启。")
}

func stopSmartDNS() {
	stopService("smartdns")
	logGreen("SmartDNS 服务已停止并关闭开机自启。")
}

func stopSniproxy() {
	stopService("sniproxy")
	logGreen("sniproxy 服务已停止并关闭开机自启。")
}

func modifyResolv(ip string) {
	content := fmt.Sprintf("nameserver %s\n", ip)
	if err := os.WriteFile("/etc/resolv.conf", []byte(content), 0o644); err != nil {
		logRed("修改 /etc/resolv.conf 失败，请检查文件权限。")
		return
	}
	logGreen("/etc/resolv.conf 已成功修改为 nameserver " + ip)
}

func checkSmartDNSInstalled() bool {
	_, err := exec.LookPath("smartdns")
	if err == nil {
		logGreen("[已安装] 检测到 SmartDNS 已安装！")
		return true
	}
	logRed("[未安装] 未检测到 SmartDNS。")
	return false
}

func installSniproxy() {
	logBlue("安装 sniproxy...")
	if err := runCmdInteractive("apt-get", "update"); err != nil {
		logRed("apt-get update 失败，请检查网络或源配置。")
		return
	}
	if err := runCmdInteractive("apt-get", "install", "-y", "sniproxy"); err != nil {
		logRed("安装 sniproxy 失败，可能源中无该包或网络不可达。")
		return
	}
	restoreSniproxy()
	logGreen("sniproxy 安装完成。")
}

func downloadToFile(url, path string, timeout time.Duration) error {
	b, err := httpGetTimeout(url, timeout)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func extractTarGz(srcPath, dstDir string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		targetPath := filepath.Join(dstDir, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, fs.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fs.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		case tar.TypeSymlink:
			if err := os.Symlink(hdr.Linkname, targetPath); err != nil {
				return err
			}
        default:
        }
	}
	return nil
}

func installSmartDNS() {
	logBlue("正在安装 SmartDNS...")
	tmpDir := "/tmp/smartdns_install"
	_ = os.MkdirAll(tmpDir, 0o755)
    stopSystemDNS()

    tarName := filepath.Base(REMOTE_SMARTDNS_URL)
	tarPath := filepath.Join(tmpDir, tarName)
	if err := downloadToFile(REMOTE_SMARTDNS_URL, tarPath, 120*time.Second); err != nil {
		logRed("SmartDNS 安装包下载失败，请检查网络连接！")
		return
	}
    if err := extractTarGz(tarPath, tmpDir); err != nil {
		logRed("SmartDNS 安装包解压失败: " + err.Error())
		return
	}
    smartdnsDir := filepath.Join(tmpDir, "smartdns")
	installPath := filepath.Join(smartdnsDir, "install")
	if _, err := os.Stat(installPath); err != nil {
		logRed("未找到安装脚本: " + installPath)
		return
	}
	_ = os.Chmod(installPath, 0o755)
	if err := runCmdInteractive(installPath, "-i"); err != nil {
		logRed("SmartDNS 安装失败，请检查日志！")
		return
	}
	logGreen("SmartDNS 安装成功！")
}

type StreamConfig map[string]map[string][]string

func getScriptDir() string {
    if wd, err := os.Getwd(); err == nil {
        return wd
    }
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func streamConfigPath() string {
	d := getScriptDir()
	return filepath.Join(d, "StreamConfig.yaml")
}

func loadStreamConfig() (StreamConfig, error) {
	path := streamConfigPath()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	cfg := StreamConfig{}
	var top string
	var sub string
	for _, raw := range lines {
		l := strings.TrimRight(raw, " \t")
		if l == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
        if !strings.HasPrefix(l, " ") && strings.HasSuffix(l, ":") {
            top = strings.TrimSuffix(strings.TrimSpace(l), ":")
            if _, ok := cfg[top]; !ok {
                cfg[top] = map[string][]string{}
            }
			sub = ""
			continue
		}
        if strings.HasPrefix(l, "  ") && strings.HasSuffix(strings.TrimSpace(l), ":") {
            sub = strings.TrimSuffix(strings.TrimSpace(l), ":")
            if top == "" {
                continue
            }
			if _, ok := cfg[top][sub]; !ok {
				cfg[top][sub] = []string{}
			}
			continue
		}
        if strings.HasPrefix(l, "    - ") {
            if top == "" || sub == "" {
                continue
            }
            domain := strings.TrimSpace(strings.TrimPrefix(l, "- "))
            domain = strings.TrimSpace(strings.TrimPrefix(domain, "- "))
            domain = strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(l, "    - ")), "-")
            domain = strings.TrimSpace(strings.TrimPrefix(l, "    - "))
			domain = strings.TrimSpace(strings.TrimPrefix(domain, "- "))
			domain = strings.TrimSpace(strings.Trim(strings.TrimPrefix(l, "    - "), "\""))
            if domain == "" {
                parts := strings.SplitN(l, "-", 2)
                if len(parts) == 2 {
                    domain = strings.TrimSpace(parts[1])
                }
            }
			if domain != "" {
				cfg[top][sub] = append(cfg[top][sub], domain)
			}
		}
	}
	return cfg, nil
}

func checkFiles() bool {
	if !fileExists(SMART_CONFIG_FILE) {
		logRed("未找到 SmartDNS 配置文件：" + SMART_CONFIG_FILE)
		logCyan("请确保 SmartDNS 已安装。")
		return false
	}
	if !fileExists(streamConfigPath()) {
		logRed("未找到流媒体配置文件：" + streamConfigPath())
		if err := downloadStreamConfig(); err != nil {
			logRed("下载流媒体配置文件失败: " + err.Error())
			return false
		}
	}
	return true
}

func downloadStreamConfig() error {
	logCyan("正在下载流媒体配置配置文件...")
	return downloadToFile(REMOTE_STREAM_CONFIG_FILE_URL, streamConfigPath(), 30*time.Second)
}

func viewStreamingPlatforms(r *bufio.Reader) {
	if !checkFiles() {
		return
	}
	cfg, err := loadStreamConfig()
	if err != nil {
		logRed("读取 StreamConfig.yaml 失败: " + err.Error())
		return
	}
	logCyan("流媒体平台列表:")
	i := 1
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	for _, k := range keys {
		fmt.Printf("%d. %s\n", i, k)
		i++
	}
	if confirm(r, CYAN+"是否查看二级键内容？(y/N): "+RESET) {
		fmt.Print(CYAN + "请输入一级流媒体平台序号：" + RESET)
		s, _ := readLine(r)
		idx, _ := strconv.Atoi(s)
		if idx < 1 || idx > len(keys) {
			logRed("无效的序号！")
			return
		}
		top := keys[idx-1]
		logCyan("二级键内容：")
		j := 1
		for k := range cfg[top] {
			fmt.Printf("%d. %s\n", j, k)
			j++
		}
	}
}

func isPlatformAdded(platform string) bool {
	lines, err := readLines(SMART_CONFIG_FILE)
	if err != nil {
		return false
	}
	prefix := "#> " + platform
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

func addDomainRules(method string, domains []string, identifier, platform string) error {
	f, err := os.OpenFile(SMART_CONFIG_FILE, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
    if _, err := fmt.Fprintf(f, "#> %s %s\n", platform, identifier); err != nil {
        return err
    }
	for _, d := range domains {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		switch method {
		case "nameserver":
			if _, err := fmt.Fprintf(f, "nameserver /%s/%s\n", d, identifier); err != nil {
				return err
			}
		case "address":
			if _, err := fmt.Fprintf(f, "address /%s/%s\n", d, identifier); err != nil {
				return err
			}
		}
	}
    _, _ = f.WriteString("\n")
	logGreen(fmt.Sprintf("已成功将 %s 的域名添加为 %s 方式，并添加注释。", platform, method))
	return nil
}

func deletePlatformRules(platform string) error {
	lines, err := readLines(SMART_CONFIG_FILE)
	if err != nil {
		return err
	}
	startPrefix := "#> " + platform
	var out []string
	skipping := false
	for _, l := range lines {
		if !skipping && strings.HasPrefix(l, startPrefix) {
			skipping = true
			continue
		}
		if skipping {
            if strings.TrimSpace(l) == "" {
                skipping = false
                continue
            }
			continue
		}
		out = append(out, l)
	}
	return writeLines(SMART_CONFIG_FILE, out)
}

func modifyPlatformRules(r *bufio.Reader, platform string, domains []string) {
	logCyan("请选择新的添加方式：")
	fmt.Println(YELLOW + "1. nameserver方式" + RESET)
	fmt.Println(YELLOW + "2. address方式" + RESET)
	s, _ := readLine(r)
	switch s {
	case "1":
		viewUpstreamDNSGroups()
		fmt.Print(CYAN + "请输入已存在的 DNS 组名称（例如：us）：" + RESET)
		group, _ := readLine(r)
		if group == "" {
			logRed("指定的 DNS 组不存在或为空！请先创建组。")
			return
		}
		if err := deletePlatformRules(platform); err != nil {
			logRed("删除旧规则失败: " + err.Error())
			return
		}
		_ = addDomainRules("nameserver", domains, group, platform)
	case "2":
		viewUpstreamDNS()
		fmt.Print(CYAN + "请输入 DNS 服务器的 IP 地址（例如：11.22.33.44）：" + RESET)
		ip, _ := readLine(r)
		if net.ParseIP(ip) == nil {
			logRed("无效的 IP 地址，请重新输入！")
			return
		}
		if err := deletePlatformRules(platform); err != nil {
			logRed("删除旧规则失败: " + err.Error())
			return
		}
		_ = addDomainRules("address", domains, ip, platform)
	default:
		logRed("无效选择，请重新输入！")
	}
}

func addStreamingPlatform(r *bufio.Reader) {
	if !checkFiles() {
		return
	}
	if !confirm(r, BLUE+"是否需要添加一个流媒体平台？(y/N): "+RESET) {
		return
	}
	cfg, err := loadStreamConfig()
	if err != nil {
		logRed("读取配置失败: " + err.Error())
		return
	}
    topKeys := make([]string, 0, len(cfg))
	for k := range cfg {
		topKeys = append(topKeys, k)
	}
	for i, k := range topKeys {
		fmt.Printf("%d. %s\n", i+1, k)
	}
	fmt.Print(CYAN + "请输入一级流媒体平台序号：" + RESET)
	s, _ := readLine(r)
	idx, _ := strconv.Atoi(s)
	if idx < 1 || idx > len(topKeys) {
		logRed("无效的序号")
		return
	}
	top := topKeys[idx-1]
    subKeys := make([]string, 0, len(cfg[top]))
	for k := range cfg[top] {
		subKeys = append(subKeys, k)
	}
	for i, k := range subKeys {
		fmt.Printf("%d. %s\n", i+1, k)
	}
	fmt.Print(CYAN + "请输入二级流媒体平台（支持序号或名称，逗号分隔多个）：" + RESET)
	sel, _ := readLine(r)
	selectedSubs := resolveMultiSelection(sel, subKeys)
	if len(selectedSubs) == 0 {
		logRed("未选择有效的二级平台")
		return
	}

	fmt.Println(CYAN + "请选择添加方式：" + RESET)
	fmt.Println(YELLOW + "1. nameserver方式" + RESET)
	fmt.Println(YELLOW + "2. address方式" + RESET)
	s, _ = readLine(r)
	method := ""
	identifier := ""
	switch s {
	case "1":
		method = "nameserver"
		viewUpstreamDNSGroups()
		fmt.Print(CYAN + "请输入已存在的 DNS 组名称（例如：us）：" + RESET)
		group, _ := readLine(r)
		if group == "" {
			logRed("指定的 DNS 组不存在！请先创建组。")
			return
		}
		identifier = group
	case "2":
		method = "address"
		viewUpstreamDNS()
		fmt.Print(CYAN + "请输入 DNS 服务器的 IP 地址（例如：11.22.33.44）：" + RESET)
		ip, _ := readLine(r)
		if net.ParseIP(ip) == nil {
			logRed("无效的 IP 地址，请重新输入！")
			return
		}
		identifier = ip
	default:
		logRed("无效选择，请重新输入！")
		return
	}

	overwriteExisting := confirm(r, YELLOW+"如遇已存在的平台，是否覆盖其配置？(y/N) "+RESET)
	for _, sub := range selectedSubs {
		domains := cfg[top][sub]
		if len(domains) == 0 {
			logYellow("跳过无域名配置的：" + sub)
			continue
		}
		if isPlatformAdded(sub) {
			if !overwriteExisting {
				logYellow("已存在，跳过：" + sub)
				continue
			}
			if err := deletePlatformRules(sub); err != nil {
				logRed("删除旧规则失败：" + sub + " - " + err.Error())
				continue
			}
		}
		_ = addDomainRules(method, domains, identifier, sub)
	}
}

func addOneRegionStreamingPlatforms(r *bufio.Reader) {
	if !checkFiles() {
		return
	}
	if !confirm(r, BLUE+"是否需要添加一个区域的流媒体？(y/N): "+RESET) {
		return
	}
	cfg, err := loadStreamConfig()
	if err != nil {
		logRed("读取配置失败: " + err.Error())
		return
	}
	topKeys := make([]string, 0, len(cfg))
	for k := range cfg {
		topKeys = append(topKeys, k)
	}
	for i, k := range topKeys {
		fmt.Printf("%d. %s\n", i+1, k)
	}
	fmt.Print(CYAN + "请输入一级流媒体平台序号：" + RESET)
	s, _ := readLine(r)
	idx, _ := strconv.Atoi(s)
	if idx < 1 || idx > len(topKeys) {
		logRed("无效的序号")
		return
	}
	top := topKeys[idx-1]

	fmt.Println(CYAN + "请选择添加方式：" + RESET)
	fmt.Println(YELLOW + "1. nameserver方式" + RESET)
	fmt.Println(YELLOW + "2. address方式" + RESET)
	s, _ = readLine(r)
	switch s {
	case "1":
		viewUpstreamDNSGroups()
		fmt.Print(CYAN + "请输入已存在的 DNS 组名称（例如：us）：" + RESET)
		group, _ := readLine(r)
		if group == "" {
			logRed("指定的 DNS 组不存在！请先创建组。")
			return
		}
		for sub, domains := range cfg[top] {
			if len(domains) == 0 {
				continue
			}
			logCyan("正在为 " + sub + " 添加域名规则...")
			_ = addDomainRules("nameserver", domains, group, sub)
		}
		logGreen("已为 " + top + " 内所有二级流媒体添加 nameserver 方式。")
	case "2":
		fmt.Print(CYAN + "请输入 DNS 服务器的 IP 地址（例如：11.22.33.44）：" + RESET)
		ip, _ := readLine(r)
		if net.ParseIP(ip) == nil {
			logRed("无效的 IP 地址，请重新输入！")
			return
		}
		for sub, domains := range cfg[top] {
			if len(domains) == 0 {
				continue
			}
			logCyan("正在为 " + sub + " 添加域名规则...")
			_ = addDomainRules("address", domains, ip, sub)
		}
		logGreen("已为 " + top + " 内所有二级流媒体添加 address 方式。")
	default:
		logRed("无效选择，请重新输入！")
	}
}

func addAllStreamingPlatforms(r *bufio.Reader) {
	if !checkFiles() {
		return
	}
	if !confirm(r, RED+"确定要添加所有流媒体平台吗？ y/N "+RESET) {
		logCyan("已取消操作，返回主菜单。")
		return
	}
	logCyan("请选择添加方式：")
	fmt.Println(YELLOW + "1. nameserver方式" + RESET)
	fmt.Println(YELLOW + "2. address方式" + RESET)
	s, _ := readLine(r)
	cfg, err := loadStreamConfig()
	if err != nil {
		logRed("读取配置失败: " + err.Error())
		return
	}
	switch s {
	case "1":
		fmt.Print(CYAN + "请输入已存在的 DNS 组名称（例如：us）：" + RESET)
		group, _ := readLine(r)
		if group == "" {
			logRed("指定的 DNS 组不存在！请先创建组。")
			return
		}
		for _, subMap := range cfg {
			for sub, domains := range subMap {
				if len(domains) == 0 {
					continue
				}
				_, _ = fmt.Fprintln(os.Stdout, "#> "+sub)
				_ = addDomainRules("nameserver", domains, group, sub)
			}
		}
		logGreen("所有流媒体平台域名已添加为 nameserver 方式。")
	case "2":
		fmt.Print(CYAN + "请输入 DNS 服务器的 IP 地址（例如：11.22.33.44）：" + RESET)
		ip, _ := readLine(r)
		if net.ParseIP(ip) == nil {
			logRed("无效的 IP 地址，请重新输入！")
			return
		}
		for _, subMap := range cfg {
			for sub, domains := range subMap {
				if len(domains) == 0 {
					continue
				}
				_, _ = fmt.Fprintln(os.Stdout, "#> "+sub)
				_ = addDomainRules("address", domains, ip, sub)
			}
		}
		logGreen("所有流媒体平台域名已添加为 address 方式。")
	default:
		logRed("无效选择，请重新输入！")
	}
}

func viewAddedPlatforms() {
	fmt.Println(CYAN + "已添加的平台:" + RESET)
	lines, err := readLines(SMART_CONFIG_FILE)
	if err != nil {
		logYellow("暂无已添加的平台或无法读取配置。")
		return
	}
	seen := map[string]bool{}
	for _, l := range lines {
        if strings.HasPrefix(l, "#> ") {
            name := strings.TrimSpace(strings.TrimPrefix(l, "#> "))
			if !seen[name] {
				fmt.Println(name)
				seen[name] = true
			}
		}
	}
	if len(seen) == 0 {
		logYellow("暂无已添加的平台。")
	}
}

func addDomainToSniproxyTable(domain string) error {
	if !fileExists(SNIPROXY_CONFIG) {
		return fmt.Errorf("sniproxy 配置文件未找到: %s", SNIPROXY_CONFIG)
	}
	lines, err := readLines(SNIPROXY_CONFIG)
	if err != nil {
		return err
	}
	hasTable := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "table {" {
			hasTable = i
			break
		}
	}
	if hasTable == -1 {
		return fmt.Errorf("sniproxy 配置文件中的 table 块未找到")
	}

	pattern := ".*" + regexp.QuoteMeta(domain) + " *"
	for _, l := range lines {
		if strings.Contains(l, domain) && strings.HasPrefix(strings.TrimSpace(l), ".*") {
			return nil
		}
	}
	insert := "    .*" + domain + " *"
	newLines := append([]string{}, lines[:hasTable+1]...)
	newLines = append(newLines, insert)
	newLines = append(newLines, lines[hasTable+1:]...)
	if err := writeLines(SNIPROXY_CONFIG, newLines); err != nil {
		return err
	}
	logGreen("已添加域名：" + domain + " 到 table 块内")
    _ = pattern
	return nil
}

func addStreamingToSniproxy(platform, sub string) {
	cfg, err := loadStreamConfig()
	if err != nil {
		logRed("读取 StreamConfig.yaml 失败: " + err.Error())
		return
	}
	if sub != "" {
		logCyan(fmt.Sprintf("正在处理平台：%s -> %s", platform, sub))
		domains := cfg[platform][sub]
		if len(domains) == 0 {
			logYellow("未找到域名配置，跳过...")
			return
		}
		for _, d := range domains {
			_ = addDomainToSniproxyTable(d)
		}
		return
	}
    logCyan("正在处理一级平台：" + platform)
	for s, domains := range cfg[platform] {
		if len(domains) == 0 {
			continue
		}
		for _, d := range domains {
			_ = addDomainToSniproxyTable(d)
		}
        _ = s
	}
}

func addStreamingDomainsToSniproxy(r *bufio.Reader) {
	if !checkFiles() {
		return
	}
	logCyan("请选择操作：")
	fmt.Println(YELLOW + "1. 添加一个流媒体平台" + RESET)
	fmt.Println(YELLOW + "2. 添加一个区域内的所有流媒体平台" + RESET)
	s, _ := readLine(r)
	cfg, err := loadStreamConfig()
	if err != nil {
		logRed("读取配置失败: " + err.Error())
		return
	}
	switch s {
    case "1":
        topKeys := make([]string, 0, len(cfg))
		for k := range cfg {
			topKeys = append(topKeys, k)
		}
		for i, k := range topKeys {
			fmt.Printf("%d. %s\n", i+1, k)
		}
		fmt.Print(CYAN + "请输入一级流媒体平台序号：" + RESET)
		s, _ = readLine(r)
		idx, _ := strconv.Atoi(s)
		if idx < 1 || idx > len(topKeys) {
			logRed("无效的序号")
			return
		}
		top := topKeys[idx-1]
        subKeys := make([]string, 0, len(cfg[top]))
		for k := range cfg[top] {
			subKeys = append(subKeys, k)
		}
		for i, k := range subKeys {
			fmt.Printf("%d. %s\n", i+1, k)
		}
		fmt.Print(CYAN + "请输入二级流媒体平台（支持序号或名称，逗号分隔多个）：" + RESET)
		sel, _ := readLine(r)
		selectedSubs := resolveMultiSelection(sel, subKeys)
		if len(selectedSubs) == 0 {
			logRed("未选择有效的二级平台")
			return
		}
		for _, sub := range selectedSubs {
			addStreamingToSniproxy(top, sub)
		}
	case "2":
		topKeys := make([]string, 0, len(cfg))
		for k := range cfg {
			topKeys = append(topKeys, k)
		}
		for i, k := range topKeys {
			fmt.Printf("%d. %s\n", i+1, k)
		}
		fmt.Print(CYAN + "请输入一级流媒体平台序号：" + RESET)
		s, _ = readLine(r)
		idx, _ := strconv.Atoi(s)
		if idx < 1 || idx > len(topKeys) {
			logRed("无效的序号")
			return
		}
		addStreamingToSniproxy(topKeys[idx-1], "")
	default:
		logRed("无效选择，请重新输入！")
	}
}

func resolveMultiSelection(input string, options []string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	tokens := strings.Split(input, ",")
	var out []string
    byName := map[string]string{}
	for _, v := range options {
		byName[strings.ToLower(strings.TrimSpace(v))] = v
	}
	for _, t := range tokens {
		tok := strings.TrimSpace(t)
		if tok == "" {
			continue
		}
        if n, err := strconv.Atoi(tok); err == nil {
            if n >= 1 && n <= len(options) {
                out = append(out, options[n-1])
            }
            continue
        }
        if v, ok := byName[strings.ToLower(tok)]; ok {
            out = append(out, v)
        }
    }
    uniq := make([]string, 0, len(out))
	seen := map[string]bool{}
	for _, v := range out {
		if !seen[v] {
			uniq = append(uniq, v)
			seen[v] = true
		}
	}
	return uniq
}

func checkAndEnableUFW(r *bufio.Reader) bool {
	if _, err := exec.LookPath("ufw"); err != nil {
		logYellow("未检测到 UFW 防火墙。是否安装 UFW？(y/N):")
		if !confirm(r, "") {
			logRed("UFW 未安装，无法继续操作。")
			return false
		}
		_ = runCmdInteractive("apt-get", "update")
		if err := runCmdInteractive("apt-get", "install", "-y", "ufw"); err != nil {
			logRed("安装 UFW 失败。")
			return false
		}
		logYellow("确保已开放 SSH 的 22 端口，否则可能无法远程访问！正在开放端口 22...")
		_ = runCmdInteractive("ufw", "allow", "22")
		logGreen("已成功开放 22 端口。")
	}
	out, _ := runCmdCapture("ufw", "status")
	if !strings.Contains(out, "Status: active") && !strings.Contains(out, "active") {
		logYellow("UFW 未启动。是否启动 UFW？(y/N):")
		if !confirm(r, "") {
			logRed("UFW 未启动，无法继续操作。")
			return false
		}
		if err := runCmdInteractive("ufw", "enable"); err != nil {
			logRed("启动 UFW 失败。")
			return false
		}
		logGreen("UFW 已成功启动！")
		logYellow("确保已开放 SSH 的 22 端口，否则可能无法远程访问！正在开放端口 22...")
		_ = runCmdInteractive("ufw", "allow", "22")
		logGreen("已成功开放 22 端口。")
	}
	return true
}

func unlockPorts(r *bufio.Reader) {
	if !checkAndEnableUFW(r) {
		return
	}
	logCyan("请输入被解锁机的 IP 地址：")
	ip, _ := readLine(r)
	if net.ParseIP(ip) == nil {
		logRed("无效的 IP 地址格式！")
		return
	}
	_ = runCmdInteractive("ufw", "allow", "from", ip, "to", "any", "port", "80", "proto", "tcp")
	_ = runCmdInteractive("ufw", "allow", "from", ip, "to", "any", "port", "80", "proto", "udp")
	_ = runCmdInteractive("ufw", "allow", "from", ip, "to", "any", "port", "443", "proto", "tcp")
	_ = runCmdInteractive("ufw", "allow", "from", ip, "to", "any", "port", "443", "proto", "udp")
	_ = runCmdInteractive("ufw", "allow", "from", ip, "to", "any", "port", "53", "proto", "udp")
	logGreen("已成功为 " + ip + " 开放以下端口：80、443、53（tcp & udp）")
}

func openCustomPort(r *bufio.Reader) {
	if !checkAndEnableUFW(r) {
		return
	}
	logCyan("请输入需要开放的端口号：")
	s, _ := readLine(r)
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		logRed("无效的端口号！请输入 1-65535 之间的数字。")
		return
	}
	_ = runCmdInteractive("ufw", "allow", fmt.Sprintf("%d/tcp", p))
	_ = runCmdInteractive("ufw", "allow", fmt.Sprintf("%d/udp", p))
	logGreen(fmt.Sprintf("已成功开放端口 %d（TCP 和 UDP）。", p))
	logGreen("ufw放开端口命令如下:")
	logYellow("sudo ufw allow from xx.xx.xx.xx to any port 53 proto udp")
}

func setGlobalDNS() {
	logCyan("正在将全局 DNS 修改为 8.8.8.8...")
	if _, err := os.Stat("/etc/resolv.conf"); err == nil {
		_ = runCmdInteractive("cp", "/etc/resolv.conf", "/etc/resolv.conf.bak")
		_ = os.Remove("/etc/resolv.conf")
		if err := os.WriteFile("/etc/resolv.conf", []byte("nameserver 8.8.8.8\n"), 0o644); err != nil {
			logRed("修改失败，请检查 /etc/resolv.conf 权限。")
			return
		}
		logGreen("成功将全局 DNS 修改为 8.8.8.8")
		return
	}
	logRed("/etc/resolv.conf 无法写入，请检查权限或手动更改 DNS。")
}

func regionTest() {
	_ = runShellInteractive("bash <(curl -L -s " + REMOTE_RegionRestrictionCheck_URL + ")")
}

func printBanner() {
	fmt.Println(BLUE + "======================================" + RESET)
	fmt.Println(GREEN + "     一键配置 SmartDNS 与 Sniproxy 脚本          " + RESET)
	fmt.Println(CYAN + "       版本：  " + SCRIPT_VERSION + "                " + RESET)
	fmt.Println(CYAN + "       更新时间：" + time.Now().Format("2006-01-02") + "         " + RESET)
	fmt.Println(CYAN + "       smartdns配置文件路径：" + SMART_CONFIG_FILE + "       " + RESET)
	fmt.Println(CYAN + "       sniproxy配置文件路径：" + SNIPROXY_CONFIG + "      " + RESET)
	fmt.Println(CYAN + "       流媒体列表：" + streamConfigPath() + " " + RESET)
	fmt.Println(BLUE + "======================================" + RESET)
	fmt.Println()
}

// ===== New Full-Screen TUI (vim-like) =====

type tuiMode int

const (
    modeTop tuiMode = iota
    modeSub
)

type tuiState struct {
    rows, cols int
    mode       tuiMode
    topKeys    []string
    subMap     map[string][]string // top -> subs
    cursor     int                 // cursor in current list
    scroll     int                 // scroll offset
    curTop     string              // current top for sub mode
    selected   map[string]bool     // key: top+"/"+sub
    help       bool
    method     string // "nameserver" or "address"
    ident      string // group name or ip
    sdActive   bool
}

func isSmartDNSActive() bool {
    out, _ := runCmdCapture("systemctl", "is-active", "smartdns")
    return strings.TrimSpace(out) == "active"
}

func getTermSize() (int, int) {
    out, err := runCmdCapture("stty", "size")
    if err != nil {
        return 24, 80
    }
    parts := strings.Fields(strings.TrimSpace(out))
    if len(parts) != 2 {
        return 24, 80
    }
    r, _ := strconv.Atoi(parts[0])
    c, _ := strconv.Atoi(parts[1])
    if r <= 0 || c <= 0 {
        return 24, 80
    }
    return r, c
}

func enableRaw() { _ = runCmdInteractive("stty", "-echo", "raw") }
func disableRaw() { _ = runCmdInteractive("stty", "sane") }

func clearScreen() { fmt.Print("\x1b[2J\x1b[H") }
func moveHome()    { fmt.Print("\x1b[H") }

func invertOn()  { fmt.Print("\x1b[7m") }
func invertOff() { fmt.Print("\x1b[27m") }

func (s *tuiState) visibleArea() int {
    // Reserve header/footer lines
    h := s.rows - 8
    if h < 5 {
        h = 5
    }
    return h
}

func (s *tuiState) clampCursor(listLen int) {
    if listLen == 0 {
        s.cursor, s.scroll = 0, 0
        return
    }
    if s.cursor < 0 {
        s.cursor = 0
    }
    if s.cursor >= listLen {
        s.cursor = listLen - 1
    }
    vis := s.visibleArea()
    if s.cursor < s.scroll {
        s.scroll = s.cursor
    }
    if s.cursor >= s.scroll+vis {
        s.scroll = s.cursor - vis + 1
    }
    if s.scroll < 0 {
        s.scroll = 0
    }
}

func (s *tuiState) renderHeader() {
    fmt.Printf("%sSmartDNS 解锁编辑器%s %s(%s)%s\n", BLUE, RESET, CYAN, SCRIPT_VERSION, RESET)
    status := "未运行"
    color := RED
    if s.sdActive {
        status = "运行中"
        color = GREEN
    }
    fmt.Printf("SmartDNS: %s%s%s  模式: %s  标识: %s\n", color, status, RESET, s.method, s.ident)
    fmt.Printf("配置: %s  列表: %s\n", SMART_CONFIG_FILE, streamConfigPath())
}

func padToWidth(s string, w int) string {
    if len([]rune(s)) >= w {
        return s
    }
    return s + strings.Repeat(" ", w-len([]rune(s)))
}

func (st *tuiState) renderList(items []string, checkbox bool) {
    vis := st.visibleArea()
    start := st.scroll
    end := start + vis
    if end > len(items) {
        end = len(items)
    }
    for i := start; i < end; i++ {
        line := items[i]
        prefix := "  "
        if st.mode == modeTop {
            prefix = " 地区 "
        }
        if checkbox {
            key := st.curTop + "/" + line
            mark := "[ ]"
            if st.selected[key] {
                mark = "[x]"
            }
            prefix = mark + " "
        }
        content := prefix + line
        if i == st.cursor {
            invertOn()
            fmt.Print(padToWidth(content, st.cols))
            invertOff()
        } else {
            fmt.Print(padToWidth(content, st.cols))
        }
        fmt.Print("\n")
    }
    // Fill remaining lines to occupy the screen
    for i := 0; i < vis-(end-start); i++ {
        fmt.Println(strings.Repeat(" ", st.cols))
    }
}

func (s *tuiState) renderFooter() {
    fmt.Println(strings.Repeat("-", s.cols))
    if s.help {
        fmt.Println("键位: ↑/↓/j/k 移动  →/l 进入  ←/h 返回  空格 勾选  m 切换方式  e 编辑标识  s 保存  q 退出")
    } else {
        fmt.Println("按 ? 查看帮助  |  m 切换 nameserver/address  |  e 编辑组名/地址  |  s 保存  |  q 退出")
    }
    // Pad remaining lines to ensure full-screen render
    // Approximate used: header(3) + rule(1) + list(visibleArea) + footer(2)
    used := 3 + 1 + s.visibleArea() + 2
    for i := 0; i < s.rows-used; i++ {
        fmt.Println(strings.Repeat(" ", s.cols))
    }
}

func (s *tuiState) render(cfg StreamConfig) {
    clearScreen()
    moveHome()
    s.rows, s.cols = getTermSize()
    s.renderHeader()
    fmt.Println(strings.Repeat("-", s.cols))
    switch s.mode {
    case modeTop:
        fmt.Println(CYAN + "一级流媒体（右键或 l 进入二级）" + RESET)
        s.renderList(s.topKeys, false)
    case modeSub:
        fmt.Printf(CYAN+"%s -> 二级流媒体（空格 勾选/取消）"+RESET+"\n", s.curTop)
        subs := s.subMap[s.curTop]
        s.renderList(subs, true)
    }
    s.renderFooter()
}

func (s *tuiState) currentItems() []string {
    if s.mode == modeTop {
        return s.topKeys
    }
    return s.subMap[s.curTop]
}

func (s *tuiState) toggleCurrent() {
    if s.mode != modeSub {
        return
    }
    items := s.currentItems()
    if len(items) == 0 {
        return
    }
    sub := items[s.cursor]
    key := s.curTop + "/" + sub
    s.selected[key] = !s.selected[key]
}

func readKey() (string, error) {
    buf := make([]byte, 3)
    n, err := os.Stdin.Read(buf)
    if err != nil {
        return "", err
    }
    if n == 1 {
        switch buf[0] {
        case 'k':
            return "up", nil
        case 'j':
            return "down", nil
        case 'h':
            return "left", nil
        case 'l':
            return "right", nil
        case ' ': // space
            return "space", nil
        case 's', 'S':
            return "save", nil
        case 'm', 'M':
            return "mode", nil
        case 'e', 'E':
            return "edit", nil
        case 'q', 'Q':
            return "quit", nil
        case '?':
            return "help", nil
        case '\r', '\n':
            return "enter", nil
        default:
            return string(buf[:1]), nil
        }
    }
    if n >= 3 && buf[0] == 0x1b && buf[1] == '[' {
        switch buf[2] {
        case 'A':
            return "up", nil
        case 'B':
            return "down", nil
        case 'C':
            return "right", nil
        case 'D':
            return "left", nil
        }
    }
    return "", nil
}

func promptLine(prompt string) string {
    // temporarily disable raw for line input
    disableRaw()
    defer enableRaw()
    fmt.Print("\n" + YELLOW + prompt + RESET + " ")
    r := bufio.NewReader(os.Stdin)
    s, _ := r.ReadString('\n')
    return strings.TrimSpace(s)
}

func (s *tuiState) saveSelection(cfg StreamConfig) string {
    if s.method != "nameserver" && s.method != "address" {
        return "请选择正确的添加方式(m)"
    }
    if strings.TrimSpace(s.ident) == "" {
        return "请设置组名或IP (e)"
    }
    count := 0
    for key, on := range s.selected {
        if !on {
            continue
        }
        parts := strings.SplitN(key, "/", 2)
        if len(parts) != 2 {
            continue
        }
        top := parts[0]
        sub := parts[1]
        domains := cfg[top][sub]
        if len(domains) == 0 {
            continue
        }
        _ = deletePlatformRules(sub)
        _ = addDomainRules(s.method, domains, s.ident, sub)
        count++
    }
    if count == 0 {
        return "未选择任何平台，无需保存"
    }
    if s.sdActive {
        ans := strings.ToLower(promptLine("是否重启 SmartDNS 应用新配置? (y/N)"))
        if ans == "y" {
            _ = runCmdInteractive("systemctl", "restart", "smartdns")
        }
    }
    return fmt.Sprintf("保存成功，已写入 %d 个平台", count)
}

func initSelectionFromConfig(sel map[string]bool, cfg StreamConfig, topKeys []string) {
    // Pre-select items that already exist in smartdns.conf (by marker line)
    lines, err := readLines(SMART_CONFIG_FILE)
    if err != nil {
        return
    }
    present := map[string]bool{}
    for _, l := range lines {
        if strings.HasPrefix(l, "#> ") {
            name := strings.TrimSpace(strings.TrimPrefix(l, "#> "))
            // comment may include identifier; keep first token as platform(sub)
            fields := strings.Fields(name)
            if len(fields) > 0 {
                present[fields[0]] = true
            }
        }
    }
    for _, top := range topKeys {
        for _, sub := range cfg[top] {
            _ = sub // not used here
        }
        for sub := range cfg[top] {
            if present[sub] {
                sel[top+"/"+sub] = true
            }
        }
    }
}

func runTUI() {
    // Load data
    if !fileExists(streamConfigPath()) {
        _ = downloadStreamConfig()
    }
    cfg, err := loadStreamConfig()
    if err != nil {
        logRed("读取 StreamConfig.yaml 失败: " + err.Error())
        return
    }
    topKeys := make([]string, 0, len(cfg))
    for k := range cfg {
        topKeys = append(topKeys, k)
    }
    // simple sort for stable order
    // (no external deps)
    for i := 0; i < len(topKeys); i++ {
        for j := i + 1; j < len(topKeys); j++ {
            if strings.Compare(topKeys[i], topKeys[j]) > 0 {
                topKeys[i], topKeys[j] = topKeys[j], topKeys[i]
            }
        }
    }
    subMap := map[string][]string{}
    for _, top := range topKeys {
        subs := make([]string, 0, len(cfg[top]))
        for s := range cfg[top] {
            subs = append(subs, s)
        }
        for i := 0; i < len(subs); i++ {
            for j := i + 1; j < len(subs); j++ {
                if strings.Compare(subs[i], subs[j]) > 0 {
                    subs[i], subs[j] = subs[j], subs[i]
                }
            }
        }
        subMap[top] = subs
    }

    st := &tuiState{
        mode:     modeTop,
        topKeys:  topKeys,
        subMap:   subMap,
        selected: map[string]bool{},
        method:   "nameserver",
        ident:    "",
        sdActive: isSmartDNSActive(),
    }
    // Preselect existing ones
    initSelectionFromConfig(st.selected, cfg, topKeys)

    // Raw mode
    enableRaw()
    // handle Ctrl-C / SIGTERM to restore terminal state
    sigc := make(chan os.Signal, 1)
    signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
    defer func() {
        signal.Stop(sigc)
        disableRaw()
    }()
    go func() {
        <-sigc
        disableRaw()
        os.Exit(1)
    }()

    // Render loop
    st.rows, st.cols = getTermSize()
    st.render(cfg)
    for {
        key, err := readKey()
        if err != nil {
            break
        }
        switch key {
        case "up":
            st.cursor--
        case "down":
            st.cursor++
        case "left":
            if st.mode == modeSub {
                st.mode = modeTop
                st.cursor, st.scroll = 0, 0
            }
        case "right", "enter":
            if st.mode == modeTop {
                if len(st.topKeys) > 0 {
                    st.curTop = st.topKeys[st.cursor]
                    st.mode = modeSub
                    st.cursor, st.scroll = 0, 0
                }
            }
        case "space":
            st.toggleCurrent()
        case "mode":
            if st.method == "nameserver" {
                st.method = "address"
            } else {
                st.method = "nameserver"
            }
        case "edit":
            if st.method == "nameserver" {
                st.ident = promptLine("请输入 DNS 组名称 (例如: us)")
            } else {
                st.ident = promptLine("请输入 DNS 服务器IP (例如: 11.22.33.44)")
            }
        case "save":
            msg := st.saveSelection(cfg)
            // Show message one-shot
            clearScreen(); moveHome()
            fmt.Println(GREEN + msg + RESET)
            fmt.Println("按任意键继续...")
            _ , _ = readKey()
        case "help":
            st.help = !st.help
        case "quit":
            return
        }
        st.clampCursor(len(st.currentItems()))
        st.render(cfg)
    }
}

func main() {
    mustRoot()
    runTUI()
}
