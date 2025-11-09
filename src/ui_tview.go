package src

import (
	"fmt"
	"net"
	"os"
	"strings"

	tcell "github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func isSmartDNSActive() bool {
	out, _ := runCmdCapture("systemctl", "is-active", "smartdns")
	return strings.TrimSpace(out) == "active"
}

func isNginxActive() bool {
	out, _ := runCmdCapture("systemctl", "is-active", "nginx")
	return strings.TrimSpace(out) == "active"
}

func isSystemResolverActive() bool {
	out, _ := runCmdCapture("systemctl", "is-active", "systemd-resolved")
	return strings.TrimSpace(out) == "active"
}

func initSelectionFromConfig(sel map[string]bool, cfg StreamConfig, topKeys []string) {
	lines, err := readLines(SMART_CONFIG_FILE)
	if err != nil {
		return
	}
	present := map[string]bool{}
	for _, l := range lines {
		if strings.HasPrefix(l, "#> ") {
			name := strings.TrimSpace(strings.TrimPrefix(l, "#> "))
			fields := strings.Fields(name)
			if len(fields) > 0 {
				present[fields[0]] = true
			}
		}
	}
	for _, top := range topKeys {
		for sub := range cfg[top] {
			if present[sub] {
				sel[top+"/"+sub] = true
			}
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
	method   string
	ident    string
	sdActive bool
	ngActive bool
	syActive bool
	cfg      StreamConfig
	topKeys  []string
	subMap   map[string][]string
	selected map[string]bool
	curTop   string
	single   bool

	groups      []dnsGroup
	activeGroup string
	selfPubV4   string

	assigned map[string]Assignment // sub -> assignment parsed from config

	dirty bool // 有未保存更改

	// initial service states at app start; used for exit restart prompt
	initialSdActive bool
	initialNgActive bool
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if strings.Compare(keys[i], keys[j]) > 0 {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

func buildTopSub(cfg StreamConfig) (topKeys []string, subMap map[string][]string) {
	topKeys = make([]string, 0, len(cfg))
	for k := range cfg {
		topKeys = append(topKeys, k)
	}
	for i := 0; i < len(topKeys); i++ {
		for j := i + 1; j < len(topKeys); j++ {
			if strings.Compare(topKeys[i], topKeys[j]) > 0 {
				topKeys[i], topKeys[j] = topKeys[j], topKeys[i]
			}
		}
	}
	subMap = map[string][]string{}
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
	return
}

type dnsGroup struct {
	Name string
	IP   string
}

func (s *tvState) headerText() string {
	way := "方式: [green]nameserver[-]"
	if s.method == "address" {
		way = "方式: [yellow]address[-]"
	}
	ident := s.ident
	if ident == "" && s.method == "nameserver" && s.activeGroup != "" {
		ident = s.activeGroup
	}
	dns := "标识: [red]未设置[-]"
	if ident != "" {
		dns = "标识: [green]" + ident + "[-]"
	}
	sd := "SmartDNS: [red]未运行[-]"
	if s.sdActive {
		sd = "SmartDNS: [green]运行中[-]"
	}
	ngx := "nginx: [red]未运行[-]"
	if s.ngActive {
		ngx = "nginx: [green]运行中[-]"
	}
	sy := "systemd-resolved: [green]运行中[-]"
	if !s.syActive {
		sy = "systemd-resolved: [gray]已停用[-]"
	} else if s.sdActive {
		// concurrent running may cause conflict
		sy = "systemd-resolved: [yellow]运行(可能冲突)[-]"
	}
	grp := "组: [gray]无[-]"
	if s.activeGroup != "" {
		grp = "组: [green]" + s.activeGroup + "[-]"
	}
	return fmt.Sprintf(" %s  |  %s  |  %s  |  %s  |  %s  |  %s", way, dns, sd, ngx, sy, grp)
}

func (s *tvState) setHeader() { s.header.SetDynamicColors(true).SetText(s.headerText()) }

func (s *tvState) setFooter() {
	s.footer.SetDynamicColors(true)
	txt := "空格: 二级勾选 / 一级全选  |  Enter 勾选  |  方向键切换  |  h/l 切换面板  |  n 新建分组  d 删除分组  r 刷新分组  |  m 切换方式  |  e 编辑组名/地址  |  s 保存  |  z 服务管理  |  q 返回分组/退出  |  Esc 关闭弹窗"
	if s.dirty {
		txt += "  [yellow]有未保存更改[-]，按 s 保存"
	}
	s.footer.SetText(txt)
}

func (s *tvState) refreshAssignments() {
	s.assigned = parseAssignments()
}

func (s *tvState) isOccupiedByOtherGroup(sub string) bool {
	a, ok := s.assigned[sub]
	if !ok {
		return false
	}
	// determine current target (method+ident)
	tgt := s.targetAssignment()
	if tgt.Ident == "" {
		return false
	}
	// occupied if existing assignment does not match current target
	if a.Method != tgt.Method {
		return true
	}
	if a.Method == "nameserver" {
		return !strings.EqualFold(strings.TrimSpace(a.Ident), strings.TrimSpace(tgt.Ident))
	}
	// address: exact match
	return strings.TrimSpace(a.Ident) != strings.TrimSpace(tgt.Ident)
}

func (s *tvState) resetSelectionForActiveGroup() {
	s.selected = map[string]bool{}
	// mark subs that belong to the current target (method + ident) as selected
	tgt := s.targetAssignment()
	if tgt.Ident == "" {
		return
	}
	for top, subs := range s.subMap {
		for _, sub := range subs {
			a, ok := s.assigned[sub]
			if !ok {
				continue
			}
			if a.Method != tgt.Method {
				continue
			}
			if a.Method == "nameserver" && strings.EqualFold(a.Ident, tgt.Ident) {
				s.selected[top+"/"+sub] = true
			}
			if a.Method == "address" && a.Ident == tgt.Ident {
				s.selected[top+"/"+sub] = true
			}
		}
	}
}

func (s *tvState) syncTargetFromAssignments() {
	if s.activeGroup == "" {
		return
	}
	if s.assigned == nil {
		s.assigned = map[string]Assignment{}
	}
	if s.activeGroup == SPECIAL_UNLOCK_GROUP_NAME {
		s.method = "address"
		preferred := strings.TrimSpace(s.ident)
		if preferred == "" {
			preferred = strings.TrimSpace(s.selfPubV4)
		}
		if ip := s.pickAddressIdent(preferred); ip != "" {
			s.ident = ip
			if ip != "" {
				s.selfPubV4 = ip
			}
		} else if s.ident == "" {
			s.ident = preferred
		}
		return
	}
	lower := strings.ToLower(strings.TrimSpace(s.activeGroup))
	for _, a := range s.assigned {
		if a.Method == "nameserver" && strings.ToLower(strings.TrimSpace(a.Ident)) == lower {
			s.method = "nameserver"
			s.ident = s.activeGroup
			return
		}
	}
	// fallback: keep address if explicitly set and still present
	if s.method == "address" && strings.TrimSpace(s.ident) != "" {
		ident := strings.TrimSpace(s.ident)
		for _, a := range s.assigned {
			if a.Method == "address" && strings.TrimSpace(a.Ident) == ident {
				return
			}
		}
	}
	// default to nameserver using group name
	s.method = "nameserver"
	s.ident = s.activeGroup
}

func (s *tvState) pickAddressIdent(preferred string) string {
	preferred = strings.TrimSpace(preferred)
	var fallback string
	for _, a := range s.assigned {
		if a.Method != "address" {
			continue
		}
		ident := strings.TrimSpace(a.Ident)
		if ident == "" {
			continue
		}
		if preferred != "" && strings.EqualFold(ident, preferred) {
			return ident
		}
		if fallback == "" {
			fallback = ident
		}
	}
	return fallback
}

// targetAssignment resolves the effective (method, ident) pair for current editing page.
func (s *tvState) targetAssignment() Assignment {
	ident := s.ident
	if s.method == "nameserver" && strings.TrimSpace(ident) == "" {
		ident = s.activeGroup
	}
	return Assignment{Method: s.method, Ident: strings.TrimSpace(ident)}
}

func (s *tvState) topMark(top string) string {
	subs := s.subMap[top]
	free := 0
	sel := 0
	for _, sub := range subs {
		if s.isOccupiedByOtherGroup(sub) {
			continue
		}
		free++
		if s.selected[top+"/"+sub] {
			sel++
		}
	}
	if free == 0 {
		// no selectable items; treat as none selected
		return "[ ]"
	}
	if sel == 0 {
		return "[ ]"
	}
	if sel == free {
		return "[*]"
	}
	// Use [=] for partial to avoid tview color reset tag "[-]" being parsed.
	return "[=]"
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
			if s.single {
				s.pages.SwitchToPage("single-sub")
			}
			s.app.SetFocus(s.right)
		})
	}
	if len(s.topKeys) > 0 {
		if cur < 0 {
			cur = 0
		}
		if cur >= len(s.topKeys) {
			cur = len(s.topKeys) - 1
		}
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
		sec := ""
		if s.selected[key] {
			mark = "[*]"
		} else if s.isOccupiedByOtherGroup(sub) {
			mark = "!"
			if a, ok := s.assigned[sub]; ok {
				if a.Method == "nameserver" {
					sec = fmt.Sprintf("被分组 %s 占用", a.Ident)
				} else if a.Method == "address" {
					name := a.Ident
					if s.selfPubV4 != "" && a.Ident == s.selfPubV4 {
						name = SPECIAL_UNLOCK_GROUP_NAME
					}
					sec = fmt.Sprintf("被 %s 占用", name)
				}
			}
		}
		s.right.AddItem(fmt.Sprintf("%s %s", mark, sub), sec, 0, func() {
			if s.isOccupiedByOtherGroup(sub) {
				return
			}
			s.selected[key] = !s.selected[key]
			s.populateRight()
			s.populateLeft()
			s.dirty = true
			s.setFooter()
		})
	}
	if len(subs) > 0 {
		if cur < 0 {
			cur = 0
		}
		if cur >= len(subs) {
			cur = len(subs) - 1
		}
		s.right.SetCurrentItem(cur)
	}
}

func (s *tvState) showEditIdent() {
	form := tview.NewForm()
	label := "DNS 组名"
	def := s.ident
	if s.method == "address" {
		label = "DNS 服务器IP"
	}
	input := tview.NewInputField().SetLabel(label + ": ").SetText(def)
	form.AddFormItem(input)
	form.AddButton("确定", func() {
		val := strings.TrimSpace(input.GetText())
		if s.method == "address" && net.ParseIP(val) == nil {
			input.SetTitle("无效IP")
			return
		}
		if s.method == "nameserver" && val == "" {
			input.SetTitle("不能为空")
			return
		}
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
	count, err := s.saveSelectionSilent()
	if err != nil {
		s.toast(err.Error())
		return
	}
	if count == 0 {
		s.toast("没有可保存的变更")
		return
	}
	if s.sdActive {
		m := tview.NewModal().SetText(fmt.Sprintf("保存成功，变更 %d 个平台\n是否重启 SmartDNS 应用新配置？", count)).
			AddButtons([]string{"重启", "稍后"}).SetDoneFunc(func(i int, l string) {
			s.pages.RemovePage("modal")
			if i == 0 {
				_ = runCmdInteractive("systemctl", "restart", "smartdns")
				s.toast("已重启 SmartDNS")
			} else {
				s.toast("保存完成")
			}
		})
		s.pages.AddPage("modal", center(50, 7, m), true, true)
	} else {
		s.toast("保存完成 (SmartDNS 未运行)")
	}
}

// saveSelectionSilent writes current selection without any modal prompts.
// It validates method and ident, applies rules, refreshes state, clears dirty.
// Returns number of platforms written, or error for invalid state.
func (s *tvState) saveSelectionSilent() (int, error) {
	if s.method != "nameserver" && s.method != "address" {
		return 0, fmt.Errorf("请选择正确的添加方式 (m)")
	}
	if strings.TrimSpace(s.ident) == "" {
		if s.method == "nameserver" && s.activeGroup != "" {
			s.ident = s.activeGroup
			s.setHeader()
		} else {
			return 0, fmt.Errorf("请设置组名或IP (e)")
		}
	}
	// target assignment for this save
	tgt := s.targetAssignment()
	changed := 0
	ngReady := fileExists(NGINX_MAIN_CONF)
	// Ensure base SmartDNS options exist
	_ = ensureSmartDNSBaseDirectives()
	// Build a quick set of selected subs (by sub name)
	selSubs := map[string]bool{}
	for key, on := range s.selected {
		if !on {
			continue
		}
		parts := strings.SplitN(key, "/", 2)
		if len(parts) != 2 {
			continue
		}
		selSubs[parts[1]] = true
	}
	// Pass 1: remove assignments belonging to current target that are now unselected
	for _, subs := range s.subMap {
		for _, sub := range subs {
			a, ok := s.assigned[sub]
			if !ok {
				continue
			}
			if a.Method != tgt.Method {
				continue
			}
			if a.Method == "nameserver" {
				if !strings.EqualFold(strings.TrimSpace(a.Ident), strings.TrimSpace(tgt.Ident)) {
					continue
				}
			} else { // address
				if strings.TrimSpace(a.Ident) != strings.TrimSpace(tgt.Ident) {
					continue
				}
			}
			if !selSubs[sub] {
				_ = deletePlatformRules(sub)
				changed++
			}
		}
	}
	// Pass 2: apply selected subs where assignment differs (or missing)
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
		domains := s.cfg[top][sub]
		if len(domains) == 0 {
			continue
		}
		a, ok := s.assigned[sub]
		if ok && a.Method == tgt.Method {
			if a.Method == "nameserver" && strings.EqualFold(a.Ident, tgt.Ident) {
				// already ours; skip rewrite
				continue
			}
			if a.Method == "address" && a.Ident == tgt.Ident {
				// already ours; skip rewrite
				continue
			}
		}
		_ = deletePlatformRules(sub)
		_ = addDomainRules(s.method, domains, s.ident, sub)
		changed++
	}
	// Ensure nginx proxy configs exist and reload nginx (if installed)
	if ngReady {
		if err := ensureNginxProxyConfigs(func(s string) { /* no-op in silent save */ }); err != nil {
			logYellow("写入 Nginx 代理配置失败: " + err.Error())
		} else {
			_ = nginxTestAndReload(func(string) {})
		}
	}
	if changed > 0 {
		s.refreshAssignments()
		s.syncTargetFromAssignments()
		s.resetSelectionForActiveGroup()
		s.populateRight()
		s.dirty = false
		s.setFooter()
	}
	return changed, nil
}

func (s *tvState) toast(msg string) {
	m := tview.NewModal().SetText(msg).AddButtons([]string{"确定"}).SetDoneFunc(func(i int, l string) { s.pages.RemovePage("modal") })
	s.pages.AddPage("modal", center(50, 5, m), true, true)
}

func center(w, h int, p tview.Primitive) tview.Primitive {
	grid := tview.NewGrid().SetRows(0, h, 0).SetColumns(0, w, 0).AddItem(p, 1, 1, 1, 1, 0, 0, true)
	return grid
}

// runOutsideUI temporarily suspends the TUI, executes task, then resumes.
func (s *tvState) runOutsideUI(desc string, task func()) {
	s.app.Suspend(func() {
		if desc != "" {
			fmt.Printf("\n[执行] %s...\n\n", desc)
		}
		task()
		fmt.Println("\n[完成] 操作结束，正在恢复界面...")
	})
}

func (s *tvState) openConfigViewer(title, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		s.toast("读取配置失败: " + err.Error())
		return
	}
	view := tview.NewTextView().
		SetText(string(data)).
		SetScrollable(true).
		SetWrap(false).
		SetDynamicColors(true).
		SetBorder(true).
		SetTitle(fmt.Sprintf("%s (Esc/q 关闭)", title)).
		SetTitleAlign(tview.AlignLeft)
	view.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc || ev.Rune() == 'q' {
			s.pages.RemovePage("modal-config")
			return nil
		}
		return ev
	})
	if s.pages.HasPage("modal-config") {
		s.pages.RemovePage("modal-config")
	}
	s.pages.AddPage("modal-config", center(100, 30, view), true, true)
	s.app.SetFocus(view)
}

