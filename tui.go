package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	accent   = lipgloss.Color("#7C5CFF")
	muted    = lipgloss.Color("#8A8A99")
	gold     = lipgloss.Color("#F5C542")
	titleSt  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(accent).Padding(0, 1)
	tabSt    = lipgloss.NewStyle().Foreground(muted).Padding(0, 1)
	mutedSt  = lipgloss.NewStyle().Foreground(muted)
	selSt    = lipgloss.NewStyle().Foreground(accent).Bold(true)
	meSt     = lipgloss.NewStyle().Foreground(gold).Bold(true)
	boxSt    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(1, 2)
	barFull  = lipgloss.NewStyle().Foreground(accent)
	barEmpty = lipgloss.NewStyle().Foreground(lipgloss.Color("#3A3A48"))
)

var tabs = []string{"Общий", "Курсы", "ДЗ", "Late days", "Профиль"}

const (
	tabOverall = iota
	tabCourses
	tabHW
	tabLate
	tabProfile
)

type courseItem struct {
	id      int
	name    string
	entries []Row
	mine    bool
}

// entry — строка любого лидерборда.
type entry struct {
	id, name string
	key      float64 // по нему сортируем
	frac     float64 // заполненность полоски 0..1
	value    string  // что пишем справа
	note     string  // мелкий комментарий
}

// seen — когда студент последний раз синкался: из вкладки students, иначе по его строкам.
func (m model) seen() map[string]time.Time {
	out := map[string]time.Time{}
	for _, r := range m.rows {
		if r.Updated.After(out[r.StudentID]) {
			out[r.StudentID] = r.Updated
		}
	}
	for _, s := range m.students {
		if s.Updated.After(out[s.ID]) {
			out[s.ID] = s.Updated
		}
	}
	return out
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < 2*time.Minute:
		return "только что"
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d дн назад", int(d.Hours()/24))
	}
	return t.Local().Format("02.01.2006")
}

type model struct {
	students  []Student
	rows      []Row
	courses   []courseItem
	tab       int
	cursor    int
	open      int // -1 — список курсов
	electives bool
	meID      string
}

func runTUI(students []Student, rows []Row, meID string) error {
	m := model{students: students, rows: rows, courses: buildCourses(rows, meID), open: -1, meID: meID}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func buildCourses(rows []Row, meID string) []courseItem {
	byID := map[int]*courseItem{}
	for _, r := range rows {
		c := byID[r.CourseID]
		if c == nil {
			c = &courseItem{id: r.CourseID, name: r.Course}
			byID[r.CourseID] = c
		}
		c.entries = append(c.entries, r)
		c.mine = c.mine || r.StudentID == meID
	}
	var out []courseItem
	for _, c := range byID {
		out = append(out, *c)
	}
	// сначала мои курсы, потом по числу участников, потом по имени
	sort.Slice(out, func(i, j int) bool {
		if out[i].mine != out[j].mine {
			return out[i].mine
		}
		if len(out[i].entries) != len(out[j].entries) {
			return len(out[i].entries) > len(out[j].entries)
		}
		return out[i].name < out[j].name
	})
	return out
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "tab", "right", "l":
		m.tab, m.open = (m.tab+1)%len(tabs), -1
	case "shift+tab", "left", "h":
		m.tab, m.open = (m.tab+len(tabs)-1)%len(tabs), -1
	case "1", "2", "3", "4", "5":
		m.tab, m.open = int(k.String()[0]-'1'), -1
	case "f":
		m.electives = !m.electives
	case "up", "k":
		if m.tab == tabCourses && m.open < 0 && m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.tab == tabCourses && m.open < 0 && m.cursor < len(m.courses)-1 {
			m.cursor++
		}
	case "enter", " ":
		if m.tab == tabCourses && m.open < 0 && len(m.courses) > 0 {
			m.open = m.cursor
		}
	case "esc", "backspace":
		m.open = -1
	}
	return m, nil
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(titleSt.Render("🏆 CU Board") + " ")
	for i, t := range tabs {
		label := fmt.Sprintf("%d %s", i+1, t)
		if i == m.tab {
			b.WriteString(selSt.Padding(0, 1).Underline(true).Render(label))
		} else {
			b.WriteString(tabSt.Render(label))
		}
	}
	b.WriteString("\n\n")

	help := "←/→ вкладки · q выход"
	switch m.tab {
	case tabCourses:
		if m.open >= 0 {
			c := m.courses[m.open]
			b.WriteString(selSt.Render(c.name) + "\n\n")
			b.WriteString(m.board(m.courseEntries(c)))
			help = "esc назад · " + help
		} else {
			b.WriteString(m.courseList())
			help = "↑/↓ выбор · enter открыть · " + help
		}
	case tabOverall:
		b.WriteString(mutedSt.Render("Средняя успеваемость по "+m.scope()) + "\n\n")
		b.WriteString(m.board(m.overallEntries()))
		help = "f факультативы вкл/выкл · " + help
	case tabHW:
		b.WriteString(mutedSt.Render("Доля ДЗ, сданных не на 0, по "+m.scope()) + "\n\n")
		b.WriteString(m.board(m.hwEntries()))
		help = "f факультативы вкл/выкл · " + help
	case tabLate:
		b.WriteString(mutedSt.Render("Сколько late days осталось") + "\n\n")
		b.WriteString(m.board(m.lateEntries()))
	case tabProfile:
		b.WriteString(m.profile())
		help = "f факультативы вкл/выкл · " + help
	}
	b.WriteString("\n" + mutedSt.Render(help))
	return boxSt.Render(b.String())
}

