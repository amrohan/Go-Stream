package server

import (
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	mediaDir   = "./videos"
	authUser   = "admin"
	authPass   = "admin"
	authCookie = "media_auth"
)

// DirEntry represents a file or directory.
type DirEntry struct {
	Name   string
	IsDir  bool
	SizeMB int64
	Path   string
}

// Breadcrumb represents a part of the navigation path.
type Breadcrumb struct {
	Name string
	Path string
}

// PageData is the data passed to the templates.
type PageData struct {
	// For Browser View
	CurrentPath string
	ParentPath  string
	Entries     []DirEntry
	Breadcrumbs []Breadcrumb

	// For Player View
	PlayingItem DirEntry
	Playlist    []DirEntry

	// For Login View
	Error string
}

type Server struct {
	templates *template.Template
}

func New() (*Server, error) {
	if _, err := os.Stat(mediaDir); os.IsNotExist(err) {
		log.Printf("Creating media directory: %s", mediaDir)
		os.MkdirAll(mediaDir, 0755)
	}

	// Note the use of Funcs to add a helper function to our templates
	templates, err := template.New("").Funcs(template.FuncMap{
		"isRoot": func(path string) bool {
			return path == "/" || path == "." || path == ""
		},
	}).ParseGlob("web/templates/*.html")

	if err != nil {
		return nil, err
	}

	return &Server{templates: templates}, nil
}

// isHtmxRequest checks for the HX-Request header.
func isHtmxRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", s.authMiddleware(s.browserHandler))
	mux.HandleFunc("/folder/", s.authMiddleware(s.browserHandler))
	mux.HandleFunc("/player/", s.authMiddleware(s.playerHandler)) // New player handler
	mux.HandleFunc("/stream/", s.authMiddleware(s.streamHandler))
	mux.HandleFunc("/upload", s.authMiddleware(s.uploadHandler))
	mux.HandleFunc("/login", s.loginHandler)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./web/static"))))
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(authCookie)
		if err != nil || cookie.Value != "true" {
			if isHtmxRequest(r) { // For HTMX requests, signal a redirect via header
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func (s *Server) loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.templates.ExecuteTemplate(w, "login.html", nil)
		return
	}
	r.ParseForm()
	if r.Form.Get("username") == authUser && r.Form.Get("password") == authPass {
		http.SetCookie(w, &http.Cookie{Name: authCookie, Value: "true", Path: "/", MaxAge: 86400})
		http.Redirect(w, r, "/", http.StatusFound)
	} else {
		s.templates.ExecuteTemplate(w, "login.html", PageData{Error: "Invalid credentials"})
	}
}