// openLogModal creates a modal TextView to stream logs into.
func (s *tvState) openLogModal(title string) *tview.TextView {
	view := tview.NewTextView()
	view.SetScrollable(true).SetWrap(false).SetDynamicColors(true)
	view.SetBorder(true).SetTitle(title + " (Esc/q 关闭)")
	view.SetTitleAlign(tview.AlignLeft)
	view.SetChangedFunc(func() { s.app.Draw() })
	view.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc || ev.Rune() == 'q' {
			s.pages.RemovePage("modal-log")
			return nil
		}
		return ev
	})
	if s.pages.HasPage("modal-log") {
		s.pages.RemovePage("modal-log")
	}
	s.pages.AddPage("modal-log", center(100, 30, view), true, true)
	s.app.SetFocus(view)
	return view
}

// flushUI refreshes runtime states and re-renders current page safely.
func (s *tvState) flushUI() {
	s.app.QueueUpdateDraw(func() {
		// refresh runtime/service states
		s.sdActive = isSmartDNSActive()
		s.ngActive = isNginxActive()
		s.syActive = isSystemResolverActive()
		// reload groups and assignments as files may have changed after install/uninstall
		s.reloadGroups()
		s.refreshAssignments()
		// If we have an active group, reselect its current selection from config
		if s.activeGroup != "" {
			s.syncTargetFromAssignments()
			s.resetSelectionForActiveGroup()
		}
		// Re-render depending on current page
		if name, _ := s.pages.GetFrontPage(); name == "groups" {
			// rebuild groups view
			s.openGroupsPage()
		} else {
			// re-render lists
			if s.curTop == "" && len(s.topKeys) > 0 {
				s.curTop = s.topKeys[0]
			}
			s.populateLeft()
			s.populateRight()
			s.setHeader()
			s.setFooter()
		}
	})
}