func kindLabel(kind string) string {
	if kind == "elective" {
		return "факультатив"
	}
	return "основной"
}

func (m model) scope() string {
	if m.electives {
		return "всем курсам (вместе с факультативами)"
	}
	return "обязательным курсам"
}

func (m model) counted(r Row) bool { return m.electives || r.Kind != "elective" }

func (m model) courseList() string {
	if len(m.courses) == 0 {
		return "Пока пусто — запусти синк.\n"
	}
	var b strings.Builder
	for i, c := range m.courses {
		cursor, name := "  ", c.name
		if i == m.cursor {
			cursor, name = selSt.Render("▸ "), selSt.Render(c.name)
		}
		info := fmt.Sprintf("  %d чел.", len(c.entries))
		line := cursor + name + mutedSt.Render(info)
		if c.mine {
			line += meSt.Render(" •")
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(mutedSt.Render("\n") + meSt.Render("•") + mutedSt.Render(" твои курсы") + "\n")
	return b.String()
}

func (m model) courseEntries(c courseItem) []entry {
	var out []entry
	for _, r := range c.entries {
		// один и тот же курс у одного может быть основным, у другого — факультативом
		name := r.Student + " (" + kindLabel(r.Kind) + ")"
		out = append(out, entry{id: r.StudentID, name: name, key: r.Percent,
			frac: r.Percent / 100, value: fmt.Sprintf("%5.1f%%", r.Percent)})
	}
	return out
}

func (m model) overallEntries() []entry {
	type agg struct {
		name string
		sum  float64
		n    int
	}
	by := map[string]*agg{}
	for _, r := range m.rows {
		if !m.counted(r) {
			continue
		}
		a := by[r.StudentID]
		if a == nil {
			a = &agg{name: r.Student}
			by[r.StudentID] = a
		}
		a.sum += r.Percent
		a.n++
	}
	var out []entry
	for id, a := range by {
		avg := a.sum / float64(a.n)
		out = append(out, entry{id: id, name: a.name, key: avg, frac: avg / 100,
			value: fmt.Sprintf("%5.1f%%", avg), note: fmt.Sprintf("курсов: %d", a.n)})
	}
	return out
}

func (m model) hwEntries() []entry {
	type agg struct {
		name        string
		done, total int
	}
	by := map[string]*agg{}
	for _, r := range m.rows {
		if !m.counted(r) {
			continue
		}
		a := by[r.StudentID]
		if a == nil {
			a = &agg{name: r.Student}
			by[r.StudentID] = a
		}
		a.done += r.HWDone
		a.total += r.HWTotal
	}
	var out []entry
	for id, a := range by {
		if a.total == 0 {
			continue
		}
		p := float64(a.done) / float64(a.total)
		out = append(out, entry{id: id, name: a.name, key: p, frac: p,
			value: fmt.Sprintf("%5.1f%%", p*100), note: fmt.Sprintf("%d/%d", a.done, a.total)})
	}
	return out
}

func (m model) lateEntries() []entry {
	most := 1
	for _, s := range m.students {
		most = max(most, s.LateDays)
	}
	var out []entry
	for _, s := range m.students {
		out = append(out, entry{id: s.ID, name: s.Name, key: float64(s.LateDays),
			frac: float64(s.LateDays) / float64(most), value: fmt.Sprintf("%3d", s.LateDays)})
	}
	return out
}

func (m model) board(es []entry) string {
	if len(es) == 0 {
		return "Пока пусто.\n"
	}
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].key != es[j].key {
			return es[i].key > es[j].key
		}
		return es[i].name < es[j].name
	})
	seen := m.seen()
	nameW, noteW := 0, 0
	for _, e := range es {
		nameW = max(nameW, lipgloss.Width(e.name))
		noteW = max(noteW, lipgloss.Width(e.note))
	}
	const barW = 24
	var b strings.Builder
	for i, e := range es {
		place := fmt.Sprintf("%2d.", i+1)
		switch i {
		case 0:
			place = "🥇 "
		case 1:
			place = "🥈 "
		case 2:
			place = "🥉 "
		}
		filled := min(max(int(e.frac*barW), 0), barW)
		bar := barFull.Render(strings.Repeat("█", filled)) + barEmpty.Render(strings.Repeat("░", barW-filled))
		name := e.name + strings.Repeat(" ", nameW-lipgloss.Width(e.name))
		note := ""
		if noteW > 0 {
			note = "  " + e.note + strings.Repeat(" ", noteW-lipgloss.Width(e.note))
		}
		when := ago(seen[e.id])
		note += "  " + when + strings.Repeat(" ", max(0, 12-lipgloss.Width(when)))
		value := e.value
		if e.id == m.meID {
			name, value = meSt.Render(name), meSt.Render(value)
			note += meSt.Render("  ← ты")
		}
		b.WriteString(fmt.Sprintf("%s %s  %s  %s%s\n", place, name, bar, value, mutedSt.Render(note)))
	}
	return b.String()
}

