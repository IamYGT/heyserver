package api

import (
	"encoding/json"
	"net/http"

	"github.com/IamYGT/heyserver/internal/services/filemanager"
)

// handleFileList lists the contents of a directory.
// Query param: ?path=/etc/nginx/
func handleFileList(fileSvc *filemanager.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			// Return allowed root directories so the UI can show entry points.
			jsonResponse(w, http.StatusOK, map[string]any{
				"roots":   fileSvc.AllowedRoots(),
				"entries": []any{},
			})
			return
		}

		entries, err := fileSvc.List(path)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{
			"path":    path,
			"entries": entries,
		})
	}
}

// handleFileRead returns the text content of a file.
// Query param: ?path=/etc/nginx/nginx.conf
func handleFileRead(fileSvc *filemanager.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			jsonError(w, http.StatusBadRequest, "path is required")
			return
		}

		content, err := fileSvc.Read(path)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{
			"path":    path,
			"content": content,
		})
	}
}

// handleFileWrite saves text content to an existing or new file.
// Body JSON: {"path": "...", "content": "..."}
func handleFileWrite(fileSvc *filemanager.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.Path == "" {
			jsonError(w, http.StatusBadRequest, "path is required")
			return
		}

		if err := fileSvc.Write(req.Path, req.Content); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ok", "path": req.Path})
	}
}

// handleFileCreate creates a new file or directory.
// Body JSON: {"path": "...", "type": "file"|"directory"}
func handleFileCreate(fileSvc *filemanager.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
			Type string `json:"type"` // "file" or "directory"
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.Path == "" {
			jsonError(w, http.StatusBadRequest, "path is required")
			return
		}

		var err error
		switch req.Type {
		case "directory", "dir":
			err = fileSvc.CreateDir(req.Path)
		default:
			err = fileSvc.CreateFile(req.Path)
		}

		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, map[string]string{"status": "created", "path": req.Path})
	}
}

// handleFileDelete deletes a file or directory (recursive).
// Query param: ?path=/srv/hserver/sites/example.com/old-dir
func handleFileDelete(fileSvc *filemanager.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			jsonError(w, http.StatusBadRequest, "path is required")
			return
		}

		if err := fileSvc.Delete(path); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted", "path": path})
	}
}

// handleFileRename renames (or moves) a file/directory.
// Body JSON: {"old_path": "...", "new_path": "..."} (also accepts legacy {"src": "...", "dst": "..."})
func handleFileRename(fileSvc *filemanager.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Src     string `json:"src"`
			Dst     string `json:"dst"`
			OldPath string `json:"old_path"`
			NewPath string `json:"new_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		// Support both field name conventions
		src := req.Src
		if src == "" {
			src = req.OldPath
		}
		dst := req.Dst
		if dst == "" {
			dst = req.NewPath
		}

		if src == "" || dst == "" {
			jsonError(w, http.StatusBadRequest, "src/old_path and dst/new_path are required")
			return
		}

		if err := fileSvc.Rename(src, dst); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "renamed", "src": src, "dst": dst})
	}
}