func runTUI() {
	if !fileExists(streamConfigPath()) {
		_ = downloadStreamConfig()
	}
	cfg, err := loadStreamConfig()
	if err != nil {
		logRed("读取 StreamConfig.yaml 失败: " + err.Error())
		return
	}
	topKeys, subMap := buildTopSub(cfg)

	st := &tvState{
		app:      tview.NewApplication(),
		header:   tview.NewTextView().SetDynamicColors(true),
		footer:   tview.NewTextView().SetDynamicColors(true),
		left:     tview.NewList().ShowSecondaryText(false),
		right:    tview.NewList().ShowSecondaryText(true),
		pages:    tview.NewPages(),
		method:   "nameserver",
		ident:    "",
		sdActive: isSmartDNSActive(),
		ngActive: isNginxActive(),
		syActive: isSystemResolverActive(),
		cfg:      cfg,
		topKeys:  topKeys,
		subMap:   subMap,
		selected: map[string]bool{},
		curTop:   "",
	}
	// try detect public IPv4 early for special unlock group
	st.selfPubV4 = getPublicIPv4()
	// record initial service states for exit prompt
	st.initialSdActive = st.sdActive
	st.initialNgActive = st.ngActive
	initSelectionFromConfig(st.selected, cfg, topKeys)
	st.reloadGroups()
	st.refreshAssignments()

	st.header.SetBorder(true).SetTitle("状态")
	st.footer.SetBorder(true).SetTitle("帮助")
	st.setHeader()
	st.setFooter()

	st.left.SetBorder(true)
	st.left.SetTitle("一级流媒体")
	st.left.SetSelectedFunc(func(i int, main, sec string, r rune) {})
	// When selection changes via up/down, update right list to preview subs
	st.left.SetChangedFunc(func(i int, main, sec string, shortcut rune) {
		if i >= 0 && i < len(st.topKeys) {
			st.curTop = st.topKeys[i]
			st.populateRight()
		}
	})
	st.left.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyRight:
			if idx := st.left.GetCurrentItem(); idx >= 0 && idx < len(st.topKeys) {
				st.curTop = st.topKeys[idx]
				st.populateRight()
				if st.single {
					st.pages.SwitchToPage("single-sub")
				}
				st.app.SetFocus(st.right)
			}
			return nil
		}
		switch ev.Rune() {
		case ' ': // select/deselect all subs in current top
			if idx := st.left.GetCurrentItem(); idx >= 0 && idx < len(st.topKeys) {
				top := st.topKeys[idx]
				subs := st.subMap[top]
				if len(subs) == 0 {
					return nil
				}
				// consider only free subs (not occupied by other groups)
				free := []string{}
				for _, sub := range subs {
					if !st.isOccupiedByOtherGroup(sub) {
						free = append(free, sub)
					}
				}
				if len(free) == 0 {
					return nil
				}
				all := true
				for _, sub := range free {
					if !st.selected[top+"/"+sub] {
						all = false
						break
					}
				}
				if all {
					for _, sub := range free {
						st.selected[top+"/"+sub] = false
					}
				} else {
					for _, sub := range free {
						st.selected[top+"/"+sub] = true
					}
				}
				if st.curTop == top {
					st.populateRight()
				}
				st.populateLeft()
				st.dirty = true
				st.setFooter()
			}
			return nil
		case 'l':
			if idx := st.left.GetCurrentItem(); idx >= 0 && idx < len(st.topKeys) {
				st.curTop = st.topKeys[idx]
				st.populateRight()
				if st.single {
					st.pages.SwitchToPage("single-sub")
				}
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
			if st.single {
				st.pages.SwitchToPage("single-top")
			}
			st.app.SetFocus(st.left)
			return nil
		case tcell.KeyRune:
			if ev.Rune() == 'h' {
				if st.single {
					st.pages.SwitchToPage("single-top")
				}
				st.app.SetFocus(st.left)
				return nil
			}
			if ev.Rune() == ' ' {
				if idx := st.right.GetCurrentItem(); idx >= 0 {
					subs := st.subMap[st.curTop]
					if idx < len(subs) {
						sub := subs[idx]
						if st.isOccupiedByOtherGroup(sub) {
							return nil
						}
						key := st.curTop + "/" + sub
						st.selected[key] = !st.selected[key]
						st.populateRight()
						st.populateLeft()
						st.dirty = true
						st.setFooter()
					}
				}
				return nil
			}
		case tcell.KeyEnter:
			if idx := st.right.GetCurrentItem(); idx >= 0 {
				subs := st.subMap[st.curTop]
				if idx < len(subs) {
					sub := subs[idx]
					if st.isOccupiedByOtherGroup(sub) {
						return nil
					}
					key := st.curTop + "/" + sub
					st.selected[key] = !st.selected[key]
					st.populateRight()
					st.populateLeft()
					st.dirty = true
					st.setFooter()
				}
			}
			return nil
		}
		return ev
	})

	st.populateLeft()
	if len(st.topKeys) > 0 {
		st.curTop = st.topKeys[0]
	}
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
		// Avoid global hotkeys when a modal-like or groups page is active
		name, _ := st.pages.GetFrontPage()
		if name == "groups" || strings.HasPrefix(name, "modal") {
			return ev
		}
		switch ev.Key() {
		case tcell.KeyRune:
			switch ev.Rune() {
			case 'q':
				if st.dirty {
					m := tview.NewModal().
						SetText("当前分组有未保存更改。\n是否保存并返回分组列表？").
						AddButtons([]string{"保存并返回", "丢弃并返回", "取消"}).
						SetDoneFunc(func(i int, l string) {
							st.pages.RemovePage("modal-leave")
							switch i {
							case 0: // 保存并返回
								if n, err := st.saveSelectionSilent(); err != nil {
									st.toast(err.Error())
								} else {
									if n == 0 {
										st.toast("未选择任何平台，无需保存")
									}
									st.openGroupsPage()
								}
							case 1: // 丢弃并返回
								st.dirty = false
								st.setFooter()
								st.openGroupsPage()
							default: // 取消
							}
						})
					st.pages.AddPage("modal-leave", center(60, 8, m), true, true)
				} else {
					st.openGroupsPage()
				}
				return nil
			case 'm':
				if st.method == "nameserver" {
					st.method = "address"
				} else {
					st.method = "nameserver"
				}
				st.setHeader()
				return nil
			case 'e':
				st.showEditIdent()
				return nil
			case 's':
				st.saveSelection()
				return nil
			case 'n':
				st.showAddGroupModal(nil)
				return nil
			case 'u':
				st.openDefaultDNSManager()
				return nil
			case 'r':
				st.reloadGroups()
				st.toast("已刷新分组")
				return nil
			case 'z':
				st.openServiceManager()
				return nil
			case 'h':
				if st.single {
					st.pages.SwitchToPage("single-top")
					st.app.SetFocus(st.left)
					return nil
				}
			case 'l':
				if st.single {
					st.pages.SwitchToPage("single-sub")
					st.app.SetFocus(st.right)
					return nil
				}
			}
		case tcell.KeyLeft:
			if st.single {
				st.pages.SwitchToPage("single-top")
				st.app.SetFocus(st.left)
				return nil
			}
		case tcell.KeyRight:
			if st.single {
				st.pages.SwitchToPage("single-sub")
				st.app.SetFocus(st.right)
				return nil
			}
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
				if narrow {
					st.pages.SwitchToPage("single-top")
				} else {
					st.pages.SwitchToPage("dual")
				}
			}
		}
		return false
	})

	// show groups page initially
	st.openGroupsPage()
	if err := st.app.Run(); err != nil {
		logRed("TUI 运行失败: " + err.Error())
	}
}

