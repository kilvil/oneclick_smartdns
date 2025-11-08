package src

import (
    "fmt"
    "net"
    "strings"

    tcell "github.com/gdamore/tcell/v2"
    "github.com/rivo/tview"
)

func isSmartDNSActive() bool {
    out, _ := runCmdCapture("systemctl", "is-active", "smartdns")
    return strings.TrimSpace(out) == "active"
}

func initSelectionFromConfig(sel map[string]bool, cfg StreamConfig, topKeys []string) {
    lines, err := readLines(SMART_CONFIG_FILE)
    if err != nil { return }
    present := map[string]bool{}
    for _, l := range lines {
        if strings.HasPrefix(l, "#> ") {
            name := strings.TrimSpace(strings.TrimPrefix(l, "#> "))
            fields := strings.Fields(name)
            if len(fields) > 0 { present[fields[0]] = true }
        }
    }
    for _, top := range topKeys {
        for sub := range cfg[top] {
            if present[sub] { sel[top+"/"+sub] = true }
        }
    }
}

type tvState struct {
    app      *tview.Application
    header   *tview.TextView
    footer   *tview.TextView
    left     *tview.List
    right    *tview.List
    dual     *tview.Flex
    topOnly  *tview.Flex
    subOnly  *tview.Flex
    pages    *tview.Pages
    help     bool
    method   string
    ident    string
    sdActive bool
    cfg      StreamConfig
    topKeys  []string
    subMap   map[string][]string
    selected map[string]bool
    curTop   string
    single   bool

    groups      []dnsGroup
    activeGroup string

    assigned map[string]Assignment // sub -> assignment parsed from config
}

func sortedKeys(m map[string][]string) []string {
    keys := make([]string, 0, len(m))
    for k := range m { keys = append(keys, k) }
    for i := 0; i < len(keys); i++ { for j := i + 1; j < len(keys); j++ { if strings.Compare(keys[i], keys[j]) > 0 { keys[i], keys[j] = keys[j], keys[i] } } }
    return keys
}

func buildTopSub(cfg StreamConfig) (topKeys []string, subMap map[string][]string) {
    topKeys = make([]string, 0, len(cfg))
    for k := range cfg { topKeys = append(topKeys, k) }
    for i := 0; i < len(topKeys); i++ { for j := i + 1; j < len(topKeys); j++ { if strings.Compare(topKeys[i], topKeys[j]) > 0 { topKeys[i], topKeys[j] = topKeys[j], topKeys[i] } } }
    subMap = map[string][]string{}
    for _, top := range topKeys {
        subs := make([]string, 0, len(cfg[top]))
        for s := range cfg[top] { subs = append(subs, s) }
        for i := 0; i < len(subs); i++ { for j := i + 1; j < len(subs); j++ { if strings.Compare(subs[i], subs[j]) > 0 { subs[i], subs[j] = subs[j], subs[i] } } }
        subMap[top] = subs
    }
    return
}

type dnsGroup struct { Name string; IP string }

func (s *tvState) headerText() string {
    way := "方式: [green]nameserver[-]"
    if s.method == "address" { way = "方式: [yellow]address[-]" }
    ident := s.ident
    if ident == "" && s.method == "nameserver" && s.activeGroup != "" { ident = s.activeGroup }
    dns := "标识: [red]未设置[-]"
    if ident != "" { dns = "标识: [green]" + ident + "[-]" }
    sd := "SmartDNS: [red]未运行[-]"
    if s.sdActive { sd = "SmartDNS: [green]运行中[-]" }
    grp := "组: [gray]无[-]"
    if s.activeGroup != "" { grp = "组: [green]" + s.activeGroup + "[-]" }
    return fmt.Sprintf(" %s  |  %s  |  %s  |  %s", way, dns, sd, grp)
}

func (s *tvState) setHeader() { s.header.SetDynamicColors(true).SetText(s.headerText()) }