// browserHandler serves the file browser view.
func (s *Server) browserHandler(w http.ResponseWriter, r *http.Request) {
	var currentPath string
	if strings.HasPrefix(r.URL.Path, "/folder/") {
		currentPath = strings.TrimPrefix(r.URL.Path, "/folder/")
	} else {
		currentPath = "/"
	}

	entries, err := s.getDirectoryContents(currentPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	parentPath := "/"
	if currentPath != "/" {
		parentPath = filepath.Dir(currentPath)
	}

	data := PageData{
		CurrentPath: currentPath,
		ParentPath:  parentPath,
		Entries:     entries,
		Breadcrumbs: s.generateBreadcrumbs(currentPath),
	}

	// Render a partial for HTMX, or a full page for normal requests.
	templateName := "browser.html"
	if !isHtmxRequest(r) {
		templateName = "layout.html"
	}
	s.templates.ExecuteTemplate(w, templateName, data)
}

// playerHandler serves the video player view.
func (s *Server) playerHandler(w http.ResponseWriter, r *http.Request) {
	itemPath := strings.TrimPrefix(r.URL.Path, "/player/")
	if itemPath == "" {
		http.Error(w, "No file path specified", http.StatusBadRequest)
		return
	}

	dirPath := filepath.Dir(itemPath)
	if dirPath == "." {
		dirPath = "/"
	}

	// Get all files in the same directory to build the playlist
	allEntries, err := s.getDirectoryContents(dirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var playlist []DirEntry
	var playingItem DirEntry
	for _, entry := range allEntries {
		if !entry.IsDir {
			playlist = append(playlist, entry)
			if entry.Path == itemPath {
				playingItem = entry
			}
		}
	}

	if playingItem.Path == "" {
		http.Error(w, "File not found in directory listing", http.StatusNotFound)
		return
	}

	data := PageData{
		CurrentPath: dirPath, // The "current" path is the folder containing the video
		PlayingItem: playingItem,
		Playlist:    playlist,
	}

	templateName := "player.html"
	if !isHtmxRequest(r) {
		templateName = "layout.html"
	}
	s.templates.ExecuteTemplate(w, templateName, data)
}

func (s *Server) getDirectoryContents(relativePath string) ([]DirEntry, error) {
	cleanPath := filepath.Clean(relativePath)
	if strings.HasPrefix(cleanPath, "..") {
		return nil, os.ErrInvalid
	}

	targetPath := filepath.Join(mediaDir, cleanPath)
	osEntries, err := os.ReadDir(targetPath)
	if err != nil {
		log.Printf("Error reading directory %s: %v", targetPath, err)
		return nil, err
	}

	var results []DirEntry
	for _, e := range osEntries {
		if strings.HasPrefix(e.Name(), ".") {
			continue // Skip hidden files
		}

		relPath, _ := filepath.Rel(mediaDir, filepath.Join(targetPath, e.Name()))
		entry := DirEntry{
			Name:  e.Name(),
			IsDir: e.IsDir(),
			Path:  filepath.ToSlash(relPath),
		}

		if !e.IsDir() {
			info, _ := e.Info()
			if info != nil {
				entry.SizeMB = info.Size() / (1024 * 1024)
			}
		}
		results = append(results, entry)
	}

	// Sort: folders first, then by name
	sort.Slice(results, func(i, j int) bool {
		if results[i].IsDir != results[j].IsDir {
			return results[i].IsDir
		}
		return strings.ToLower(results[i].Name) < strings.ToLower(results[j].Name)
	})

	return results, nil
}

func (s *Server) generateBreadcrumbs(path string) []Breadcrumb {
	if path == "/" || path == "." {
		return []Breadcrumb{{Name: "home", Path: "/"}}
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	crumbs := []Breadcrumb{{Name: "home", Path: "/"}}

	var current string
	for _, part := range parts {
		current = filepath.Join(current, part)
		crumbs = append(crumbs, Breadcrumb{Name: part, Path: filepath.ToSlash(current)})
	}
	return crumbs
}

// streamHandler and uploadHandler remain mostly the same as your provided code.
// They are solid and don't need HTMX-specific changes.
// ... (paste your existing streamHandler and uploadHandler here) ...

func (s *Server) streamHandler(w http.ResponseWriter, r *http.Request) {
	filePath := strings.TrimPrefix(r.URL.Path, "/stream/")
	cleanPath := filepath.Clean(filePath)
	if strings.HasPrefix(cleanPath, "..") {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	fullPath := filepath.Join(mediaDir, cleanPath)

	file, err := os.Open(fullPath)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		http.Error(w, "Stat error", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (s *Server) uploadHandler(w http.ResponseWriter, r *http.Request) {
	// 10 GB limit, for example
	if err := r.ParseMultipartForm(10 << 30); err != nil {
		http.Error(w, "File too large", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Could not get uploaded file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	targetPath := r.FormValue("path")
	cleanPath := filepath.Clean(targetPath)
	if strings.HasPrefix(cleanPath, "..") || cleanPath == ".." {
		http.Error(w, "Invalid upload path", http.StatusBadRequest)
		return
	}

	fullFolder := filepath.Join(mediaDir, cleanPath)
	if err := os.MkdirAll(fullFolder, 0755); err != nil {
		log.Printf("Error creating upload directory %s: %v", fullFolder, err)
		http.Error(w, "Cannot create upload directory", http.StatusInternalServerError)
		return
	}

	safeFilename := filepath.Base(header.Filename)
	destPath := filepath.Join(fullFolder, safeFilename)

	out, err := os.Create(destPath)
	if err != nil {
		log.Printf("Error creating file %s: %v", destPath, err)
		http.Error(w, "Cannot save file", http.StatusInternalServerError)
		return
	}
	defer out.Close()

	_, err = io.Copy(out, file)
	if err != nil {
		log.Printf("Error writing file %s: %v", safeFilename, err)
		http.Error(w, "Error writing file data", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully uploaded %s to %s", safeFilename, fullFolder)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Upload successful"))
}
