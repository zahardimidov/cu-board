package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

func appDir() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".cu-board")
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

type session struct {
	Cookie string `json:"cookie"`
}

func sessionPath() string { return filepath.Join(appDir(), "session.json") }

func loadSession() string {
	b, err := os.ReadFile(sessionPath())
	if err != nil {
		return ""
	}
	var s session
	_ = json.Unmarshal(b, &s)
	return s.Cookie
}

func saveSession(cookie string) error {
	b, _ := json.Marshal(session{Cookie: cookie})
	return os.WriteFile(sessionPath(), b, 0o600)
}

// ensureSession возвращает рабочий клиент LMS: сохранённая сессия,
// а если она протухла — вход через окно Chrome.
func ensureSession(force bool) (*LMS, Me, error) {
	if !force {
		if c := loadSession(); c != "" {
			l := NewLMS(c)
			if me, err := l.Me(); err == nil {
				return l, me, nil
			}
		}
	}
	fmt.Println("→ Открываю браузер — войди в аккаунт ЦУ, окно закроется само.")
	cookie, err := browserLogin()
	if err != nil {
		return nil, Me{}, err
	}
	l := NewLMS(cookie)
	me, err := l.Me()
	if err != nil {
		return nil, Me{}, err
	}
	if err := saveSession(cookie); err != nil {
		return nil, Me{}, err
	}
	return l, me, nil
}

// macBrowsers — Chromium-браузеры на macOS, которые умеют DevTools-протокол.
// Safari и Firefox его не поддерживают. На Windows chromedp сам находит Chrome, а без него — Edge.
var macBrowsers = []string{
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
	"/Applications/Yandex.app/Contents/MacOS/Yandex",
	"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
	"/Applications/Arc.app/Contents/MacOS/Arc",
	"/Applications/Opera.app/Contents/MacOS/Opera",
	"/Applications/Vivaldi.app/Contents/MacOS/Vivaldi",
}

func browserLogin() (string, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.UserDataDir(filepath.Join(appDir(), "chrome")),
		chromedp.WindowSize(520, 800),
	)
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		found := false
		for _, p := range macBrowsers {
			for _, cand := range []string{p, filepath.Join(home, p)} { // и ~/Applications
				if _, err := os.Stat(cand); err == nil && !found {
					opts, found = append(opts, chromedp.ExecPath(cand)), true
				}
			}
		}
		if !found {
			return "", errors.New("не нашёл Chromium-браузер: поставь Google Chrome (Safari и Firefox не подходят)")
		}
	}
	actx, cancelA := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelA()
	bctx, cancelB := chromedp.NewContext(actx)
	defer cancelB()

	// Navigate ждёт загрузки страницы; SSO-редиректы на id.centraluniversity.ru проходят внутри.
	if err := chromedp.Do(bctx, chromedp.Navigate(baseURL+"/learn/")); err != nil {
		return "", fmt.Errorf("не удалось запустить браузер (нужен Chrome, Edge или другой Chromium-браузер): %w", err)
	}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	deadline := time.After(5 * time.Minute)
	for {
		select {
		case <-deadline:
			return "", errors.New("вход не выполнен: вышло время (5 минут)")
		case <-tick.C:
		}
		res, err := chromedp.Call(bctx, network.GetCookies, network.GetCookiesParams{URLs: []string{baseURL}})
		if err != nil {
			return "", errors.New("вход не выполнен: окно браузера закрыто")
		}
		for _, c := range res.Cookies {
			if c.Name == "bff.cookie" {
				if _, err := NewLMS(c.Value).Me(); err == nil {
					return c.Value, nil
				}
			}
		}
	}
}
