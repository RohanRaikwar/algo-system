// Command megacheck logs in to MEGA with MEGA_EMAIL / MEGA_PASSWORD and
// prints the whole folder tree, to verify archiver credentials.
package main

import (
	"fmt"
	"log"
	"os"

	mega "github.com/t3rm1n4l/go-mega"
)

func main() {
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
