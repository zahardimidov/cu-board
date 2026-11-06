package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// Row — результаты одного студента по одному курсу.
type Row struct {
	StudentID string
	Student   string
	CourseID  int
	Course    string
	Kind      string  // required / elective (courseStudentsStatus из LMS)
	Percent   float64 // успеваемость, %
	Score     float64 // балл как в LMS, 0..10
	HWDone    int     // ДЗ, сданных не на 0
	HWTotal   int     // ДЗ с выставленной оценкой (включая 0)
	Updated   time.Time
}

// Student — данные студента, не привязанные к курсу.
type Student struct {
	ID       string
	Name     string
	LateDays int
	Updated  time.Time
}

type Store interface {
	Save(st Student, rows []Row) error
	Load() ([]Student, []Row, error)
	Delete(studentID string) error
	Ignored() ([]string, error) // курсы, которые не учитываем вообще (SOFT и т.п.)
	Name() string
}

// secrets/credentials.json — ключ service account, secrets/sheet_id — id таблицы.
// Вшиваются в бинарник при сборке, чтобы друзьям ничего не настраивать.
//
//go:embed secrets/*
var secrets embed.FS

func openStore() (Store, error) {
	creds, _ := secrets.ReadFile("secrets/credentials.json")
	sheetID, _ := secrets.ReadFile("secrets/sheet_id")
	if p := os.Getenv("CU_BOARD_CREDENTIALS"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		creds = b
	}
	if v := os.Getenv("CU_BOARD_SHEET_ID"); v != "" {
		sheetID = []byte(v)
	}
	id := strings.TrimSpace(string(sheetID))
	if len(creds) == 0 || id == "" {
		return &fileStore{path: filepath.Join(appDir(), "db.json")}, nil
	}
	svc, err := sheets.NewService(context.Background(), option.WithCredentialsJSON(creds))
	if err != nil {
		return nil, err
	}
	s := &sheetStore{svc: svc, id: id}
	return s, s.ensureTabs()
}

// ---------- Google Sheets ----------

const (
	metaTab   = "meta"
	ignoreTab = "ignore"
)

// defaultIgnored — чем заполнить вкладку ignore при её создании.
var defaultIgnored = []string{"Стресс-менеджмент и эмоциональный интеллект"}

type tabSpec struct {
	name    string
	header  []any
	keyCols []int // колонки, составляющие ключ строки
}

var (
	dataTab = tabSpec{"data",
		[]any{"student_id", "student", "course_id", "course", "kind", "percent", "score", "hw_done", "hw_total", "updated"},
		[]int{0, 2}}
	studentsTab = tabSpec{"students",
		[]any{"student_id", "student", "late_days", "updated"},
		[]int{0}}
)

func (t tabSpec) lastCol() string { return string(rune('A' + len(t.header) - 1)) }

func (t tabSpec) key(v []any) string {
	parts := make([]string, len(t.keyCols))
	for i, c := range t.keyCols {
		if c < len(v) {
			parts[i] = fmt.Sprint(v[c])
		}
	}
	return strings.Join(parts, "|")
}

type sheetStore struct {
	svc  *sheets.Service
	id   string
	tabs map[string]int64 // название вкладки -> sheetId (нужен для удаления строк)
}

func (s *sheetStore) Name() string { return "Google Sheets" }

