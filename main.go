package main

import (
	_ "embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/search"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/neovim/go-client/nvim"

	fynevim "github.com/gjkliewer/fynevim/widget"
)

//go:embed init.lua
var nvimInit string

const defaultWinWidth = 500
const defaultWinHeight = 600

var notesDir string

var fileMatches []FileMatch
var notesList *List

type FileMatch struct {
	fullName string
	start    int
	end      int
}

func (fm *FileMatch) shortName() string {
	return filepath.Base(fm.fullName)
}

func (fm *FileMatch) displayName() string {
	if filepath.Ext(fm.shortName()) == ".md" {
		return strings.TrimSuffix(fm.shortName(), filepath.Ext(fm.shortName()))
	}
	return fm.shortName()
}

type ExtendedEntry struct {
	widget.Entry

	OnTypedKeyFunc func(*fyne.KeyEvent)
}

func NewExtendedEntry() *ExtendedEntry {
	entry := &ExtendedEntry{}
	entry.ExtendBaseWidget(entry)
	return entry
}

func (ee *ExtendedEntry) SetOnTypedKey(o func(*fyne.KeyEvent)) {
	ee.OnTypedKeyFunc = o
}

func (ee *ExtendedEntry) TypedKey(ke *fyne.KeyEvent) {
	ee.OnTypedKeyFunc(ke)
	ee.Entry.TypedKey(ke)
}

type List struct {
	widget.List

	selectedItem   widget.ListItemID
	OnTypedKeyFunc func(*List, *fyne.KeyEvent)
}

func NewList(length func() int, createItem func() fyne.CanvasObject, updateItem func(widget.ListItemID, fyne.CanvasObject)) *List {
	list := &List{}
	list.Length = length
	list.CreateItem = createItem
	list.UpdateItem = updateItem
	list.ExtendBaseWidget(list)
	return list
}

func (el *List) SetOnTypedKey(o func(*List, *fyne.KeyEvent)) {
	el.OnTypedKeyFunc = o
}

func (el *List) TypedKey(ke *fyne.KeyEvent) {
	if el.OnTypedKeyFunc != nil {
		el.OnTypedKeyFunc(el, ke)
	}
}

var editor *fynevim.Editor
var richText *widget.RichText
var log *slog.Logger

func main() {
	log = initLogger()

	a := app.New()
	window := a.NewWindow("NeoNV")
	window.Resize(fyne.NewSize(defaultWinWidth, defaultWinHeight))

	homedir, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	notesDir = filepath.Join(homedir, ".NeoNV", "notes")
	os.Mkdir(notesDir, os.ModePerm|0755)

	editor = fynevim.NewEditor(
		log,
		[]nvim.ChildProcessOption{
			// TODO Did this because can't find command 'nvim' when started from macos Finder,
			//      need to make this work for more than just nvim homebrew install
			nvim.ChildProcessCommand("/opt/homebrew/bin/nvim"),
			nvim.ChildProcessArgs(
				"--embed",
				"--clean",
				"-n", // disable swap files
			),
			nvim.ChildProcessDir(notesDir),
		},
	)
	defer editor.Nvim.Close()

	// process init.lua
	var _result interface{}
	editor.Nvim.ExecLua(nvimInit, _result)

	// editor.ShowLineNumbers = true
	// editor.ShowWhitespace = true

	buildMatches("")
	notesList = NewList(
		func() int {
			return len(fileMatches)
		},
		// create item
		func() fyne.CanvasObject {
			return widget.NewLabel("PLACEHOLDER")
		},
		// update item
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			obj.(*widget.Label).SetText(fileMatches[id].displayName())
		},
	)
	notesList.SetOnTypedKey(func(l *List, fke *fyne.KeyEvent) {
		log.Debug("notes list typed key", "fke", fke)
		item := l.selectedItem
		switch fke.Name {
		case "J":
			item = item + 1
		case "K":
			item = item - 1
		}
		l.Select(item)
	})

	searchBar := NewExtendedEntry()
	searchBar.SetPlaceHolder("Search or create...")

	window.SetContent(container.NewBorder(
		searchBar, // north
		nil,       // south
		notesList, // west
		nil,       // east
		editor,    // center
	))

	notesList.OnSelected = func(id widget.ListItemID) {
		notesList.selectedItem = id
		openFileFromList(id)
	}

	notesList.OnUnselected = func(id widget.ListItemID) {
		err := editor.Nvim.Command("bdelete!")
		if err != nil {
			panic(err)
		}
	}

	// register search area shortcut
	superLShortcut := &desktop.CustomShortcut{KeyName: fyne.KeyL, Modifier: fyne.KeyModifierSuper}
	window.Canvas().AddShortcut(superLShortcut, func(_ fyne.Shortcut) {
		window.Canvas().Focus(searchBar)
	})

	searchBar.SetOnTypedKey(func(fke *fyne.KeyEvent) {
		switch fke.Name {
		case fyne.KeyEscape:
			searchBar.SetText("")
		}
	})

	searchBar.OnSubmitted = func(input string) {
		if len(fileMatches) == 0 {
			err := editor.Nvim.Command("bdelete!")
			if err != nil {
				panic(err)
			}
			err = editor.Nvim.Command(fmt.Sprintf("e %v.md", input))
			if err != nil {
				panic(err)
			}
			buildMatches(input)
			notesList.ScrollToTop() // this causes note list to refresh
			openFileFromList(0)
		}
		window.Canvas().Focus(editor)
	}

	searchBar.OnChanged = func(input string) {
		buildMatches(input)
		notesList.ScrollToTop() // this causes note list to refresh
		openFileFromList(0)
	}

	window.Canvas().SetOnTypedKey(func(fke *fyne.KeyEvent) {
		switch fke.Name {
		case fyne.KeyEscape:
			window.Canvas().Focus(editor)
		}
	})

	// start focus on the search bar
	window.Canvas().Focus(searchBar)

	log.Debug("starting window")
	window.ShowAndRun()
}

