package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	mathrand "math/rand"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/c2h5oh/hide"
	"github.com/gorilla/mux"
	_ "github.com/honeycombio/honeycomb-opentelemetry-go"
	"github.com/honeycombio/otel-launcher-go/launcher"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/sirupsen/logrus"
	"github.com/uptrace/opentelemetry-go-extra/otelsql"
	"github.com/uptrace/opentelemetry-go-extra/otelsqlx"
	"github.com/urfave/cli"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

const (
	MAX_SQLITE_CONNS = 1
)

var (
	log *logrus.Entry
	db  *sqlx.DB
)

type DBRow struct {
	ID         hide.Int64 `db:"id" json:"id"`
	CreatedAt  time.Time  `db:"created_at" json:"createdAt"`
	ModifiedAt time.Time  `db:"modified_at" json:"modifiedAt"`
}

type Seedling struct {
	DBRow
	Name        string `db:"name" json:"name"`
	Description string `db:"description" json:"description"`
}

type (
	responseData struct {
		status int
		size   int
	}

	loggingResponseWriter struct {
		http.ResponseWriter
		responseData *responseData
	}
)

func init() {
	var err error
	db, err = otelsqlx.Open("sqlite3",
		"garden.sqlite3?cache=shared&_synchronous=normal&_journal_mode=WAL",
		otelsql.WithAttributes(semconv.DBSystemSqlite))
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(MAX_SQLITE_CONNS)

	if _, err := db.Exec("PRAGMA temp_store = MEMORY;"); err != nil {
		logrus.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA mmap_size = 30000000000;"); err != nil {
		logrus.Fatal(err)
	}
}

func WithLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		responseData := &responseData{
			status: 0,
			size:   0,
		}
		lrw := &loggingResponseWriter{
			ResponseWriter: w, // compose original http.ResponseWriter
			responseData:   responseData,
		}
		next.ServeHTTP(lrw, r)
		duration := time.Since(start)

		log.WithFields(logrus.Fields{
			"uri":      r.RequestURI,
			"method":   r.Method,
			"status":   responseData.status,
			"duration": duration,
			"size":     responseData.size,
		}).Info("Finished request")
	})
}

func writeJSONErr(w http.ResponseWriter, err string, code int) {
	w.WriteHeader(code)
	w.Write([]byte(fmt.Sprintf(`{"error":"%s"}`, err)))
}

func seedlingHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf(`{"seedlings":[]}`)))
}

func serveCmd(cliCtx *cli.Context) error {
	r := mux.NewRouter()
	r.PathPrefix("/outputs/").
		Handler(http.StripPrefix("/outputs/",
			http.FileServer(http.Dir("./bucket/outputs"))))
	r.Handle("/api/v1/seedlings", WithLogging(http.HandlerFunc(ListSeedlings))).Methods("GET")
	r.Handle("/api/v1/seedlings", WithLogging(http.HandlerFunc(CreateSeedling))).Methods("POST")
	r.Handle("/api/v1/seedlings/{id}", WithLogging(http.HandlerFunc(GetSeedling))).Methods("GET")
	r.Handle("/api/v1/seedlings/{id}", WithLogging(http.HandlerFunc(DeleteSeedling))).Methods("DELETE")
	r.Handle("/api/v1/seedlings/{id}", WithLogging(http.HandlerFunc(UpdateSeedling))).Methods("PUT")

	log.WithField("service", "garden-api").Info("Listening on :7777")
	http.ListenAndServe(":7777", otelhttp.NewHandler(r, "garden-api"))
	return nil
}