func (s *sheetStore) ensureTabs() error {
	sp, err := s.svc.Spreadsheets.Get(s.id).Do()
	if err != nil {
		return fmt.Errorf("нет доступа к таблице (расшарь её на email service account): %w", err)
	}
	have := map[string]bool{}
	for _, sh := range sp.Sheets {
		have[sh.Properties.Title] = true
	}
	var reqs []*sheets.Request
	for _, name := range []string{dataTab.name, studentsTab.name, metaTab, ignoreTab} {
		if !have[name] {
			reqs = append(reqs, &sheets.Request{AddSheet: &sheets.AddSheetRequest{Properties: &sheets.SheetProperties{Title: name}}})
		}
	}
	if len(reqs) > 0 {
		if _, err := s.svc.Spreadsheets.BatchUpdate(s.id, &sheets.BatchUpdateSpreadsheetRequest{Requests: reqs}).Do(); err != nil {
			return err
		}
		if sp, err = s.svc.Spreadsheets.Get(s.id).Do(); err != nil {
			return err
		}
	}
	s.tabs = map[string]int64{}
	for _, sh := range sp.Sheets {
		s.tabs[sh.Properties.Title] = sh.Properties.SheetId
	}

	// Проверяем версию ДО любой записи, чтобы старый бинарник ничего не испортил.
	meta, err := s.svc.Spreadsheets.Values.Get(s.id, metaTab+"!B1").ValueRenderOption("UNFORMATTED_VALUE").Do()
	if err != nil {
		return err
	}
	if len(meta.Values) > 0 && len(meta.Values[0]) > 0 {
		if v := int(num(meta.Values[0][0])); v > dataVersion {
			return fmt.Errorf("Эта версия cu-board (%s) устарела: данные в таблице уже формата %d, а она понимает только %d.\n  %s",
				version, v, dataVersion, updateHint())
		}
	}

	// заголовки и версию пишем всегда — чтобы они соответствовали текущему формату
	headers := []*sheets.ValueRange{
		{Range: metaTab + "!A1", Values: [][]any{{"data_version", dataVersion}}},
		{Range: ignoreTab + "!A1", Values: [][]any{{"Не учитывать курсы (часть названия или id курса, по одному в строке)"}}},
	}
	if !have[ignoreTab] {
		var vals [][]any
		for _, c := range defaultIgnored {
			vals = append(vals, []any{c})
		}
		headers = append(headers, &sheets.ValueRange{Range: ignoreTab + "!A2", Values: vals})
	}
	for _, t := range []tabSpec{dataTab, studentsTab} {
		headers = append(headers, &sheets.ValueRange{Range: t.name + "!A1", Values: [][]any{t.header}})
	}
	_, err = s.svc.Spreadsheets.Values.BatchUpdate(s.id, &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "RAW", Data: headers,
	}).Do()
	return err
}

func (s *sheetStore) read(t tabSpec) ([][]any, error) {
	resp, err := s.svc.Spreadsheets.Values.Get(s.id, t.name+"!A2:"+t.lastCol()).
		ValueRenderOption("UNFORMATTED_VALUE").Do()
	if err != nil {
		return nil, err
	}
	return resp.Values, nil
}