func openFileFromList(id int) {
	log.Debug("Opening list item", "id", id)
	if len(fileMatches) == 0 {
		// remove buffer
		err := editor.Nvim.Command("bdelete!")
		if err != nil {
			panic(err)
		}
		log.Debug("Unselecting list")
		notesList.UnselectAll()
		return
	}
	err := editor.Nvim.Command("bdelete!")
	if err != nil {
		panic(err)
	}
	file := fileMatches[id]
	err = editor.Nvim.Command(fmt.Sprintf("e %v", file.shortName()))
	if err != nil {
		panic(err)
	}
	log.Debug("Selecting list item", "id", id)
	notesList.Select(id)
	// open file in preview mode
	editor.PreviewMode()
}

func buildMatches(input string) {
	fileMatches = nil
	if input == "" {
		fileList, err := os.ReadDir(notesDir)
		for _, f := range fileList {
			fileMatches = append(fileMatches, FileMatch{fullName: filepath.Join(notesDir, f.Name())})
		}
		if err != nil {
			panic(err)
		}
		return
	}

	err := filepath.WalkDir(notesDir, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if de.IsDir() {
			return nil
		}

		fileContents, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		matcher := search.New(language.English, search.Loose)

		// check file name
		start, end := matcher.Index([]byte(de.Name()), []byte(input))
		if start != -1 {
			fileMatches = append(fileMatches, FileMatch{fullName: filepath.Join(notesDir, de.Name())})
			return nil
		}

		// check file contents
		start, end = matcher.Index(fileContents, []byte(input))
		if start == -1 {
			return nil
		}

		fileMatches = append(fileMatches, FileMatch{fullName: filepath.Join(notesDir, de.Name()), start: start, end: end})
		return nil
	})

	if err != nil {
		log.Error("error searching files: error from walk", "err", err)
	}

	log.Debug("Found matches:", "input", input, "matches", fileMatches)
}

func logLevel() slog.Level {
	switch level := os.Getenv("NEONV_LOG_LEVEL"); level {
	case "DEBUG":
		return slog.LevelDebug
	default:
		return slog.LevelError
	}
}

func initLogger() *slog.Logger {
	var programLevel = new(slog.LevelVar)
	programLevel.Set(logLevel())
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: programLevel})
	l := slog.New(h)
	return l
}
