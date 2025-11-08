package src

import (
    "bufio"
    "fmt"
    "os"
    "path/filepath"
    "regexp"
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
    if !checkFiles() { return }
    cfg, err := loadStreamConfig()
    if err != nil { logRed("读取 StreamConfig.yaml 失败: " + err.Error()); return }
    logCyan("流媒体平台列表:")
    i := 1
    keys := make([]string, 0, len(cfg))
    for k := range cfg { keys = append(keys, k) }
    for _, k := range keys { fmt.Printf("%d. %s\n", i, k); i++ }
    if confirm(r, CYAN+"是否查看二级键内容？(y/N): "+RESET) {
        fmt.Print(CYAN + "请输入一级流媒体平台序号：" + RESET)
        s, _ := readLine(r)
        idx, _ := strconv.Atoi(s)
        if idx < 1 || idx > len(keys) { logRed("无效的序号！"); return }
        top := keys[idx-1]
        logCyan("二级键内容：")
        j := 1
        for k := range cfg[top] { fmt.Printf("%d. %s\n", j, k); j++ }
    }
}

func resolveMultiSelection(input string, options []string) []string {
    if strings.TrimSpace(input) == "" { return nil }
    tokens := strings.Split(input, ",")
    var out []string
    byName := map[string]string{}
    for _, v := range options { byName[strings.ToLower(strings.TrimSpace(v))] = v }
    for _, t := range tokens {
        t = strings.ToLower(strings.TrimSpace(t))
        if t == "" { continue }
        if n, ok := byName[t]; ok { out = append(out, n); continue }
        if idx, err := strconv.Atoi(t); err == nil {
            if idx >= 1 && idx <= len(options) { out = append(out, options[idx-1]) }
        }
    }
    return out
}

func addStreamingToSniproxy(platform, sub string) {
    cfg, err := loadStreamConfig()
    if err != nil { logRed("读取 StreamConfig.yaml 失败: " + err.Error()); return }
    if sub != "" {
        logCyan(fmt.Sprintf("正在处理平台：%s -> %s", platform, sub))
        domains := cfg[platform][sub]
        if len(domains) == 0 { logYellow("未找到域名配置，跳过..."); return }
        for _, d := range domains { _ = addDomainToSniproxyTable(d) }
        return
    }
    logCyan("正在处理一级平台：" + platform)
    for s, domains := range cfg[platform] {
        if len(domains) == 0 { continue }
        for _, d := range domains { _ = addDomainToSniproxyTable(d) }
        _ = s
    }
}

func addStreamingDomainsToSniproxy(r *bufio.Reader) {
    if !checkFiles() { return }
    logCyan("请选择操作：")
    fmt.Println(YELLOW + "1. 添加一个流媒体平台" + RESET)
    fmt.Println(YELLOW + "2. 添加一个区域内的所有流媒体平台" + RESET)
    s, _ := readLine(r)
    cfg, err := loadStreamConfig(); if err != nil { logRed("读取配置失败: " + err.Error()); return }
    switch s {
    case "1":
        topKeys := make([]string, 0, len(cfg)); for k := range cfg { topKeys = append(topKeys, k) }
        for i, k := range topKeys { fmt.Printf("%d. %s\n", i+1, k) }
        fmt.Print(CYAN + "请输入一级流媒体平台序号：" + RESET)
        s, _ = readLine(r)
        idx, _ := strconv.Atoi(s)
        if idx < 1 || idx > len(topKeys) { logRed("无效的序号"); return }
        top := topKeys[idx-1]
        subKeys := make([]string, 0, len(cfg[top])); for k := range cfg[top] { subKeys = append(subKeys, k) }
        for i, k := range subKeys { fmt.Printf("%d. %s\n", i+1, k) }
        fmt.Print(CYAN + "请输入二级流媒体平台（支持序号或名称，逗号分隔多个）：" + RESET)
        sel, _ := readLine(r)
        selectedSubs := resolveMultiSelection(sel, subKeys)
        if len(selectedSubs) == 0 { logRed("未选择有效的二级平台"); return }
        for _, sub := range selectedSubs { addStreamingToSniproxy(top, sub) }
    case "2":
        topKeys := make([]string, 0, len(cfg)); for k := range cfg { topKeys = append(topKeys, k) }
        for i, k := range topKeys { fmt.Printf("%d. %s\n", i+1, k) }
        fmt.Print(CYAN + "请输入一级流媒体平台序号：" + RESET)
        s, _ = readLine(r)
        idx, _ := strconv.Atoi(s)
        if idx < 1 || idx > len(topKeys) { logRed("无效的序号"); return }
        addStreamingToSniproxy(topKeys[idx-1], "")
    default:
        logRed("无效选择，请重新输入！")
    }
}

// sniproxy table injection helper
func addDomainToSniproxyTable(domain string) error {
    if !fileExists(SNIPROXY_CONFIG) {
        return fmt.Errorf("sniproxy 配置文件未找到: %s", SNIPROXY_CONFIG)
    }
    lines, err := readLines(SNIPROXY_CONFIG)
    if err != nil { return err }
    hasTable := -1
    for i, l := range lines { if strings.TrimSpace(l) == "table {" { hasTable = i; break } }
    if hasTable == -1 { return fmt.Errorf("sniproxy 配置文件中的 table 块未找到") }
    for _, l := range lines {
        if strings.Contains(l, domain) && strings.HasPrefix(strings.TrimSpace(l), ".*") { return nil }
    }
    insert := "    .*" + regexp.QuoteMeta(domain) + " *"
    // present config uses raw domain without QuoteMeta while matching; keep consistent with original behavior
    insert = "    .*" + domain + " *"
    newLines := append([]string{}, lines[:hasTable+1]...)
    newLines = append(newLines, insert)
    newLines = append(newLines, lines[hasTable+1:]...)
    if err := writeLines(SNIPROXY_CONFIG, newLines); err != nil { return err }
    logGreen("已添加域名：" + domain + " 到 table 块内")
    return nil
}

// misc utilities retained
func regionTest() { _ = runShellInteractive("bash <(curl -L -s " + REMOTE_RegionRestrictionCheck_URL + ")") }

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
