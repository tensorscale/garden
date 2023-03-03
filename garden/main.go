package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"go/format"
	"io/ioutil"
	mathrand "math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
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

	gogpt "github.com/sashabaranov/go-gpt3"
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

	if _, err := os.Stat("./repos/default"); os.IsNotExist(err) {
		if err := os.MkdirAll("./repos/default", 0755); err != nil {
			logrus.Fatal(err)
		}
		if err := os.Chdir("./repos/default"); err != nil {
			logrus.Fatal(err)
		}

		cmd := exec.Command("git", "init")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			logrus.Fatal(err)
		}

		if err := os.Chdir("../.."); err != nil {
			logrus.Fatal(err)
		}
	}

	cmd := exec.Command("docker", "ps")
	if err := cmd.Run(); err != nil {
		logrus.WithField("error", err).Fatal("Docker must be running")
	}

	cmd = exec.Command("docker", "network", "inspect", "seedlings")
	if err := cmd.Run(); err != nil {
		cmd = exec.Command("docker", "network", "create", "seedlings")
		if err := cmd.Run(); err != nil {
			logrus.WithField("error", err).Fatal("Failed to create docker network")
		}
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
	if err := http.ListenAndServe(":7777", otelhttp.NewHandler(r, "garden-api")); err != nil {
		return err
	}
	return nil
}