// upsert обновляет строки по ключу и дописывает новые. Строки владельца owner (колонка A),
// которых нет в rows, а также дубли ключей удаляются — например, курсы прошлого семестра.
func (s *sheetStore) upsert(t tabSpec, rows [][]any, owner string) error {
	existing, err := s.read(t)
	if err != nil {
		return err
	}
	fresh := map[string]bool{}
	for _, v := range rows {
		fresh[t.key(v)] = true
	}
	idx := map[string]int{} // ключ -> номер строки в таблице
	var stale []int
	for i, v := range existing {
		n, k := i+2, t.key(v)
		mine := len(v) > 0 && fmt.Sprint(v[0]) == owner
		if _, dup := idx[k]; (dup || !fresh[k]) && mine {
			stale = append(stale, n)
			continue
		}
		idx[k] = n
	}
	var updates []*sheets.ValueRange
	var appends [][]any
	for _, v := range rows {
		if n, ok := idx[t.key(v)]; ok {
			updates = append(updates, &sheets.ValueRange{
				Range: fmt.Sprintf("%s!A%d:%s%d", t.name, n, t.lastCol(), n), Values: [][]any{v},
			})
		} else {
			appends = append(appends, v)
		}
	}
	if len(updates) > 0 {
		_, err := s.svc.Spreadsheets.Values.BatchUpdate(s.id, &sheets.BatchUpdateValuesRequest{
			ValueInputOption: "RAW", Data: updates,
		}).Do()
		if err != nil {
			return err
		}
	}
	if len(stale) > 0 {
		// удаляем снизу вверх, чтобы номера оставшихся строк не съезжали
		var reqs []*sheets.Request
		for i := len(stale) - 1; i >= 0; i-- {
			reqs = append(reqs, &sheets.Request{DeleteDimension: &sheets.DeleteDimensionRequest{Range: &sheets.DimensionRange{
				SheetId: s.tabs[t.name], Dimension: "ROWS", StartIndex: int64(stale[i] - 1), EndIndex: int64(stale[i]),
			}}})
		}
		if _, err := s.svc.Spreadsheets.BatchUpdate(s.id, &sheets.BatchUpdateSpreadsheetRequest{Requests: reqs}).Do(); err != nil {
			return err
		}
	}
	if len(appends) > 0 {
		_, err := s.svc.Spreadsheets.Values.Append(s.id, t.name+"!A:"+t.lastCol(), &sheets.ValueRange{Values: appends}).
			ValueInputOption("RAW").InsertDataOption("INSERT_ROWS").Do()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *sheetStore) Save(st Student, rows []Row) error {
	data := make([][]any, 0, len(rows))
	for _, r := range rows {
		data = append(data, []any{r.StudentID, r.Student, r.CourseID, r.Course, r.Kind,
			r.Percent, r.Score, r.HWDone, r.HWTotal, r.Updated.Format(time.RFC3339)})
	}
	if err := s.upsert(dataTab, data, st.ID); err != nil {
		return err
	}
	return s.upsert(studentsTab, [][]any{{st.ID, st.Name, st.LateDays, st.Updated.Format(time.RFC3339)}}, st.ID)
}

func (s *sheetStore) Ignored() ([]string, error) {
	resp, err := s.svc.Spreadsheets.Values.Get(s.id, ignoreTab+"!A2:A").ValueRenderOption("UNFORMATTED_VALUE").Do()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range resp.Values {
		if len(v) > 0 {
			out = append(out, fmt.Sprint(v[0]))
		}
	}
	return out, nil
}

// Delete убирает все строки студента: upsert с пустым набором удаляет все «свои» строки.
func (s *sheetStore) Delete(studentID string) error {
	if err := s.upsert(dataTab, nil, studentID); err != nil {
		return err
	}
	return s.upsert(studentsTab, nil, studentID)
}

func (s *sheetStore) Load() ([]Student, []Row, error) {
	data, err := s.read(dataTab)
	if err != nil {
		return nil, nil, err
	}
	var rows []Row
	pos := map[string]int{} // ключ -> индекс в rows; при дублях побеждает последняя строка
	for _, v := range data {
		if len(v) < len(dataTab.header) {
			continue // строка старого формата — обновится при следующем синке её владельца
		}
		r := Row{
			StudentID: str(v[0]), Student: str(v[1]), CourseID: int(num(v[2])), Course: str(v[3]), Kind: str(v[4]),
			Percent: num(v[5]), Score: num(v[6]), HWDone: int(num(v[7])), HWTotal: int(num(v[8])), Updated: date(v[9]),
		}
		if i, ok := pos[dataTab.key(v)]; ok {
			rows[i] = r
			continue
		}
		pos[dataTab.key(v)] = len(rows)
		rows = append(rows, r)
	}
	sts, err := s.read(studentsTab)
	if err != nil {
		return nil, nil, err
	}
	var students []Student
	for _, v := range sts {
		if len(v) < len(studentsTab.header) {
			continue
		}
		students = append(students, Student{ID: str(v[0]), Name: str(v[1]), LateDays: int(num(v[2])), Updated: date(v[3])})
	}
	return students, rows, nil
}

func str(v any) string { return fmt.Sprint(v) }

func num(v any) float64 {
	f, _ := strconv.ParseFloat(strings.ReplaceAll(fmt.Sprint(v), ",", "."), 64)
	return f
}

func date(v any) time.Time {
	t, _ := time.Parse(time.RFC3339, fmt.Sprint(v))
	return t
}

// ---------- локальный файл (если таблица не настроена) ----------

type fileStore struct{ path string }

type fileDB struct {
	Students []Student
	Rows     []Row
}

func (f *fileStore) Name() string { return "локальный файл " + f.path }

// Ignored для локального режима: ~/.cu-board/ignore.txt, по курсу в строке.
func (f *fileStore) Ignored() ([]string, error) {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(f.path), "ignore.txt"))
	if err != nil {
		return defaultIgnored, nil
	}
	return strings.Split(string(b), "\n"), nil
}

func (f *fileStore) read() fileDB {
	var db fileDB
	if b, err := os.ReadFile(f.path); err == nil {
		_ = json.Unmarshal(b, &db) // файл старого формата просто начнётся заново
	}
	return db
}

func (f *fileStore) Load() ([]Student, []Row, error) {
	db := f.read()
	return db.Students, db.Rows, nil
}

func (f *fileStore) Delete(studentID string) error {
	db := f.read()
	var students []Student
	for _, s := range db.Students {
		if s.ID != studentID {
			students = append(students, s)
		}
	}
	var rows []Row
	for _, r := range db.Rows {
		if r.StudentID != studentID {
			rows = append(rows, r)
		}
	}
	b, _ := json.MarshalIndent(fileDB{students, rows}, "", "  ")
	return os.WriteFile(f.path, b, 0o600)
}

func (f *fileStore) Save(st Student, rows []Row) error {
	db := f.read()
	students := []Student{st}
	for _, s := range db.Students {
		if s.ID != st.ID {
			students = append(students, s)
		}
	}
	for _, r := range db.Rows {
		if r.StudentID != st.ID { // свои строки целиком заменяем свежими
			rows = append(rows, r)
		}
	}
	b, _ := json.MarshalIndent(fileDB{students, rows}, "", "  ")
	return os.WriteFile(f.path, b, 0o600)
}
