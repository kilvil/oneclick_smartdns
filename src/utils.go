package src

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

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

func ensureDir(path string) error { return os.MkdirAll(path, 0o755) }
func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }

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

// runCmdPipe runs a command and streams stdout/stderr lines to onLine.
// It doesn't take over the TTY; safe to call within TUI.
func runCmdPipe(onLine func(string), name string, args ...string) error {
	cmd := exec.Command(name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan struct{}, 2)
	reader := func(r io.Reader) {
		sc := bufio.NewScanner(r)
		// increase buffer for long lines
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 1024*1024)
		for sc.Scan() {
			if onLine != nil {
				onLine(sc.Text())
			}
		}
		done <- struct{}{}
	}
	go reader(stdout)
	go reader(stderr)
	<-done
	<-done
	return cmd.Wait()
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

func downloadToFile(url, path string, timeout time.Duration) error {
    b, err := httpGetTimeout(url, timeout)
    if err != nil {
        return err
    }
    return os.WriteFile(path, b, 0o644)
}

// isPrivateIPv4 checks RFC1918, CGNAT, loopback and link-local ranges.
func isPrivateIPv4(ip net.IP) bool {
    ip4 := ip.To4()
    if ip4 == nil {
        return true
    }
    a := ip4[0]
    b := ip4[1]
    // 10.0.0.0/8
    if a == 10 {
        return true
    }
    // 172.16.0.0/12
    if a == 172 && b >= 16 && b <= 31 {
        return true
    }
    // 192.168.0.0/16
    if a == 192 && b == 168 {
        return true
    }
    // 100.64.0.0/10 (CGNAT)
    if a == 100 && b >= 64 && b <= 127 {
        return true
    }
    // 127.0.0.0/8 loopback
    if a == 127 {
        return true
    }
    // 169.254.0.0/16 link-local
    if a == 169 && b == 254 {
        return true
    }
    return false
}

func firstPublicIPv4FromInterfaces() string {
    ifaces, _ := net.Interfaces()
    for _, iface := range ifaces {
        if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
            continue
        }
        addrs, _ := iface.Addrs()
        for _, a := range addrs {
            var ip net.IP
            switch v := a.(type) {
            case *net.IPNet:
                ip = v.IP
            case *net.IPAddr:
                ip = v.IP
            }
            if ip == nil {
                continue
            }
            ip4 := ip.To4()
            if ip4 == nil {
                continue
            }
            if !isPrivateIPv4(ip4) {
                return ip4.String()
            }
        }
    }
    return ""
}

// getPublicIPv4 tries multiple strategies to obtain the server's public IPv4.
// Order: env override -> OpenDNS dig -> ipify -> ifconfig.co -> interface guess.
func getPublicIPv4() string {
    if v := strings.TrimSpace(os.Getenv("SMARTDNS_SELF_PUBLIC_IPV4")); v != "" {
        if net.ParseIP(v) != nil {
            return v
        }
    }
    // OpenDNS via dig (if available)
    if _, err := exec.LookPath("dig"); err == nil {
        if out, err := runCmdCapture("sh", "-lc", "dig +short -4 myip.opendns.com @resolver1.opendns.com 2>/dev/null | head -n1"); err == nil {
            ip := strings.TrimSpace(out)
            if net.ParseIP(ip) != nil {
                return ip
            }
        }
    }
    // ipify.org
    if b, err := httpGetTimeout("https://api.ipify.org", 4*time.Second); err == nil {
        ip := strings.TrimSpace(string(b))
        if net.ParseIP(ip) != nil {
            return ip
        }
    }
    // ifconfig.co
    if b, err := httpGetTimeout("https://ifconfig.co/ip", 4*time.Second); err == nil {
        ip := strings.TrimSpace(string(b))
        if net.ParseIP(ip) != nil {
            return ip
        }
    }
    // Guess from interfaces
    return firstPublicIPv4FromInterfaces()
}

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