func (s *tvState) setFooter() {
    s.footer.SetDynamicColors(true)
    if s.help {
        s.footer.SetText("q 退出 | 空格: 二级勾选/一级全选 | Enter 勾选 | 方向键切换 | g 选择分组 | n 新建分组 | r 刷新分组 | m 切换方式 | e 编辑标识 | s 保存 | z 服务管理 | ? 帮助")
    } else {
        s.footer.SetText("? 帮助  |  空格: 二级勾选 / 一级全选  |  g 选择分组  n 创建分组  r 刷新分组  |  m 切换 nameserver/address  |  e 编辑组名/地址  |  s 保存  |  z 服务管理  |  q 退出")
    }
}

func (s *tvState) refreshAssignments() {
    s.assigned = parseAssignments()
}

func (s *tvState) isOccupiedByOtherGroup(sub string) bool {
    a, ok := s.assigned[sub]
    if !ok {
        return false
    }
    // occupied only when assigned by nameserver to a different group
    return a.Method == "nameserver" && a.Ident != "" && a.Ident != s.activeGroup
}

func (s *tvState) resetSelectionForActiveGroup() {
    s.selected = map[string]bool{}
    if s.activeGroup == "" {
        return
    }
    // mark subs that belong to activeGroup (nameserver) as selected
    for top, subs := range s.subMap {
        for _, sub := range subs {
            a, ok := s.assigned[sub]
            if ok && a.Method == "nameserver" && a.Ident == s.activeGroup {
                s.selected[top+"/"+sub] = true
            }
        }
    }
}

func (s *tvState) topMark(top string) string {
    subs := s.subMap[top]
    free := 0
    sel := 0
    for _, sub := range subs {
        if s.isOccupiedByOtherGroup(sub) { continue }
        free++
        if s.selected[top+"/"+sub] { sel++ }
    }
    if free == 0 {
        // no selectable items; treat as none selected
        return "[ ]"
    }
    if sel == 0 { return "[ ]" }
    if sel == free { return "[*]" }
    return "[-]"
}

func (s *tvState) populateLeft() {
    cur := s.left.GetCurrentItem()
    s.left.Clear()
    for _, k := range s.topKeys {
        k := k
        label := fmt.Sprintf("%s %s", s.topMark(k), k)
        s.left.AddItem(label, "", 0, func() {
            s.curTop = k
            s.populateRight()
            if s.single { s.pages.SwitchToPage("single-sub") }
            s.app.SetFocus(s.right)
        })
    }
    if len(s.topKeys) > 0 {
        if cur < 0 { cur = 0 }
        if cur >= len(s.topKeys) { cur = len(s.topKeys) - 1 }
        s.left.SetCurrentItem(cur)
    }
}

func (s *tvState) populateRight() {
    cur := s.right.GetCurrentItem()
    s.right.Clear()
    subs := s.subMap[s.curTop]
    for _, sub := range subs {
        sub := sub
        key := s.curTop + "/" + sub
        mark := "[ ]"
        if s.selected[key] {
            mark = "[*]"
        } else if s.isOccupiedByOtherGroup(sub) {
            mark = "!"
        }
        s.right.AddItem(fmt.Sprintf("%s %s", mark, sub), "", 0, func() {
            if s.isOccupiedByOtherGroup(sub) {
                return
            }
            s.selected[key] = !s.selected[key]
            s.populateRight()
            s.populateLeft()
        })
    }
    if len(subs) > 0 {
        if cur < 0 { cur = 0 }
        if cur >= len(subs) { cur = len(subs) - 1 }
        s.right.SetCurrentItem(cur)
    }
}

