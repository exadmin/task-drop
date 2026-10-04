package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	configPath := flag.String("config", filepath.Join(filepath.Dir(exe), "properties.json"), "Path to properties.json")
	flag.Parse()
	c, err := loadConfig(*configPath)
	if err == nil {
		err = execute(c)
	}
	if err != nil {
		reportFatal(err)
		os.Exit(1)
	}
}

func execute(c Config) error {
	if err := os.MkdirAll(filepath.Dir(c.LogFile), 0755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	f, err := os.OpenFile(c.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()
	log.SetOutput(f)
	defer log.SetOutput(os.Stderr)
	preparePlatform(c)
	err = run(c)
	if err != nil {
		log.Printf("Application failed: %v", err)
	} else {
		log.Print("Application stopped")
	}
	return err
}