// ----- Service Manager (SmartDNS / Nginx) -----

func (s *tvState) openServiceManager() {
	options := tview.NewList().ShowSecondaryText(false)
	options.SetBorder(true).SetTitle("服务管理")
	options.AddItem("紧急重置 DNS -> 8.8.8.8", "停止 smartdns/systemd-resolved 并覆盖 /etc/resolv.conf", 0, func() {
		s.pages.RemovePage("modal")
		s.confirmEmergencyResetDNS()
	})
	options.AddItem("更新流媒体配置 (StreamConfig.yaml)", "从远程源拉取并刷新界面", 0, func() {
		s.pages.RemovePage("modal")
		logView := s.openLogModal("更新流媒体配置")
		go func() {
			append := func(line string) { s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) }) }
			append("下载最新 StreamConfig.yaml ...")
			if err := downloadStreamConfig(); err != nil {
				append("[失败] 下载失败: " + err.Error())
				return
			}
			append("解析配置 ...")
			newCfg, err := loadStreamConfig()
			if err != nil {
				append("[失败] 解析失败: " + err.Error())
				return
			}
			s.app.QueueUpdateDraw(func() {
				s.cfg = newCfg
				s.topKeys, s.subMap = buildTopSub(newCfg)
				if s.curTop == "" || len(s.subMap[s.curTop]) == 0 {
					if len(s.topKeys) > 0 {
						s.curTop = s.topKeys[0]
					}
				}
				s.refreshAssignments()
				s.resetSelectionForActiveGroup()
				s.populateLeft()
				s.populateRight()
				s.setHeader()
				s.setFooter()
			})
			append("[完成] 已更新并应用最新流媒体配置")
		}()
	})
	options.AddItem("覆盖系统 DNS -> 127.0.0.1", "停用 systemd-resolved 并写入 /etc/resolv.conf", 0, func() {
		s.pages.RemovePage("modal")
		logView := s.openLogModal("覆盖系统 DNS -> 127.0.0.1")
		go func() {
			append := func(line string) { s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) }) }
			append("停止 systemd-resolved ...")
			_ = runCmdPipe(append, "systemctl", "stop", "systemd-resolved")
			append("禁用 systemd-resolved 开机自启 ...")
			_ = runCmdPipe(append, "systemctl", "disable", "systemd-resolved")
			append("写入 /etc/resolv.conf -> 127.0.0.1 ...")
			modifyResolv("127.0.0.1")
			append("完成: 已将系统 DNS 覆盖为 127.0.0.1")
			s.flushUI()
		}()
	})
	options.AddItem("恢复系统 DNS（systemd-resolved）", "启用并启动 systemd-resolved", 0, func() {
		s.pages.RemovePage("modal")
		logView := s.openLogModal("恢复系统 DNS")
		go func() {
			append := func(line string) { s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) }) }
			append("启用 systemd-resolved 开机自启 ...")
			_ = runCmdPipe(append, "systemctl", "enable", "systemd-resolved")
			append("启动 systemd-resolved ...")
			_ = runCmdPipe(append, "systemctl", "start", "systemd-resolved")
			append("完成: 已恢复系统 DNS（/etc/resolv.conf 可能由 resolved 接管）")
			s.flushUI()
		}()
	})
	options.AddItem("SmartDNS", "安装/卸载/启动/停止/重启", 0, func() { s.pages.RemovePage("modal"); s.openSmartDNSActions() })
	options.AddItem("Nginx", "安装/启动/停止/重载/查看配置", 0, func() { s.pages.RemovePage("modal"); s.openNginxActions() })
	options.AddItem("关闭", "", 0, func() { s.pages.RemovePage("modal") })
	s.pages.AddPage("modal", center(50, 12, options), true, true)
}

