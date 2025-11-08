package src

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type StreamConfig map[string]map[string][]string

type Assignment struct {
	Method string // "nameserver" or "address"
	Ident  string // group name or IP
}

// parseAssignments reads SMART_CONFIG_FILE and extracts per-sub assignment from
// our managed blocks that start with "#> <sub> <ident>" and followed by
// nameserver/address lines until a blank line.
func parseAssignments() map[string]Assignment {
	out := map[string]Assignment{}
	lines, err := readLines(SMART_CONFIG_FILE)
	if err != nil {
		return out
	}
	var curSub string
	var curIdent string
	inBlock := false
	for _, l := range lines {
		if strings.HasPrefix(l, "#> ") {
			// start a new block
			inBlock = true
			curSub = ""
			curIdent = ""
			rest := strings.TrimSpace(strings.TrimPrefix(l, "#> "))
			if rest != "" {
				// first token is sub name; rest is ident (may be empty)
				fields := strings.Fields(rest)
				if len(fields) > 0 {
					curSub = fields[0]
					if len(fields) > 1 {
						curIdent = strings.Join(fields[1:], " ")
					}
				}
			}
			// initialize with unknown method; will be set by following lines
			if curSub != "" {
				out[curSub] = Assignment{Method: "", Ident: curIdent}
			}
			continue
		}
		if inBlock {
			if strings.TrimSpace(l) == "" {
				// end of block
				inBlock = false
				curSub = ""
				curIdent = ""
				continue
			}
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "nameserver ") || strings.HasPrefix(t, "address ") {
				if curSub != "" {
					a := out[curSub]
					if strings.HasPrefix(t, "nameserver ") {
						a.Method = "nameserver"
					} else {
						a.Method = "address"
					}
					// keep ident from comment (group or IP)
					out[curSub] = a
				}
			}
		}
	}
	return out
}

func streamConfigPath() string {
	d := getScriptDir()
	return filepath.Join(d, "StreamConfig.yaml")
}

// lightweight parser for the existing StreamConfig.yaml structure
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
			domain := strings.TrimSpace(strings.TrimPrefix(l, "    - "))
			domain = strings.Trim(domain, "\"")
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

// --- legacy CLI helpers kept for completeness (unused by TUI) ---

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
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if n, ok := byName[t]; ok {
			out = append(out, n)
			continue
		}
		if idx, err := strconv.Atoi(t); err == nil {
			if idx >= 1 && idx <= len(options) {
				out = append(out, options[idx-1])
			}
		}
	}
	return out
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

// syncSubToSniproxy ensures all domains defined for (top/sub) exist in sniproxy table.
func syncSubToSniproxy(cfg StreamConfig, top, sub string) error {
	if cfg == nil {
		return fmt.Errorf("cfg 为空")
	}
	subMap, ok := cfg[top]
	if !ok {
		return fmt.Errorf("未找到一级平台 %s", top)
	}
	domains, ok := subMap[sub]
	if !ok {
		return fmt.Errorf("未找到二级平台 %s/%s", top, sub)
	}
    // 合并一次性写入，避免重复重写
    return addDomainsToSniproxyTables(domains)
}

// sniproxy table injection helpers (write into named tables http_hosts / https_hosts)
// Ensure only one block exists for each named table; merge and dedupe entries; ignore sample lines.
func addDomainToSniproxyTable(domain string) error { return addDomainsToSniproxyTables([]string{domain}) }

func addDomainsToSniproxyTables(domains []string) error {
    if !fileExists(SNIPROXY_CONFIG) {
        return fmt.Errorf("sniproxy 配置文件未找到: %s", SNIPROXY_CONFIG)
    }
    lines, err := readLines(SNIPROXY_CONFIG)
    if err != nil {
        return err
    }

    // Build normalized set from input
    norm := func(d string) string {
        d = strings.TrimSpace(d)
        if d == "" { return "" }
        // keep consistent with existing behavior: raw domain, prefix .* and suffix *
        return ".*" + d + " *"
    }
    addSet := map[string]struct{}{}
    addOrder := []string{}
    for _, d := range domains {
        n := norm(d)
        if n == "" { continue }
        if _, ok := addSet[n]; !ok {
            addSet[n] = struct{}{}
            addOrder = append(addOrder, n)
        }
    }

    updateTable := func(name string, lines []string) ([]string, error) {
        // Collect existing managed lines from all same-named table blocks, then remove them
        var out []string
        out = make([]string, 0, len(lines))
        existing := []string{}
        // helper to test if a line is our managed pattern entry
        isManaged := func(t string) bool {
            t = strings.TrimSpace(t)
            // managed pattern style: ".*<domain> *"
            return strings.HasPrefix(t, ".*") && strings.HasSuffix(t, " *")
        }
        in := false
        depth := 0
        for i := 0; i < len(lines); i++ {
            t := strings.TrimSpace(lines[i])
            if !in {
                if strings.HasPrefix(t, "table "+name) && strings.Contains(t, "{") {
                    // enter block, skip writing header now (we'll reconstruct later)
                    in = true
                    depth = 0
                    // count braces on same line
                    brace := strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
                    depth += brace
                    if depth <= 0 {
                        // single-line malformed block; skip it entirely
                        in = false
                    }
                    continue
                }
                out = append(out, lines[i])
                continue
            }
            // inside target table block; accumulate managed entries, skip all until we close this block
            tt := strings.TrimSpace(lines[i])
            if isManaged(tt) {
                existing = append(existing, tt)
            }
            // track depth to find end
            brace := strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
            depth += brace
            if depth <= 0 {
                // end of this table block
                in = false
            }
            // do not copy original lines
        }

        // Deduplicate while preserving existing order, then append new ones
        seen := map[string]struct{}{}
        merged := []string{}
        for _, e := range existing {
            if _, ok := seen[e]; ok { continue }
            seen[e] = struct{}{}
            merged = append(merged, e)
        }
        for _, e := range addOrder {
            if _, ok := seen[e]; ok { continue }
            seen[e] = struct{}{}
            merged = append(merged, e)
        }

        // Reconstruct single canonical block and append to end of file
        block := []string{"table "+name+" {"}
        for _, m := range merged {
            block = append(block, "    "+m)
        }
        block = append(block, "}")
        // ensure file ends with a newline before appending a block for readability
        if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
            out = append(out, "")
        }
        out = append(out, block...)
        return out, nil
    }

    // Update http_hosts then https_hosts sequentially
    nl, err := updateTable("http_hosts", lines)
    if err != nil { return err }
    nl2, err := updateTable("https_hosts", nl)
    if err != nil { return err }
    if err := writeLines(SNIPROXY_CONFIG, nl2); err != nil {
        return err
    }
    logGreen("已同步域名到 sniproxy 的 table http_hosts/https_hosts")
    return nil
}

// misc utilities retained
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
