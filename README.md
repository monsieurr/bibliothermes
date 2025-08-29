# Bibliothermes
Allow the user to retireve bookmarks from different browsers (Firefox, Chrome, ...).
The bookmarks can then be viewed directly from the terminal.
The goal is to provide a lightweight program running in the terminal to quickly access the user's bookmarks from all browsers
without opening a specific browser

```
> help

--- Bookmark Manager Help ---
  list              - Show bookmarks as clickable hyperlinks
  list fav          - Show only favorite bookmarks as hyperlinks
  list links        - Show bookmarks with visible URLs (for basic terminals)
  open <id>         - Open the bookmark with the given ID
  fav <id>          - Toggle favorite status for a bookmark
  import            - Scan for new bookmarks from installed browsers
  set-browser <cmd> - Set the command to open links (e.g., 'firefox')
  save              - Save all changes to bookmarks.json
  help              - Show this help message
  exit              - Quit the program
```
