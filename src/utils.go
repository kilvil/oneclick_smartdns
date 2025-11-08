package src

import (
    "bufio"
    "context"
    "errors"
    "fmt"
    "io"
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
