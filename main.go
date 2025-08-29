// main.go
package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

const (
	bookmarksFile = "bookmarks.json"

	// ANSI escape codes for styling
	Reset  = "\x1b[0m"
	Bold   = "\x1b[1m"
	Yellow = "\x1b[33m"
	Cyan   = "\x1b[36m"
	Blue   = "\x1b[34m"
	Gray   = "\x1b[90m" // ADDED: Color for the raw URL text
)

// =============================================================================
// == 📂 DATA STRUCTURES
// =============================================================================
type Bookmark struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Favorite bool   `json:"favorite"`
}
type Config struct {
	DefaultBrowserCmd string `json:"default_browser_cmd"`
}
type AppState struct {
	Bookmarks []Bookmark `json:"bookmarks"`
	Config    Config     `json:"config"`
	nextID    int
}

// =============================================================================
// == 💾 STORAGE (JSON)
// =============================================================================
func (s *AppState) saveState() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("could not marshal state: %w", err)
	}
	return os.WriteFile(bookmarksFile, data, 0644)
}
func loadState() (*AppState, error) {
	state := &AppState{nextID: 1}
	data, err := os.ReadFile(bookmarksFile)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No 'bookmarks.json' found. Creating a new one.")
			switch runtime.GOOS {
			case "darwin":
				state.Config.DefaultBrowserCmd = "open"
			case "linux":
				state.Config.DefaultBrowserCmd = "xdg-open"
			case "windows":
				state.Config.DefaultBrowserCmd = "cmd /c start"
			}
			return state, state.saveState()
		}
		return nil, fmt.Errorf("could not read %s: %w", bookmarksFile, err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("could not unmarshal JSON: %w", err)
	}
	if len(state.Bookmarks) > 0 {
		maxID := 0
		for _, b := range state.Bookmarks {
			if b.ID > maxID {
				maxID = b.ID
			}
		}
		state.nextID = maxID + 1
	}
	return state, nil
}
func (s *AppState) addBookmark(name, url string) {
	for _, b := range s.Bookmarks {
		if b.URL == url {
			return
		}
	}
	s.Bookmarks = append(s.Bookmarks, Bookmark{ID: s.nextID, Name: name, URL: url})
	s.nextID++
}

// =============================================================================
// == 🌐 BROWSER BOOKMARK IMPORTER
// =============================================================================
type chromeBookmarkNode struct {
	Type     string               `json:"type"`
	Name     string               `json:"name"`
	URL      string               `json:"url"`
	Children []chromeBookmarkNode `json:"children"`
}

