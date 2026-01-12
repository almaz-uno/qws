# QWS — Quick Window Switcher

A carousel-style window switcher for X11.

Inspired by [alttab](https://github.com/sagb/alttab) — the X11 window switcher designed for minimalistic window managers.

## Build

```bash
go build -o qws ./cmd/qws
```

## Usage

```bash
./qws
```

After launch:
1. Press `Alt+Tab` to invoke the carousel switcher
2. Use `←→` (arrow keys) or `Tab` to navigate through windows
3. **Mouse support**: Hover over any window card to highlight it, click to select
4. Press `Enter` to activate the selected window
5. Press `ESC` to cancel

The carousel displays window thumbnails in a 3D perspective view.

## Features

- **2.5D Carousel UI**: Cover Flow-style window display with perspective effect
- **MRU Ordering**: Windows sorted by Most Recently Used order
- **Thumbnail Previews**: Live window thumbnails via XComposite
- **Smart Placeholders**: Fallback icons when thumbnails unavailable
- **Keyboard Navigation**: Arrow keys, Tab, Enter, and Escape support
- **Mouse Support**: Hover to highlight windows (orange glow), click to select
- **Always On Top**: Overlay window with no WM decorations

## Screenshots

The carousel shows windows in a 3D perspective:
- Center window is displayed at full size
- Side windows are scaled down and slightly rotated
- Blue glow highlights the selected window
- Orange glow highlights hovered windows
- Shadows provide depth perception

## Requirements

- X11 server
- Window manager with EWMH support (i3, bspwm, openbox, xfce, etc.)
- Compositor (for window thumbnails) - e.g., picom, compton, xcompmgr
- DejaVu Sans font (for text rendering in placeholders)

## Architecture

```
qws/
├── cmd/qws/        # Main application
│   └── main.go
├── pkg/
│   ├── x11/        # X11 operations
│   │   ├── conn.go     # X server connection
│   │   └── windows.go  # Window management
│   ├── keygrab/    # Key grabbing
│   │   └── keygrab.go
│   ├── mru/        # Most Recently Used list
│   │   └── mru.go
│   ├── focus/      # Focus tracking
│   │   └── watcher.go
│   ├── composite/  # XComposite for thumbnails
│   │   └── capture.go
│   ├── carousel/   # 2.5D carousel rendering
│   │   ├── renderer.go # Carousel graphics
│   │   └── window.go   # X11 window for display
│   └── ui/         # User interface
│       ├── selector.go      # Graphical carousel selector
│       └── text_selector.go # Legacy text selector
└── doc/
    └── windows-switcher.asciidoc  # Documentation
```

## Next Phase

- **Phase 4**: Configuration file, fallback for systems without compositor, testing on different WMs

## Dependencies

- `github.com/jezek/xgb` — X11 protocol handling
- `github.com/fogleman/gg` — 2D/2.5D graphics rendering for carousel

## License

MIT