func (s *tvState) showEditIdent() {
    form := tview.NewForm()
    label := "DNS 组名"
    def := s.ident
    if s.method == "address" { label = "DNS 服务器IP" }
    input := tview.NewInputField().SetLabel(label + ": ").SetText(def)
    form.AddFormItem(input)
    form.AddButton("确定", func() {
        val := strings.TrimSpace(input.GetText())
        if s.method == "address" && net.ParseIP(val) == nil { input.SetTitle("无效IP"); return }
        if s.method == "nameserver" && val == "" { input.SetTitle("不能为空"); return }
        s.ident = val
        s.setHeader()
        s.pages.RemovePage("modal")
        s.app.SetFocus(s.right)
    })
    form.AddButton("取消", func() { s.pages.RemovePage("modal") })
    form.SetBorder(true).SetTitle("编辑标识").SetTitleAlign(tview.AlignLeft)
    modal := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(form, 0, 1, true)
    s.pages.AddPage("modal", center(60, 7, modal), true, true)
}

func (s *tvState) saveSelection() {
    if s.method != "nameserver" && s.method != "address" { s.toast("请选择正确的添加方式 (m)"); return }
    if strings.TrimSpace(s.ident) == "" {
        if s.method == "nameserver" && s.activeGroup != "" { s.ident = s.activeGroup; s.setHeader() } else { s.toast("请设置组名或IP (e)"); return }
    }
    count := 0
    for key, on := range s.selected {
        if !on { continue }
        parts := strings.SplitN(key, "/", 2)
        if len(parts) != 2 { continue }
        top := parts[0]; sub := parts[1]
        domains := s.cfg[top][sub]
        if len(domains) == 0 { continue }
        _ = deletePlatformRules(sub)
        _ = addDomainRules(s.method, domains, s.ident, sub)
        count++
    }
    if count == 0 { s.toast("未选择任何平台，无需保存"); return }
    // refresh assignments and UI selection state after write
    s.refreshAssignments()
    s.resetSelectionForActiveGroup()
    s.populateRight()
    if s.sdActive {
        m := tview.NewModal().SetText(fmt.Sprintf("保存成功，已写入 %d 个平台\n是否重启 SmartDNS 应用新配置？", count)).
            AddButtons([]string{"重启", "稍后"}).SetDoneFunc(func(i int, l string) {
                s.pages.RemovePage("modal")
                if i == 0 { _ = runCmdInteractive("systemctl", "restart", "smartdns"); s.toast("已重启 SmartDNS") } else { s.toast("保存完成") }
            })
        s.pages.AddPage("modal", center(50, 7, m), true, true)
    } else { s.toast("保存完成 (SmartDNS 未运行)") }
}

func (s *tvState) toast(msg string) {
    m := tview.NewModal().SetText(msg).AddButtons([]string{"确定"}).SetDoneFunc(func(i int, l string) { s.pages.RemovePage("modal") })
    s.pages.AddPage("modal", center(50, 5, m), true, true)
}

func center(w, h int, p tview.Primitive) tview.Primitive {
    grid := tview.NewGrid().SetRows(0, h, 0).SetColumns(0, w, 0).AddItem(p, 1, 1, 1, 1, 0, 0, true)
    return grid
}

