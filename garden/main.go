package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
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
	"github.com/davecgh/go-spew/spew"
	"github.com/gorilla/mux"
	_ "github.com/honeycombio/honeycomb-opentelemetry-go"
	"github.com/honeycombio/otel-launcher-go/launcher"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	gogpt "github.com/sashabaranov/go-gpt3"
	"github.com/sirupsen/logrus"
	"github.com/uptrace/opentelemetry-go-extra/otelsql"
	"github.com/uptrace/opentelemetry-go-extra/otelsqlx"
	"github.com/urfave/cli"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"golang.org/x/tools/go/packages"
)

const (
	MAX_SQLITE_CONNS = 1
)

var (
	log                       *logrus.Entry
	db                        *sqlx.DB
	SeedlingStepProtobufs     = "SeedlingStepProtobufs"
	SeedlingStepServer        = "SeedlingStepServer"
	SeedlingStepDockerfile    = "SeedlingStepDockerfile"
	SeedlingStepDockerCompose = "SeedlingStepDockerCompose"
	SeedlingStepClient        = "SeedlingStepClient"
	SeedlingStepComplete      = "SeedlingStepComplete"
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
	Step        string `db:"step" json:"step"`
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

var (
	protoPrompt = `
Write me a protobufs file for a gRPC method that %s

Make sure to start it with lines like:

syntax = "proto3";
option go_package = "./protobufs";

The file will be called %s.proto. Do not override any of my file names.

My directory layout is:

$ ls .
Dockerfile          client              docker-compose.yaml go.mod              protobufs           server

My go.mod is:

module %s

go 1.19

There are some arguments and variations that a user will be likely to request.
Make sure to include them.
`
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

	seedlings := []Seedling{}
	if err := db.Select(&seedlings, "SELECT * FROM seedlings"); err != nil {
		logrus.Fatal(err)
	}
	for _, seedling := range seedlings {
		if seedling.Step != SeedlingStepComplete {
			go gptThread(seedling)
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

func cleanFilePath(file string) string {
	invalidCharsRegex := regexp.MustCompile(`[^\w-.]`)
	cleanedPath := strings.ReplaceAll(strings.TrimSpace(file), " ", "_")
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

	dockerfileContents := `FROM debian:bookworm-slim
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

	gitignoreContents := `
logs
`
	if err := ioutil.WriteFile(filepath.Join(basePath, ".gitignore"), []byte(gitignoreContents), 0644); err != nil {
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

	go gptThread(seedling)

	return nil
}

func writeSeedlingToRepo(ctx context.Context, seedling Seedling) error {
	// TODO: more languages etc
	if err := initGoRepo(ctx, seedling); err != nil {
		return err
	}

	return nil
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
	s.Step = SeedlingStepProtobufs

	result, err := db.NamedExecContext(r.Context(), `
	 INSERT INTO seedlings
	 (name, description, created_at, modified_at, step)
	 VALUES (:name, :description, :created_at, :modified_at, :step)
	 `, &s)
	if err != nil {
		logrus.WithField("error", err).Error("failed to insert seedling")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// get last inserted row id and set s.ID
	id, err := result.LastInsertId()
	if err != nil {
		logrus.WithField("error", err).Error("failed to get last inserted id")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	s.ID = hide.Int64(id)

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

	var seedling Seedling
	if err := db.GetContext(r.Context(),
		&seedling,
		"SELECT * FROM seedlings WHERE id = $1",
		hide.Default.Int64Deobfuscate(int64(numID)),
	); err != nil {
		if err == sql.ErrNoRows {
			logrus.WithField("id", id).Error("seedling not found")
			http.Error(w, "seedling not found", http.StatusNotFound)
			return
		}
		logrus.WithField("error", err).Error("failed to get seedling")
		http.Error(w, "internal server error", http.StatusInternalServerError)
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

	// do a git rm -r repos/seedlings/s.description
	// and do a git commit -am "delete seedling"
	cmd := exec.Command("git", "rm", "-r", seedling.Name)
	cmd.Dir = "./repos/default"
	if err := cmd.Run(); err != nil {
		logrus.WithField("error", err).Error("failed to delete seedling")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	cmd = exec.Command("git", "commit", "-am", "delete seedling")
	cmd.Dir = "./repos/default"
	if err := cmd.Run(); err != nil {
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

	if err := db.SelectContext(r.Context(), &ss,
		"SELECT * FROM seedlings ORDER BY created_at DESC"); err != nil {
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

func gptThread(seedling Seedling) {
	ctx := context.Background()
	c := gogpt.NewClient(os.Getenv("OPENAI_API_KEY"))
	maxErrs := 5
	step := 0
	steps := []string{
		SeedlingStepProtobufs,
		SeedlingStepServer,
		SeedlingStepDockerfile,
		SeedlingStepComplete,
	}

	errs := 0
	prompt := ""
	for {
		if steps[step] == SeedlingStepComplete {
			break
		}

		repoPath := ""
		codeType := ""
		cmdCmd := ""
		cmdArgs := []string{}
		logrus.Warn("step: ", steps[step])

		switch steps[step] {
		case SeedlingStepProtobufs:
			prompt = fmt.Sprintf(`%s
Write the code. Write only the code.
`+"```protobuf", fmt.Sprintf(
				protoPrompt,
				seedling.Description,
				seedling.Name,
				seedling.Name,
			))
			repoPath = filepath.Join("protobufs", seedling.Name+".proto")
			codeType = "proto"
			cmdCmd = "protoc"
			cmdArgs = []string{
				"-I=.",
				"--go_out=.",
				"--go-grpc_out=.",
				repoPath,
			}
		case SeedlingStepServer:
			protoFile := filepath.Join(
				"repos",
				"default",
				seedling.Name,
				"protobufs",
				seedling.Name+".pb.go",
			)
			protoBufDefs, err := getStructAndInterfaceDefinitionsFromFile(protoFile)
			if err != nil {
				logrus.Fatal(err)
			}
			grpcFile := filepath.Join(
				"repos",
				"default",
				seedling.Name,
				"protobufs",
				seedling.Name+"_grpc.pb.go",
			)
			grpcDefs, err := getStructAndInterfaceDefinitionsFromFile(grpcFile)
			if err != nil {
				logrus.Fatal(err)
			}
			serverFile := filepath.Join(
				"repos",
				"default",
				seedling.Name,
				"protobufs",
				"server",
				"main.go",
			)

			libDefs, err := getNonStandardLibraryDefinitionsFromFile(serverFile)
			if err != nil {
				logrus.Fatal(err)
			}

			prompt = fmt.Sprintf(`%s
Now we will write a server implementation for the service method(s).

Here are library methods etc. that might be useful:

%s

Some of the generated protobuf code looks like this:

%s

And the gRPC:

%s

1. We can use external libraries, packages, and binaries.
2. We assume we are running in a Docker container (Linux).
3. We will have the service listen on port 8000, with insecure connection
   settings.
4. Our code will be efficient, and performant. It will use relevant algorithms
   and data structures.
5. In the same file, also include an HTTP server that will listen on port 8001,
   take in JSON equivalent to the gRPC call, and call the equivalent gRPC
   server method.

Think step by step -- what's the best way to solve the problem?

Make sure it's an actual production implementation of the service.

Implement everything. Don't leave anything out.

Make sure to include all imports you need in the import section. Double check
those imports.

Write the code. Write only the code.
`+"```go", prompt, strings.Join(libDefs, "\n"), strings.Join(protoBufDefs, "\n"), strings.Join(grpcDefs, "\n"))
			repoPath = filepath.Join("server", "main.go")
			codeType = "go"
			cmdCmd = "sh"
			cmdArgs = []string{
				"-c",
				"go get ./... && go build -o /tmp/server ./server",
			}
		case SeedlingStepDockerfile:
			prompt = fmt.Sprintf(`%s
Now write a Dockerfile (multi-stage build) to build and run your server.

Here is an example:

FROM debian:bookworm-slim AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
  golang \
  git \
  protobuf-compiler \
  ca-certificates
WORKDIR /app
COPY go.mod .
RUN go mod tidy && go mod download
COPY protobufs/<service>.proto protobufs/
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
ENV PATH="/root/go/bin:${PATH}"
RUN protoc --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  protobufs/<service>.proto
COPY server/*.go .
RUN go get ./...
RUN go build -o server .

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
  <package_1> \
  <package_2>
COPY --from=builder /app/server /app/server
WORKDIR /app
CMD ["./server"]

Make sure to install any external libraries, packages, and binaries you need.

Make sure to include this line:

RUN go get ./...

Think step by step -- what's the best way to build the file?

Write the code. Write only the code.
`+"```dockerfile", prompt)
			repoPath = filepath.Join("Dockerfile")
			codeType = "dockerfile"
			cmdCmd = "docker"
			if runtime.GOARCH == "arm64" && runtime.GOOS == "darwin" {
				cmdArgs = []string{
					"buildx",
					"build",
					"--platform",
					"linux/amd64",
					"-t",
					seedling.Name,
					".",
				}
			} else {
				cmdArgs = []string{
					"build",
					"-t",
					seedling.Name,
					".",
				}
			}
		default:
			logrus.WithField("step", steps[step]).Error("unknown step")
			return
		}

		file := filepath.Join(
			"repos",
			"default",
			seedling.Name,
			repoPath,
		)
		buildCmd := exec.Command(cmdCmd, cmdArgs...)
		buildCmd.Dir = filepath.Join(
			"repos",
			"default",
			seedling.Name,
		)

		gptOutput, err := gpt(ctx, c, prompt)
		if err != nil {
			logrus.WithField("error", err).Error("failed to get gpt output")
			return
		}

		logrus.Warn("GPT OUT")
		fmt.Println(gptOutput)
		logrus.Warn("GPT OUT END")

		if output, err := runSeedling(
			file,
			codeType,
			buildCmd,
			gptOutput,
		); err != nil {
			spew.Dump(buildCmd)
			fmt.Println(output)
			if output == "" {
				logrus.WithField("error", err).Error("something went wrong on seedling run that wasn't build, exiting")
				return
			}
			errs++
			if errs > maxErrs {
				logrus.Error("Max errors exceeded")
				return
			}

			lines := strings.Split(output, "\n")
			if len(lines) > 25 {
				lines = lines[len(lines)-25:]
			}
			output = strings.Join(lines, "\n")

			prompt += "```This code didn't work:\n```" + codeType + "\n" + gptOutput + "```\n\nIt got an error: \n" + output
			prompt += "\n\nFix the code. I'll repeat the specification below.\n"
		} else {
			if step+1 == len(steps) {
				logrus.Info("Seedling run complete.")
				return
			}
			if _, err := db.ExecContext(
				ctx,
				"UPDATE seedlings SET step = $1 WHERE id = $2",
				steps[step+1],
				seedling.ID,
			); err != nil {
				logrus.WithField("error", err).Error("failed to update seedling step")
				return
			}
			prompt += "\n\n" + gptOutput + "\n\n"
			prompt += "```\n\nGreat. That worked. Let's move on to the next step.\n\n"
			step += 1
		}

		/*
			if err := classifySeedlingOutput(); err != nil {
				thread += "\n\nI got this output: " + exec["output"]
				isFinishedPrompt := "I ran this code:\n\n" + gptOutput + "\n\nAnd got this output:\n\n" + exec["output"] + "\n\nMy expected output was this:\n\n" + expectedOutput + "Return the word yes or no to indicate if I got the correct output.\n\nAnswer:"
				isFinished := gpt(c, ctx, isFinishedPrompt)
				if isFinished == "yes" {
					return
				} else {
					thread += isFinishedPrompt + "\n\nI got the wrong output. Provide a fix to get the expected output. Use Markdown.\n\n" + threadStartSeq
				}
			}
		*/
	}
}