func (s *tvState) confirmEmergencyResetDNS() {
	text := "将停止 smartdns 和 systemd-resolved，并把 /etc/resolv.conf 设置为 8.8.8.8。\n确定要执行紧急重置吗？"
	m := tview.NewModal().SetText(text).AddButtons([]string{"执行", "取消"}).SetDoneFunc(func(i int, l string) {
		s.pages.RemovePage("modal-emg")
		if i == 0 {
			logView := s.openLogModal("紧急重置 DNS")
			go func() {
				append := func(line string) { s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) }) }
				append("停止 smartdns ...")
				_ = runCmdPipe(append, "systemctl", "stop", "smartdns")
				append("停止 systemd-resolved ...")
				_ = runCmdPipe(append, "systemctl", "stop", "systemd-resolved")
				append("禁用 systemd-resolved 开机自启 ...")
				_ = runCmdPipe(append, "systemctl", "disable", "systemd-resolved")
				append("写入 /etc/resolv.conf -> 8.8.8.8 ...")
				modifyResolv("8.8.8.8")
				append("完成: 已紧急重置 DNS 为 8.8.8.8")
				s.flushUI()
			}()
		}
	})
	s.pages.AddPage("modal-emg", center(70, 8, m), true, true)
}

func (s *tvState) openSmartDNSActions() {
	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle("SmartDNS")
	list.AddItem("安装", "从发布包安装", 0, func() {
		s.pages.RemovePage("modal")
		logView := s.openLogModal("安装 SmartDNS")
		go func() {
			append := func(line string) {
				s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) })
			}
			if err := installSmartDNSStream(append); err != nil {
				append("[失败] " + err.Error())
			} else {
				append("[完成] SmartDNS 安装成功")
			}
			s.flushUI()
		}()
	})
	list.AddItem("卸载", "移除服务与二进制（保留配置）", 0, func() {
		s.pages.RemovePage("modal")
		s.confirmUninstallSmartDNS()
	})
	list.AddItem("启动", "", 0, func() {
		s.pages.RemovePage("modal")
		logView := s.openLogModal("启动 SmartDNS")
		go func() {
			append := func(line string) { s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) }) }
			append("启动 smartdns ...")
			_ = runCmdPipe(append, "systemctl", "start", "smartdns")
			append("启用 smartdns 开机自启 ...")
			_ = runCmdPipe(append, "systemctl", "enable", "smartdns")
			append("完成: SmartDNS 已启动（未覆盖系统 DNS）")
			s.flushUI()
		}()
	})
	list.AddItem("停止", "", 0, func() {
		s.pages.RemovePage("modal")
		logView := s.openLogModal("停止 SmartDNS")
		go func() {
			append := func(line string) { s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) }) }
			append("停止 smartdns ...")
			_ = runCmdPipe(append, "systemctl", "stop", "smartdns")
			append("禁用 smartdns 开机自启 ...")
			_ = runCmdPipe(append, "systemctl", "disable", "smartdns")
			append("完成: SmartDNS 已停止")
			s.flushUI()
		}()
	})
	list.AddItem("重启", "", 0, func() {
		s.pages.RemovePage("modal")
		logView := s.openLogModal("重启 SmartDNS")
		go func() {
			append := func(line string) { s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) }) }
			append("重启 smartdns ...")
			_ = runCmdPipe(append, "systemctl", "restart", "smartdns")
			append("完成: SmartDNS 已重启")
			s.flushUI()
		}()
	})
	list.AddItem("查看配置", SMART_CONFIG_FILE, 0, func() {
		s.pages.RemovePage("modal")
		s.openConfigViewer("SmartDNS 配置", SMART_CONFIG_FILE)
	})
	list.AddItem("返回", "", 0, func() { s.pages.RemovePage("modal"); s.openServiceManager() })
	s.pages.AddPage("modal", center(50, 14, list), true, true)
}

