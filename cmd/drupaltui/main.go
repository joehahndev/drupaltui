// drupaltui is a terminal UI for writing content on a Drupal site over
// JSON:API with Basic Auth. It supports browsing, creating and editing
// markdown content, and publishing/unpublishing — and nothing destructive.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/joehahndev/drupaltui/internal/config"
	"github.com/joehahndev/drupaltui/internal/ui"
)

var version = "0.1.0"

func main() {
	var (
		cfgPath     = flag.String("config", "", "path to config file (default: platform config dir)")
		urlOverride = flag.String("url", "", "site base URL (overrides config)")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("drupaltui " + version)
		return
	}

	path := *cfgPath
	if path == "" {
		var err error
		path, err = config.DefaultPath()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}

	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if *urlOverride != "" {
		cfg.BaseURL = *urlOverride
	}

	app := ui.NewApp(cfg, path, os.Getenv("DRUPALTUI_CLIENT_SECRET"))
	if _, err := tea.NewProgram(app, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