func gpt(ctx context.Context, c *gogpt.Client, prompt string) (string, error) {
	fmt.Println("====== PROMPTING GPT ======")
	fmt.Println(prompt)
	fmt.Println("===========================")

	req := gogpt.CompletionRequest{
		Model: "text-alpha-002-longcontext-0818",
		// Model:     "text-alpha-002-latest",
		MaxTokens: 2048,
		Prompt:    prompt,
		Stop:      []string{"```"},
	}
	resp, err := c.CreateCompletion(ctx, req)
	if err != nil {
		return "", err
	}

	fmt.Println()
	logrus.WithField("finishReason", resp.Choices[0].FinishReason).Warn("GOT GPT RESPONSE")
	fmt.Println("========= GPT OUT =========")
	fmt.Println(resp.Choices[0].Text)
	fmt.Println("===========================")
	return resp.Choices[0].Text, nil
}

func runSeedling(
	file string,
	codeType string,
	buildCmd *exec.Cmd,
	gptOut string,
) (string, error) {
	gptOut = strings.TrimPrefix(gptOut, "```"+codeType)
	gptOut = strings.TrimSuffix(gptOut, "```")
	gptOut = strings.TrimSpace(gptOut)

	if err := ioutil.WriteFile(file, []byte(gptOut), 0644); err != nil {
		return "", err
	}

	byteOutput, err := buildCmd.CombinedOutput()
	if err != nil {
		return string(byteOutput), err
	}

	gitAddCmd := exec.Command("git", "add", ".")
	gitAddCmd.Stdout = os.Stdout
	gitAddCmd.Stderr = os.Stderr
	gitAddCmd.Dir = filepath.Join("repos", "default")
	if err := gitAddCmd.Run(); err != nil {
		return "", err
	}

	gitCmd := exec.Command("git", "commit", "-m", "seedling update")
	gitCmd.Stdout = os.Stdout
	gitCmd.Stderr = os.Stderr
	gitCmd.Dir = filepath.Join("repos", "default")
	if err := gitCmd.Run(); err != nil {
		return "", err
	}

	return "", nil
}