func (s *tvState) confirmUninstallSmartDNS() {
	m := tview.NewModal().SetText("确认卸载 SmartDNS？\n将移除服务与二进制，保留 /etc/smartdns 配置。").AddButtons([]string{"确定", "取消"}).SetDoneFunc(func(i int, l string) {
		s.pages.RemovePage("modal")
		if i == 0 {
			s.runOutsideUI("卸载 SmartDNS", uninstallSmartDNS)
			s.flushUI()
			s.toast("已卸载 SmartDNS（配置保留），界面已刷新")
		}
	})
	s.pages.AddPage("modal", center(60, 8, m), true, true)
}

func (s *tvState) openNginxActions() {
	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle("Nginx")
	list.AddItem("安装 (nginx-extras)", "通过 apt 安装 nginx-extras 并启用", 0, func() {
		s.pages.RemovePage("modal")
		logView := s.openLogModal("安装 Nginx")
		go func() {
			append := func(line string) { s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) }) }
			if err := installNginxStream(append); err != nil {
				append("[失败] " + err.Error())
			} else {
				append("[完成] Nginx 安装成功")
			}
			s.flushUI()
		}()
	})
	list.AddItem("修复并加载 stream 模块", "写入模块加载文件并校验 nginx -t", 0, func() {
		s.pages.RemovePage("modal")
		logView := s.openLogModal("修复 Nginx stream 模块")
		go func() {
			append := func(line string) { s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) }) }
			if err := writeStreamLoaderConf(); err != nil {
				append("写入模块加载文件失败: " + err.Error())
			} else {
				append("已写入 /etc/nginx/modules-enabled/50-mod-stream.conf")
			}
			if err := nginxTestAndReload(append); err != nil {
				append("[失败] nginx -t 或重载失败: " + err.Error())
			} else {
				append("[完成] 模块加载并校验成功")
			}
			s.flushUI()
		}()
	})
	list.AddItem("写入/刷新代理配置并重载", "为 80/443 写入反向代理并 nginx -t && reload", 0, func() {
		s.pages.RemovePage("modal")
		logView := s.openLogModal("写入 Nginx 配置并重载")
		go func() {
			append := func(line string) { s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) }) }
			if err := ensureNginxProxyConfigs(append); err != nil {
				append("[失败] " + err.Error())
			} else {
				if err := nginxTestAndReload(append); err != nil {
					append("[失败] nginx -t 或重载失败: " + err.Error())
				} else {
					append("[完成] Nginx 配置已生效")
				}
			}
			s.flushUI()
		}()
	})
	list.AddItem("启动", "", 0, func() {
		s.pages.RemovePage("modal")
		logView := s.openLogModal("启动 Nginx")
		go func() {
			append := func(line string) { s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) }) }
			_ = runCmdPipe(append, "systemctl", "start", "nginx")
			s.flushUI()
		}()
	})
	list.AddItem("停止", "", 0, func() {
		s.pages.RemovePage("modal")
		logView := s.openLogModal("停止 Nginx")
		go func() {
			append := func(line string) { s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) }) }
			_ = runCmdPipe(append, "systemctl", "stop", "nginx")
			s.flushUI()
		}()
	})
	list.AddItem("重启", "", 0, func() {
		s.pages.RemovePage("modal")
		logView := s.openLogModal("重启 Nginx")
		go func() {
			append := func(line string) { s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) }) }
			_ = runCmdPipe(append, "systemctl", "restart", "nginx")
			s.flushUI()
		}()
	})
	list.AddItem("查看 nginx.conf", NGINX_MAIN_CONF, 0, func() {
		s.pages.RemovePage("modal")
		s.openConfigViewer("nginx.conf", NGINX_MAIN_CONF)
	})
	list.AddItem("查看 stream 配置", NGINX_STREAM_CONF_FILE, 0, func() {
		s.pages.RemovePage("modal")
		s.openConfigViewer("stream 配置", NGINX_STREAM_CONF_FILE)
	})
	list.AddItem("查看 http 配置", NGINX_HTTP_CONF_FILE, 0, func() {
		s.pages.RemovePage("modal")
		s.openConfigViewer("http 配置", NGINX_HTTP_CONF_FILE)
	})
	list.AddItem("返回", "", 0, func() { s.pages.RemovePage("modal"); s.openServiceManager() })
	s.pages.AddPage("modal", center(60, 16, list), true, true)
}

