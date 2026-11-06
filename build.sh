#!/bin/sh
# Собирает бинарники для друзей в dist/ с вшитым ключом таблицы.
#
#   ./build.sh            версия = git-тег (или хеш коммита)
#   VERSION=v1.2.0 ./build.sh
#
# Нужны secrets/credentials.json и secrets/sheet_id.
# Ссылку на папку Яндекс Диска положи в secrets/download_url —
# её покажет старая версия, когда попросит обновиться.
#
# Полученные бинарники содержат ключ: выкладывай их только по ссылке для своих,
# не в публичный репозиторий и не в GitHub Releases.
set -eu
cd "$(dirname "$0")"

for f in secrets/credentials.json secrets/sheet_id; do
	[ -s "$f" ] || { echo "✗ нет $f — см. README, раздел «Для разработчика»"; exit 1; }
done

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
URL="$(cat secrets/download_url 2>/dev/null || true)"
LDFLAGS="-s -w -X main.version=$VERSION -X main.downloadURL=$URL"

rm -rf dist && mkdir -p dist
build() {
	echo "→ $1/$2 → dist/$3"
	CGO_ENABLED=0 GOOS="$1" GOARCH="$2" go build -ldflags "$LDFLAGS" -o "dist/$3" .
}
build darwin  arm64 cu-board-mac-arm
build darwin  amd64 cu-board-mac-intel
build windows amd64 cu-board.exe
build linux   amd64 cu-board-linux

echo "✓ $VERSION собрана в dist/ — залей файлы на Яндекс Диск."
[ -n "$URL" ] || echo "  (подсказка: положи ссылку на папку в secrets/download_url)"