func getStructAndInterfaceDefinitionsFromFile(filepath string) ([]string, error) {
	// Read the file contents into a []byte
	fileContents, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil, err
	}

	// Initialize the token set and parser
	set := token.NewFileSet()
	parsedFile, err := parser.ParseFile(set, "", string(fileContents), parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// Traverse the AST and collect all struct and interface definitions
	typeDefs := make([]string, 0)
	for _, decl := range parsedFile.Decls {
		if genDecl, ok := decl.(*ast.GenDecl); ok && genDecl.Tok == token.TYPE {
			for _, spec := range genDecl.Specs {
				switch typeSpec := spec.(type) {
				case *ast.TypeSpec:
					switch typeSpec.Type.(type) {
					case *ast.StructType, *ast.InterfaceType:
						// Print the *ast.TypeSpec to a string using go/printer
						var typeDef bytes.Buffer
						err := printer.Fprint(&typeDef, set, typeSpec)
						if err != nil {
							return nil, err
						}
						typeDefs = append(typeDefs, typeDef.String())
					}
				}
			}
		}
	}

	return typeDefs, nil
}

func getNonStandardLibraryDefinitionsFromFile(filename string) ([]string, error) {
	// Load the packages and their dependencies
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax,
	}
	pkgs, err := packages.Load(cfg, filename)
	if err != nil {
		return nil, err
	}

	// Traverse the packages and collect all non-standard library method, struct, and interface definitions
	typeDefs := make([]string, 0)
	for _, pkg := range pkgs {
		if !strings.HasPrefix(pkg.PkgPath, "std") {
			fmt.Printf("Found non-standard library package: %s\n", pkg.PkgPath)
			for _, file := range pkg.Syntax {
				fmt.Printf("Analyzing file: %s\n", pkg.Fset.File(file.Pos()).Name())
				// Traverse the file's AST and collect all method, struct, and interface definitions
				fileSet := token.NewFileSet()
				for _, decl := range file.Decls {
					switch decl := decl.(type) {
					case *ast.FuncDecl:
						if decl.Recv != nil {
							typeDefs = append(typeDefs, fmt.Sprintf("%s.%s", pkg.PkgPath, decl.Name.Name))
							fmt.Printf("Found method: %s.%s\n", pkg.PkgPath, decl.Name.Name)
						}
					case *ast.GenDecl:
						if decl.Tok == token.TYPE {
							for _, spec := range decl.Specs {
								if typeSpec, ok := spec.(*ast.TypeSpec); ok {
									if structType, ok := typeSpec.Type.(*ast.StructType); ok {
										// Print the *ast.StructType to a string using go/printer
										var structDef bytes.Buffer
										err := printer.Fprint(&structDef, fileSet, structType)
										if err != nil {
											return nil, err
										}
										typeDefs = append(typeDefs, fmt.Sprintf("%s.%s", pkg.PkgPath, typeSpec.Name.Name))
										typeDefs = append(typeDefs, structDef.String())
										fmt.Printf("Found struct: %s.%s\n", pkg.PkgPath, typeSpec.Name.Name)
									} else if interfaceType, ok := typeSpec.Type.(*ast.InterfaceType); ok {
										// Print the *ast.InterfaceType to a string using go/printer
										var interfaceDef bytes.Buffer
										err := printer.Fprint(&interfaceDef, fileSet, interfaceType)
										if err != nil {
											return nil, err
										}
										typeDefs = append(typeDefs, fmt.Sprintf("%s.%s", pkg.PkgPath, typeSpec.Name.Name))
										typeDefs = append(typeDefs, interfaceDef.String())
										fmt.Printf("Found interface: %s.%s\n", pkg.PkgPath, typeSpec.Name.Name)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return typeDefs, nil
}