// profile — мои курсы, отсортированные по успеваемости, и место на каждом.
func (m model) profile() string {
	if m.meID == "" {
		return "Профиль появится после первого синка (запусти cu-board без аргументов).\n"
	}
	var me Student
	for _, s := range m.students {
		if s.ID == m.meID {
			me = s
		}
	}
	var mine []Row
	for _, r := range m.rows {
		if r.StudentID == m.meID {
			mine = append(mine, r)
		}
	}
	if len(mine) == 0 {
		return "Твоих данных пока нет в базе — запусти синк.\n"
	}
	if me.Name == "" {
		me.Name = mine[0].Student
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].Percent > mine[j].Percent })

	sum, n, done, total := 0.0, 0, 0, 0
	for _, r := range mine {
		if m.counted(r) {
			sum, n = sum+r.Percent, n+1
			done, total = done+r.HWDone, total+r.HWTotal
		}
	}
	var b strings.Builder
	b.WriteString(meSt.Render(me.Name) + mutedSt.Render(fmt.Sprintf("  ·  late days: %d  ·  синк: %s", me.LateDays, ago(m.seen()[m.meID]))) + "\n")
	if n > 0 {
		hw := "—"
		if total > 0 {
			hw = fmt.Sprintf("%.0f%% (%d/%d)", float64(done)/float64(total)*100, done, total)
		}
		b.WriteString(mutedSt.Render(fmt.Sprintf("Средняя по %s: ", m.scope())) + selSt.Render(fmt.Sprintf("%.1f%%", sum/float64(n))) +
			mutedSt.Render("  ·  ДЗ: ") + selSt.Render(hw) + "\n")
	}
	b.WriteString("\n")

	nameW := 0
	for _, r := range mine {
		nameW = max(nameW, lipgloss.Width(truncate(r.Course, 34)))
	}
	const barW = 20
	for _, r := range mine {
		r.Course = truncate(r.Course, 34)
		filled := min(max(int(r.Percent/100*barW), 0), barW)
		bar := barFull.Render(strings.Repeat("█", filled)) + barEmpty.Render(strings.Repeat("░", barW-filled))
		name := r.Course + strings.Repeat(" ", nameW-lipgloss.Width(r.Course))
		if !m.counted(r) {
			name = mutedSt.Render(name)
		}
		place, of := m.placeOn(r)
		info := fmt.Sprintf("  место %d/%d  ·  ДЗ %d/%d  ·  %s", place, of, r.HWDone, r.HWTotal, kindLabel(r.Kind))
		b.WriteString(fmt.Sprintf("%s  %s  %5.1f%%%s\n", name, bar, r.Percent, mutedSt.Render(info)))
	}
	return b.String()
}

// placeOn — моё место на курсе строки r среди всех, кто его синкнул.
func (m model) placeOn(r Row) (place, of int) {
	place = 1
	for _, o := range m.rows {
		if o.CourseID != r.CourseID {
			continue
		}
		of++
		if o.StudentID != r.StudentID && o.Percent > r.Percent {
			place++
		}
	}
	return place, of
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
