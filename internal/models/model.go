package models

type DirEntry struct {
	Name   string `json:"name"`
	IsDir  bool   `json:"isDir"`
	SizeMB int64  `json:"size_mb,omitempty"`
	Path   string `json:"path"`
}
