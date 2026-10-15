// Command megacheck logs in to MEGA with MEGA_EMAIL / MEGA_PASSWORD and
// prints the whole folder tree, to verify archiver credentials.
//
// With -day it instead downloads one archived day folder and unpacks each
// file to plain SQLite:
//
//	go run ./cmd/megacheck -day 2026-10-23 -out data/extract
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"trading-systemv1/internal/archiver"
	"trading-systemv1/internal/markethours"

	mega "github.com/t3rm1n4l/go-mega"
)

func main() {
	day := flag.String("day", "", "download one archived IST day (YYYY-MM-DD)")
	out := flag.String("out", "data/extract", "with -day: output folder (a YYYY-MM-DD subfolder is made)")
	flag.Parse()
	if *day != "" {
		d, err := time.ParseInLocation("2006-01-02", *day, markethours.IST)
		if err != nil {
			log.Fatalf("-day %q: want YYYY-MM-DD", *day)
		}
		paths, err := archiver.FetchDayFolder(context.Background(), archiver.LoadConfig(), d, filepath.Join(*out, *day))
		for _, p := range paths {
			fmt.Println(p)
		}
		if err != nil {
			log.Fatalf("fetch %s: %v", *day, err)
		}
		return
	}

	m := mega.New()
	if err := m.Login(os.Getenv("MEGA_EMAIL"), os.Getenv("MEGA_PASSWORD")); err != nil {
		log.Fatalf("login failed: %v", err)
	}
	fmt.Println("login ok")
	if q, err := m.GetQuota(); err == nil {
		fmt.Printf("storage used %.1f MB of %.1f GB\n", float64(q.Cstrg)/(1<<20), float64(q.Mstrg)/(1<<30))
	}
	walk(m, m.FS.GetRoot(), "")
}

// walk prints the folder tree under n.
func walk(m *mega.Mega, n *mega.Node, prefix string) {
	children, err := m.FS.GetChildren(n)
	if err != nil {
		log.Fatalf("list %s: %v", n.GetName(), err)
	}
	for _, c := range children {
		if c.GetType() == mega.FOLDER {
			fmt.Printf("%s%s/\n", prefix, c.GetName())
			walk(m, c, prefix+"  ")
		} else {
			fmt.Printf("%s%s (%d bytes)\n", prefix, c.GetName(), c.GetSize())
		}
	}
}
