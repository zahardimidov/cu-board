package main

// version и downloadURL подставляет build.sh:
//
//	go build -ldflags "-X main.version=v1.0.0 -X main.downloadURL=https://disk.yandex.ru/d/..."
var (
	version     = "dev"
	downloadURL = ""
)

// dataVersion — версия формата данных в таблице И формулы расчёта.
// Увеличивай при любом изменении колонок или того, как считаются проценты:
// старые бинарники увидят номер больше своего и попросят обновиться,
// а не будут писать несовместимые данные.
const dataVersion = 4

func updateHint() string {
	if downloadURL == "" {
		return "Скачай новую версию у того, кто дал тебе cu-board."
	}
	return "Скачай новую: " + downloadURL
}
