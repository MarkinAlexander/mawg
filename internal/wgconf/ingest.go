package wgconf

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"
)

type NamedConfig struct {
	OriginalName string
	Raw          []byte
	Config       Config
}

func IsZipName(name string) bool {
	return strings.EqualFold(path.Ext(name), ".zip")
}

func IsConfName(name string) bool {
	return strings.EqualFold(path.Ext(name), ".conf")
}

func IngestFile(filename string, data []byte) ([]NamedConfig, error) {
	if IsZipName(filename) {
		return IngestZip(data)
	}
	if !IsConfName(filename) {
		return nil, fmt.Errorf("%s: не .conf и не .zip файл", filename)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}
	return []NamedConfig{{OriginalName: path.Base(filename), Raw: data, Config: cfg}}, nil
}

func IngestZip(data []byte) ([]NamedConfig, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("zip: %w", err)
	}
	var out []NamedConfig
	var errs []string
	for _, f := range zr.File {
		name := path.Base(f.Name)
		if f.FileInfo().IsDir() || !IsConfName(name) || strings.HasPrefix(f.Name, "__MACOSX") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: open: %v", name, err))
			continue
		}
		body, err := io.ReadAll(io.LimitReader(rc, 1<<20))
		rc.Close()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: read: %v", name, err))
			continue
		}
		cfg, err := Parse(body)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		out = append(out, NamedConfig{OriginalName: name, Raw: body, Config: cfg})
	}
	if len(out) == 0 {
		if len(errs) > 0 {
			return nil, fmt.Errorf("в zip нет валидных конфигов: %s", strings.Join(errs, "; "))
		}
		return nil, fmt.Errorf("в zip нет .conf файлов")
	}
	return out, nil
}

func Dedupe(in []NamedConfig) (kept []NamedConfig, dupes []string) {
	seen := map[string]bool{}
	for _, nc := range in {
		id := nc.Config.PrivateKey + "|" + nc.Config.Peer.PublicKey + "|" + nc.Config.Endpoint()
		if seen[id] {
			dupes = append(dupes, nc.OriginalName)
			continue
		}
		seen[id] = true
		kept = append(kept, nc)
	}
	return kept, dupes
}
