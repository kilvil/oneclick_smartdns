package src

import (
    "fmt"
    "os"
    "os/exec"
    "strings"
)

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

func restoreService(service string) { _ = manageService(service, "start", "启动"); _ = manageService(service, "enable", "设置为开机启动") }
func stopService(service string)    { _ = manageService(service, "stop", "停止"); _ = manageService(service, "disable", "关闭开机自启") }

func checkSmartDNSStatus()  { checkServiceStatus("smartdns", "SmartDNS") }
func checkSystemDNSStatus() { checkServiceStatus("systemd-resolved", "系统 DNS") }
func checkSniproxyStatus()  { checkServiceStatus("sniproxy", "sniproxy") }

func restoreSystemDNS() { stopService("smartdns"); restoreService("systemd-resolved"); logGreen("系统 DNS 服务已启动并设置为开机启动。") }
func restoreSniproxy() { restoreService("sniproxy"); logGreen("sniproxy 服务已启动并设置为开机启动。") }

func startSmartDNS() { stopService("systemd-resolved"); restoreService("smartdns"); modifyResolv("127.0.0.1"); logGreen("SmartDNS 服务已启动并设置为开机启动！") }
func stopSystemDNS() { stopService("systemd-resolved"); logGreen("系统 DNS 服务已停止并关闭开机自启。") }
func stopSmartDNS() { stopService("smartdns"); logGreen("SmartDNS 服务已停止并关闭开机自启。") }
func stopSniproxy() { stopService("sniproxy"); logGreen("sniproxy 服务已停止并关闭开机自启。") }

func restartService(service string) { _ = manageService(service, "restart", "重启") }
func restartSmartDNS()               { restartService("smartdns") }
func restartSniproxy()               { restartService("sniproxy") }

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