func main() {
	// for testing purposes, use a simple task
	seedlingDesc := "Write a Hello World program in Go."
	expectedOutput := "Hello, World!\n"

	// call the gpt_thread function
	result := gpt_thread(seedlingDesc, expectedOutput)

	// print the result
	fmt.Println(result)

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

func cleanFilePath(filePath string) string {
	invalidCharsRegex := regexp.MustCompile(`[^\w-.]`)
	cleanedPath := strings.ReplaceAll(filePath, " ", "-")
	cleanedPath = invalidCharsRegex.ReplaceAllString(cleanedPath, "")
	return strings.ToLower(cleanedPath)
}

func initGoRepo(ctx context.Context, seedling Seedling) error {
	dirpath := cleanFilePath(seedling.Name)
	basePath := filepath.Join("repos", "default", dirpath)
	if err := os.MkdirAll(filepath.Join(basePath, "protobufs"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(basePath, "server"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(basePath, "client"), 0755); err != nil {
		return err
	}

	defaultModContents := fmt.Sprintf(`module %s

go 1.19`, dirpath)
	if err := ioutil.WriteFile(filepath.Join(basePath, "go.mod"), []byte(defaultModContents), 0644); err != nil {
		logrus.WithField("error", err).Error("failed to write to go.mod")
	}

	protoContents := `syntax = "proto3";

option go_package = ".";`
	if err := ioutil.WriteFile(filepath.Join(basePath, "protobufs", fmt.Sprintf("%s.proto", dirpath)), []byte(protoContents), 0644); err != nil {
		logrus.WithField("error", err).Error("failed to write to protobufs")
	}

	serverContents := `package main

func main() {
	fmt.Println("Welcome to seedling")
}`
	if err := ioutil.WriteFile(filepath.Join(basePath, "server", "main.go"), []byte(serverContents), 0644); err != nil {
		logrus.WithField("error", err).Error("failed to write to server")
	}

	clientContents := `package main

func main() {
	fmt.Println("Welcome to seedling")
}`
	if err := ioutil.WriteFile(filepath.Join(basePath, "client", "main.go"), []byte(clientContents), 0644); err != nil {
		logrus.WithField("error", err).Error("failed to write to client")
	}

	dockerfileContents := `FROM debian:slim
COPY . /app
`
	if err := ioutil.WriteFile(filepath.Join(basePath, "Dockerfile"), []byte(dockerfileContents), 0644); err != nil {
		logrus.WithField("error", err).Error("failed to write to client")
	}

	composeContents := fmt.Sprintf(`version: "3.9"
services:
  %s:
    image: %s
    networks:
    - seedlings
	volumes:
    - ../secrets:/secrets

networks:
  seedlings:
    external: true
`, dirpath, dirpath)
	if err := ioutil.WriteFile(filepath.Join(basePath, "docker-compose.yaml"), []byte(composeContents), 0644); err != nil {
		logrus.WithField("error", err).Error("failed to write to client")
	}

	cmd := exec.CommandContext(ctx, "git", "add", ".")
	cmd.Dir = filepath.Join(basePath)
	if err := cmd.Run(); err != nil {
		logrus.WithField("error", err).Error("failed to init git repo")
		return err
	}

	cmd = exec.CommandContext(ctx, "git", "commit", "-m", ".")
	cmd.Dir = filepath.Join(basePath)
	if err := cmd.Run(); err != nil {
		logrus.WithField("error", err).Error("failed to init git repo")
		return err
	}

	return nil
}

func writeSeedlingToRepo(ctx context.Context, seedling Seedling) error {
	// TODO: more languages etc
	if err := initGoRepo(ctx, seedling); err != nil {
		return err
	}

	return nil
}

func growSeedSession(ctx context.Context) {

}

func CreateSeedling(w http.ResponseWriter, r *http.Request) {
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

	s.Name = cleanFilePath(s.Name)

	if _, err := db.NamedExecContext(r.Context(), `
	 INSERT INTO seedlings
	 (name, description, created_at, modified_at)
	 VALUES (:name, :description, :created_at, :modified_at)
	 `, &s); err != nil {
		logrus.WithField("error", err).Error("failed to insert seedling")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := writeSeedlingToRepo(r.Context(), s); err != nil {
		logrus.WithField("error", err).Error("failed to write seedling to repo")
		writeJSONErr(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(&s); err != nil {
		logrus.WithField("error", err).Error("failed to encode response")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
}

func GetSeedling(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	if id == "" {
		logrus.Error("id is required")
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

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

	numID, err := strconv.Atoi(id)
	if err != nil {
		logrus.WithField("error", err).Error("failed to convert id to int")
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if _, err := db.ExecContext(
		r.Context(),
		"DELETE FROM seedlings WHERE id = $1",
		hide.Default.Int64Deobfuscate(int64(numID)),
	); err != nil {
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

func gpt_thread(seedlingDesc string, expectedOutput string) string {
	c := gogpt.NewClient(os.Getenv("OPENAI_API_KEY"))
	ctx := context.Background()

	maxRuns := 5

	// our instructions for service are written here
	thread := seedlingDesc
	threadStartSeq := "```go"

	thread += " Use markdown.\n\n" + threadStartSeq

	// loop until runs is 0
	for i := 0; i < maxRuns; i++ {

		gptOutput := gpt(c, ctx, thread)

		exec := runAndClassify(gptOutput)

		if exec["error"] != "" {
			thread += "\n\nI got this error: " + exec["error"]

			thread += "\n\nHere is the fix for the error. Use Markdown."
			thread += threadStartSeq
		} else {
			thread += "\n\nI got this output: " + exec["output"]
			isFinishedPrompt := "I ran this code:\n\n" + gptOutput + "\n\nAnd got this output:\n\n" + exec["output"] + "\n\nMy expected output was this:\n\n" + expectedOutput + "Return the word yes or no to indicate if I got the correct output.\n\nAnswer:"
			isFinished := gpt(c, ctx, isFinishedPrompt)
			if isFinished == "yes" {
				return gptOutput
			} else {
				thread += isFinishedPrompt + "\n\nI got the wrong output. Provide a fix to get the expected output. Use Markdown.\n\n" + threadStartSeq
			}
		}
	}
	return ""
}

func gpt(c *gogpt.Client, ctx context.Context, prompt string) string {
	req := gogpt.CompletionRequest{
		Model:     "text-davinci-003",
		MaxTokens: 1000,
		Prompt:    prompt,
		Stop:      []string{"```"},
	}
	resp, err := c.CreateCompletion(ctx, req)
	if err != nil {
		return ""
	}
	fmt.Println(resp.Choices[0].Text)
	return resp.Choices[0].Text
}

func runAndClassify(code string) map[string]string {
	// remove the markdown syntax
	code = strings.TrimPrefix(code, "```go")
	code = strings.TrimSuffix(code, "```")

	// format the code
	formatted, err := format.Source([]byte(code))
	if err != nil {
		return map[string]string{"error": err.Error(), "output": ""}
	}
	code = string(formatted) // convert the formatted bytes to string

	// create a temporary file
	tmpfile, err := os.CreateTemp("", "gpt-*.go")
	if err != nil {
		return map[string]string{"error": err.Error(), "output": ""}
	}
	defer os.Remove(tmpfile.Name()) // clean up

	// write the code to the file
	if _, err := tmpfile.Write([]byte(code)); err != nil {
		return map[string]string{"error": err.Error(), "output": ""}
	}
	if err := tmpfile.Close(); err != nil {
		return map[string]string{"error": err.Error(), "output": ""}
	}

	// run the file with go run
	cmd := exec.Command("go", "run", tmpfile.Name())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return map[string]string{"error": stderr.String(), "output": ""}
	}
	return map[string]string{"error": "", "output": stdout.String()}
}