// ----- Upstream group management -----

func parseUpstreamGroups() []dnsGroup {
	var groups []dnsGroup
	lines, err := readLines(SMART_CONFIG_FILE)
	if err != nil {
		return groups
	}
	seen := map[string]bool{}
	for _, l := range lines {
		if m := reServerGroupLine.FindStringSubmatch(l); len(m) == 3 {
			ip := m[1]
			name := m[2]
			if !seen[name] {
				groups = append(groups, dnsGroup{Name: name, IP: ip})
				seen[name] = true
			}
		}
	}
	for i := 0; i < len(groups); i++ {
		for j := i + 1; j < len(groups); j++ {
			if strings.Compare(groups[i].Name, groups[j].Name) > 0 {
				groups[i], groups[j] = groups[j], groups[i]
			}
		}
	}
	return groups
}

func (s *tvState) reloadGroups() {
	s.groups = parseUpstreamGroups()
	if s.activeGroup != "" {
		ok := false
		for _, g := range s.groups {
			if g.Name == s.activeGroup {
				ok = true
				break
			}
		}
		if !ok {
			s.activeGroup = ""
		}
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
		if ev.Key() == tcell.KeyEsc {
			s.pages.RemovePage("modal")
			return nil
		}
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
		if net.ParseIP(ip) == nil {
			ipInput.SetTitle("无效IP")
			return
		}
		if name == "" {
			nameInput.SetTitle("不能为空")
			return
		}
		for _, g := range s.groups {
			if strings.EqualFold(g.Name, name) {
				nameInput.SetTitle("已存在")
				return
			}
		}
		line := fmt.Sprintf("server %s IP -group %s -exclude-default-group", ip, name)
		if err := insertServerIntoConfig(line, SMART_CONFIG_FILE); err != nil {
			s.toast("创建分组失败: " + err.Error())
			return
		}
		s.reloadGroups()
		s.activeGroup = name
		s.refreshAssignments()
		s.syncTargetFromAssignments()
		s.setHeader()
		s.pages.RemovePage("modal-add-group")
		if after != nil {
			after()
		}
	})
	form.AddButton("取消", func() { s.pages.RemovePage("modal-add-group") })
	form.SetBorder(true).SetTitle("创建DNS分组").SetTitleAlign(tview.AlignLeft)
	modal := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(form, 0, 1, true)
	s.pages.AddPage("modal-add-group", center(60, 10, modal), true, true)
}

// ----- Default upstream DNS manager -----

func (s *tvState) openDefaultDNSManager() {
	// build list of current default servers
	ds := parseDefaultServers()
	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle("默认上游 DNS (A添加推荐, C自定义, X删除, Esc关闭)")
	for i, ip := range ds {
		idx := i
		list.AddItem(ip, "", 0, func() {
			// no-op on enter; deletion uses X
			_ = idx
		})
	}
	list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyRune {
			switch ev.Rune() {
			case 'a', 'A':
				s.showRecommendedDNS()
				return nil
			case 'c', 'C':
				s.showAddDefaultDNS()
				return nil
			case 'x', 'X':
				if len(ds) == 0 {
					return nil
				}
				idx := list.GetCurrentItem()
				if idx < 0 || idx >= len(ds) {
					return nil
				}
				s.confirmDeleteDefaultDNS(idx, ds[idx])
				return nil
			}
		}
		if ev.Key() == tcell.KeyEsc {
			s.pages.RemovePage("modal-default-dns")
			return nil
		}
		return ev
	})
	if s.pages.HasPage("modal-default-dns") {
		s.pages.RemovePage("modal-default-dns")
	}
	s.pages.AddPage("modal-default-dns", center(60, 15, list), true, true)
}

func (s *tvState) refreshDefaultDNSManager() { s.openDefaultDNSManager() }

func (s *tvState) showRecommendedDNS() {
	type rec struct{ ip, desc string }
	recs := []rec{
		{"223.5.5.5", "AliDNS"},
		{"223.6.6.6", "AliDNS"},
		{"119.29.29.29", "DNSPod"},
		{"1.1.1.1", "Cloudflare"},
		{"1.0.0.1", "Cloudflare"},
		{"8.8.8.8", "Google"},
		{"8.8.4.4", "Google"},
		{"9.9.9.9", "Quad9"},
		{"114.114.114.114", "114DNS"},
		{"180.76.76.76", "Baidu"},
	}
	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle("添加推荐 DNS (Enter添加, Esc返回)")
	for _, r := range recs {
		rr := r
		label := rr.ip + " (" + rr.desc + ")"
		list.AddItem(label, "", 0, func() {
			_ = addDefaultServer(rr.ip)
			s.pages.RemovePage("modal-rec-dns")
			s.refreshDefaultDNSManager()
		})
	}
	list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			s.pages.RemovePage("modal-rec-dns")
			return nil
		}
		return ev
	})
	s.pages.AddPage("modal-rec-dns", center(50, 15, list), true, true)
}

func (s *tvState) showAddDefaultDNS() {
	form := tview.NewForm()
	ip := tview.NewInputField().SetLabel("DNS IP: ")
	form.AddFormItem(ip)
	form.AddButton("添加", func() {
		val := strings.TrimSpace(ip.GetText())
		if net.ParseIP(val) == nil {
			ip.SetTitle("无效IP")
			return
		}
		if err := addDefaultServer(val); err != nil {
			s.toast("添加失败: " + err.Error())
			return
		}
		s.pages.RemovePage("modal-add-dns")
		s.refreshDefaultDNSManager()
	})
	form.AddButton("取消", func() { s.pages.RemovePage("modal-add-dns") })
	form.SetBorder(true).SetTitle("添加默认 DNS").SetTitleAlign(tview.AlignLeft)
	s.pages.AddPage("modal-add-dns", center(50, 8, form), true, true)
}

func (s *tvState) confirmDeleteDefaultDNS(idx int, ip string) {
	m := tview.NewModal().SetText("确认删除默认 DNS: " + ip + "？").AddButtons([]string{"删除", "取消"}).SetDoneFunc(func(i int, l string) {
		s.pages.RemovePage("modal-del-dns")
		if i == 0 {
			if err := removeDefaultServerAt(idx); err != nil {
				s.toast("删除失败: " + err.Error())
				return
			}
			s.refreshDefaultDNSManager()
		}
	})
	s.pages.AddPage("modal-del-dns", center(50, 8, m), true, true)
}