func main() {
	mathrand.Seed(time.Now().UnixNano())
	logrus.SetReportCaller(true)
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
		CallerPrettyfier: func(f *runtime.Frame) (second string, first string) {
			_, b, _, _ := runtime.Caller(0)
			basepath := filepath.Dir(b)
			rel, err := filepath.Rel(basepath, f.File)
			if err != nil {
				logrus.Error("Couldn't determine file path\n", err)
			}
			return "", fmt.Sprintf("%-40s", fmt.Sprintf(" garden-api %s:%d", rel, f.Line))
		},
	})

	os.Setenv("HONEYCOMB_API_KEY", "zi944sIGUTu1wozNDajQlA")

	log = logrus.WithField("service_name", "garden-api")

	os.Setenv("OTEL_SERVICE_NAME", "garden-api-prod")
	otelShutdown, err := launcher.ConfigureOpenTelemetry()
	if err != nil {
		log.Fatalf("error setting up OTel SDK - %e", err)
	}
	defer otelShutdown()

	go func() {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "unknown"
		}
		markerReq, err := http.NewRequest(
			"POST",
			"https://api.honeycomb.io/1/markers/garden-api-prod",
			bytes.NewBuffer([]byte(fmt.Sprintf(`{
			"message": "garden-api started on %s",
			"type": "process-start"
		}`, hostname))),
		)
		if err != nil {
			log.Error(err, "failed to create Honeycomb marker request")
			return
		}
		markerReq.Header.Set("X-Honeycomb-Team", "zi944sIGUTu1wozNDajQlA")
		if _, err := http.DefaultClient.Do(markerReq); err != nil {
			log.Error(err, "failed to Do Honeycomb marker request")
		}
	}()

	http.DefaultClient = &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}

	app := &cli.App{
		Name:  "garden-api",
		Usage: "Backend API for garden.ai",
		Commands: []cli.Command{
			{
				Name:   "serve",
				Usage:  "Run business logic API (HTTP)",
				Action: serveCmd,
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

// CreateSeedling inserts a new seedling into the database and returns its id
func CreateSeedling(w http.ResponseWriter, r *http.Request) {
	// Parse and validate the request body as a seedling struct
	var s Seedling
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		logrus.WithField("error", err).Error("failed to decode request body")
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if s.Name == "" {
		logrus.Error("name is required")
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Insert the seedling into the database
	if _, err := db.NamedExecContext(r.Context(), "INSERT INTO seedlings (name, description, created_at, modified_at) VALUES (:name, :description, :created_at, :modified_at)", &s); err != nil {
		logrus.WithField("error", err).Error("failed to insert seedling")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Return the created seedling as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(&s); err != nil {
		logrus.WithField("error", err).Error("failed to encode response")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}

// GetSeedling retrieves a seedling by its id from the database and returns it as JSON
func GetSeedling(w http.ResponseWriter, r *http.Request) {
	// Get the id parameter from the URL
	vars := mux.Vars(r)
	id := vars["id"]
	if id == "" {
		logrus.Error("id is required")
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	// Query the database for the seedling with the given id
	var s Seedling
	if err := db.GetContext(r.Context(), &s, "SELECT * FROM seedlings WHERE id = $1", id); err != nil {
		if err == sql.ErrNoRows {
			logrus.WithField("id", id).Error("seedling not found")
			http.Error(w, "seedling not found", http.StatusNotFound)
			return
		}
		logrus.WithField("error", err).Error("failed to get seedling")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Return the seedling as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(&s); err != nil {
		logrus.WithField("error", err).Error("failed to encode response")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}

// UpdateSeedling updates a seedling by its id with the given fields and returns it as JSON
func UpdateSeedling(w http.ResponseWriter, r *http.Request) {
	// Get the id parameter from the URL
	vars := mux.Vars(r)
	id := vars["id"]
	if id == "" {
		logrus.Error("id is required")
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	// Parse and validate the request body as a seedling struct
	var s Seedling
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		logrus.WithField("error", err).Error("failed to decode request body")
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if s.Name == "" {
		logrus.Error("name is required")
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Set the id and modified_at fields of the seedling
	sid, _ := strconv.Atoi(id)
	s.ID = hide.Int64(sid)
	s.ModifiedAt = time.Now()

	// Update the seedling in the database with the given fields
	if _, err := db.NamedExecContext(r.Context(), "UPDATE seedlings SET name = :name, description = :description, modified_at = :modified_at WHERE id = :id", &s); err != nil {
		logrus.WithField("error", err).Error("failed to update seedling")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Return the updated seedling as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(&s); err != nil {
		logrus.WithField("error", err).Error("failed to encode response")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}

// DeleteSeedling deletes a seedling by its id from the database and returns a success message
func DeleteSeedling(w http.ResponseWriter, r *http.Request) {
	// Get the id parameter from the URL
	vars := mux.Vars(r)
	id := vars["id"]
	if id == "" {
		logrus.Error("id is required")
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	// Delete the seedling from the database with the given id
	if _, err := db.ExecContext(r.Context(), "DELETE FROM seedlings WHERE id = $1", id); err != nil {
		logrus.WithField("error", err).Error("failed to delete seedling")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Return a success message
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"message": "seedling deleted"}); err != nil {
		logrus.WithField("error", err).Error("failed to encode response")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}

// ListSeedlings retrieves all seedlings from the database and returns them as JSON
func ListSeedlings(w http.ResponseWriter, r *http.Request) {
	// Query the database for all seedlings
	ss := []Seedling{}

	if err := db.SelectContext(r.Context(), &ss, "SELECT * FROM seedlings"); err != nil {
		logrus.WithField("error", err).Error("failed to get seedlings")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Return the seedlings as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(&ss); err != nil {
		logrus.WithField("error", err).Error("failed to encode response")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}