func runTUI() {
    if !fileExists(streamConfigPath()) { _ = downloadStreamConfig() }
    cfg, err := loadStreamConfig(); if err != nil { logRed("读取 StreamConfig.yaml 失败: " + err.Error()); return }
    topKeys, subMap := buildTopSub(cfg)

    st := &tvState{
        app:      tview.NewApplication(),
        header:   tview.NewTextView().SetDynamicColors(true),
        footer:   tview.NewTextView().SetDynamicColors(true),
        left:     tview.NewList().ShowSecondaryText(false),
        right:    tview.NewList().ShowSecondaryText(false),
        pages:    tview.NewPages(),
        method:   "nameserver",
        ident:    "",
        sdActive: isSmartDNSActive(),
        cfg:      cfg,
        topKeys:  topKeys,
        subMap:   subMap,
        selected: map[string]bool{},
        curTop:   "",
    }
    initSelectionFromConfig(st.selected, cfg, topKeys)
    st.reloadGroups()

    st.header.SetBorder(true).SetTitle("状态")
    st.footer.SetBorder(true).SetTitle("帮助")
    st.setHeader(); st.setFooter()

    st.left.SetBorder(true)
    st.left.SetTitle("一级流媒体")
    st.left.SetSelectedFunc(func(i int, main, sec string, r rune) {})
    st.left.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
        switch ev.Key() {
        case tcell.KeyRight:
            if idx := st.left.GetCurrentItem(); idx >= 0 && idx < len(st.topKeys) {
                st.curTop = st.topKeys[idx]
                st.populateRight()
                if st.single { st.pages.SwitchToPage("single-sub") }
                st.app.SetFocus(st.right)
            }
            return nil
        }
        switch ev.Rune() {
        case ' ': // select/deselect all subs in current top
            if idx := st.left.GetCurrentItem(); idx >= 0 && idx < len(st.topKeys) {
                top := st.topKeys[idx]
                subs := st.subMap[top]
                if len(subs) == 0 { return nil }
                // consider only free subs (not occupied by other groups)
                free := []string{}
                for _, sub := range subs { if !st.isOccupiedByOtherGroup(sub) { free = append(free, sub) } }
                if len(free) == 0 { return nil }
                all := true
                for _, sub := range free { if !st.selected[top+"/"+sub] { all = false; break } }
                if all { for _, sub := range free { st.selected[top+"/"+sub] = false } } else { for _, sub := range free { st.selected[top+"/"+sub] = true } }
                if st.curTop == top { st.populateRight() }
                st.populateLeft()
            }
            return nil
        case 'l':
            if idx := st.left.GetCurrentItem(); idx >= 0 && idx < len(st.topKeys) {
                st.curTop = st.topKeys[idx]
                st.populateRight()
                if st.single { st.pages.SwitchToPage("single-sub") }
                st.app.SetFocus(st.right)
            }
            return nil
        }
        return ev
    })

    st.right.SetBorder(true).SetTitle("二级流媒体 (空格勾选)")
    st.right.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
        switch ev.Key() {
        case tcell.KeyLeft:
            if st.single { st.pages.SwitchToPage("single-top") }
            st.app.SetFocus(st.left)
            return nil
        case tcell.KeyRune:
            if ev.Rune() == 'h' { if st.single { st.pages.SwitchToPage("single-top") }; st.app.SetFocus(st.left); return nil }
            if ev.Rune() == ' ' {
                if idx := st.right.GetCurrentItem(); idx >= 0 {
                    subs := st.subMap[st.curTop]
                    if idx < len(subs) {
                        sub := subs[idx]
                        if st.isOccupiedByOtherGroup(sub) { return nil }
                        key := st.curTop + "/" + sub
                        st.selected[key] = !st.selected[key]
                        st.populateRight()
                        st.populateLeft()
                    }
                }
                return nil
            }
        case tcell.KeyEnter:
            if idx := st.right.GetCurrentItem(); idx >= 0 {
                subs := st.subMap[st.curTop]
                if idx < len(subs) {
                    sub := subs[idx]
                    if st.isOccupiedByOtherGroup(sub) { return nil }
                    key := st.curTop + "/" + sub
                    st.selected[key] = !st.selected[key]
                    st.populateRight()
                    st.populateLeft()
                }
            }
            return nil
        }
        return ev
    })

    st.populateLeft()
    if len(st.topKeys) > 0 { st.curTop = st.topKeys[0] }
    st.populateRight()

    bodyDual := tview.NewFlex().AddItem(st.left, 0, 1, true).AddItem(st.right, 0, 2, false)
    st.dual = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(st.header, 5, 0, false).AddItem(bodyDual, 0, 1, true).AddItem(st.footer, 3, 0, false)

    st.topOnly = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(st.header, 5, 0, false).AddItem(st.left, 0, 1, true).AddItem(st.footer, 3, 0, false)
    st.subOnly = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(st.header, 5, 0, false).AddItem(st.right, 0, 1, true).AddItem(st.footer, 3, 0, false)

    st.pages.AddPage("dual", st.dual, true, true)
    st.pages.AddPage("single-top", st.topOnly, true, false)
    st.pages.AddPage("single-sub", st.subOnly, true, false)

    // Groups page should be the entry
    st.app.SetRoot(st.pages, true)
    st.app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
        // Avoid global hotkeys when a modal or groups page is active
        if name, _ := st.pages.GetFrontPage(); name == "modal" || name == "groups" {
            return ev
        }
        switch ev.Key() {
        case tcell.KeyRune:
            switch ev.Rune() {
            case 'q': st.app.Stop(); return nil
            case '?': st.help = !st.help; st.setFooter(); return nil
            case 'm': if st.method == "nameserver" { st.method = "address" } else { st.method = "nameserver" }; st.setHeader(); return nil
            case 'e': st.showEditIdent(); return nil
            case 's': st.saveSelection(); return nil
            case 'g': st.openGroupsPage(); return nil
            case 'n': st.showAddGroupModal(nil); return nil
            case 'r': st.reloadGroups(); st.toast("已刷新分组"); return nil
            case 'z': st.openServiceManager(); return nil
            case 'h': if st.single { st.pages.SwitchToPage("single-top"); st.app.SetFocus(st.left); return nil }
            case 'l': if st.single { st.pages.SwitchToPage("single-sub"); st.app.SetFocus(st.right); return nil }
            }
        case tcell.KeyLeft:
            if st.single { st.pages.SwitchToPage("single-top"); st.app.SetFocus(st.left); return nil }
        case tcell.KeyRight:
            if st.single { st.pages.SwitchToPage("single-sub"); st.app.SetFocus(st.right); return nil }
        }
        return ev
    })

    st.app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
        w, _ := screen.Size()
        narrow := w < 90
        if narrow != st.single {
            st.single = narrow
            // switch only when not on groups page
            if name, _ := st.pages.GetFrontPage(); name != "groups" {
                if narrow { st.pages.SwitchToPage("single-top") } else { st.pages.SwitchToPage("dual") }
            }
        }
        return false
    })

    // show groups page initially
    st.openGroupsPage()
    if err := st.app.Run(); err != nil { logRed("TUI 运行失败: " + err.Error()) }
}

