package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-midway/midway"
)

// FileStat stores and display a file's information as JSON
type FileStat struct {
	Name  string
	Path  string
	Size  int64
	MTime time.Time
}

// MarshalJSON implements encoding/json.Marshaler
func (file FileStat) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type  string    `json:"type"`
		Name  string    `json:"name"`
		Path  string    `json:"path"`
		Size  int64     `json:"size"`
		MTime time.Time `json:"mtime"`
	}{
		Type:  "file",
		Name:  file.Name,
		Path:  file.Path,
		Size:  file.Size,
		MTime: file.MTime,
	})
}

// DirStat stores and display a directory's information as JSON
type DirStat struct {
	Name  string
	Path  string
	MTime time.Time
}

// MarshalJSON implements encoding/json.Marshaler
func (file DirStat) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type  string    `json:"type"`
		Name  string    `json:"name"`
		Path  string    `json:"path"`
		MTime time.Time `json:"mtime"`
	}{
		Type:  "directory",
		Name:  file.Name,
		Path:  file.Path,
		MTime: file.MTime,
	})
}

// StatError represents an error in JSON format
type StatError struct {
	Code int
	Path string
}

// Message return message for a given error
func (err StatError) Message() string {
	msg := http.StatusText(err.Code)
	if msg == "" {
		return "unknown error"
	}
	return msg
}

// Error implements error interface
func (err StatError) Error() string {
	return fmt.Sprintf("error %d: %s", err.Code, err.Message())
}

// NewStatError returns a new StatError
func NewStatError(code int, path string) *StatError {
	return &StatError{
		Code: code,
		Path: path,
	}
}

// MarshalJSON implements encoding/json.Marshaler
func (err StatError) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Status  string `json:"status"`
		Code    int    `json:"code"`
		Path    string `json:"path"`
		Message string `json:"message"`
	}{
		Status:  "error",
		Code:    err.Code,
		Path:    err.Path,
		Message: err.Message(),
	})
}

func statsEndpoint(ctx context.Context, req interface{}) (stats interface{}, err error) {

	path := req.(string)

	// TODO: build the absolute file / dir path for stat and open
	stat, err := os.Stat(path)

	// if file not found
	if os.IsNotExist(err) {
		err = NewStatError(http.StatusNotFound, path)
		return
	}

	// permission problem
	if err != nil {
		perr, _ := err.(*os.PathError)
		if perr.Err.Error() == os.ErrPermission.Error() {
			err = NewStatError(http.StatusForbidden, path)
		}
		return
	}

	// for files
	if stat.Mode().IsRegular() {

		// test permission
		var file *os.File
		file, err = os.OpenFile(path, os.O_RDONLY, 0444)
		if err != nil {
			perr, _ := err.(*os.PathError)
			if perr.Err.Error() == os.ErrPermission.Error() {
				err = NewStatError(http.StatusForbidden, path)
			}
			return
		}
		file.Close() // close immediately

		stats = FileStat{
			Name:  stat.Name(),
			Path:  path,
			Size:  stat.Size(),
			MTime: stat.ModTime(),
		}
		return
	}

	// for directories
	if stat.Mode().IsDir() {
		stats = DirStat{
			Name:  stat.Name(),
			Path:  path,
			MTime: stat.ModTime(),
		}
		return
	}

	return
}

func handleEndpoint(endpoint func(ctx context.Context, req interface{}) (resp interface{}, err error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		ctx := context.Background()

		// handle path request
		resp, err := endpoint(ctx, r.URL.Path)

		// handle error
		if err != nil {
			switch serr := err.(type) {
			case *StatError:
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(serr.Code)
				jsonw := json.NewEncoder(w)
				jsonw.Encode(serr)
			default:
				statusCode := http.StatusInternalServerError
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(statusCode)
				jsonw := json.NewEncoder(w)
				jsonw.Encode(struct {
					Code    int    `json:"code"`
					Status  string `json:"status"`
					Message string `json:"message"`
				}{
					Code:    statusCode,
					Status:  "error",
					Message: err.Error(),
				})
			}
			return
		}

		// handle normal response
		w.Header().Set("Content-Type", "application/json")
		jsonw := json.NewEncoder(w)
		jsonw.Encode(resp)

		log.Printf("resp: %#v", resp)
	}
}

// ServeAPI generates a middleware to serve API for file / directory information
// query
func ServeAPI(path string, root http.FileSystem) midway.Middleware {

	path = strings.TrimRight(path, "/") // strip trailing slash
	pathWithSlash := path + "/"
	pathLen := len(pathWithSlash)

	// wrap endpoints
	handleStats := handleEndpoint(statsEndpoint)

	return func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			// serve API endpoint
			if r.URL.Path == path {
				http.Redirect(w, r, pathWithSlash, http.StatusMovedPermanently)
				return
			}
			if strings.HasPrefix(r.URL.Path, pathWithSlash) {
				r.URL.Path = strings.TrimRight(r.URL.Path[pathLen:], "/") // strip base path

				// stats of file / directory
				if strings.HasPrefix(r.URL.Path, "stats/") {
					r.URL.Path = r.URL.Path[6:]
					handleStats(w, r)
					return
				}

				// if no matching endpoint
				statusCode := http.StatusNotFound
				w.Header().Add("Content-Type", "application/json")
				w.WriteHeader(statusCode)
				jsonw := json.NewEncoder(w)
				jsonw.Encode(struct {
					Code    int    `json:"code"`
					Status  string `json:"status"`
					Message string `json:"message"`
				}{
					Code:    statusCode,
					Status:  "error",
					Message: "not a valid API endpoint",
				})

				return
			}
			// server file / directory info query at the URL
			if r.Header.Get("Content-Type") == "application/goserve+json" {
				// TODO: also detect the request content-type: "goserve+json/application"
				// and return file info
			}

			// defers to inner handler
			inner.ServeHTTP(w, r)
		})
	}
}
