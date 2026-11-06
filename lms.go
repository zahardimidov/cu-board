package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const baseURL = "https://my.centraluniversity.ru"

var ErrUnauthorized = errors.New("сессия LMS недействительна")

type LMS struct {
	cookie string
	http   *http.Client
}

func NewLMS(cookie string) *LMS {
	return &LMS{cookie: cookie, http: &http.Client{Timeout: 20 * time.Second}}
}

// get делает GET с одним повтором: LMS иногда отвечает по таймауту.
func (l *LMS) get(path string, out any) error {
	err := l.getOnce(path, out)
	if err != nil && !errors.Is(err, ErrUnauthorized) {
		time.Sleep(time.Second)
		err = l.getOnce(path, out)
	}
	return err
}

func (l *LMS) getOnce(path string, out any) error {
	req, err := http.NewRequest("GET", baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Cookie", "bff.cookie="+l.cookie)
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9")
	resp, err := l.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type Me struct {
	ID        string `json:"id"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	LateDays  int    `json:"lateDaysBalance"`
}

func (m Me) Display() string {
	if m.LastName == "" {
		return m.FirstName
	}
	return m.FirstName + " " + string([]rune(m.LastName)[:1]) + "."
}

func (l *LMS) Me() (Me, error) {
	var me Me
	err := l.get("/api/micro-lms/students/me", &me)
	return me, err
}

type Course struct {
	ID       int
	Name     string
	Kind     string  // required / elective
	HWDone   int     // ДЗ, сданных не на 0
	HWTotal  int     // ДЗ с выставленной оценкой
	Percent  float64 // успеваемость: % от возможного по уже оценённым заданиям
	Score    float64 // балл как в LMS (по 10-балльной шкале, с учётом будущих заданий)
	Official float64 // округлённый total из LMS
	Acts     []ActStat
}

type perfCourse struct {
	ID         int      `json:"id"`
	Name       string   `json:"name"`
	IsArchived bool     `json:"isArchived"`
	Total      float64  `json:"total"`
	Blocker    *float64 `json:"blockerScore"`
	Status     string   `json:"courseStudentsStatus"`
}

type perfTask struct {
	State      string   `json:"state"`
	Score      *float64 `json:"score"`
	ExtraScore *float64 `json:"extraScore"`
	MaxScore   float64  `json:"maxScore"`
	Activity   struct {
		ID                int     `json:"id"`
		Name              string  `json:"name"`
		Weight            float64 `json:"weight"`
		MaxExercisesCount int     `json:"maxExercisesCount"`
		BestScoresCount   *int    `json:"bestScoresCount"`
	} `json:"activity"`
}

// Courses возвращает курсы с оценками, пропуская курсы без взвешенных активностей
// (физра, ознакомительные и т.п.).
func (l *LMS) Courses() ([]Course, error) {
	var perf struct {
		Courses []perfCourse `json:"courses"`
	}
	if err := l.get("/api/micro-lms/performance/student", &perf); err != nil {
		return nil, err
	}
	var out []Course
	for _, c := range perf.Courses {
		if c.IsArchived || c.Blocker == nil || c.Status == "listener" {
			continue
		}
		var detail struct {
			Tasks []perfTask `json:"tasks"`
		}
		if err := l.get(fmt.Sprintf("/api/micro-lms/courses/%d/student-performance", c.ID), &detail); err != nil {
			return nil, err
		}
		r := computeCourse(c.ID, detail.Tasks)
		// без весов — физра и т.п.; без оценок — курс только начался, 0% был бы враньём
		if !r.weighted || !r.hasGrades {
			continue
		}
		out = append(out, Course{
			ID: c.ID, Name: cleanName(c.Name), Official: c.Total, Kind: c.Status,
			HWDone: r.hwDone, HWTotal: r.hwTotal,
			Percent: r.percent, Score: r.score, Acts: r.acts,
		})
	}
	return out, nil
}

var emojiRe = regexp.MustCompile(`[\x{1F000}-\x{1FAFF}\x{2600}-\x{27BF}\x{FE0F}]`)

func cleanName(s string) string {
	return strings.Join(strings.Fields(emojiRe.ReplaceAllString(s, "")), " ")
}