// ----- Service Manager (SmartDNS / sniproxy) -----

func (s *tvState) openServiceManager() {
    options := tview.NewList().ShowSecondaryText(false)
    options.SetBorder(true).SetTitle("服务管理")
    options.AddItem("SmartDNS", "安装/卸载/启动/停止/重启", 0, func() { s.pages.RemovePage("modal"); s.openSmartDNSActions() })
    options.AddItem("sniproxy", "安装/启动/停止/重启", 0, func() { s.pages.RemovePage("modal"); s.openSniproxyActions() })
    options.AddItem("关闭", "", 0, func() { s.pages.RemovePage("modal") })
    s.pages.AddPage("modal", center(50, 12, options), true, true)
}

func (s *tvState) openSmartDNSActions() {
    list := tview.NewList().ShowSecondaryText(false)
    list.SetBorder(true).SetTitle("SmartDNS")
    list.AddItem("安装", "从发布包安装", 0, func() { s.pages.RemovePage("modal"); installSmartDNS(); s.sdActive = isSmartDNSActive(); s.setHeader(); s.toast("安装完成或已触发安装流程") })
    list.AddItem("卸载", "移除服务与二进制（保留配置）", 0, func() { s.pages.RemovePage("modal"); s.confirmUninstallSmartDNS() })
    list.AddItem("启动", "", 0, func() { s.pages.RemovePage("modal"); startSmartDNS(); s.sdActive = true; s.setHeader(); s.toast("已启动 SmartDNS 并设置开机自启") })
    list.AddItem("停止", "", 0, func() { s.pages.RemovePage("modal"); stopSmartDNS(); s.sdActive = isSmartDNSActive(); s.setHeader(); s.toast("已停止 SmartDNS 并关闭开机自启") })
    list.AddItem("重启", "", 0, func() { s.pages.RemovePage("modal"); stopSmartDNS(); startSmartDNS(); s.sdActive = isSmartDNSActive(); s.setHeader(); s.toast("已重启 SmartDNS") })
    list.AddItem("返回", "", 0, func() { s.pages.RemovePage("modal"); s.openServiceManager() })
    s.pages.AddPage("modal", center(50, 14, list), true, true)
}

