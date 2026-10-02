#!/bin/sh
# Обновляет запечённые списки пресетов из репозитория itdoginfo/allow-domains.
# Скачивает выбранные файлы в internal/magitrickle/data/allow-domains и
# перезаписывает MANIFEST.json (источник, коммит, дата).
# Запуск: sh deploy/update-presets.sh
set -e

OWNER_REPO=itdoginfo/allow-domains
RAW="https://raw.githubusercontent.com/$OWNER_REPO/main"
DEST="$(dirname "$0")/../internal/magitrickle/data/allow-domains"

# формат: локальный путь (относительно DEST) = путь в репозитории-источнике
FILES="
domains/telegram.lst=Services/telegram.lst
domains/meta.lst=Services/meta.lst
domains/discord.lst=Services/discord.lst
domains/twitter.lst=Services/twitter.lst
domains/tiktok.lst=Services/tiktok.lst
domains/roblox.lst=Services/roblox.lst
domains/google_ai.lst=Services/google_ai.lst
domains/google_meet.lst=Services/google_meet.lst
domains/google_play.lst=Services/google_play.lst
categories/porn.lst=Categories/porn.lst
categories/anime.lst=Categories/anime.lst
subnets4/telegram.lst=Subnets/IPv4/telegram.lst
subnets4/meta.lst=Subnets/IPv4/meta.lst
subnets4/discord.lst=Subnets/IPv4/discord.lst
subnets4/twitter.lst=Subnets/IPv4/twitter.lst
subnets4/roblox.lst=Subnets/IPv4/roblox.lst
subnets4/google_meet.lst=Subnets/IPv4/google_meet.lst
subnets6/telegram.lst=Subnets/IPv6/telegram.lst
subnets6/meta.lst=Subnets/IPv6/meta.lst
subnets6/discord.lst=Subnets/IPv6/discord.lst
subnets6/twitter.lst=Subnets/IPv6/twitter.lst
"

for pair in $FILES; do
	dst=${pair%%=*}
	src=${pair#*=}
	mkdir -p "$DEST/$(dirname "$dst")"
	echo "$src -> $dst"
	curl -fsS --retry 3 -o "$DEST/$dst" "$RAW/$src"
done

COMMIT=$(curl -fsS "https://api.github.com/repos/$OWNER_REPO/commits/main" |
	tr ',' '\n' | grep '"sha"' | head -1 | grep -o '[0-9a-f]\{40\}')
UPDATED=$(date +%F)

cat > "$DEST/MANIFEST.json" <<EOF
{
 "source": "https://github.com/$OWNER_REPO",
 "commit": "$COMMIT",
 "updated": "$UPDATED"
}
EOF
echo "MANIFEST: $OWNER_REPO@$COMMIT ($UPDATED)"
