package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const usage = `cu-board — рейтинг успеваемости ЦУ

  cu-board          войти (если нужно), обновить свои оценки и открыть борд
  cu-board board    только открыть борд
  cu-board sync     только обновить свои оценки
  cu-board login    войти заново через браузер
  cu-board explain  показать, как посчитана твоя успеваемость
  cu-board leave    удалить свои данные из общей таблицы
  cu-board version  показать версию
`

func main() {
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	if err := run(cmd); err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		os.Exit(1)
	}
}

func run(cmd string) error {
	switch cmd {
	case "", "board", "sync", "login", "explain", "leave":
	case "version", "--version", "-v":
		fmt.Printf("cu-board %s (формат данных %d)\n", version, dataVersion)
		return nil
	default:
		fmt.Print(usage)
		return nil
	}

	if cmd == "explain" {
		lms, _, err := ensureSession(false)
		if err != nil {
			return err
		}
		return explain(lms)
	}

	store, err := openStore()
	if err != nil {
		return err
	}

	if cmd == "leave" {
		return leave(store)
	}

	meID := ""
	if cmd != "board" {
		lms, me, err := ensureSession(cmd == "login")
		if err != nil {
			return err
		}
		meID = me.ID
		fmt.Printf("✓ Привет, %s\n", me.Display())
		if err := sync(lms, me, store); err != nil {
			return err
		}
		_ = os.WriteFile(appDir()+"/me", []byte(me.ID), 0o600)
		if cmd == "sync" || cmd == "login" {
			return nil
		}
	} else if b, err := os.ReadFile(appDir() + "/me"); err == nil {
		meID = string(b)
	}

	students, rows, err := store.Load()
	if err != nil {
		return err
	}
	// скрываем игнорируемые курсы и у тех, кто ещё не пересинкался
	ign, err := store.Ignored()
	if err != nil {
		return err
	}
	kept := rows[:0]
	for _, r := range rows {
		if !ignoreList(ign).has(r.CourseID, r.Course) {
			kept = append(kept, r)
		}
	}
	return runTUI(students, kept, meID)
}

func sync(lms *LMS, me Me, store Store) error {
	fmt.Println("→ Забираю оценки из LMS…")
	courses, err := lms.Courses()
	if err != nil {
		return err
	}
	ign, err := store.Ignored()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	rows := make([]Row, 0, len(courses))
	skipped := 0
	for _, c := range courses {
		if ignoreList(ign).has(c.ID, c.Name) {
			skipped++ // в таблицу не пишем; старые строки удалит Save
			continue
		}
		rows = append(rows, Row{StudentID: me.ID, Student: me.Display(), CourseID: c.ID, Course: c.Name, Kind: c.Kind,
			Percent: c.Percent, Score: c.Score, HWDone: c.HWDone, HWTotal: c.HWTotal, Updated: now})
	}
	st := Student{ID: me.ID, Name: me.Display(), LateDays: me.LateDays, Updated: now}
	if err := store.Save(st, rows); err != nil {
		return err
	}
	fmt.Printf("✓ %d курсов сохранено (%s)", len(rows), store.Name())
	if skipped > 0 {
		fmt.Printf(", %d не учитываются (вкладка ignore)", skipped)
	}
	fmt.Println()
	return nil
}

func explain(lms *LMS) error {
	courses, err := lms.Courses()
	if err != nil {
		return err
	}
	for _, c := range courses {
		fmt.Printf("\n%s [%s]  —  %.1f%%  (балл LMS %.2f, total %.0f, ДЗ сдано %d/%d)\n",
			c.Name, kindLabel(c.Kind), c.Percent, c.Score, c.Official, c.HWDone, c.HWTotal)
		for _, a := range c.Acts {
			tag := ""
			if a.Div > a.Graded {
				tag = fmt.Sprintf("  [сумма / %d]", a.Div)
			}
			if a.Bonus {
				tag = "  [бонус: не в знаменателе]"
			}
			fmt.Printf("  %-32s вес %3.0f%%  оценено %2d/%-2d  средний %5.2f%s\n",
				a.Name, a.Weight*100, a.Graded, a.Total, a.Avg, tag)
		}
	}
	return nil
}

func leave(store Store) error {
	_, me, err := ensureSession(false)
	if err != nil {
		return err
	}
	fmt.Printf("Удалить данные «%s» из общей таблицы? Друзья перестанут видеть тебя в борде. [y/N] ", me.Display())
	var answer string
	_, _ = fmt.Scanln(&answer)
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "д" && a != "yes" && a != "да" {
		fmt.Println("Отменено.")
		return nil
	}
	if err := store.Delete(me.ID); err != nil {
		return err
	}
	fmt.Println("✓ Твои данные удалены. Следующий запуск cu-board снова добавит тебя в рейтинг.")
	return nil
}
