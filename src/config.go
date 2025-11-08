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
