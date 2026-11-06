package main

import (
	"regexp"
	"sort"
	"strings"
)

// Правила расчёта успеваемости (как в таблице «Учеба ЦУ»):
//
//	балл активности = среднее оценок (по 10-балльной шкале) по уже оценённым заданиям
//	                  (state evaluated/failed; не сдано в срок = 0, на проверке/не выдано — не считается)
//	успеваемость    = Σ вес × балл / Σ весов активностей, где есть хоть одна оценка
//
// Бонусные активности только добавляют баллы и не входят в знаменатель —
// иначе нулевые бонусы (например, в теорвере у LMS вес 0.15) тянут процент вниз.

// override — ручная правка активности для курсов, где LMS настроена не так, как в правилах курса.
type override struct {
	Weight        float64 // 0 — оставить вес из LMS
	EvaluatedOnly bool    // не считать failed-задания (несданные необязательные / ещё не проверенные)
	// MinCount > 0: балл = min(10, сумма оценок / min(выдано, MinCount)) —
	// «надо сдать хотя бы MinCount заданий», недостающие считаются нулями.
	MinCount int
}

// overrides: курс → подстрока названия активности (без учёта регистра) → правка.
var overrides = map[int]map[string]override{
	// Методы непрерывной оптимизации: в LMS ДЗ с весом 0, бонусы 0.2.
	// По правилам курса: ДЗ 20%, бонусы 10%, КР 40%, экзамен 40%.
	// ДЗ: сумма баллов / min(выдано, 13) — надо сдать хотя бы 13 из всех выданных.
	1479: {
		"дз":    {Weight: 0.2, EvaluatedOnly: true, MinCount: 13},
		"бонус": {Weight: 0.1},
	},
}

type ActStat struct {
	Name   string
	Weight float64
	Graded int     // оценённых заданий
	Total  int     // всего заданий за курс (по LMS)
	Avg    float64 // средний балл по оценённым, 0..10
	Div    int     // на сколько делили сумму (обычно = Graded)
	Bonus  bool
}

type courseResult struct {
	percent, score  float64
	weighted        bool
	hasGrades       bool // есть хоть одна оценка в основной (не бонусной) активности
	hwDone, hwTotal int
	acts            []ActStat
}

func isGraded(t perfTask) bool {
	return (t.State == "evaluated" || t.State == "failed") && t.Score != nil
}

func findOverride(courseID int, name string) (override, bool) {
	low := strings.ToLower(name)
	for sub, o := range overrides[courseID] {
		if strings.Contains(low, sub) {
			return o, true
		}
	}
	return override{}, false
}

func computeCourse(courseID int, tasks []perfTask) courseResult {
	type acc struct {
		ActStat
		scores        []float64 // нормированные оценки 0..10 по оценённым
		best          *int
		evaluatedOnly bool
		minCount      int
		issued        int // всего выданных заданий (в любом статусе)
	}
	var res courseResult
	byID := map[int]*acc{}
	var order []int
	for _, t := range tasks {
		a := byID[t.Activity.ID]
		if a == nil {
			a = &acc{ActStat: ActStat{
				Name:   t.Activity.Name,
				Weight: t.Activity.Weight,
				Total:  t.Activity.MaxExercisesCount,
				Bonus:  strings.Contains(strings.ToLower(t.Activity.Name), "бонус"),
			}, best: t.Activity.BestScoresCount}
			if o, ok := findOverride(courseID, t.Activity.Name); ok {
				if o.Weight > 0 {
					a.Weight = o.Weight
				}
				a.evaluatedOnly, a.minCount = o.EvaluatedOnly, o.MinCount
			}
			byID[t.Activity.ID] = a
			order = append(order, t.Activity.ID)
		}
		a.issued++
		if isHomework(t.Activity.Name) && isGraded(t) {
			res.hwTotal++
			if *t.Score > 0 {
				res.hwDone++
			}
		}
		if !isGraded(t) || (a.evaluatedOnly && t.State != "evaluated") {
			continue
		}
		s := *t.Score
		if t.ExtraScore != nil {
			s += *t.ExtraScore
		}
		norm := 0.0
		if t.MaxScore > 0 {
			norm = s / t.MaxScore * 10
		}
		a.scores = append(a.scores, norm)
	}

	num, den := 0.0, 0.0
	for _, id := range order {
		a := byID[id]
		if a.Weight == 0 {
			continue
		}
		res.weighted = true
		a.Graded = len(a.scores)
		a.Total = max(a.Total, a.Graded)
		if a.Graded > 0 {
			sum := 0.0
			for _, s := range a.scores {
				sum += s
			}
			n := a.Graded
			if a.minCount > 0 {
				n = min(a.minCount, a.issued)
			}
			a.Avg, a.Div = min(10, sum/float64(n)), n
			num += a.Weight * a.Avg
			if !a.Bonus {
				den += a.Weight
			}
		}
		// балл «как в LMS»: сумма лучших оценок / число заданий за весь курс
		sorted := append([]float64(nil), a.scores...)
		sort.Sort(sort.Reverse(sort.Float64Slice(sorted)))
		n := a.Total
		if a.best != nil && *a.best > 0 {
			sorted = sorted[:min(len(sorted), *a.best)]
			n = min(n, *a.best)
		}
		if n > 0 {
			sum := 0.0
			for _, s := range sorted {
				sum += s
			}
			res.score += a.Weight * sum / float64(n)
		}
		res.acts = append(res.acts, a.ActStat)
	}
	if den > 0 {
		res.hasGrades = true
		res.percent = num / den * 10 // num в шкале 0..10 → проценты
	}
	return res
}

// isHomework — активность с домашками (в т.ч. с нулевым весом, как в МНО), без бонусных.
var hwRe = regexp.MustCompile(`(?i)домашн|(^|[^\pL])дз([^\pL]|$)`)

func isHomework(name string) bool {
	return hwRe.MatchString(name) && !strings.Contains(strings.ToLower(name), "бонус")
}
