package main

import (
	"bytes"
	"fmt"
	mathrand "math/rand"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
	MAX_SQLITE_CONNS             = 1
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
	db, err := otelsqlx.Open("sqlite3",
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
	r.Handle("/seedlings", WithLogging(
		http.HandlerFunc(seedlingHandler),
	))
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
