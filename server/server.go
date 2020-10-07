package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/99designs/gqlgen/handler"
	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	goRedis "github.com/go-redis/redis/v8"
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/vaughan0/go-ini"
	"git.sr.ht/~sircmpwn/dowork"

	"git.sr.ht/~sircmpwn/core-go/auth"
	"git.sr.ht/~sircmpwn/core-go/config"
	"git.sr.ht/~sircmpwn/core-go/database"
	"git.sr.ht/~sircmpwn/core-go/redis"
)

var (
	requestsProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "api_requests_processed_total",
		Help: "Total number of API requests processed",
	})
	requestDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "api_request_duration_millis",
		Help:    "Duration of processed HTTP requests in milliseconds",
		Buckets: []float64{10, 20, 40, 80, 120, 300, 600, 900, 1800},
	})
)

type Server struct {
	conf        ini.File
	router      chi.Router
	schema      graphql.ExecutableSchema
	service     string
	queues      []*work.Queue
}

// Creates a new common server context for a SourceHut GraphQL daemon.
func NewServer(service string, conf ini.File) *Server {
	server := &Server{
		conf:    conf,
		router:  chi.NewRouter(),
		service: service,
	}
	return server
}

// Returns the chi Router being used for this sever.
func (server *Server) Router() chi.Router {
	return server.router
}

// Adds a GraphQL schema for this server. The second parameter shall be the
// list of scopes, as strings, which are supported by this schema. This
// function configures routes for the router; all middlewares must be
// configured before this is called.
func (server *Server) WithSchema(
	schema graphql.ExecutableSchema, scopes []string) *Server {
	server.schema = schema

	var (
		complexity int
		err        error
	)
	if limit, ok := server.conf.Get(
		server.service + "::api", "max-complexity"); ok {
		complexity, err = strconv.Atoi(limit)
		if err != nil {
			panic(err)
		}
	} else {
		complexity = 250
	}

	srv := handler.GraphQL(schema,
		handler.ComplexityLimit(complexity),
		handler.RecoverFunc(emailRecover))

	server.router.Handle("/query", srv)
	if config.Debug {
		server.router.Handle("/",
			playground.Handler("GraphQL playground", "/query"))
	}
	server.router.Handle("/query/metrics", promhttp.Handler())
	server.router.Get("/query/api-meta.json", func(w http.ResponseWriter, r *http.Request) {
		info := struct {
			Scopes []string `json:"scopes"`
		}{scopes}

		j, err := json.Marshal(&info)
		if err != nil {
			panic(err)
		}

		w.Header().Add("Content-Type", "application/json")
		w.Write(j)
	})
	return server
}

// Adds the default middleware to this server, including:
//
// - Configuration middleware
// - PostgresSQL connection pool
// - Redis connection
// - Authentication middleware
// - Standard rigging: logging, x-real-ip, instrumentation, etc
func (server *Server) WithDefaultMiddleware() *Server {
	pgcs, ok := server.conf.Get(server.service, "connection-string")
	if !ok {
		log.Fatalf("No connection string provided in config.ini")
	}

	db, err := sql.Open("postgres", pgcs)
	if err != nil {
		log.Fatalf("Failed to open a database connection: %v", err)
	}

	rcs, ok := server.conf.Get("sr.ht", "redis-host")
	if !ok {
		rcs = "redis://"
	}
	ropts, err := goRedis.ParseURL(rcs)
	if err != nil {
		log.Fatalf("Invalid sr.ht::redis-host in config.ini: %e", err)
	}
	rc := goRedis.NewClient(ropts)

	apiconf := fmt.Sprintf("%s::api", server.service)

	var timeout time.Duration
	if to, ok := server.conf.Get(apiconf, "max-duration"); ok {
		timeout, err = time.ParseDuration(to)
		if err != nil {
			panic(err)
		}
	} else {
		timeout = 3 * time.Second
	}

	server.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			end := time.Now()
			elapsed := end.Sub(start)
			requestDuration.Observe(float64(elapsed.Milliseconds()))
			requestsProcessed.Inc()
		})
	})
	server.router.Use(config.Middleware(server.conf, server.service))
	server.router.Use(database.Middleware(db))
	server.router.Use(redis.Middleware(rc))
	server.router.Use(middleware.RealIP)
	server.router.Use(middleware.Logger)
	server.router.Use(middleware.Timeout(timeout))
	server.router.Use(auth.Middleware(server.conf, apiconf))
	return server
}

// Add user-defined middleware to the server
func (server *Server) WithMiddleware(
	middlewares ...func(http.Handler) http.Handler) *Server {
	server.router.Use(middlewares...)
	return server
}

// Add dowork task queues for this server to manage
func (server *Server) WithQueues(queues ...*work.Queue) *Server {
	server.queues = append(server.queues, queues...)
	for _, queue := range queues {
		queue.Start(context.Background())
	}
	return server
}

// Run the server. Blocks until SIGINT is received.
func (server *Server) Run() {
	qlisten, err := net.Listen("tcp", config.Addr)
	if err != nil {
		panic(err)
	}
	log.Printf("Running on %s", config.Addr)
	qserver := &http.Server{Handler: server.router}
	go qserver.Serve(qlisten)

	mux := &http.ServeMux{}
	mux.Handle("/metrics", promhttp.Handler())
	pserver := &http.Server{Handler: mux}
	plisten, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	log.Printf("Prometheus listening on :%d", plisten.Addr().(*net.TCPAddr).Port)
	go pserver.Serve(plisten)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	signal.Reset(os.Interrupt)
	log.Println("SIGINT caught, initiating warm shutdown")
	log.Println("SIGINT again to terminate immediately and drop pending requests & tasks")

	log.Println("Terminating server...")
	ctx, cancel := context.WithDeadline(context.Background(),
		time.Now().Add(30 * time.Second))
	qserver.Shutdown(ctx)
	cancel()

	log.Println("Terminating work queues...")
	log.Printf("Progress available via Prometheus stats on port %d",
		plisten.Addr().(*net.TCPAddr).Port)
	work.Join(server.queues...)
	qserver.Close()
	log.Println("Server terminated.")
}
