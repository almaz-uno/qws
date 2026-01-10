# QWS — Quick Window Switcher

A carousel-style window switcher for X11.

Inspired by [alttab](https://github.com/sagb/alttab) — the X11 window switcher designed for minimalistic window managers.

## Status: Phase 1 (MVP) ✅

Basic functionality implemented:
- ✅ X11 connection
- ✅ Fetching window list via EWMH
- ✅ Alt+Tab keygrab
- ✅ Text-based window selection interface
- ✅ Activating selected window

## Build

```bash
go build -o qws ./cmd/qws
```

## Usage

```bash
./qws
```

After launch:
1. Press `Alt+Tab` to invoke the switcher
2. Use `↑↓` or `j/k` to navigate through the window list
3. Press `Enter` to activate the selected window
4. Press `ESC` or `q` to cancel

## Requirements

- X11 server
- Window manager with EWMH support (i3, bspwm, openbox, xfce, etc.)

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
│   └── ui/         # User interface
│       └── selector.go # Text-based selector
└── doc/
    └── windows-switcher.asciidoc  # Documentation
```

## Next Phases

- **Phase 2**: MRU list, window icons, thumbnails via XComposite
- **Phase 3**: Carousel UI with 2.5D effect
- **Phase 4**: Configuration, fallback for systems without compositor

## Dependencies

- `github.com/jezek/xgb` — X11 protocol handling
- `golang.org/x/term` — terminal operations

## License

MIT
