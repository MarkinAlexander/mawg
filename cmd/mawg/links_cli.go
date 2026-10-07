package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mawg/internal/links"
)

const linksUsage = "использование: mawg links parse <url|file|-> | mawg links fetch <url>"

func cmdLinks(args []string) int {
	if len(args) == 0 {
		fmt.Println(linksUsage)
		return 2
	}
	switch args[0] {
	case "parse":
		return cmdLinksParse(args[1:])
	case "fetch":
		return cmdLinksFetch(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "неизвестная подкоманда %q\n%s\n", args[0], linksUsage)
		return 2
	}
}

func cmdLinksParse(args []string) int {
	if len(args) != 1 {
		fmt.Println(linksUsage)
		return 2
	}
	arg := args[0]
	switch {
	case arg == "-":
		body, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Println("stdin:", err)
			return 1
		}
		return printLinks(links.ParseSubscription("stdin", string(body)))
	case isLinkScheme(arg):
		n, err := links.ParseLink("manual", arg)
		if err != nil {
			fmt.Println("ошибка:", err)
			return 1
		}
		return printLinks(links.Result{Source: "manual", Nodes: []links.Node{n}})
	case strings.HasPrefix(arg, "http://"), strings.HasPrefix(arg, "https://"):
		return fetchSubscription(arg, false)
	default:
		return parseFile(arg)
	}
}

func cmdLinksFetch(args []string) int {
	if len(args) != 1 {
		fmt.Println(linksUsage)
		return 2
	}
	if !strings.HasPrefix(args[0], "http://") && !strings.HasPrefix(args[0], "https://") {
		fmt.Println("fetch ждёт http/https URL подписки")
		return 2
	}
	return fetchSubscription(args[0], true)
}

func isLinkScheme(s string) bool {
	i := strings.Index(s, "://")
	if i <= 0 {
		return false
	}
	switch strings.ToLower(s[:i]) {
	case "vless", "trojan", "wireguard", "amneziawg", "vpn":
		return true
	}
	return false
}

func parseFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Println("ошибка:", err)
		return 1
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.EqualFold(filepath.Ext(path), ".conf") {
		n, err := links.NodeFromConf(name, "", data)
		if err != nil {
			fmt.Println("ошибка:", err)
			return 1
		}
		return printLinks(links.Result{Source: name, Nodes: []links.Node{n}})
	}
	return printLinks(links.ParseSubscription(name, string(data)))
}

func fetchSubscription(rawURL string, withHeaders bool) int {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	f, err := links.Fetch(ctx, rawURL)
	if err != nil {
		fmt.Println("ошибка:", err)
		return 1
	}
	if withHeaders {
		fmt.Printf("Content-Type: %s\n", f.ContentType)
		if f.Sub.Userinfo != "" {
			fmt.Println("Subscription-Userinfo:", f.Sub.Userinfo)
		}
		if f.Sub.UpdateIntervalHours > 0 {
			fmt.Printf("Profile-Update-Interval: %g ч\n", f.Sub.UpdateIntervalHours)
		}
	}
	return printLinks(f.Result)
}

func printLinks(res links.Result) int {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		fmt.Println("ошибка:", err)
		return 1
	}
	os.Stdout.Write(buf.Bytes())
	return 0
}