// ----- Group-first navigation -----

func (s *tvState) openGroupsPage() {
	s.activeGroup = ""
	s.setHeader()
	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle("DNS 分组 (Enter进入, N新增, D删除, R刷新, U默认DNS, Q退出)")
	s.footer.SetText("Enter 进入配置  |  n 新建分组  d 删除  r 刷新  u 默认DNS  |  z 服务管理  |  q 退出  |  进入配置后按 s 保存")
	// refresh groups data
	s.reloadGroups()
	for _, g := range s.groups {
		gg := g
		label := fmt.Sprintf("%s (%s)", g.Name, g.IP)
		list.AddItem(label, "", 0, func() {
			s.activeGroup = gg.Name
			s.refreshAssignments()
			s.syncTargetFromAssignments()
			s.resetSelectionForActiveGroup()
			// default to first top
			if len(s.topKeys) > 0 {
				s.curTop = s.topKeys[0]
			}
			// prepare config view and switch
			s.populateLeft()
			s.populateRight()
			if s.single {
				s.pages.SwitchToPage("single-top")
			} else {
				s.pages.SwitchToPage("dual")
			}
			s.setHeader()
		})
	}
	// Append special virtual group for unlock machine
	// Refresh public IPv4 before rendering label
	if s.selfPubV4 == "" {
		s.selfPubV4 = getPublicIPv4()
	}
	spIP := s.selfPubV4
	if spIP == "" {
		spIP = "(未获取公网IP，进入后可按 e 修改)"
	}
	spLabel := fmt.Sprintf("%s (address: %s)", SPECIAL_UNLOCK_GROUP_NAME, spIP)
	list.AddItem(spLabel, "将所选域名解析到本机公网 IPv4", 0, func() {
		s.activeGroup = SPECIAL_UNLOCK_GROUP_NAME
		// refresh ip once upon enter
		s.selfPubV4 = getPublicIPv4()
		s.refreshAssignments()
		s.syncTargetFromAssignments()
		s.resetSelectionForActiveGroup()
		if len(s.topKeys) > 0 {
			s.curTop = s.topKeys[0]
		}
		s.populateLeft()
		s.populateRight()
		if s.single {
			s.pages.SwitchToPage("single-top")
		} else {
			s.pages.SwitchToPage("dual")
		}
		s.setHeader()
		// 提示可以按 e 修改 IP
		if s.ident == "" {
			s.toast("未能自动获取公网 IPv4，请按 e 手动设置")
		}
	})
	list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyRune {
			switch ev.Rune() {
			case 'n', 'N':
				s.showAddGroupModal(func() { s.openGroupsPage() })
				return nil
			case 'u', 'U':
				s.openDefaultDNSManager()
				return nil
			case 'z', 'Z':
				s.openServiceManager()
				return nil
			case 'd', 'D':
				if len(s.groups) == 0 {
					return nil
				}
				idx := list.GetCurrentItem()
				if idx < 0 || idx >= len(s.groups) {
					return nil
				}
				s.confirmDeleteGroup(s.groups[idx])
				return nil
			case 'r', 'R':
				s.openGroupsPage()
				return nil
			case 'q', 'Q':
				s.confirmExit()
				return nil
			}
		}
		if ev.Key() == tcell.KeyEsc {
			s.confirmExit()
			return nil
		}
		return ev
	})
	// add/replace the "groups" page
	if s.pages.HasPage("groups") {
		s.pages.RemovePage("groups")
	}
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(s.header, 5, 0, false).AddItem(list, 0, 1, true).AddItem(s.footer, 3, 0, false)
	s.pages.AddPage("groups", body, true, true)
	s.pages.SwitchToPage("groups")
}

// confirmExit prompts to optionally restart originally running services (smartdns/nginx) before exiting.
func (s *tvState) confirmExit() {
	toRestart := []string{}
	if s.initialSdActive {
		toRestart = append(toRestart, "smartdns")
	}
	if s.initialNgActive {
		toRestart = append(toRestart, "nginx")
	}
	if len(toRestart) == 0 {
		s.app.Stop()
		return
	}
	text := "退出前是否重启以下已运行服务以应用最新配置？\n\n"
	for _, svc := range toRestart {
		text += " - " + svc + "\n"
	}
	m := tview.NewModal().
		SetText(text).
		AddButtons([]string{"重启并退出", "直接退出", "取消"}).
		SetDoneFunc(func(i int, l string) {
			s.pages.RemovePage("modal-exit")
			switch i {
			case 0:
				// restart selected services with log, then exit
				logView := s.openLogModal("重启服务并退出")
				go func() {
					append := func(line string) { s.app.QueueUpdateDraw(func() { fmt.Fprintln(logView, line) }) }
					for _, svc := range toRestart {
						append("重启 " + svc + " ...")
						_ = runCmdPipe(append, "systemctl", "restart", svc)
					}
					append("完成: 正在退出 ...")
					s.app.QueueUpdateDraw(func() { s.pages.RemovePage("modal-log") })
					s.app.Stop()
				}()
			case 1:
				s.app.Stop()
			default:
				// cancel
			}
		})
	s.pages.AddPage("modal-exit", center(60, 12, m), true, true)
}

func (s *tvState) confirmDeleteGroup(target dnsGroup) {
	text := fmt.Sprintf("确认删除分组 %s (%s)？\n仅会移除 smartdns.conf 中的该上游配置。", target.Name, target.IP)
	m := tview.NewModal().SetText(text).AddButtons([]string{"删除", "取消"}).SetDoneFunc(func(i int, l string) {
		s.pages.RemovePage("modal-del-group")
		if i == 0 {
			if err := deleteGroupFromConfig(target.Name); err != nil {
				s.toast("删除失败: " + err.Error())
				return
			}
			if strings.EqualFold(s.activeGroup, target.Name) {
				s.activeGroup = ""
			}
			s.reloadGroups()
			s.openGroupsPage()
			s.toast("已删除分组 " + target.Name)
		}
	})
	s.pages.AddPage("modal-del-group", center(60, 9, m), true, true)
}
