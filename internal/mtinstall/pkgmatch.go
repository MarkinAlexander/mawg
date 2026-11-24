package mtinstall

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

// pkgBinMatches - совпадает ли бинарь MT на диске с бинарем внутри пакета
// (member - путь бинаря внутри пакета, на роутере он же и на диске).
// Ответ opkg «up to date» сам по себе не повод переписывать пакет: живой
// случай 24.11 (OpenWrt, UBIFS) - принудительная переустановка уже
// актуального rev23 переписала ~7МБ, жёсткая перезагрузка следом оставила
// файлы MT и базу opkg нулевой длины. Подмена бинаря вручную без
// переключения базы (кейс 0.42) по-прежнему ловится - бинарь отличается.
// Ошибка = пакет не разобран (ar-формат, data.tar.xz): переустанавливаем.
func pkgBinMatches(pkgPath, member, disk string) (bool, error) {
	f, err := os.Open(pkgPath)
	if err != nil {
		return false, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return false, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return false, errors.New("в пакете нет data.tar.gz")
		}
		if err != nil {
			return false, err
		}
		if strings.TrimPrefix(hdr.Name, "./") != "data.tar.gz" {
			continue
		}
		inner, err := gzip.NewReader(tr)
		if err != nil {
			return false, err
		}
		want, err := memberHash(tar.NewReader(inner), strings.TrimPrefix(member, "/"))
		if err != nil {
			return false, err
		}
		h := sha256.New()
		diskFile, err := os.Open(disk)
		if err != nil {
			return false, nil
		}
		_, err = io.Copy(h, diskFile)
		diskFile.Close()
		if err != nil {
			return false, err
		}
		return hex.EncodeToString(h.Sum(nil)) == want, nil
	}
}

// apkSameVersion - установлен ли уже пакет той же версии, что ассет
// (magitrickle_0.8.2-r23_openwrt_x86_64.apk): apk add на такой файл
// ничего не меняет («OK: 25.5 MiB in 219 packages»), а панель ждала смены
// версии и пугала «бинарь мог не замениться» (0.52, 25.12: повторное
// нажатие на rev23). Не разобрали - false, панель ведёт себя как раньше.
func apkSameVersion(run func(string, ...string) ([]byte, error), asset string) bool {
	ver, ok := strings.CutPrefix(asset, "magitrickle_")
	if !ok {
		return false
	}
	if ver, ok = strings.CutSuffix(ver, ".apk"); !ok {
		return false
	}
	if ver, _, ok = strings.Cut(ver, "_openwrt_"); !ok || ver == "" {
		return false
	}
	out, err := run("apk", "list", "--installed")
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(out), "\n") {
		inst, ok := strings.CutPrefix(l, "magitrickle-")
		if !ok {
			continue
		}
		fields := strings.Fields(inst)
		if len(fields) == 0 {
			return false
		}
		// prerelease-сборки именуются с _rN, apk печатает -rN
		return strings.ReplaceAll(fields[0], "_r", "-r") == strings.ReplaceAll(ver, "_r", "-r")
	}
	return false
}

// memberHash - sha256 содержимого члена архива с данным именем (без
// ведущего «./»); пусто, если члена нет.
func memberHash(tr *tar.Reader, name string) (string, error) {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", errors.New("в пакете нет " + name)
		}
		if err != nil {
			return "", err
		}
		if strings.TrimPrefix(hdr.Name, "./") != name {
			continue
		}
		h := sha256.New()
		if _, err := io.Copy(h, tr); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
}