func parseChromeBookmarks(node chromeBookmarkNode, state *AppState) {
	if node.Type == "url" && node.URL != "" {
		state.addBookmark(node.Name, node.URL)
	}
	for _, child := range node.Children {
		parseChromeBookmarks(child, state)
	}
}
func importFromChrome(path string, state *AppState) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("could not read file: %w", err)
	}
	var root struct {
		Roots map[string]chromeBookmarkNode `json:"roots"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("could not parse JSON: %w", err)
	}
	for _, node := range root.Roots {
		parseChromeBookmarks(node, state)
	}
	return nil
}
func importFromFirefox(path string, state *AppState) error {
	immutableURI := fmt.Sprintf("file:%s?_immutable=1", path)
	db, err := sql.Open("sqlite3", immutableURI)
	if err != nil {
		return fmt.Errorf("could not open firefox sqlite db: %w", err)
	}
	defer db.Close()
	query := `SELECT b.title, p.url FROM moz_bookmarks AS b JOIN moz_places AS p ON b.fk = p.id WHERE b.type = 1 AND b.title IS NOT NULL;`
	rows, err := db.Query(query)
	if err != nil {
		return fmt.Errorf("could not query firefox bookmarks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var title, url string
		if err := rows.Scan(&title, &url); err == nil {
			state.addBookmark(title, url)
		}
	}
	return nil
}
func getBrowserPaths() (map[string][]string, map[string]string) {
	usr, _ := user.Current()
	homeDir := usr.HomeDir
	chromeLikePaths := make(map[string][]string)
	firefoxPaths := make(map[string]string)
	switch runtime.GOOS {
	case "darwin":
		appSupport := filepath.Join(homeDir, "Library/Application Support")
		chromeLikePaths["Chrome"] = []string{filepath.Join(appSupport, "Google/Chrome/Default/Bookmarks")}
		chromeLikePaths["Brave"] = []string{filepath.Join(appSupport, "BraveSoftware/Brave-Browser/Default/Bookmarks")}
		chromeLikePaths["Edge"] = []string{filepath.Join(appSupport, "Microsoft Edge/Default/Bookmarks")}
		firefoxPaths["firefox_dir"] = filepath.Join(appSupport, "Firefox/Profiles")
	case "linux":
		configDir := filepath.Join(homeDir, ".config")
		chromeLikePaths["Chrome"] = []string{filepath.Join(configDir, "google-chrome/Default/Bookmarks")}
		chromeLikePaths["Brave"] = []string{filepath.Join(configDir, "BraveSoftware/Brave-Browser/Default/Bookmarks")}
		firefoxPaths["firefox_dir"] = filepath.Join(homeDir, ".mozilla/firefox")
	case "windows":
		appData := filepath.Join(homeDir, "AppData/Local")
		chromeLikePaths["Chrome"] = []string{filepath.Join(appData, "Google/Chrome/User Data/Default/Bookmarks")}
		chromeLikePaths["Brave"] = []string{filepath.Join(appData, "BraveSoftware/Brave-Browser/User Data/Default/Bookmarks")}
		chromeLikePaths["Edge"] = []string{filepath.Join(appData, "Microsoft/Edge/User Data/Default/Bookmarks")}
		firefoxPaths["firefox_dir"] = filepath.Join(homeDir, "AppData/Roaming/Mozilla/Firefox/Profiles")
	}
	return chromeLikePaths, firefoxPaths
}
func (s *AppState) importBookmarks() {
	chromeLikePaths, firefoxDirs := getBrowserPaths()
	initialCount := len(s.Bookmarks)
	foundAnyBrowser := false
	for browser, paths := range chromeLikePaths {
		for _, path := range paths {
			if _, err := os.Stat(path); err == nil {
				if importErr := importFromChrome(path, s); importErr == nil {
					fmt.Printf("Successfully checked for %s bookmarks.\n", browser)
					foundAnyBrowser = true
				}
			}
		}
	}
	if firefoxDir, ok := firefoxDirs["firefox_dir"]; ok {
		foundFirefoxDB := false
		filepath.WalkDir(firefoxDir, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Name() == "places.sqlite" {
				foundFirefoxDB = true
				if importErr := importFromFirefox(path, s); importErr != nil {
					fmt.Printf("Notice: Failed to import from Firefox at %s: %v\n", path, importErr)
				} else {
					fmt.Println("Successfully checked for Firefox bookmarks.")
					foundAnyBrowser = true
				}
				return filepath.SkipDir
			}
			return nil
		})
		if !foundFirefoxDB {
			fmt.Println("Notice: Could not find a Firefox 'places.sqlite' file.")
		}
	}
	newCount := len(s.Bookmarks) - initialCount
	if newCount > 0 {
		fmt.Printf("✅ Imported %d new bookmarks. Run 'save' to persist them.\n", newCount)
	} else if foundAnyBrowser {
		fmt.Println("No new bookmarks found.")
	} else {
		fmt.Println("Could not find any supported browser bookmarks on default paths.")
	}
}

// =============================================================================
// == ⚙️ REPL COMMANDS & LOGIC
// =============================================================================
func printHelp() {
	// UPDATED: Added the new 'list links' command to the help text
	fmt.Println("\n--- Bookmark Manager Help ---")
	fmt.Println("  list              - Show bookmarks as clickable hyperlinks")
	fmt.Println("  list fav          - Show only favorite bookmarks as hyperlinks")
	fmt.Println("  list links        - Show bookmarks with visible URLs (for basic terminals)")
	fmt.Println("  open <id>         - Open the bookmark with the given ID")
	fmt.Println("  fav <id>          - Toggle favorite status for a bookmark")
	fmt.Println("  import            - Scan for new bookmarks from installed browsers")
	fmt.Println("  set-browser <cmd> - Set the command to open links (e.g., 'firefox')")
	fmt.Println("  save              - Save all changes to bookmarks.json")
	fmt.Println("  help              - Show this help message")
	fmt.Println("  exit              - Quit the program")
	fmt.Println("---------------------------")
}

func (s *AppState) handleCommand(input string) (shouldExit bool) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return false
	}
	command, args := parts[0], parts[1:]
	switch command {
	case "list", "ls":
		// CHANGED: Check for command variations like 'list fav' or 'list links'
		showFavsOnly := false
		showLinksFormat := false
		if len(args) > 0 {
			if args[0] == "fav" {
				showFavsOnly = true
			} else if args[0] == "links" {
				showLinksFormat = true
			}
		}

		sort.Slice(s.Bookmarks, func(i, j int) bool {
			return strings.ToLower(s.Bookmarks[i].Name) < strings.ToLower(s.Bookmarks[j].Name)
		})
		count := 0
		for _, b := range s.Bookmarks {
			if showFavsOnly && !b.Favorite {
				continue
			}
			favMarker := ""
			if b.Favorite {
				favMarker = Yellow + "★ " + Reset
			}

			if showLinksFormat {
				// ADDED: Logic for the new, simple text format
				fmt.Printf("%s[%d]%s %s%s - %s%s%s\n", Bold+Cyan, b.ID, Reset, favMarker, b.Name, Gray, b.URL, Reset)
			} else {
				// Original hyperlink format for modern terminals
				linkText := fmt.Sprintf("\x1b]8;;%s\x07%s%s%s\x1b]8;;\x07", b.URL, Blue, b.Name, Reset)
				fmt.Printf("%s[%d]%s %s%s\n", Bold+Cyan, b.ID, Reset, favMarker, linkText)
			}
			count++
		}
		if count == 0 {
			if showFavsOnly {
				fmt.Println("No favorites found.")
			} else {
				fmt.Println("No bookmarks found.")
			}
		}
	case "open":
		if len(args) < 1 {
			fmt.Println("Usage: open <id>")
			return false
		}
		id, err := strconv.Atoi(args[0])
		if err != nil {
			fmt.Println("Invalid ID.")
			return false
		}
		for _, b := range s.Bookmarks {
			if b.ID == id {
				fmt.Printf("Opening '%s'...\n", b.Name)
				cmdParts := strings.Fields(s.Config.DefaultBrowserCmd)
				cmd := exec.Command(cmdParts[0], append(cmdParts[1:], b.URL)...)
				if err := cmd.Start(); err != nil {
					fmt.Printf("Error: %v\n", err)
				}
				return false
			}
		}
		fmt.Println("ID not found.")
	case "fav":
		if len(args) < 1 {
			fmt.Println("Usage: fav <id>")
			return false
		}
		id, err := strconv.Atoi(args[0])
		if err != nil {
			fmt.Println("Invalid ID.")
			return false
		}
		found := false
		for i, b := range s.Bookmarks {
			if b.ID == id {
				s.Bookmarks[i].Favorite = !s.Bookmarks[i].Favorite
				status := "added to"
				if !s.Bookmarks[i].Favorite {
					status = "removed from"
				}
				fmt.Printf("Bookmark '%s' %s favorites.\n", b.Name, status)
				found = true
				break
			}
		}
		if !found {
			fmt.Println("ID not found.")
		}
	case "import":
		s.importBookmarks()
	case "set-browser":
		if len(args) < 1 {
			fmt.Printf("Usage: set-browser <cmd>\nCurrent: '%s'\n", s.Config.DefaultBrowserCmd)
			return false
		}
		s.Config.DefaultBrowserCmd = strings.Join(args, " ")
		fmt.Printf("Browser command set to: '%s'\n", s.Config.DefaultBrowserCmd)
	case "save":
		if err := s.saveState(); err != nil {
			fmt.Printf("Error: %v\n", err)
		} else {
			fmt.Println("✅ State saved to", bookmarksFile)
		}
	case "help":
		printHelp()
	case "exit", "quit":
		return true
	default:
		fmt.Printf("Unknown command: '%s'.\n", command)
	}
	return false
}

// =============================================================================
// == 🚀 MAIN FUNCTION
// =============================================================================
func main() {
	state, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Welcome to the Go Bookmark Manager! Type 'help' for commands.")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		if state.handleCommand(scanner.Text()) {
			break
		}
	}
	if err := state.saveState(); err != nil {
		fmt.Fprintf(os.Stderr, "Could not save on exit: %v\n", err)
	} else {
		fmt.Println("\nChanges saved. Goodbye! 👋")
	}
}
