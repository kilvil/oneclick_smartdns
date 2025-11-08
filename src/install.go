package src

import (
    "archive/tar"
    "compress/gzip"
    "errors"
    "io"
    "io/fs"
    "os"
    "path/filepath"
    "time"
)

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

func removeIfExists(path string) error { if _, err := os.Stat(path); err == nil { return os.Remove(path) }; return nil }

func uninstallSmartDNS() {
    logBlue("正在卸载 SmartDNS...")
    _ = runCmdInteractive("systemctl", "stop", "smartdns")
    _ = runCmdInteractive("systemctl", "disable", "smartdns")

    if _, err := os.Stat("/usr/sbin/smartdns"); err == nil {
        if _, err2 := os.Stat("/etc/init.d/smartdns"); err2 == nil {
            _ = runCmdInteractive("/etc/init.d/smartdns", "stop")
        }
    }

    _ = removeIfExists("/usr/sbin/smartdns")
    _ = removeIfExists("/usr/bin/smartdns")
    _ = removeIfExists("/etc/init.d/smartdns")
    _ = removeIfExists("/etc/systemd/system/smartdns.service")
    _ = runCmdInteractive("systemctl", "daemon-reload")

    logGreen("已卸载 SmartDNS（二进制与服务文件）。保留配置目录 /etc/smartdns。")
}
