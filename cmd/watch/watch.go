package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

func main() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	done := make(chan bool)
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				log.Println("event:", event)
				st, err := os.Stat(event.Name)
				if err != nil {
					log.Printf("stat: %v", err)
				} else {
					log.Printf("file: %s, size: %d", st.Name(), st.Size())
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					log.Println("modified file:", event.Name)
				}
				if event.Op&fsnotify.Create == fsnotify.Create {
					log.Printf("ent: %s", event.Name)
					watchRecursive(watcher, event.Name)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
			}
		}
	}()

	// err = watcher.Add("/tmp/foo")
	dir := "/tmp/foo"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	err = watchRecursive(watcher, dir)
	if err != nil {
		log.Fatal(err)
	}
	<-done
}

func watchRecursive(watcher *fsnotify.Watcher, root string) error {
	err := filepath.Walk(root, func(walkPath string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// log.Printf("walk: %s, isdir: %v", walkPath, fi.IsDir())
		if fi.IsDir() {
			// log.Printf("rec: %s, dir: %v", path, fi.IsDir())
			if err = watcher.Add(walkPath); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}
