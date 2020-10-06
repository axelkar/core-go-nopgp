package server

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"git.sr.ht/~sircmpwn/getopt"
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
	"git.sr.ht/~sircmpwn/core-go/crypto"
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

var (
	debug bool
	addr  string
)

// Loads the application configuration, reads options from the command line,
// and initializes some internals based on these results.
func LoadConfig(defaultAddr string) ini.File {
	addr = defaultAddr
	var (
		config ini.File
		err    error
	)
	opts, _, err := getopt.Getopts(os.Args, "b:d")
	if err != nil {
		panic(err)
	}

	for _, opt := range opts {
		switch opt.Option {
		case 'b':
			addr = opt.Value
		case 'd':
			debug = true
		}
	}

	for _, path := range []string{"../config.ini", "/etc/sr.ht/config.ini"} {
		config, err = ini.LoadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		log.Fatalf("Failed to load config file: %v", err)
	}

	crypto.InitCrypto(config)
	return config
}

// Prepares a router with the sr.ht API middleware pre-configured. This
// connects to PostgreSQL to rig up the database middlewares.
func MakeRouter(service string, conf ini.File, schema graphql.ExecutableSchema,
	middlewares ...func(http.Handler) http.Handler) chi.Router {

	pgcs, ok := conf.Get(service, "connection-string")
	if !ok {
		log.Fatalf("No connection string provided in config.ini")
	}

	db, err := sql.Open("postgres", pgcs)
	if err != nil {
		log.Fatalf("Failed to open a database connection: %v", err)
	}

	rcs, ok := conf.Get("sr.ht", "redis-host")
	if !ok {
		rcs = "redis://"
	}
	ropts, err := goRedis.ParseURL(rcs)
	if err != nil {
		log.Fatalf("Invalid sr.ht::redis-host in config.ini: %e", err)
	}
	rc := goRedis.NewClient(ropts)

	apiconf := fmt.Sprintf("%s::api", service)

	var timeout time.Duration
	if to, ok := conf.Get(apiconf, "max-duration"); ok {
		timeout, err = time.ParseDuration(to)
		if err != nil {
			panic(err)
		}
	} else {
		timeout = 3 * time.Second
	}

	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			end := time.Now()
			elapsed := end.Sub(start)
			requestDuration.Observe(float64(elapsed.Milliseconds()))
			requestsProcessed.Inc()
		})
	})
	router.Use(config.Middleware(conf, service))
	router.Use(database.Middleware(db))
	router.Use(redis.Middleware(rc))
	router.Use(middleware.RealIP)
	router.Use(middleware.Logger)
	router.Use(middleware.Timeout(timeout))
	router.Use(database.Middleware(db))
	router.Use(auth.Middleware(conf, apiconf))
	router.Use(middlewares...)

	var complexity int
	if limit, ok := conf.Get(apiconf, "max-complexity"); ok {
		complexity, err = strconv.Atoi(limit)
		if err != nil {
			panic(err)
		}
	} else {
		complexity = 250
	}

	// XXX: EmailRecover doesn't need to take config now that it's on the
	// request context
	srv := handler.GraphQL(schema,
		handler.ComplexityLimit(complexity),
		handler.RecoverFunc(EmailRecover(conf, debug, service)))

	router.Handle("/query", srv)
	router.Handle("/query/metrics", promhttp.Handler())

	if debug {
		router.Handle("/", playground.Handler("GraphQL playground", "/query"))
	}

	return router
}

// Runs the API server.
func ListenAndServe(router chi.Router) {
	log.Printf("running on %s", addr)
	log.Fatal(http.ListenAndServe(addr, router))
}
