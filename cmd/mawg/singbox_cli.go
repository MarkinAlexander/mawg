package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mawg/internal/singbox"
)

const singboxUsage = "использование: mawg singbox discover"

func cmdSingbox(args []string) int {
	if len(args) == 0 || args[0] != "discover" {
		fmt.Println(singboxUsage)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	reports := singbox.Discovery(ctx)
	exitCode := 0
	for _, rep := range reports {
		if rep.Error != "" {
			exitCode = 1
			fmt.Printf("%s: ошибка: %s\n", rep.Source, rep.Error)
			continue
		}
		latest := rep.Latest()
		if latest == nil {
			fmt.Printf("%s: релизов нет\n", rep.Source)
			continue
		}
		sha := "нет"
		if latest.SHA256SUM {
			sha = "есть"
		}
		fmt.Printf("%s: релизов %d, последний %s (%s), SHA256SUMS: %s\n",
			rep.Source, len(rep.Releases), latest.Tag,
			latest.Published.Format("2006-01-02"), sha)
		for _, arch := range singbox.SortedArches(latest.Matrix) {
			f := latest.Matrix[arch]
			flavor := ""
			if f.Plain {
				flavor = "plain"
			}
			if f.UPX {
				if flavor != "" {
					flavor += "+"
				}
				flavor += "upx"
			}
			fmt.Printf("  %-20s %s\n", arch, flavor)
		}
		if len(rep.Releases) > 1 {
			tags := make([]string, 0, len(rep.Releases)-1)
			for _, r := range rep.Releases[1:] {
				tags = append(tags, r.Tag)
			}
			fmt.Println("  предыдущие:", strings.Join(tags, ", "))
		}
	}
	return exitCode
}