func (s *tvState) confirmUninstallSmartDNS() {
    m := tview.NewModal().SetText("确认卸载 SmartDNS？\n将移除服务与二进制，保留 /etc/smartdns 配置。").AddButtons([]string{"确定", "取消"}).SetDoneFunc(func(i int, l string) {
        s.pages.RemovePage("modal")
        if i == 0 { uninstallSmartDNS(); s.sdActive = isSmartDNSActive(); s.setHeader(); s.toast("已卸载 SmartDNS（配置保留）") }
    })
    s.pages.AddPage("modal", center(60, 8, m), true, true)
}

func (s *tvState) openSniproxyActions() {
    list := tview.NewList().ShowSecondaryText(false)
    list.SetBorder(true).SetTitle("sniproxy")
    list.AddItem("安装", "通过 apt 安装", 0, func() { s.pages.RemovePage("modal"); installSniproxy(); s.toast("已触发安装 sniproxy") })
    list.AddItem("启动", "", 0, func() { s.pages.RemovePage("modal"); restoreSniproxy(); s.toast("已启动 sniproxy 并设置开机自启") })
    list.AddItem("停止", "", 0, func() { s.pages.RemovePage("modal"); stopSniproxy(); s.toast("已停止 sniproxy 并关闭开机自启") })
    list.AddItem("重启", "", 0, func() { s.pages.RemovePage("modal"); restartSniproxy(); s.toast("已重启 sniproxy") })
    list.AddItem("返回", "", 0, func() { s.pages.RemovePage("modal"); s.openServiceManager() })
    s.pages.AddPage("modal", center(50, 12, list), true, true)
}

// ----- Upstream group management -----

func parseUpstreamGroups() []dnsGroup {
    var groups []dnsGroup
    lines, err := readLines(SMART_CONFIG_FILE)
    if err != nil { return groups }
    seen := map[string]bool{}
    for _, l := range lines {
        if m := reServerGroupLine.FindStringSubmatch(l); len(m) == 3 {
            ip := m[1]; name := m[2]
            if !seen[name] { groups = append(groups, dnsGroup{Name: name, IP: ip}); seen[name] = true }
        }
    }
    for i := 0; i < len(groups); i++ { for j := i + 1; j < len(groups); j++ { if strings.Compare(groups[i].Name, groups[j].Name) > 0 { groups[i], groups[j] = groups[j], groups[i] } } }
    return groups
}

func (s *tvState) reloadGroups() {
    s.groups = parseUpstreamGroups()
    if s.activeGroup != "" {
        ok := false
        for _, g := range s.groups { if g.Name == s.activeGroup { ok = true; break } }
        if !ok { s.activeGroup = "" }
    }
    s.setHeader()
}

func (s *tvState) openGroupManager() {
    ensureSmartDNSDir()
    list := tview.NewList().ShowSecondaryText(false)
    list.SetBorder(true).SetTitle("DNS 分组 (Enter选择, N新增, Esc关闭)")
    for _, g := range s.groups {
        label := fmt.Sprintf("%s (%s)", g.Name, g.IP)
        gg := g
        list.AddItem(label, "", 0, func() {
            s.activeGroup = gg.Name
            s.ident = gg.Name
            s.method = "nameserver"
            s.setHeader()
            s.pages.RemovePage("modal")
        })
    }
    list.SetDoneFunc(func() { s.pages.RemovePage("modal") })
    list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
        if ev.Key() == tcell.KeyRune {
            switch ev.Rune() {
            case 'n', 'N':
                s.showAddGroupModal(func() { s.pages.RemovePage("modal"); s.openGroupManager() })
                return nil
            }
        }
        if ev.Key() == tcell.KeyEsc { s.pages.RemovePage("modal"); return nil }
        return ev
    })
    s.pages.AddPage("modal", center(60, 15, list), true, true)
}

