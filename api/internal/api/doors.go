package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/katharinasick/aocn/api/internal/app"
	"github.com/katharinasick/aocn/api/internal/doors"
	"github.com/katharinasick/aocn/api/internal/schedule"
)

// Everything was uploaded weeks before. These handlers decide on each request whether a
// door is open, so at 06:00 nothing runs: requests just get a different answer. The website
// and the CLI both ask here, and nowhere else decides.

// Doors is what the door endpoints need.
type Doors struct {
	Schedule *schedule.Schedule
	Storage  doors.Storage
	Pages    *doors.Pages
	// Now is the clock; nil means time.Now. Tests set it.
	Now func() time.Time
}

func (d Doors) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// closedResponse answers for a door that isn't open yet, with when it opens, so the website
// can count down and the CLI can say when to come back.
type closedResponse struct {
	Door    int       `json:"door"`
	OpensAt time.Time `json:"opensAt"`
}

type pageResponse struct {
	Door    int       `json:"door"`
	OpensAt time.Time `json:"opensAt"`
	doors.Page
}

// gate parses the door number and decides whether it may be served. It answers the request
// itself when it may not, and returns ok only for an open door.
func (d Doors) gate(w http.ResponseWriter, r *http.Request) (door int, opensAt time.Time, ok bool) {
	// The answer changes at the opening, and later with previewers depends on who asks, so
	// nothing between here and the caller may keep a copy. The website caches what it renders.
	w.Header().Set("Cache-Control", "no-store")

	door, err := strconv.Atoi(r.PathValue("door"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "no such door")
		return 0, time.Time{}, false
	}
	opensAt, err = d.Schedule.OpensAt(door)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "no such door")
		return 0, time.Time{}, false
	}
	if open, _ := d.Schedule.IsOpen(door, d.now()); !open {
		writeJSON(w, http.StatusNotFound, closedResponse{Door: door, OpensAt: opensAt})
		return 0, time.Time{}, false
	}
	return door, opensAt, true
}

// handleDoorPage answers with what the website shows for an open door, and 404 with the
// opening time before that.
func handleDoorPage(log app.Logger, d Doors) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		door, opensAt, ok := d.gate(w, r)
		if !ok {
			return
		}
		page, err := d.Pages.Get(r.Context(), door, d.now())
		if err != nil {
			writeMissingDoor(log, w, door, "page", err)
			return
		}
		writeJSON(w, http.StatusOK, pageResponse{Door: door, OpensAt: opensAt, Page: page})
	})
}

// handleDoorBundle streams an open door's bundle, for aocn open. Bundles are small, so the
// API streams them itself rather than handing out a link to the bucket.
func handleDoorBundle(log app.Logger, d Doors) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		door, _, ok := d.gate(w, r)
		if !ok {
			return
		}
		bundle, err := d.Storage.Open(r.Context(), doors.BundleName(door))
		if err != nil {
			writeMissingDoor(log, w, door, "bundle", err)
			return
		}
		defer bundle.Close()

		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="door-%02d.tar.gz"`, door))
		if _, err := io.Copy(w, bundle); err != nil {
			log.Error("Streaming a bundle failed", "door", door, "error", err)
		}
	})
}

// writeMissingDoor answers for an open door whose files can't be read. That's our mistake,
// never the caller's, so it's logged as an error.
func writeMissingDoor(log app.Logger, w http.ResponseWriter, door int, what string, err error) {
	if errors.Is(err, doors.ErrNotFound) {
		log.Error("An open door was never uploaded", "door", door, "file", what)
	} else {
		log.Error("Reading an open door failed", "door", door, "file", what, "error", err)
	}
	writeJSONError(w, http.StatusServiceUnavailable, "this door can't be served right now, try again in a few minutes")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{message})
}
