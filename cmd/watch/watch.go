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
				if event.Op&fsnotify.Write == fsnotify.Write {
					log.Println("modified file:", event.Name)
				}
				if event.Op&fsnotify.Create == fsnotify.Create {
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
	err = watchRecursive(watcher, "/tmp/foo")
	if err != nil {
		log.Fatal(err)
	}
	<-done
}

func watchRecursive(watcher *fsnotify.Watcher, path string) error {
	err := filepath.Walk(path, func(walkPath string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		log.Printf("rec: %s, dir: %v", path, fi.IsDir())
		if fi.IsDir() {
			if err = watcher.Add(walkPath); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}