func (s *tvState) showAddGroupModal(after func()) {
    form := tview.NewForm()
    ipInput := tview.NewInputField().SetLabel("上游DNS IP: ")
    nameInput := tview.NewInputField().SetLabel("分组名称: ")
    form.AddFormItem(ipInput)
    form.AddFormItem(nameInput)
    form.AddButton("创建", func() {
        ip := strings.TrimSpace(ipInput.GetText())
        name := strings.TrimSpace(nameInput.GetText())
        if net.ParseIP(ip) == nil { ipInput.SetTitle("无效IP"); return }
        if name == "" { nameInput.SetTitle("不能为空"); return }
        for _, g := range s.groups { if strings.EqualFold(g.Name, name) { nameInput.SetTitle("已存在"); return } }
        line := fmt.Sprintf("server %s IP -group %s -exclude-default-group", ip, name)
        if err := insertServerIntoConfig(line, SMART_CONFIG_FILE); err != nil { s.toast("创建分组失败: " + err.Error()); return }
        s.reloadGroups(); s.activeGroup = name; s.ident = name; s.method = "nameserver"; s.setHeader(); s.pages.RemovePage("modal"); if after != nil { after() }
    })
    form.AddButton("取消", func() { s.pages.RemovePage("modal") })
    form.SetBorder(true).SetTitle("创建DNS分组").SetTitleAlign(tview.AlignLeft)
    modal := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(form, 0, 1, true)
    s.pages.AddPage("modal", center(60, 10, modal), true, true)
}

// ----- Group-first navigation -----

func (s *tvState) openGroupsPage() {
    s.activeGroup = ""
    s.setHeader()
    list := tview.NewList().ShowSecondaryText(false)
    list.SetBorder(true).SetTitle("DNS 分组 (Enter进入, N新增, R刷新, Q退出)")
    s.footer.SetText("Enter 进入配置  |  n 新建分组  r 刷新  |  q 退出  |  g 返回分组列表")
    // refresh groups data
    s.reloadGroups()
    for _, g := range s.groups {
        gg := g
        label := fmt.Sprintf("%s (%s)", g.Name, g.IP)
        list.AddItem(label, "", 0, func() {
            s.activeGroup = gg.Name
            s.ident = gg.Name
            s.method = "nameserver"
            s.refreshAssignments()
            s.resetSelectionForActiveGroup()
            // default to first top
            if len(s.topKeys) > 0 { s.curTop = s.topKeys[0] }
            // prepare config view and switch
            s.populateLeft()
            s.populateRight()
            if s.single { s.pages.SwitchToPage("single-top") } else { s.pages.SwitchToPage("dual") }
            s.setHeader()
        })
    }
    list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
        if ev.Key() == tcell.KeyRune {
            switch ev.Rune() {
            case 'n', 'N':
                s.showAddGroupModal(func() { s.openGroupsPage() })
                return nil
            case 'r', 'R':
                s.openGroupsPage()
                return nil
            case 'q', 'Q':
                s.app.Stop()
                return nil
            }
        }
        if ev.Key() == tcell.KeyEsc { s.app.Stop(); return nil }
        return ev
    })
    // add/replace the "groups" page
    if s.pages.HasPage("groups") { s.pages.RemovePage("groups") }
    body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(s.header, 5, 0, false).AddItem(list, 0, 1, true).AddItem(s.footer, 3, 0, false)
    s.pages.AddPage("groups", body, true, true)
    s.pages.SwitchToPage("groups")
}
