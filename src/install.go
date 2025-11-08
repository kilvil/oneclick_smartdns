package src

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

func installSniproxy() {
    logBlue("安装 sniproxy...")
    _ = installSniproxyStream(func(s string) { fmt.Println(s) })
}

// Streaming, non-interactive installer for sniproxy. All outputs are sent to log.
func installSniproxyStream(log func(string)) error {
    if log == nil {
        log = func(string) {}
    }
    log("执行: apt-get update")
    if err := runCmdPipe(func(s string) { log(s) }, "apt-get", "update"); err != nil {
        return err
    }
    log("执行: apt-get install -y sniproxy")
    if err := runCmdPipe(func(s string) { log(s) }, "apt-get", "install", "-y", "sniproxy"); err != nil {
        return err
    }
    // 写入 drop-in，显式指定配置路径
    log("写入 systemd drop-in (-c /etc/sniproxy.conf)")
    if err := ensureSniproxyOverride(); err != nil {
        log("[警告] 写入 drop-in 失败: " + err.Error())
    }
    log("启动并设置开机自启 sniproxy")
    _ = runCmdPipe(func(s string) { log(s) }, "systemctl", "start", "sniproxy")
    _ = runCmdPipe(func(s string) { log(s) }, "systemctl", "enable", "sniproxy")
    log("sniproxy 安装完成")
    return nil
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

// Streaming variant: logs progress and runs install script with pipe.
func installSmartDNSStream(log func(string)) error {
	if log == nil {
		log = func(string) {}
	}
	log("准备安装 SmartDNS ...")
	tmpDir := "/tmp/smartdns_install"
	_ = os.MkdirAll(tmpDir, 0o755)
	log("停止并禁用 systemd-resolved (避免冲突)")
	_ = runCmdPipe(func(s string) { log(s) }, "systemctl", "stop", "systemd-resolved")
	_ = runCmdPipe(func(s string) { log(s) }, "systemctl", "disable", "systemd-resolved")

	tarName := filepath.Base(REMOTE_SMARTDNS_URL)
	tarPath := filepath.Join(tmpDir, tarName)
	log("下载 SmartDNS 安装包: " + REMOTE_SMARTDNS_URL)
	if err := downloadToFile(REMOTE_SMARTDNS_URL, tarPath, 120*time.Second); err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	log("解压安装包 ...")
	if err := extractTarGz(tarPath, tmpDir); err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}
	smartdnsDir := filepath.Join(tmpDir, "smartdns")
	installPath := filepath.Join(smartdnsDir, "install")
	if _, err := os.Stat(installPath); err != nil {
		return fmt.Errorf("未找到安装脚本: %s", installPath)
	}
	_ = os.Chmod(installPath, 0o755)
	log("执行安装脚本: " + installPath + " -i")
	if err := runCmdPipe(func(s string) { log(s) }, installPath, "-i"); err != nil {
		return fmt.Errorf("安装脚本失败: %w", err)
	}
	log("SmartDNS 安装成功！")
	return nil
}

func removeIfExists(path string) error {
	if _, err := os.Stat(path); err == nil {
		return os.Remove(path)
	}
	return nil
}

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
