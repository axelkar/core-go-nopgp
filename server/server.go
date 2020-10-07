package server

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
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
}

func NewServer(service string, conf ini.File) *Server {
	server := &Server{
		conf:    conf,
		router:  chi.NewRouter(),
		service: service,
	}
	return server
}

func (server *Server) Router() chi.Router {
	return server.router
}

func (server *Server) WithSchema(schema graphql.ExecutableSchema) *Server {
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

	// TODO: Remove config parameter from EmailRecover
	rec := EmailRecover(server.conf, config.Debug, server.service)
	srv := handler.GraphQL(schema,
		handler.ComplexityLimit(complexity),
		handler.RecoverFunc(rec))

	server.router.Handle("/query", srv)
	if config.Debug {
		server.router.Handle("/",
			playground.Handler("GraphQL playground", "/query"))
	}
	server.router.Handle("/query/metrics", promhttp.Handler())
	return server
}

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
	server.router.Use(database.Middleware(db))
	server.router.Use(auth.Middleware(server.conf, apiconf))
	return server
}

func (server *Server) WithMiddleware(
	middlewares ...func(http.Handler) http.Handler) *Server {
	server.router.Use(middlewares...)
	return server
}

func (server *Server) MakeServer() (*http.Server, net.Listener) {
	listen, err := net.Listen("tcp", config.Addr)
	if err != nil {
		panic(err)
	}
	log.Printf("Running on %s", config.Addr)
	return &http.Server{Handler: server.router}, listen
}
