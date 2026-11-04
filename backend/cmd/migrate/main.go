package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"

	"github.com/hosama-adem/twofold/backend/internal/platform/migrate"
)

func main() {
	_ = godotenv.Load()
	_ = godotenv.Load("../.env")

	command := flag.String("command", "up", "command: up | down | new")
	dir := flag.String("dir", "migrations", "migrations directory")
	name := flag.String("name", "", "name for migrate-new")
	flag.Parse()

	absDir, err := filepath.Abs(*dir)
	if err != nil {
		fail(err.Error())
	}

	switch *command {
	case "new":
		if *name == "" {
			fail("usage: go run ./cmd/migrate -command=new -name=add_foo")
		}
		ts := time.Now().UTC().Format("20060102150405")
		file := filepath.Join(absDir, fmt.Sprintf("%s_%s.sql", ts, *name))
		body := "-- +goose Up\n\n-- +goose Down\n"
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			fail(err.Error())
		}
		fmt.Println("created", file)
		return
	case "up", "down":
		databaseURL := os.Getenv("DATABASE_URL")
		if databaseURL == "" {
			fail("DATABASE_URL is required")
		}
		if *command == "up" {
			err = migrate.Up(databaseURL, absDir)
		} else {
			err = migrate.Down(databaseURL, absDir)
		}
	default:
		fail(fmt.Sprintf("unknown command %q", *command))
	}
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
